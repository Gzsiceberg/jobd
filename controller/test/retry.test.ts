import { expect, it } from 'vitest';
import { schedulerStorage } from './storage';

it('requeues failed jobs with the same IDs behind queued work and resets execution fields', async () => {
  const { scheduler: s, api } = schedulerStorage();
  s.register('w', 'host');
  const a = s.submit(['false']);
  s.claim('w');
  s.output(a.id, 'w', '/tmp/old.log');
  s.cancel(a.id);
  s.finish(a.id, 'w', false, 1, 'failed');
  const b = s.submit(['false']);
  s.claim('w');
  s.finish(b.id, 'w', false, 1, 'failed');
  const queued = s.submit(['true']);
  const post = (path: string) => api.request(path, { method: 'POST' });
  expect((await post(`/jobs/${queued.id}/retry`)).status).toBe(409);
  expect((await post('/jobs/missing/retry')).status).toBe(404);
  expect(await (await post(`/jobs/${a.id}/retry`)).json()).toEqual({ retried: 1 });
  expect(s.job(a.id)).toMatchObject({
    id: a.id, status: 'queued', command: ['false'], created_at: a.created_at,
    started_at: null, finished_at: null, worker_id: null, output_path: null,
    exit_code: null, error: null, cancel_requested: 0,
  });
  expect(await (await post('/jobs/retry-all')).json()).toEqual({ retried: 1 });
  expect(await (await post('/jobs/retry-all')).json()).toEqual({ retried: 0 });
  for (const id of [queued.id, a.id, b.id]) {
    expect(s.claim('w')?.id).toBe(id);
    s.finish(id, 'w', true, 0, null);
  }
  expect(s.retry()).toEqual({ retried: 0 });
});
