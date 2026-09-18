import { expect, it } from 'vitest';
import { createQueueApi } from '../src/api/queues';
import { QueueSecrets } from '../src/secrets';
import { schedulerStorage } from './storage';

it('atomically removes all non-running jobs only in the selected authenticated queue', async () => {
  const a = schedulerStorage();
  const b = schedulerStorage();
  const api = createQueueApi(
    (name, request) => (name === 'a' ? a : b).api.fetch(request),
    'auth',
  );
  const secrets = new QueueSecrets(a.storage, 'a', btoa('k'.repeat(32)));
  await secrets.set('API_KEY', 'keep');
  const s = a.scheduler;
  s.register('w', 'host');
  for (const success of [true, false]) {
    const job = s.submit(['true']);
    s.claim('w');
    s.finish(job.id, 'w', success, success ? 0 : 1, success ? null : 'failed');
  }
  const running = s.submit(['sleep', '60']);
  s.claim('w');
  s.cancel(running.id);
  s.output(running.id, 'w', '/tmp/keep.log');
  let latest = running;
  for (let i = 0; i < 150; i++) latest = s.submit(['true']);
  const other = b.scheduler.submit(['untouched']);
  const request = (key: string) =>
    api.request('https://test/queues/a/jobs/remove-all', {
      method: 'POST',
      headers: { Authorization: `Bearer ${key}` },
    });
  expect((await request('wrong')).status).toBe(401);
  const response = await request('auth');
  expect(response.status).toBe(200);
  expect(await response.json()).toEqual({ removed: 152, kept_running: 1 });
  expect(s.list(100, 0)).toHaveLength(1);
  expect(s.job(running.id)).toMatchObject({
    status: 'running',
    cancel_requested: 1,
    output_path: '/tmp/keep.log',
  });
  expect(s.worker('w').current_job_id).toBe(running.id);
  expect(b.scheduler.job(other.id).status).toBe('queued');
  expect(await secrets.environment()).toEqual({ API_KEY: 'keep' });
  expect(await (await request('auth')).json()).toEqual({
    removed: 0,
    kept_running: 1,
  });
  s.finish(running.id, 'w', true, 0, null);
  expect(await (await request('auth')).json()).toEqual({
    removed: 1,
    kept_running: 0,
  });
  expect(await (await request('auth')).json()).toEqual({
    removed: 0,
    kept_running: 0,
  });
  expect(Number(s.submit(['next']).id)).toBeGreaterThan(Number(latest.id));
});
