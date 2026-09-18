import { expect, it } from 'vitest';
import { Scheduler } from '../src/jobs/scheduler';
import { schedulerStorage } from './storage';

it('preserves legacy jobs without exposing or updating the retired field', () => {
  const { storage, scheduler: s } = schedulerStorage();
  storage.sql.exec(
    'ALTER TABLE jobs ADD COLUMN progress REAL NOT NULL DEFAULT 0',
  );
  s.register('w', 'host');
  const saved = s.submit(['true']);
  storage.sql.exec('UPDATE jobs SET progress = 0.5 WHERE id = ?', saved.id);
  const reopened = new Scheduler(storage);
  expect(reopened.job(saved.id)).not.toHaveProperty('progress');
  expect(reopened.list(100, 0)[0]).toEqual(saved);
  expect(reopened.claim('w')?.id).toBe(saved.id);
  expect(reopened.finish(saved.id, 'w', true, 0, null).status).toBe(
    'succeeded',
  );
  expect(reopened.submit(['next']).id).not.toBe(saved.id);
  expect(
    storage.sql
      .exec('SELECT progress FROM jobs WHERE id = ?', saved.id)
      .toArray()[0].progress,
  ).toBe(0.5);
});

it('omits the retired field from job responses and removes its endpoint', async () => {
  const { api, scheduler: s } = schedulerStorage();
  s.register('w', 'host');
  const job = s.submit(['true']);
  s.claim('w');
  const response = await api.request(`/jobs/${job.id}/progress`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ worker_id: 'w', progress: 0.5 }),
  });
  expect(response.status).toBe(404);
  const completed = await api.request(`/jobs/${job.id}/complete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ worker_id: 'w', exit_code: 0 }),
  });
  expect(completed.status).toBe(200);
  expect(await completed.json()).not.toHaveProperty('progress');
});
