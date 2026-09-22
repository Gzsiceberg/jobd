import { expect, it } from 'vitest';
import { Scheduler } from '../src/jobs/scheduler';
import { schedulerStorage } from './storage';

it('batch removal deduplicates, continues after failures, and preserves IDs', async () => {
  const { scheduler: s, api, storage } = schedulerStorage();
  s.register('w', 'host');
  const finished = s.submit(['finished']);
  s.claim('w');
  s.finish(finished.id, 'w', true, 0, null);
  const running = s.submit(['running']);
  s.claim('w');
  s.cancel(running.id);
  const queued = s.submit(['queued']);
  const response = await api.request('/jobs/remove', {
    method: 'POST',
    body: JSON.stringify({
      ids: [finished.id, running.id, 'missing', queued.id, finished.id],
    }),
  });
  expect(response.status).toBe(200);
  expect(await response.json()).toEqual({
    succeeded: [finished.id, queued.id],
    failed: [
      { id: running.id, error: expect.any(String) },
      { id: 'missing', error: expect.any(String) },
    ],
  });
  expect(s.list(100, 0)).toEqual([
    expect.objectContaining({
      id: running.id,
      status: 'running',
      cancel_requested: 1,
    }),
  ]);
  expect(s.worker('w').current_job_id).toBe(running.id);
  s.finish(running.id, 'w', true, 0, null);
  const last = await api.request('/jobs/remove', {
    method: 'POST',
    body: JSON.stringify({ ids: [running.id] }),
  });
  expect(await last.json()).toEqual({ succeeded: [running.id], failed: [] });
  expect(s.list(100, 0)).toEqual([]);
  expect(new Scheduler(storage).submit(['next']).id).toBe('4');
});
