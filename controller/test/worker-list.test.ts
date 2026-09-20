import { expect, it, vi } from 'vitest';
import { createQueueApi } from '../src/api/queues';
import { issueWorkerToken } from '../src/api/worker-tokens';
import { Scheduler, workerTimeoutMs } from '../src/jobs/scheduler';
import { schedulerStorage } from './storage';

it('records verified expiry, refreshes it, persists it, and restricts listing to admins', async () => {
  const { api, scheduler, storage } = schedulerStorage();
  const gateway = createQueueApi(
    (_name, request) => api.fetch(request),
    'admin',
  );
  const first = await issueWorkerToken('admin', 'batch', 3600);
  const second = await issueWorkerToken('admin', 'batch', 7200);
  const request = (path: string, token: string, body?: unknown) =>
    gateway.request(`https://example.com/queues/batch${path}`, {
      method: body === undefined ? 'GET' : 'POST',
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
        'X-Jobd-Token-Expires-At': 'forged',
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  expect(
    (
      await request('/workers/register', first.token, {
        worker_id: 'w1',
        hostname: 'host',
      })
    ).status,
  ).toBe(200);
  expect(scheduler.worker('w1').token_expired_time).toBeNull();
  const heartbeat = await request('/workers/w1/heartbeat', first.token, {});
  expect(heartbeat.status).toBe(200);
  expect(await heartbeat.json()).toMatchObject({
    token_expired_time: first.expires_at,
  });
  expect(scheduler.worker('w1').token_expired_time).toBe(first.expires_at);
  expect((await request('/workers/w1/claim', second.token, {})).status).toBe(
    200,
  );
  expect(scheduler.worker('w1').token_expired_time).toBe(first.expires_at);
  expect(
    (await request('/workers/w1/heartbeat', second.token, {})).status,
  ).toBe(200);
  expect(new Scheduler(storage).worker('w1').token_expired_time).toBe(
    second.expires_at,
  );
  expect((await request('/workers', first.token)).status).toBe(403);
  expect((await request('/workers', 'invalid')).status).toBe(401);
  await request('/workers/register', 'admin', {
    worker_id: 'w2',
    hostname: 'other',
  });
  expect((await request('/workers/w1/heartbeat', 'admin', {})).status).toBe(
    200,
  );
  expect((await request('/workers/w2/heartbeat', 'admin', {})).status).toBe(
    200,
  );
  const response = await request('/workers', 'admin');
  expect(response.status).toBe(200);
  expect(await response.json()).toMatchObject({
    workers: [
      {
        worker_id: 'w1',
        hostname: 'host',
        token_expired_time: second.expires_at,
      },
      { worker_id: 'w2', hostname: 'other', token_expired_time: null },
    ],
  });
});

it('lists only online workers, including idle, busy and paused workers', async () => {
  const now = Date.now();
  const clock = vi.spyOn(Date, 'now').mockReturnValue(now);
  try {
    const { api, scheduler, storage } = schedulerStorage();
    for (const id of ['idle', 'busy', 'paused', 'offline', 'boundary']) {
      scheduler.register(id, id);
    }
    scheduler.submit(['echo', 'test']);
    scheduler.claim('busy');
    scheduler.setWorkerPaused('paused', true);
    for (const [id, age] of [
      ['idle', workerTimeoutMs - 1],
      ['offline', workerTimeoutMs + 1],
      ['boundary', workerTimeoutMs],
    ] as const) {
      storage.sql.exec(
        'UPDATE workers SET last_heartbeat = ? WHERE worker_id = ?',
        new Date(now - age).toISOString(),
        id,
      );
    }
    const response = await api.request('/workers');
    expect(response.status).toBe(200);
    const body = await response.json<{ workers: { worker_id: string }[] }>();
    expect(body.workers.map((worker) => worker.worker_id)).toEqual([
      'busy',
      'idle',
      'paused',
    ]);
    expect(scheduler.worker('offline').status).toBe('offline');
    scheduler.heartbeat('offline');
    expect(scheduler.listWorkers().map((worker) => worker.worker_id)).toContain(
      'offline',
    );
  } finally {
    clock.mockRestore();
  }
});

it('migrates existing workers without losing them', () => {
  const { storage, scheduler, sqlite } = schedulerStorage();
  scheduler.register('old', 'legacy');
  sqlite.exec('ALTER TABLE workers DROP COLUMN token_expired_time');
  expect(new Scheduler(storage).listWorkers()).toMatchObject([
    { worker_id: 'old', token_expired_time: null },
  ]);
});
