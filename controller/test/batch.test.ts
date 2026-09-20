import { expect, it } from 'vitest';
import { schedulerStorage } from './storage';

it('urgent preserves order, skips invalid jobs, and deduplicates IDs', async () => {
  const { scheduler: s, api } = schedulerStorage();
  s.register('w', 'host');
  const running = s.submit(['running']);
  s.claim('w');
  const a = s.submit(['a']),
    b = s.submit(['b']),
    c = s.submit(['c']);
  const response = await api.request('/jobs/urgent', {
    method: 'POST',
    body: JSON.stringify({ ids: [c.id, running.id, 'missing', a.id, c.id] }),
  });
  expect(response.status).toBe(200);
  expect(await response.json()).toEqual({
    succeeded: [c.id, a.id],
    failed: [
      { id: running.id, error: expect.any(String) },
      { id: 'missing', error: expect.any(String) },
    ],
  });
  expect(
    s
      .list(100, 0)
      .filter((j) => j.status === 'queued')
      .map((j) => j.id),
  ).toEqual([c.id, a.id, b.id]);
  expect(s.job(running.id).status).toBe('running');
});

it('retry appends successful targets in supplied order and continues past failures', async () => {
  const { scheduler: s, api } = schedulerStorage();
  s.register('w', 'host');
  const a = s.submit(['a']);
  s.claim('w');
  s.finish(a.id, 'w', false, 1, 'failed');
  const b = s.submit(['b']);
  s.claim('w');
  s.finish(b.id, 'w', false, 1, 'failed');
  const queued = s.submit(['queued']);
  const response = await api.request('/jobs/retry', {
    method: 'POST',
    body: JSON.stringify({ ids: [b.id, queued.id, 'missing', a.id, b.id] }),
  });
  expect(response.status).toBe(200);
  expect(await response.json()).toEqual({
    succeeded: [b.id, a.id],
    failed: [
      { id: queued.id, error: expect.any(String) },
      { id: 'missing', error: expect.any(String) },
    ],
  });
  expect(s.list(100, 0).map((j) => j.id)).toEqual([queued.id, b.id, a.id]);
  expect(s.job(a.id)).toMatchObject({
    error: null,
    exit_code: null,
    worker_id: null,
  });
});

it.each(['pause', 'resume'])(
  '%s returns successful workers and per-prefix failures',
  async (action) => {
    const { scheduler: s, api } = schedulerStorage();
    for (const id of [
      'first-full',
      'last-full',
      'ambiguous-a',
      'ambiguous-b',
    ]) {
      s.register(id, 'host');
      if (action === 'resume') s.setWorkerPaused(id, true);
    }
    const response = await api.request(`/workers/${action}`, {
      method: 'POST',
      body: JSON.stringify({
        ids: ['first', 'ambiguous', 'missing', 'last', 'first', 'first-full'],
      }),
    });
    expect(response.status).toBe(200);
    expect(await response.json()).toMatchObject({
      succeeded: [{ worker_id: 'first-full' }, { worker_id: 'last-full' }],
      failed: [
        { id: 'ambiguous', error: expect.stringContaining('Ambiguous') },
        { id: 'missing', error: 'Worker not found' },
      ],
    });
    expect(s.worker('first-full').paused).toBe(action === 'pause' ? 1 : 0);
    expect(s.worker('last-full').paused).toBe(action === 'pause' ? 1 : 0);
    expect(s.worker('ambiguous-a').paused).toBe(action === 'pause' ? 0 : 1);
  },
);

it.each(['/jobs/retry', '/jobs/urgent', '/workers/pause', '/workers/resume'])(
  'validates %s batches before mutations',
  async (path) => {
    const { scheduler: s, api } = schedulerStorage();
    s.register('worker', 'host');
    const job = s.submit(['job']);
    if (path === '/jobs/retry') {
      s.claim('worker');
      s.finish(job.id, 'worker', false, 1, 'failed');
    }
    if (path === '/workers/resume') s.setWorkerPaused('worker', true);
    const id = path.startsWith('/workers/') ? 'worker' : job.id;
    const beforeJob = s.job(job.id);
    const beforeWorker = s.worker('worker');
    for (const body of [
      {},
      { ids: [] },
      { ids: [''] },
      { ids: [1] },
      { ids: [id, ''] },
      { ids: [id, '   '] },
      { ids: [id, 1] },
      { ids: [id, 'x'.repeat(4097)] },
      { ids: Array(101).fill(id) },
    ]) {
      expect(
        (
          await api.request(path, {
            method: 'POST',
            body: JSON.stringify(body),
          })
        ).status,
      ).toBe(400);
      expect(s.job(job.id)).toEqual(beforeJob);
      expect(s.worker('worker')).toEqual(beforeWorker);
    }
    const response = await api.request(path, {
      method: 'POST',
      body: JSON.stringify({ ids: Array(100).fill(id) }),
    });
    expect(response.status).toBe(200);
    expect(await response.json()).toMatchObject({
      succeeded: [expect.anything()],
      failed: [],
    });
  },
);

it.each(['/jobs/retry', '/jobs/urgent', '/workers/pause', '/workers/resume'])(
  '%s returns per-target failures when every target fails',
  async (path) => {
    const { api } = schedulerStorage();
    const response = await api.request(path, {
      method: 'POST',
      body: JSON.stringify({ ids: ['missing-a', 'missing-b', 'missing-a'] }),
    });
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({
      succeeded: [],
      failed: [
        { id: 'missing-a', error: expect.any(String) },
        { id: 'missing-b', error: expect.any(String) },
      ],
    });
  },
);
