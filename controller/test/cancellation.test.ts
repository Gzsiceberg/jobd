import { expect, it } from 'vitest';
import { createApi } from '../src/api/router';
import { Scheduler } from '../src/jobs/scheduler';
import { schedulerStorage } from './storage';

it('persists cancellation until the owner reports, without releasing the running slot', async () => {
  const { scheduler: s, storage } = schedulerStorage();
  const api = createApi(s);
  s.register('a', 'host-a');
  s.register('b', 'host-b');
  const job = s.submit(['sleep', '60']);
  const next = s.submit(['true']);
  expect(
    (await api.request(`/jobs/${job.id}/cancel`, { method: 'POST' })).status,
  ).toBe(409);
  s.claim('a');
  expect(
    (await api.request(`/jobs/${job.id}/cancel`, { method: 'POST' })).status,
  ).toBe(200);
  s.cancel(job.id);
  const reopened = new Scheduler(storage);
  expect(reopened.heartbeat('a').cancel_job_id).toBe(job.id);
  expect(reopened.heartbeat('b').cancel_job_id).toBeNull();
  expect(reopened.claim('a')?.id).toBe(job.id);
  expect(reopened.job(job.id)).toMatchObject({
    status: 'running',
    cancel_requested: 1,
  });
  expect(() => reopened.remove(job.id)).toThrow('running');
  reopened.clear();
  expect(() =>
    reopened.finish(job.id, 'b', false, -1, 'Job cancelled by user'),
  ).toThrow('not assigned');
  reopened.progress(job.id, 'a', 0.5);
  reopened.progress(job.id, 'a', 0.25);
  reopened.finish(job.id, 'a', false, -1, 'Job cancelled by user');
  reopened.finish(job.id, 'a', false, -1, 'Job cancelled by user');
  expect(reopened.job(job.id)).toMatchObject({
    status: 'failed',
    error: 'Job cancelled by user',
    progress: 0.5,
  });
  expect(reopened.heartbeat('a').cancel_job_id).toBeNull();
  expect(reopened.claim('a')?.id).toBe(next.id);
});

it('preserves the real outcome if completion wins the cancellation race', () => {
  const { scheduler: s } = schedulerStorage();
  s.register('w', 'host');
  const j = s.submit(['true']);
  s.claim('w');
  s.cancel(j.id);
  s.finish(j.id, 'w', true, 0, null);
  expect(s.job(j.id)).toMatchObject({ status: 'succeeded', progress: 1 });
  expect(() => s.cancel(j.id)).toThrow('Only running');
});
