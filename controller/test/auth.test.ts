import { expect, it } from 'vitest';
import { createQueueApi } from '../src/api/queues';

it('authenticates every method and route before forwarding or validation', async () => {
  let calls = 0;
  const api = createQueueApi(() => {
    calls++;
    return new Response('ok');
  }, 'secret');
  for (const path of [
    '/queues/default/jobs',
    '/queues/default/workers/register',
    '/queues/INVALID/jobs',
    '/unknown',
  ]) {
    for (const method of ['GET', 'POST', 'DELETE', 'OPTIONS', 'HEAD']) {
      for (const authorization of [
        '',
        'Bearer wrong',
        'secret',
        'Basic secret',
      ]) {
        const response = await api.request(path, {
          method,
          headers: { Authorization: authorization },
        });
        expect(response.status).toBe(401);
      }
    }
  }
  expect(calls).toBe(0);
  expect(
    (
      await api.request('/queues/default/jobs', {
        headers: { Authorization: 'Bearer secret' },
      })
    ).status,
  ).toBe(200);
  expect(calls).toBe(1);
});

it('fails closed when the controller key is absent or empty', async () => {
  for (const key of [undefined, '', '   ']) {
    const api = createQueueApi(() => {
      throw new Error('must not forward');
    }, key);
    expect((await api.request('/queues/default/jobs')).status).toBe(503);
  }
});
