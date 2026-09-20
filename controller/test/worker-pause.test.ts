import { afterEach, expect, it, vi } from 'vitest';
import { Scheduler, workerTimeoutMs } from '../src/jobs/scheduler';
import { createQueueApi } from '../src/api/queues';
import { issueWorkerToken } from '../src/api/worker-tokens';
import { schedulerStorage } from './storage';

afterEach(() => vi.useRealTimers());

it('pauses new assignments without breaking current claim recovery or completion', () => {
  const { scheduler: s } = schedulerStorage();
  s.register('abcdef-1', 'host');
  const first = s.submit(['true']);
  const second = s.submit(['true']);
  expect(s.claim('abcdef-1')?.id).toBe(first.id);
  expect(s.setWorkerPaused('abc', true).paused).toBe(1);
  expect(s.setWorkerPaused('abc', true).paused).toBe(1);
  expect(s.claim('abcdef-1')?.id).toBe(first.id);
  expect(s.heartbeat('abcdef-1').current_job_id).toBe(first.id);
  s.finish(first.id, 'abcdef-1', true, 0, null);
  expect(s.claim('abcdef-1')).toBeNull();
  expect(s.job(second.id).status).toBe('queued');
  s.setWorkerPaused('abc', false);
  s.setWorkerPaused('abc', false);
  expect(s.claim('abcdef-1')?.id).toBe(second.id);
});

it('preserves pause across re-registration, storage reopening and timeout cleanup', () => {
  vi.useFakeTimers();
  const { scheduler: s, storage } = schedulerStorage();
  s.register('worker', 'host');
  s.submit(['true']);
  s.claim('worker');
  s.setWorkerPaused('worker', true);
  vi.advanceTimersByTime(workerTimeoutMs);
  s.list(100, 0);
  expect(s.worker('worker')).toMatchObject({
    paused: 1,
    status: 'offline',
    current_job_id: null,
  });
  const reopened = new Scheduler(storage);
  expect(reopened.register('worker', 'new-host').paused).toBe(1);
  reopened.submit(['true']);
  expect(reopened.claim('worker')).toBeNull();
});

it('leaves queued jobs available to other workers while an idle worker is paused', () => {
  const { scheduler: s } = schedulerStorage();
  s.register('paused', 'one');
  s.register('available', 'two');
  s.setWorkerPaused('paused', true);
  const job = s.submit(['true']);
  expect(s.claim('paused')).toBeNull();
  expect(s.claim('available')?.id).toBe(job.id);
});

it('migrates old worker tables without losing registrations', () => {
  const { sqlite, storage } = schedulerStorage();
  sqlite.exec(`DROP TABLE workers;
    CREATE TABLE workers (worker_id TEXT PRIMARY KEY, hostname TEXT NOT NULL,
      status TEXT NOT NULL, last_heartbeat TEXT NOT NULL, current_job_id TEXT UNIQUE);
    INSERT INTO workers VALUES ('old', 'host', 'idle', '2026-01-01', NULL);`);
  const s = new Scheduler(storage);
  expect(s.worker('old').paused).toBe(0);
  s.setWorkerPaused('old', true);
  expect(new Scheduler(storage).worker('old').paused).toBe(1);
});

it('resolves all registered workers, rejects ambiguity and treats prefixes literally', async () => {
  const { scheduler: s, api } = schedulerStorage();
  s.register('abc-1', 'one');
  s.register('abc-2', 'two');
  const request = (id: string) =>
    api.request(`/workers/${encodeURIComponent(id)}/pause`, { method: 'POST' });
  const conflict = await request('abc');
  expect(conflict.status).toBe(409);
  expect(await conflict.text()).toContain('abc-1, abc-2');
  expect(s.worker('abc-1').paused).toBe(0);
  expect(s.worker('abc-2').paused).toBe(0);
  for (const id of ['missing', '%', '_'])
    expect((await request(id)).status).toBe(404);
  expect((await request('abc-1')).status).toBe(200);
  s.register('abc', 'exact');
  expect(s.setWorkerPaused('abc', true).worker_id).toBe('abc');
  expect(s.worker('abc-2').paused).toBe(0);
});

it('allows only admins to pause and resume', async () => {
  const { scheduler: s, api } = schedulerStorage();
  s.register('worker', 'host');
  const outer = createQueueApi((_queue, req) => api.fetch(req), 'admin');
  const { token } = await issueWorkerToken('admin', 'batch', 3600);
  for (const action of ['pause', 'resume']) {
    for (const [key, status] of [
      [token, 403],
      ['', 401],
      ['admin', 200],
    ] as const) {
      const result = await outer.request(
        `https://example.com/queues/batch/workers/w/${action}`,
        {
          method: 'POST',
          headers: { Authorization: `Bearer ${key}` },
        },
      );
      expect(result.status).toBe(status);
    }
  }
  expect(s.worker('worker').paused).toBe(0);
});
