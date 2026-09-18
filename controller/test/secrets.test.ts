import { describe, expect, it } from 'vitest';
import { QueueSecrets } from '../src/secrets';
import { createApi } from '../src/api/router';
import { createQueueApi } from '../src/api/queues';
import { schedulerStorage } from './storage';

const key = btoa('k'.repeat(32));
function setup(scope = 'queue-a', masterKey: string | undefined = key) {
  const { storage, sqlite, scheduler } = schedulerStorage();
  const secrets = new QueueSecrets(storage, scope, masterKey);
  return {
    storage,
    sqlite,
    scheduler,
    secrets,
    api: createApi(scheduler, secrets),
  };
}

describe('queue secrets', () => {
  it('encrypts at rest, randomizes updates, and binds ciphertext to queue/name', async () => {
    const { secrets, sqlite, storage } = setup();
    await secrets.set('API_KEY', 'sensitive-value');
    const first = sqlite.prepare('SELECT * FROM queue_secrets').get();
    expect(JSON.stringify(first)).not.toContain('sensitive-value');
    expect(await secrets.environment()).toEqual({ API_KEY: 'sensitive-value' });
    await secrets.set('API_KEY', 'sensitive-value');
    expect(sqlite.prepare('SELECT * FROM queue_secrets').get()).not.toEqual(
      first,
    );
    await expect(
      new QueueSecrets(storage, 'other-queue', key).environment(),
    ).rejects.toThrow('Unable to decrypt');
    await expect(
      new QueueSecrets(storage, 'queue-a', btoa('x'.repeat(32))).environment(),
    ).rejects.toThrow('Unable to decrypt');
    sqlite.prepare("UPDATE queue_secrets SET name = 'OTHER'").run();
    await expect(secrets.environment()).rejects.toThrow('Unable to decrypt');
  });

  it('validates input, bounds storage, supports empty values and deletion', async () => {
    const { secrets } = setup();
    for (const name of ['JOBD_API_KEY', 'JOBD_PROGRESS_SOCKET', 'A=B', '1BAD'])
      await expect(secrets.set(name, 'secret')).rejects.toThrow('name');
    for (const value of ['a\0b', 'x'.repeat(4097), 'é'.repeat(2049), 123])
      await expect(secrets.set('KEY', value)).rejects.toThrow('Value');
    await secrets.set('EMPTY', '');
    expect(await secrets.environment()).toEqual({ EMPTY: '' });
    secrets.remove('EMPTY');
    secrets.remove('EMPTY');
    for (let i = 0; i < 64; i++) await secrets.set(`KEY_${i}`, 'v');
    await secrets.set('KEY_0', 'updated');
    await expect(secrets.set('OVERFLOW', 'v')).rejects.toThrow('64');
    expect(secrets.names()).toHaveLength(64);
  });

  it('keeps queues independent and omits secrets from empty claims', async () => {
    const a = setup('a');
    const b = setup('b');
    await a.secrets.set('API_KEY', 'a-secret');
    await b.secrets.set('API_KEY', 'b-secret');
    expect(await a.secrets.environment()).toEqual({ API_KEY: 'a-secret' });
    expect(await b.secrets.environment()).toEqual({ API_KEY: 'b-secret' });
    a.secrets.remove('API_KEY');
    expect(b.secrets.names()).toEqual(['API_KEY']);
    b.scheduler.register('worker', 'host');
    const response = await b.api.request('https://test/workers/worker/claim', {
      method: 'POST',
      body: '{}',
    });
    expect(await response.json()).toEqual({ job: null });
  });

  it('fails closed without a valid encryption key', async () => {
    const { secrets, storage } = setup();
    await expect(
      new QueueSecrets(storage, 'queue-a').set('KEY', 'secret'),
    ).rejects.toThrow('key');
    await secrets.set('KEY', 'secret');
    await expect(
      new QueueSecrets(storage, 'queue-a').environment(),
    ).rejects.toThrow('key');
  });

  it('authenticates management, lists only names, and requires HTTPS', async () => {
    const { api } = setup();
    const outer = createQueueApi((_, request) => api.fetch(request), 'auth');
    const request = (
      url: string,
      method = 'GET',
      body?: unknown,
      auth = 'auth',
    ) =>
      outer.request(url, {
        method,
        headers: {
          Authorization: `Bearer ${auth}`,
          'Content-Type': 'application/json',
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
    expect(
      (
        await request(
          'https://test/queues/a/env/KEY',
          'PUT',
          { value: 'secret' },
          'wrong',
        )
      ).status,
    ).toBe(401);
    expect(
      (
        await request('http://test/queues/a/env/KEY', 'PUT', {
          value: 'secret',
        })
      ).status,
    ).toBe(400);
    expect(
      (
        await request('https://test/queues/a/env/KEY', 'PUT', {
          value: 'secret',
        })
      ).status,
    ).toBe(200);
    const list = await request('https://test/queues/a/env');
    expect(list.headers.get('Cache-Control')).toBe('no-store');
    expect(await list.json()).toEqual({ names: ['KEY'] });
    expect((await request('https://test/queues/a/env/KEY')).status).toBe(404);
    expect(
      (await request('https://test/queues/a/env/KEY', 'DELETE')).status,
    ).toBe(200);
  });

  it('delivers secrets only in HTTPS claims, never job records', async () => {
    const { api, scheduler, secrets, sqlite } = setup();
    scheduler.register('worker', 'host');
    const job = scheduler.submit(['true']);
    await secrets.set('API_KEY', 'secret');
    const claim = (scheme: string) =>
      api.request(`${scheme}://test/workers/worker/claim`, {
        method: 'POST',
        body: '{}',
        headers: { 'Content-Type': 'application/json' },
      });
    expect((await claim('http')).status).toBe(400);
    expect(scheduler.job(job.id).status).toBe('queued');
    sqlite.prepare("UPDATE queue_secrets SET ciphertext = 'invalid'").run();
    expect((await claim('https')).status).toBe(503);
    expect(scheduler.job(job.id).status).toBe('queued');
    await secrets.set('API_KEY', 'secret');
    const response = await claim('https');
    expect(response.headers.get('Cache-Control')).toBe('no-store');
    expect(await response.json()).toMatchObject({
      job: { id: job.id },
      environment: { API_KEY: 'secret' },
    });
    expect(JSON.stringify(scheduler.job(job.id))).not.toContain('secret');
    await secrets.set('API_KEY', 'changed');
    expect(await (await claim('https')).json()).toMatchObject({
      environment: { API_KEY: 'changed' },
    });
  });
});
