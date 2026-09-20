import { ApiError } from '../api/errors';
import type { Worker } from '../workers/types';
import type { Job, JobStatus } from './types';

type WorkerRow = { [Key in keyof Worker]: Worker[Key] };

type JobRow = {
  id: string;
  status: JobStatus;
  command: string;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  worker_id: string | null;
  exit_code: number | null;
  error: string | null;
  output_path: string | null;
  hostname: string | null;
  cancel_requested: number;
};

export const workerTimeoutMs = 2 * 60 * 1000;
const disconnectedError = 'Worker disconnected; outcome unknown';

const jobColumns =
  'id, status, command, created_at, started_at, finished_at, worker_id, exit_code, error, output_path, cancel_requested, (SELECT hostname FROM workers WHERE workers.worker_id = jobs.worker_id) AS hostname';

/** Queue operations use the Durable Object's SQLite storage directly. */
export class Scheduler {
  constructor(private storage: DurableObjectStorage) {
    this.storage.sql.exec(`CREATE TABLE IF NOT EXISTS jobs (
      sequence INTEGER PRIMARY KEY AUTOINCREMENT,
      id TEXT NOT NULL UNIQUE,
      status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
      command TEXT NOT NULL,
      created_at TEXT NOT NULL,
      started_at TEXT,
      finished_at TEXT,
      worker_id TEXT,
      exit_code INTEGER,
      error TEXT,
      cancel_requested INTEGER NOT NULL DEFAULT 0,
      output_path TEXT,
      queue_position INTEGER NOT NULL DEFAULT 0
    )`);
    this.storage.sql.exec(
      'CREATE INDEX IF NOT EXISTS jobs_queue_order ON jobs(status, queue_position, sequence)',
    );
    this.storage.sql.exec(`CREATE UNIQUE INDEX IF NOT EXISTS jobs_running_worker
      ON jobs(worker_id) WHERE status = 'running'`);
    this.storage.sql.exec(`CREATE TABLE IF NOT EXISTS workers (
      worker_id TEXT PRIMARY KEY NOT NULL,
      hostname TEXT NOT NULL,
      status TEXT NOT NULL CHECK (status IN ('idle', 'busy')),
      last_heartbeat TEXT NOT NULL,
      current_job_id TEXT UNIQUE,
      paused INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1))
    )`);
    const columns = this.storage.sql
      .exec<{ name: string }>('PRAGMA table_info(workers)')
      .toArray();
    if (!columns.some((column) => column.name === 'paused')) {
      this.storage.sql.exec(
        'ALTER TABLE workers ADD COLUMN paused INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1))',
      );
    }
  }

  /** Never replay work: a disconnected process may still be executing it. */
  private expireWorkers(now = Date.now()): void {
    const cutoff = new Date(now - workerTimeoutMs).toISOString();
    this.storage.transactionSync(() => {
      this.storage.sql.exec(
        `UPDATE jobs SET status = 'failed', error = ?, exit_code = NULL,
        finished_at = ?, cancel_requested = 0
        WHERE status = 'running' AND worker_id IN
          (SELECT worker_id FROM workers WHERE last_heartbeat <= ?)`,
        disconnectedError,
        new Date(now).toISOString(),
        cutoff,
      );
      this.storage.sql.exec(
        "UPDATE workers SET status = 'idle', current_job_id = NULL WHERE last_heartbeat <= ? AND (status != 'idle' OR current_job_id IS NOT NULL)",
        cutoff,
      );
    });
  }

  job(id: string): Job {
    const row = this.storage.sql
      .exec<JobRow>(`SELECT ${jobColumns} FROM jobs WHERE id = ?`, id)
      .toArray()[0];
    if (!row) throw new ApiError(404, 'Job not found');
    return { ...row, command: JSON.parse(row.command) as string[] };
  }

  worker(id: string): Worker {
    const worker = this.storage.sql
      .exec<WorkerRow>('SELECT * FROM workers WHERE worker_id = ?', id)
      .toArray()[0];
    if (!worker) throw new ApiError(404, 'Worker not found');
    return {
      ...worker,
      status:
        Date.parse(worker.last_heartbeat) + workerTimeoutMs <= Date.now()
          ? 'offline'
          : worker.status,
    };
  }

  submit(command: string[]): Job {
    return this.storage.transactionSync(() => {
      const row = this.storage.sql
        .exec<{ sequence: number }>(
          `INSERT INTO jobs (id, status, command, created_at)
         VALUES ('', 'queued', ?, ?) RETURNING sequence`,
          JSON.stringify(command),
          new Date().toISOString(),
        )
        .toArray()[0];
      const id = String(row.sequence);
      this.storage.sql.exec(
        'UPDATE jobs SET id = ?, queue_position = sequence WHERE sequence = ?',
        id,
        row.sequence,
      );
      return this.job(id);
    });
  }

  list(limit: number, offset: number): Job[] {
    this.expireWorkers();
    return this.storage.sql
      .exec<JobRow>(
        `SELECT ${jobColumns} FROM jobs ORDER BY CASE status WHEN 'running' THEN 0 WHEN 'queued' THEN 1 ELSE 2 END, queue_position, sequence LIMIT ? OFFSET ?`,
        limit,
        offset,
      )
      .toArray()
      .map((row) => ({ ...row, command: JSON.parse(row.command) as string[] }));
  }

  latest(kind: 'added' | 'run'): Job {
    const row = this.storage.sql
      .exec<{ id: string }>(
        kind === 'added'
          ? 'SELECT id FROM jobs ORDER BY sequence DESC LIMIT 1'
          : 'SELECT id FROM jobs WHERE started_at IS NOT NULL ORDER BY started_at DESC, sequence DESC LIMIT 1',
      )
      .toArray()[0];
    if (!row) throw new ApiError(404, 'No matching job');
    return this.job(row.id);
  }

  clear(): void {
    this.expireWorkers();
    this.storage.sql.exec(
      "DELETE FROM jobs WHERE status IN ('succeeded', 'failed')",
    );
  }

  removeAll(): { removed: number; kept_running: number } {
    this.expireWorkers();
    return this.storage.transactionSync(() => {
      this.storage.sql.exec("DELETE FROM jobs WHERE status != 'running'");
      const removed = this.storage.sql
        .exec<{ count: number }>('SELECT changes() AS count')
        .toArray()[0].count;
      const kept_running = this.storage.sql
        .exec<{ count: number }>(
          "SELECT COUNT(*) AS count FROM jobs WHERE status = 'running'",
        )
        .toArray()[0].count;
      return { removed, kept_running };
    });
  }

  remove(id: string): void {
    this.expireWorkers();
    this.storage.transactionSync(() => {
      if (this.job(id).status === 'running')
        throw new ApiError(409, 'Cannot remove a running job');
      this.storage.sql.exec('DELETE FROM jobs WHERE id = ?', id);
    });
  }

  retry(id?: string): { retried: number } {
    return this.storage.transactionSync(() => {
      if (id !== undefined && this.job(id).status !== 'failed')
        throw new ApiError(409, 'Only failed jobs can be retried');
      const jobs = this.storage.sql.exec<{ id: string }>(
        `SELECT id FROM jobs WHERE status = 'failed'${id === undefined ? '' : ' AND id = ?'} ORDER BY queue_position, sequence`,
        ...(id === undefined ? [] : [id]),
      ).toArray();
      for (const job of jobs) {
        this.storage.sql.exec(
          `UPDATE jobs SET status = 'queued', started_at = NULL, finished_at = NULL,
           worker_id = NULL, output_path = NULL, exit_code = NULL,
           error = NULL, cancel_requested = 0,
           queue_position = (SELECT COALESCE(MAX(queue_position), 0) + 1 FROM jobs)
           WHERE id = ?`, job.id,
        );
      }
      return { retried: jobs.length };
    });
  }

  reorder(id: string, other?: string): void {
    this.storage.transactionSync(() => {
      for (const target of other === undefined ? [id] : [id, other]) {
        if (this.job(target).status !== 'queued')
          throw new ApiError(409, 'Only queued jobs can be reordered');
      }
      if (other !== undefined) {
        const position = (target: string) =>
          this.storage.sql
            .exec<{ queue_position: number }>(
              'SELECT queue_position FROM jobs WHERE id = ?',
              target,
            )
            .toArray()[0].queue_position;
        const first = position(id),
          second = position(other);
        this.storage.sql.exec(
          'UPDATE jobs SET queue_position = ? WHERE id = ?',
          second,
          id,
        );
        this.storage.sql.exec(
          'UPDATE jobs SET queue_position = ? WHERE id = ?',
          first,
          other,
        );
      } else {
        this.storage.sql.exec(
          "UPDATE jobs SET queue_position = (SELECT MIN(queue_position) - 1 FROM jobs WHERE status = 'queued') WHERE id = ?",
          id,
        );
      }
    });
  }

  cancel(id: string): Job {
    return this.storage.transactionSync(() => {
      const job = this.job(id);
      if (job.status !== 'running')
        throw new ApiError(409, 'Only running jobs can be cancelled');
      this.storage.sql.exec(
        'UPDATE jobs SET cancel_requested = 1 WHERE id = ?',
        id,
      );
      return this.job(id);
    });
  }

  output(id: string, workerId: string, path: string): Job {
    const job = this.ownedJob(id, workerId);
    if (job.status !== 'running') throw new ApiError(409, 'Job is not running');
    this.storage.sql.exec(
      'UPDATE jobs SET output_path = ? WHERE id = ?',
      path,
      id,
    );
    return this.job(id);
  }

  setWorkerPaused(id: string, paused: boolean): Worker {
    return this.storage.transactionSync(() => {
      // Exact IDs win; substr treats SQL wildcard characters literally.
      const exact = this.storage.sql
        .exec<{ worker_id: string }>(
          'SELECT worker_id FROM workers WHERE worker_id = ?',
          id,
        )
        .toArray();
      const matches = exact.length
        ? exact
        : this.storage.sql
            .exec<{ worker_id: string }>(
              'SELECT worker_id FROM workers WHERE substr(worker_id, 1, length(?)) = ? ORDER BY worker_id',
              id,
              id,
            )
            .toArray();
      if (!matches.length) throw new ApiError(404, 'Worker not found');
      if (matches.length > 1)
        throw new ApiError(
          409,
          `Ambiguous worker prefix; use a longer ID: ${matches.map((worker) => worker.worker_id).join(', ')}`,
        );
      const workerId = matches[0].worker_id;
      this.storage.sql.exec(
        'UPDATE workers SET paused = ? WHERE worker_id = ?',
        paused ? 1 : 0,
        workerId,
      );
      return this.worker(workerId);
    });
  }

  register(workerId: string, hostname: string): Worker {
    // Registration retries preserve any existing assignment.
    this.storage.sql.exec(
      `INSERT INTO workers (worker_id, hostname, status, last_heartbeat)
      VALUES (?, ?, 'idle', ?)
      ON CONFLICT(worker_id) DO UPDATE SET
        hostname = excluded.hostname, last_heartbeat = excluded.last_heartbeat`,
      workerId,
      hostname,
      new Date().toISOString(),
    );
    return this.worker(workerId);
  }

  heartbeat(id: string): Worker & { cancel_job_id: string | null } {
    return this.storage.transactionSync(() => {
      this.storage.sql.exec(
        'UPDATE workers SET last_heartbeat = ? WHERE worker_id = ?',
        new Date().toISOString(),
        id,
      );
      const worker = this.worker(id);
      const cancelJobID =
        worker.current_job_id &&
        this.job(worker.current_job_id).cancel_requested
          ? worker.current_job_id
          : null;
      return { ...worker, cancel_job_id: cancelJobID };
    });
  }

  claim(id: string): Job | null {
    return this.storage.transactionSync(() => {
      const worker = this.worker(id);
      this.storage.sql.exec(
        'UPDATE workers SET last_heartbeat = ? WHERE worker_id = ?',
        new Date().toISOString(),
        id,
      );
      // A lost claim response must not assign a second job to the same worker.
      if (worker.current_job_id) return this.job(worker.current_job_id);
      if (worker.paused) return null;
      const next = this.storage.sql
        .exec<{ id: string }>(
          "SELECT id FROM jobs WHERE status = 'queued' ORDER BY queue_position, sequence LIMIT 1",
        )
        .toArray()[0];
      if (!next) return null;
      this.storage.sql.exec(
        "UPDATE jobs SET status = 'running', started_at = ?, worker_id = ? WHERE id = ?",
        new Date().toISOString(),
        id,
        next.id,
      );
      this.storage.sql.exec(
        "UPDATE workers SET status = 'busy', current_job_id = ? WHERE worker_id = ?",
        next.id,
        id,
      );
      return this.job(next.id);
    });
  }

  private ownedJob(id: string, workerId: string): Job {
    const job = this.job(id);
    if (job.worker_id !== workerId)
      throw new ApiError(409, 'Job is not assigned to this worker');
    return job;
  }

  finish(
    id: string,
    workerId: string,
    succeeded: boolean,
    exitCode: number | null,
    error: string | null,
  ): Job {
    return this.storage.transactionSync(() => {
      const job = this.ownedJob(id, workerId);
      if (job.status === 'failed' && job.error === disconnectedError)
        throw new ApiError(409, 'Worker disconnected; job outcome is unknown');
      const status = succeeded ? 'succeeded' : 'failed';
      if (job.status === status) return job;
      if (job.status !== 'running')
        throw new ApiError(409, 'Job is not running');
      this.worker(workerId);
      this.storage.sql.exec(
        `UPDATE jobs SET status = ?, exit_code = ?, error = ?,
        finished_at = ? WHERE id = ?`,
        status,
        exitCode,
        succeeded ? null : error,
        new Date().toISOString(),
        id,
      );
      this.storage.sql.exec(
        "UPDATE workers SET status = 'idle', current_job_id = NULL WHERE worker_id = ?",
        workerId,
      );
      return this.job(id);
    });
  }
}
