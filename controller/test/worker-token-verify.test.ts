import { expect, it } from 'vitest';
import { createQueueApi } from '../src/api/queues';
import { issueWorkerToken } from '../src/api/worker-tokens';

const admin = 'test-admin-secret';
const noForward = () => {
  throw new Error('verification must not access queue storage');
};

it('verifies worker tokens with queue and expiry without returning the token', async () => {
  const api = createQueueApi(noForward, admin);
  const key = await issueWorkerToken(admin, 'batch', 3600);
  const response = await api.request(
    'https://example.com/queues/batch/auth/worker-token/verify',
    { headers: { Authorization: `Bearer ${key.token}` } },
  );
  expect(response.status).toBe(200);
  expect(response.headers.get('Cache-Control')).toBe('no-store');
  expect(await response.json()).toEqual({
    valid: true,
    queue: 'batch',
    expires_at: key.expires_at,
  });
});

it('rejects invalid, expired, tampered, wrong-queue and admin credentials', async () => {
  const api = createQueueApi(noForward, admin);
  const key = await issueWorkerToken(admin, 'batch', 3600);
  const expired = await issueWorkerToken(admin, 'batch', 1, 100);
  const future = await issueWorkerToken(
    admin,
    'batch',
    60,
    Math.floor(Date.now() / 1000) + 3600,
  );
  const otherSigner = await issueWorkerToken('other-secret', 'batch', 60);
  for (const [token, queue, status] of [
    ['', 'batch', 401],
    ['invalid', 'batch', 401],
    [expired.token, 'batch', 401],
    [future.token, 'batch', 401],
    [otherSigner.token, 'batch', 401],
    [key.token + 'tampered', 'batch', 401],
    [key.token, 'other', 403],
    [admin, 'batch', 403],
  ] as const) {
    const response = await api.request(
      `https://example.com/queues/${queue}/auth/worker-token/verify`,
      { headers: token ? { Authorization: `Bearer ${token}` } : {} },
    );
    expect(response.status).toBe(status);
    expect(response.headers.get('Cache-Control')).toBe('no-store');
    if (status === 401)
      expect(response.headers.get('WWW-Authenticate')).toBe('Bearer');
    expect(await response.json()).toHaveProperty('error');
  }
});

it('requires HTTPS and GET, and fails closed without controller authentication', async () => {
  const key = await issueWorkerToken(admin, 'batch', 60);
  const api = createQueueApi(noForward, admin);
  const headers = { Authorization: `Bearer ${key.token}` };
  const path = 'example.com/queues/batch/auth/worker-token/verify';
  expect((await api.request(`http://${path}`, { headers })).status).toBe(400);
  for (const method of ['POST', 'PUT', 'DELETE', 'HEAD']) {
    expect(
      (await api.request(`https://${path}`, { method, headers })).status,
    ).toBe(403);
  }
  const response = await createQueueApi(noForward).request(`https://${path}`, {
    headers,
  });
  expect(response.status).toBe(503);
  expect(response.headers.get('Cache-Control')).toBe('no-store');
});
