import { expect, it } from 'vitest';
import { createQueueApi } from '../src/api/queues';
import { createApi } from '../src/api/router';
import type { Scheduler } from '../src/jobs/scheduler';
import { schedulerStorage } from './storage';

it('routes each name to independent state and preserves request bodies', async () => {
  const queues = new Map<string, Scheduler>();
  const api = createQueueApi((name, request) => {
    let scheduler = queues.get(name);
    if (!scheduler) {
      scheduler = schedulerStorage().scheduler;
      queues.set(name, scheduler);
    }
    return createApi(scheduler).fetch(request);
  });
  const post = (path: string, body: unknown) =>
    api.request(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  expect((await post('/queues/alpha/jobs', { command: ['true'] })).status).toBe(
    201,
  );
  for (const name of ['alpha', 'beta']) {
    await post(`/queues/${name}/workers/register`, {
      worker_id: 'worker',
      hostname: 'vm',
    });
    await post(`/queues/${name}/workers/worker/claim`, {});
  }
  const assigned = queues.get('alpha')!.claim('worker')!;
  expect(assigned.status).toBe('running');
  expect(queues.get('beta')!.claim('worker')).toBeNull();
  expect(queues.get('beta')?.worker('worker').current_job_id).toBeNull();
  const id = assigned.id;
  expect((await api.request(`/queues/alpha/jobs/${id}`)).status).toBe(200);
  expect((await api.request(`/queues/beta/jobs/${id}`)).status).toBe(404);
});

it('rejects invalid names and unscoped routes before forwarding', async () => {
  const api = createQueueApi(() => {
    throw new Error('Must not forward');
  });
  for (const name of ['UPPER', 'bad.name', 'a'.repeat(64), 'bad%20name']) {
    expect((await api.request(`/queues/${name}/jobs`)).status).toBe(400);
  }
  expect((await api.request('/jobs')).status).toBe(404);
});
