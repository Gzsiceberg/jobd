import { expect, it } from 'vitest';
import { createQueueApi } from '../src/api/queues';
import {
  issueWorkerKey,
  verifyWorkerKey,
  maxWorkerKeySeconds,
} from '../src/api/worker-keys';

const admin = 'test-admin-secret';

it('issues queue-scoped keys only to admins over HTTPS, without forwarding', async () => {
  const api = createQueueApi(() => {
    throw new Error('must not forward');
  }, admin);
  const request = (
    body: unknown,
    token = admin,
    protocol = 'https',
    queue = 'batch',
  ) =>
    api.request(`${protocol}://example.com/queues/${queue}/auth/worker-key`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(body),
    });
  const response = await request({ duration_seconds: 3600 });
  expect(response.status).toBe(201);
  expect(response.headers.get('Cache-Control')).toBe('no-store');
  const result = await response.json<{
    api_key: string;
    expires_at: string;
    queue: string;
  }>();
  const claims = await verifyWorkerKey(admin, result.api_key);
  expect(claims).toMatchObject({ queue: 'batch', role: 'worker' });
  expect(claims!.exp - claims!.iat).toBe(3600);
  expect(result.expires_at).toBe(new Date(claims!.exp * 1000).toISOString());
  expect((await request({ duration_seconds: 1 }, result.api_key)).status).toBe(
    403,
  );
  expect(
    (await request({ duration_seconds: 1 }, 'old-shared-key')).status,
  ).toBe(401);
  expect((await request({ duration_seconds: 1 }, admin, 'http')).status).toBe(
    400,
  );
  expect(
    (await request({ duration_seconds: 1 }, admin, 'https', 'INVALID')).status,
  ).toBe(400);
  for (const body of [
    {},
    { duration_seconds: 0 },
    { duration_seconds: -1 },
    { duration_seconds: 1.5 },
    { duration_seconds: '24h' },
    { duration_seconds: maxWorkerKeySeconds + 1 },
    { duration_seconds: 10, role: 'admin' },
  ]) {
    expect((await request(body)).status).toBe(400);
  }
  expect(
    (await request({ duration_seconds: maxWorkerKeySeconds })).status,
  ).toBe(201);
});

it('rejects altered, expired, future-issued and wrong-secret tokens', async () => {
  const { api_key: key } = await issueWorkerKey(admin, 'batch', 10, 100);
  expect(await verifyWorkerKey(admin, key, 100)).not.toBeNull();
  expect(await verifyWorkerKey(admin, key, 109)).not.toBeNull();
  expect(await verifyWorkerKey(admin, key, 110)).toBeNull();
  expect(await verifyWorkerKey(admin, key, 99)).toBeNull();
  expect(await verifyWorkerKey('different', key, 100)).toBeNull();
  const parts = key.split('.');
  const claims = JSON.parse(
    atob(parts[1].replaceAll('-', '+').replaceAll('_', '/')),
  ) as Record<string, unknown>;
  for (const changes of [
    { queue: 'other' },
    { role: 'admin' },
    { exp: 10000 },
  ]) {
    const altered = btoa(JSON.stringify({ ...claims, ...changes }))
      .replaceAll('+', '-')
      .replaceAll('/', '_')
      .replace(/=+$/, '');
    expect(
      await verifyWorkerKey(admin, `${parts[0]}.${altered}.${parts[2]}`, 100),
    ).toBeNull();
  }
  for (const token of [
    '',
    'secret',
    key + '.extra',
    key.slice(0, -10),
    'x'.repeat(3000),
  ]) {
    expect(await verifyWorkerKey(admin, token, 100)).toBeNull();
  }
});

it('allows only job reads and worker lifecycle, scoped to one queue', async () => {
  let calls = 0;
  const api = createQueueApi(() => {
    calls++;
    return new Response('ok');
  }, admin);
  const { api_key: key } = await issueWorkerKey(admin, 'batch', 60);
  const request = (
    method: string,
    path: string,
    token = key,
    queue = 'batch',
  ) =>
    api.request(`https://example.com/queues/${queue}${path}`, {
      method,
      headers: { Authorization: `Bearer ${token}` },
    });
  for (const path of [
    '/jobs',
    '/jobs?limit=10',
    '/jobs/1',
    '/jobs/latest?kind=run',
  ]) {
    expect((await request('GET', path)).status).toBe(200);
  }
  for (const path of [
    '/workers/register',
    '/workers/worker-1/heartbeat',
    '/workers/worker-1/claim',
    '/jobs/1/output',
    '/jobs/1/complete',
    '/jobs/1/fail',
  ]) {
    expect((await request('POST', path)).status).toBe(200);
  }
  const allowedCalls = calls;
  for (const [method, path] of [
    ['POST', '/jobs'],
    ['POST', '/jobs/clear'],
    ['POST', '/jobs/remove-all'],
    ['POST', '/jobs/swap'],
    ['DELETE', '/jobs/1'],
    ['POST', '/jobs/1/cancel'],
    ['POST', '/jobs/1/urgent'],
    ['GET', '/env'],
    ['PUT', '/env/SECRET'],
    ['DELETE', '/env/SECRET'],
    ['POST', '/auth/worker-key'],
    ['GET', '/unknown'],
    ['HEAD', '/jobs'],
    ['POST', '/jobs/%31/complete'],
    ['POST', '/workers/a%2Fclaim/claim'],
  ]) {
    expect((await request(method, path)).status).toBe(403);
  }
  expect((await request('GET', '/jobs', key, 'other')).status).toBe(403);
  expect(calls).toBe(allowedCalls);
  const expired = await issueWorkerKey(admin, 'batch', 1, 100);
  expect(
    (await request('POST', '/jobs/1/complete', expired.api_key)).status,
  ).toBe(401);
  expect(calls).toBe(allowedCalls);
  for (const [method, path] of [
    ['POST', '/jobs'],
    ['DELETE', '/jobs/1'],
    ['PUT', '/env/SECRET'],
  ]) {
    expect((await request(method, path, admin)).status).toBe(200);
  }
});
