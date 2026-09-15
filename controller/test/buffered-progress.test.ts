import { expect, it } from 'vitest';
import { createApi } from '../src/api/router';
import { schedulerStorage } from './storage';

it('ignores heartbeat progress and accepts separate owned progress uploads', async () => {
  const { scheduler: s } = schedulerStorage();
  const api = createApi(s);
  s.register('a', 'host');
  s.register('b', 'other');
  const first = s.submit(['first']);
  const next = s.submit(['next']);
  s.claim('a');
  const beat = (job_id: string, progress: number) =>
    api.request('/workers/a/heartbeat', {
      method: 'POST',
      body: JSON.stringify({ progress: { job_id, progress } }),
    });
  expect((await beat(first.id, 0.5)).status).toBe(200);
  expect(s.job(first.id).progress).toBe(0);
  s.progress(first.id, 'a', 0.5);
  s.cancel(first.id);
  expect(s.heartbeat('a').cancel_job_id).toBe(first.id);
  s.progress(first.id, 'a', 0.25);
  expect(s.job(first.id).progress).toBe(0.5);
  expect(() => s.progress(first.id, 'b', 0.9)).toThrow('not assigned');
  s.finish(first.id, 'a', false, -1, 'Job cancelled by user', 0.75);
  expect(s.job(first.id).progress).toBe(0.75);
  s.claim('a');
  expect((await beat(first.id, 1)).status).toBe(200);
  expect(s.job(first.id).progress).toBe(0.75);
  expect(s.job(next.id).progress).toBe(0);
  s.clear();
  expect((await beat(first.id, 1)).status).toBe(200); // Deleted previous job.
  expect((await beat(next.id, 2)).status).toBe(200); // Ignored legacy field, not an upload.
});

it('flushes progress in failure reports and accepts old heartbeat/report payloads', async () => {
  const { scheduler: s } = schedulerStorage();
  const api = createApi(s);
  s.register('w', 'host');
  const job = s.submit(['false']);
  s.claim('w');
  expect(
    (await api.request('/workers/w/heartbeat', { method: 'POST', body: '{}' }))
      .status,
  ).toBe(200);
  const finish = (progress: number) =>
    api.request(`/jobs/${job.id}/fail`, {
      method: 'POST',
      body: JSON.stringify({
        worker_id: 'w',
        exit_code: 1,
        error: 'failed',
        progress,
      }),
    });
  expect((await finish(2)).status).toBe(400);
  expect((await finish(0.8)).status).toBe(200);
  expect((await finish(0.8)).status).toBe(200);
  expect(s.job(job.id).progress).toBe(0.8);
});
