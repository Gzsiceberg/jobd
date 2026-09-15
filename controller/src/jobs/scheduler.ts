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
  progress: number;
  exit_code: number | null;
  error: string | null;
};

const jobColumns =
  'id, status, command, created_at, started_at, finished_at, worker_id, progress, exit_code, error';

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
      progress REAL NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 1),
      exit_code INTEGER,
      error TEXT
    )`);
    this.storage.sql.exec(
      'CREATE INDEX IF NOT EXISTS jobs_queued ON jobs(status, sequence)',
    );
    this.storage.sql.exec(`CREATE UNIQUE INDEX IF NOT EXISTS jobs_running_worker
      ON jobs(worker_id) WHERE status = 'running'`);
    this.storage.sql.exec(`CREATE TABLE IF NOT EXISTS workers (
      worker_id TEXT PRIMARY KEY NOT NULL,
      hostname TEXT NOT NULL,
      status TEXT NOT NULL CHECK (status IN ('idle', 'busy')),
      last_heartbeat TEXT NOT NULL,
      current_job_id TEXT UNIQUE
    )`);
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
    return worker;
  }

  submit(command: string[]): Job {
    const id = crypto.randomUUID();
    this.storage.sql.exec(
      `INSERT INTO jobs (id, status, command, created_at)
      VALUES (?, 'queued', ?, ?)`,
      id,
      JSON.stringify(command),
      new Date().toISOString(),
    );
    return this.job(id);
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

  heartbeat(id: string): Worker {
    this.storage.sql.exec(
      'UPDATE workers SET last_heartbeat = ? WHERE worker_id = ?',
      new Date().toISOString(),
      id,
    );
    return this.worker(id);
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
      const next = this.storage.sql
        .exec<{ id: string }>(
          "SELECT id FROM jobs WHERE status = 'queued' ORDER BY sequence LIMIT 1",
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

  progress(id: string, workerId: string, progress: number): Job {
    const job = this.ownedJob(id, workerId);
    if (job.status !== 'running') throw new ApiError(409, 'Job is not running');
    this.storage.sql.exec(
      'UPDATE jobs SET progress = MAX(progress, ?) WHERE id = ?',
      progress,
      id,
    );
    return this.job(id);
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
      const status = succeeded ? 'succeeded' : 'failed';
      if (job.status === status) return job;
      if (job.status !== 'running')
        throw new ApiError(409, 'Job is not running');
      this.worker(workerId);
      this.storage.sql.exec(
        `UPDATE jobs SET status = ?, exit_code = ?, error = ?,
        finished_at = ?, progress = ? WHERE id = ?`,
        status,
        exitCode,
        succeeded ? null : error,
        new Date().toISOString(),
        succeeded ? 1 : job.progress,
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
