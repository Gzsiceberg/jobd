import { expect, it } from 'vitest';
import { Scheduler } from '../src/jobs/scheduler';
import { schedulerStorage } from './storage';

it('persists individual rows across scheduler instances and does not write on reads', () => {
  const { sqlite, storage, scheduler } = schedulerStorage();
  const first = scheduler.submit(['echo', 'first']);
  const second = scheduler.submit(['echo', 'second']);
  scheduler.register('worker', 'host');
  const reloaded = new Scheduler(storage);
  const before = sqlite.prepare('SELECT total_changes() AS count').all();
  expect(reloaded.job(first.id)).toEqual(first);
  expect(reloaded.worker('worker').hostname).toBe('host');
  expect(sqlite.prepare('SELECT total_changes() AS count').all()).toEqual(
    before,
  );
  expect(reloaded.claim('worker')?.id).toBe(first.id);
  expect(reloaded.job(second.id)).toEqual(second);
  reloaded.register('worker', 'new-host');
  expect(reloaded.claim('worker')?.id).toBe(first.id);
  reloaded.finish(first.id, 'worker', true, 0, null);
  expect(reloaded.claim('worker')?.id).toBe(second.id);
});

it('rolls back a claim when updating its worker fails', () => {
  const { sqlite, scheduler } = schedulerStorage();
  const job = scheduler.submit(['true']);
  scheduler.register('worker', 'host');
  sqlite.exec(`CREATE TRIGGER reject_assignment BEFORE UPDATE OF current_job_id ON workers
    BEGIN SELECT RAISE(ABORT, 'test failure'); END`);
  expect(() => scheduler.claim('worker')).toThrow('test failure');
  expect(scheduler.job(job.id)).toEqual(job);
  expect(scheduler.worker('worker').current_job_id).toBeNull();
});
