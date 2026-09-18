import { describe, expect, it } from 'vitest';
import { Scheduler } from '../src/jobs/scheduler';
import { schedulerStorage } from './storage';

describe('queue management', () => {
  it('orders queued jobs without changing submission history and swaps atomically', () => {
    const { scheduler: s } = schedulerStorage();
    s.register('w', 'host');
    const a = s.submit(['a']),
      b = s.submit(['b']),
      c = s.submit(['c']);
    s.reorder(c.id);
    expect(s.list(100, 0).map((j) => j.id)).toEqual([c.id, a.id, b.id]);
    s.reorder(c.id, b.id);
    expect(s.list(100, 0).map((j) => j.id)).toEqual([b.id, a.id, c.id]);
    expect(s.latest('added').id).toBe(c.id);
    expect(s.claim('w')?.id).toBe(b.id);
    expect(() => s.reorder(a.id, b.id)).toThrow('Only queued');
    expect(() => s.remove(b.id)).toThrow('running');
    expect(s.list(1, 1)[0].id).toBe(a.id);
    s.remove(a.id);
    s.clear();
    expect(s.list(100, 0)).toHaveLength(2);
    s.finish(b.id, 'w', true, 0, null);
    s.clear();
    expect(s.list(100, 0).map((j) => j.id)).toEqual([c.id]);
  });

  it('reports output with ownership checks and retains it after completion', async () => {
    const { scheduler: s, api } = schedulerStorage();
    s.register('w', 'host');
    const j = s.submit(['sleep', '10']);
    expect(() => s.latest('run')).toThrow('No matching');
    s.claim('w');
    expect(() => s.output(j.id, 'other', '/tmp/no')).toThrow('not assigned');
    const response = await api.request(`/jobs/${j.id}/output`, {
      method: 'POST',
      body: JSON.stringify({ worker_id: 'w', output_path: '/tmp/log' }),
    });
    expect(response.status).toBe(200);
    expect(s.latest('run')).toMatchObject({
      id: j.id,
      hostname: 'host',
      output_path: '/tmp/log',
    });
    s.finish(j.id, 'w', true, 0, null);
    expect(s.job(j.id).output_path).toBe('/tmp/log');
    expect(() => s.output(j.id, 'w', '/tmp/other')).toThrow('not running');
  });

  it('exposes management routes and validates pagination', async () => {
    const { scheduler: s, api } = schedulerStorage();
    const a = s.submit(['a']),
      b = s.submit(['b']);
    expect((await api.request('/jobs?limit=101')).status).toBe(400);
    expect((await api.request('/jobs?offset=-1')).status).toBe(400);
    expect((await api.request('/jobs/latest?kind=no')).status).toBe(400);
    expect((await api.request('/jobs/latest')).status).toBe(200);
    expect(
      (
        await api.request('/jobs/swap', {
          method: 'POST',
          body: JSON.stringify({ first: a.id, second: b.id }),
        })
      ).status,
    ).toBe(200);
    expect(s.list(100, 0)[0].id).toBe(b.id);
    expect(
      (await api.request(`/jobs/${a.id}/urgent`, { method: 'POST' })).status,
    ).toBe(200);
    expect(s.list(100, 0)[0].id).toBe(a.id);
    expect(
      (await api.request(`/jobs/${a.id}`, { method: 'DELETE' })).status,
    ).toBe(200);
    expect((await api.request('/jobs/clear', { method: 'POST' })).status).toBe(
      200,
    );
    expect((await api.request('/jobs')).status).toBe(200);
  });

  it('initializes the current schema and reopens without changing FIFO order', () => {
    const { scheduler: s, storage } = schedulerStorage();
    const a = s.submit(['a']),
      b = s.submit(['b']);
    const reopened = new Scheduler(storage);
    new Scheduler(storage);
    expect(reopened.list(100, 0).map((j) => j.id)).toEqual([a.id, b.id]);
    expect(reopened.job(a.id).output_path).toBeNull();
    expect(reopened.job(a.id).cancel_requested).toBe(0);
    reopened.register('w', 'host');
    expect(reopened.claim('w')?.id).toBe(a.id);
  });
});
