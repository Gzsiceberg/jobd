import { expect, it } from 'vitest';
import { Scheduler } from '../src/jobs/scheduler';
import { schedulerStorage } from './storage';

it('allocates per-queue increasing IDs and never reuses deleted IDs', () => {
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

it('reopens without rewriting job IDs, assignments, metadata or queue order', () => {
  const { scheduler: s, storage, sqlite } = schedulerStorage();
  s.register('w', 'host');
  const a = s.submit(['a']);
  const b = s.submit(['b']);
  const c = s.submit(['c']);
  s.claim('w');
  s.progress(a.id, 'w', 0.5);
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
