import { expect, it } from 'vitest';
import { Scheduler } from '../src/jobs/scheduler';
import { schedulerStorage } from './storage';

it('allocates per-queue increasing IDs without resetting on individual deletion', () => {
  const { scheduler: s, storage } = schedulerStorage();
  expect(s.submit(['a']).id).toBe('1');
  expect(s.submit(['b']).id).toBe('2');
  s.remove('2');
  expect(new Scheduler(storage).submit(['c']).id).toBe('3');
  s.remove('1');
  s.remove('3');
  expect(s.submit(['d']).id).toBe('4');
  expect(schedulerStorage().scheduler.submit(['other queue']).id).toBe('1');
});

it('clears finished records and resets empty queue IDs, including after reopening', async () => {
  const { scheduler: s, storage, api } = schedulerStorage();
  s.register('w', 'host');
  for (const success of [true, false]) {
    const job = s.submit(['test']);
    s.claim('w');
    s.finish(job.id, 'w', success, success ? 0 : 1, success ? null : 'failed');
  }
  expect((await api.request('/jobs/clear', { method: 'POST' })).status).toBe(
    200,
  );
  expect(s.list(100, 0)).toEqual([]);
  expect(new Scheduler(storage).submit(['new']).id).toBe('1');
  // Clearing an already empty queue also resets its counter.
  s.remove('1');
  s.clear();
  s.clear();
  expect(s.submit(['again']).id).toBe('1');
  expect(s.worker('w').current_job_id).toBeNull();
});

it('does not reset IDs or delete queued/running jobs when clearing', () => {
  const { scheduler: s } = schedulerStorage();
  s.register('w', 'host');
  const running = s.submit(['running']);
  s.claim('w');
  const queued = s.submit(['queued']);
  s.clear();
  expect(s.job(running.id).status).toBe('running');
  expect(s.job(queued.id).status).toBe('queued');
  expect(s.submit(['next']).id).toBe('3');
  s.finish(running.id, 'w', true, 0, null);
  s.clear();
  expect(s.list(100, 0).map((job) => job.id)).toEqual(['2', '3']);
  expect(s.submit(['more']).id).toBe('4');
});

it('reopens without rewriting job IDs, assignments, metadata or queue order', () => {
  const { scheduler: s, storage, sqlite } = schedulerStorage();
  s.register('w', 'host');
  const a = s.submit(['a']);
  const b = s.submit(['b']);
  const c = s.submit(['c']);
  s.claim('w');
  s.cancel(a.id);
  s.output(a.id, 'w', '/tmp/existing.log');
  s.reorder(c.id);
  const before = s.list(100, 0);
  const reopened = new Scheduler(storage);
  expect(reopened.list(100, 0)).toEqual(before);
  expect(reopened.list(100, 0).map((job) => job.id)).toEqual([
    a.id,
    c.id,
    b.id,
  ]);
  expect(reopened.worker('w').current_job_id).toBe(a.id);
  expect(reopened.claim('w')?.id).toBe(a.id);
  expect(reopened.submit(['d']).id).toBe('4');
  expect(
    sqlite
      .prepare("SELECT name FROM sqlite_master WHERE name = 'jobd_migrations'")
      .all(),
  ).toEqual([]);
});
