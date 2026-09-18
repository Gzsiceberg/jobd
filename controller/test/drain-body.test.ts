import { describe, expect, it } from 'vitest';
import { createApi } from '../src/api/router';
import { createQueueApi } from '../src/api/queues';
import { schedulerStorage } from './storage';

describe('request body lifetime', () => {
  it('drains ignored management bodies before returning', async () => {
    const { scheduler } = schedulerStorage();
    const api = createApi(scheduler);
    const request = new Request('http://localhost/jobs/clear', {
      method: 'POST',
      body: '{}',
    });
    const response = await api.fetch(request);
    expect(response.status).toBe(200);
    expect(request.bodyUsed).toBe(true);
    expect(request.body?.locked).toBe(false);
  });

  it('drains rejected uploads without forwarding them', async () => {
    const api = createQueueApi(() => {
      throw new Error('Unauthorized request forwarded');
    }, 'secret');
    const request = new Request('http://localhost/queues/test/jobs', {
      method: 'POST',
      body: '{}',
    });
    const response = await api.fetch(request);
    expect(response.status).toBe(401);
    expect(request.bodyUsed).toBe(true);
    expect(request.body?.locked).toBe(false);
  });

  it('preserves bodies consumed by JSON handlers', async () => {
    const { scheduler } = schedulerStorage();
    const api = createApi(scheduler);
    const response = await api.request('/jobs', {
      method: 'POST',
      body: JSON.stringify({ command: ['echo', 'hello'] }),
    });
    expect(response.status).toBe(201);
    expect(await response.json()).toMatchObject({ command: ['echo', 'hello'] });
  });
});
