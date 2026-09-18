import { describe, expect, it } from 'vitest';
import { z } from 'zod';
import { schedulerStorage } from './storage';

function setup() {
  const { scheduler, api } = schedulerStorage();
  scheduler.register('a', 'host-a');
  scheduler.register('b', 'host-b');
  return { scheduler, api };
}

describe('minimal queue', () => {
  it('assigns a job once and returns the same assignment on a claim retry', async () => {
    const { scheduler, api } = setup();
    const job = scheduler.submit(['echo', 'hello']);
    const responses = await Promise.all(
      ['a', 'b'].map(async (id) =>
        api.request(`/workers/${id}/claim`, { method: 'POST', body: '{}' }),
      ),
    );
    const assignments = await Promise.all(
      responses.map(async (r) =>
        z.object({ job: z.unknown() }).parse(await r.json()),
      ),
    );
    expect(assignments.filter((r) => r.job !== null)).toHaveLength(1);
    const next = scheduler.submit(['echo', 'next']);
    expect(scheduler.claim('a')?.id).toBe(job.id);
    expect(scheduler.job(next.id).status).toBe('queued');
  });

  it('tracks progress and completion, enforcing ownership and terminal states', () => {
    const { scheduler } = setup();
    const job = scheduler.submit(['true']);
    scheduler.claim('a');
    expect(() => scheduler.progress(job.id, 'b', 0.5)).toThrow('not assigned');
    scheduler.progress(job.id, 'a', 0.5);
    scheduler.finish(job.id, 'a', true, 0, null);
    scheduler.finish(job.id, 'a', true, 0, null);
    expect(scheduler.job(job.id)).toMatchObject({
      status: 'succeeded',
      progress: 1,
      exit_code: 0,
    });
    expect(scheduler.worker('a').current_job_id).toBeNull();
    expect(() => scheduler.finish(job.id, 'a', false, 1, 'failed')).toThrow(
      'not running',
    );
  });

  it('validates input with Zod and reports failures', async () => {
    const { scheduler, api } = setup();
    const response = await api.request('/jobs', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ command: 'not argv' }),
    });
    expect(response.status).toBe(400);
    const job = scheduler.submit(['false']);
    scheduler.claim('a');
    scheduler.finish(job.id, 'a', false, 1, 'Process exited with code 1');
    expect(scheduler.job(job.id).status).toBe('failed');
    expect(scheduler.claim('a')).toBeNull();
  });
});
