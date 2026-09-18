import { expect, it } from 'vitest';
import { createQueueApi } from '../src/api/queues';
import { issueWorkerKey } from '../src/api/worker-keys';

const admin = 'test-admin-secret';
const noForward = () => {
  throw new Error('verification must not access queue storage');
};

it('verifies worker tokens with queue and expiry without returning the token', async () => {
  const api = createQueueApi(noForward, admin);
  const key = await issueWorkerKey(admin, 'batch', 3600);
  const response = await api.request(
    'https://example.com/queues/batch/auth/worker-key/verify',
    { headers: { Authorization: `Bearer ${key.api_key}` } },
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
  const key = await issueWorkerKey(admin, 'batch', 3600);
  const expired = await issueWorkerKey(admin, 'batch', 1, 100);
  const future = await issueWorkerKey(
    admin,
    'batch',
    60,
    Math.floor(Date.now() / 1000) + 3600,
  );
  const otherSigner = await issueWorkerKey('other-secret', 'batch', 60);
  for (const [token, queue, status] of [
    ['', 'batch', 401],
    ['invalid', 'batch', 401],
    [expired.api_key, 'batch', 401],
    [future.api_key, 'batch', 401],
    [otherSigner.api_key, 'batch', 401],
    [key.api_key + 'tampered', 'batch', 401],
    [key.api_key, 'other', 403],
    [admin, 'batch', 403],
  ] as const) {
    const response = await api.request(
      `https://example.com/queues/${queue}/auth/worker-key/verify`,
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
  const key = await issueWorkerKey(admin, 'batch', 60);
  const api = createQueueApi(noForward, admin);
  const headers = { Authorization: `Bearer ${key.api_key}` };
  const path = 'example.com/queues/batch/auth/worker-key/verify';
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
