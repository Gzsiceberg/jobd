import { afterEach, expect, it, vi } from 'vitest';
import { Scheduler, workerTimeoutMs } from '../src/jobs/scheduler';
import { schedulerStorage } from './storage';

afterEach(() => vi.useRealTimers());

it.each(['clear', 'remove', 'removeAll'] as const)(
  '%s expires stale assignments without requiring a list, preserving healthy running jobs',
  (action) => {
    vi.useFakeTimers();
    const { scheduler: s } = schedulerStorage();
    s.register('stale', 'old-host');
    s.register('healthy', 'live-host');
    const stale = s.submit(['sleep', '600']);
    const healthy = s.submit(['sleep', '600']);
    const queued = s.submit(['true']);
    s.claim('stale');
    s.claim('healthy');
    s.cancel(stale.id);
    vi.advanceTimersByTime(workerTimeoutMs - 1);
    s.heartbeat('healthy');
    vi.advanceTimersByTime(1);
    if (action === 'remove') s.remove(stale.id);
    else if (action === 'clear') s.clear();
    else expect(s.removeAll()).toEqual({ removed: 2, kept_running: 1 });
    expect(() => s.job(stale.id)).toThrow('Job not found');
    expect(s.worker('stale')).toMatchObject({
      status: 'offline',
      current_job_id: null,
    });
    expect(s.job(healthy.id).status).toBe('running');
    expect(s.worker('healthy').current_job_id).toBe(healthy.id);
    expect(() => s.remove(healthy.id)).toThrow('Cannot remove a running job');
    if (action !== 'removeAll') expect(s.job(queued.id).status).toBe('queued');
    expect(() => s.finish(stale.id, 'stale', false, 1, 'late')).toThrow(
      'Job not found',
    );
  },
);

it('listing expires running and cancelling jobs at two minutes without retrying or losing metadata', async () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-01-01T00:00:00Z'));
  const { scheduler: s, storage, api } = schedulerStorage();
  s.register('a', 'host-a');
  s.register('b', 'host-b');
  s.register('idle', 'host-idle');
  const first = s.submit(['sleep', '600']);
  const second = s.submit(['sleep', '600']);
  const queued = s.submit(['true']);
  s.claim('a');
  s.claim('b');
  s.output(first.id, 'a', '/tmp/log');
  s.cancel(second.id);
  const deadline = Date.now() + workerTimeoutMs;
  vi.setSystemTime(deadline - 1);
  s.list(100, 0);
  expect(s.job(first.id).status).toBe('running');
  expect(s.job(second.id).cancel_requested).toBe(1);
  vi.setSystemTime(deadline);
  // Listing expires all stale assignments, not just this page's jobs.
  const response = await api.request('/jobs?limit=1&offset=1');
  expect(response.status).toBe(200);
  const reopened = new Scheduler(storage);
  for (const id of [first.id, second.id]) {
    expect(reopened.job(id)).toMatchObject({
      status: 'failed',
      error: 'Worker disconnected; outcome unknown',
      exit_code: null,
      cancel_requested: 0,
      finished_at: new Date(deadline).toISOString(),
    });
  }
  expect(reopened.job(first.id)).toMatchObject({
    worker_id: 'a',
    hostname: 'host-a',
    output_path: '/tmp/log',
  });
  for (const id of ['a', 'b', 'idle']) {
    expect(reopened.worker(id)).toMatchObject({
      status: 'offline',
      current_job_id: null,
    });
  }
  expect(reopened.job(queued.id).status).toBe('queued');
  vi.advanceTimersByTime(workerTimeoutMs);
  reopened.list(100, 0);
  expect(reopened.job(first.id).finished_at).toBe(
    new Date(deadline).toISOString(),
  );
});

it('heartbeats protect healthy workers and late reports cannot affect a new assignment', async () => {
  vi.useFakeTimers();
  const { scheduler: s, api } = schedulerStorage();
  s.register('a', 'host');
  const job = s.submit(['sleep', '600']);
  s.claim('a');
  vi.advanceTimersByTime(workerTimeoutMs - 1);
  s.heartbeat('a');
  vi.advanceTimersByTime(workerTimeoutMs - 1);
  s.list(100, 0);
  expect(s.job(job.id).status).toBe('running');
  vi.advanceTimersByTime(1);
  s.list(100, 0);
  expect(s.heartbeat('a')).toMatchObject({
    status: 'idle',
    current_job_id: null,
    cancel_job_id: null,
  });
  expect(s.job(job.id).status).toBe('failed');
  const next = s.submit(['true']);
  expect(s.claim('a')?.id).toBe(next.id);
  for (const success of [true, false]) {
    expect(() => s.finish(job.id, 'a', success, 0, 'late result')).toThrow(
      'disconnected',
    );
    expect(s.worker('a').current_job_id).toBe(next.id);
  }
  const late = await api.request(`/jobs/${job.id}/fail`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      worker_id: 'a',
      error: 'late failure',
      exit_code: 1,
    }),
  });
  expect(late.status).toBe(409);
  expect(() => s.output(job.id, 'a', '/tmp/late')).toThrow('not running');
  s.finish(next.id, 'a', true, 0, null);
  s.finish(next.id, 'a', true, 0, null);
});

it('does not expire jobs on other APIs; reconnecting before a list preserves the assignment', () => {
  vi.useFakeTimers();
  const { scheduler: s } = schedulerStorage();
  s.register('a', 'host');
  const job = s.submit(['sleep', '600']);
  s.claim('a');
  vi.advanceTimersByTime(workerTimeoutMs);
  expect(s.job(job.id).status).toBe('running');
  expect(s.latest('run').status).toBe('running');
  s.output(job.id, 'a', '/tmp/log');
  s.cancel(job.id);
  expect(s.heartbeat('a').cancel_job_id).toBe(job.id);
  s.list(100, 0);
  expect(s.job(job.id).status).toBe('running');
  vi.advanceTimersByTime(workerTimeoutMs);
  expect(s.register('a', 'host').current_job_id).toBe(job.id);
  vi.advanceTimersByTime(workerTimeoutMs);
  expect(s.claim('a')?.id).toBe(job.id);
  vi.advanceTimersByTime(workerTimeoutMs);
  s.finish(job.id, 'a', false, 1, 'real failure');
  s.list(100, 0);
  expect(s.job(job.id)).toMatchObject({
    status: 'failed',
    error: 'real failure',
    exit_code: 1,
  });
});
