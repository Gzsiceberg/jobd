import { Hono } from 'hono';
import { z } from 'zod';
import { drainBody } from './drain-body';
import {
  isAdminKey,
  issueWorkerToken,
  maxWorkerTokenSeconds,
  verifyWorkerToken,
  workerRouteAllowed,
} from './worker-tokens';

const queueName = z.string().regex(/^[a-z0-9][a-z0-9_-]{0,62}$/);

/** Route a named queue to its own storage instance; keep internal routes unchanged. */
export function createQueueApi(
  forward: (name: string, request: Request) => Response | Promise<Response>,
  masterKey?: string,
) {
  const app = new Hono<{
    Variables: {
      workerClaims: NonNullable<Awaited<ReturnType<typeof verifyWorkerToken>>>;
    };
  }>();
  app.use('*', drainBody);
  app.use('*', async (c, next) => {
    c.header('Cache-Control', 'no-store');
    if (!masterKey?.trim()) {
      return c.json(
        { error: 'Controller authentication is not configured' },
        503,
      );
    }
    const authorization = c.req.header('Authorization') ?? '';
    const token = authorization.startsWith('Bearer ')
      ? authorization.slice(7)
      : '';
    if (token && (await isAdminKey(token, masterKey))) {
      await next();
      return;
    }
    const claims = await verifyWorkerToken(masterKey, token);
    if (!claims) {
      c.header('WWW-Authenticate', 'Bearer');
      return c.json({ error: 'Unauthorized' }, 401);
    }
    const match = /^\/queues\/([a-z0-9][a-z0-9_-]{0,62})(\/.*)$/.exec(
      new URL(c.req.url).pathname,
    );
    if (
      !match ||
      match[1] !== claims.queue ||
      !workerRouteAllowed(c.req.method, match[2])
    ) {
      return c.json({ error: 'Forbidden' }, 403);
    }
    c.set('workerClaims', claims);
    await next();
  });
  app.get('/queues/:name/auth/worker-token/verify', (c) => {
    if (new URL(c.req.url).protocol !== 'https:') {
      return c.json({ error: 'Worker token verification requires HTTPS' }, 400);
    }
    const claims = c.get('workerClaims');
    if (!claims) {
      return c.json({ error: 'A worker token is required' }, 403);
    }
    return c.json({
      valid: true,
      queue: claims.queue,
      expires_at: new Date(claims.exp * 1000).toISOString(),
    });
  });
  app.post('/queues/:name/auth/worker-token', async (c) => {
    // Only administrators reach this endpoint; worker tokens are denied above.
    if (new URL(c.req.url).protocol !== 'https:') {
      return c.json({ error: 'Worker token generation requires HTTPS' }, 400);
    }
    const name = queueName.safeParse(c.req.param('name'));
    if (!name.success) return c.json({ error: 'Invalid queue name' }, 400);
    let body: unknown;
    try {
      body = await c.req.json();
    } catch {
      return c.json({ error: 'Invalid JSON' }, 400);
    }
    const duration = z
      .object({
        duration_seconds: z.number().int().min(1).max(maxWorkerTokenSeconds),
      })
      .strict()
      .safeParse(body);
    if (!duration.success)
      return c.json(
        { error: 'duration_seconds must be an integer between 1 and 2592000' },
        400,
      );
    return c.json(
      await issueWorkerToken(
        masterKey!,
        name.data,
        duration.data.duration_seconds,
      ),
      201,
    );
  });
  app.all('/queues/:name/*', (c) => {
    const name = queueName.safeParse(c.req.param('name'));
    if (!name.success) {
      return c.json(
        { error: 'Queue name must match [a-z0-9][a-z0-9_-]{0,62}' },
        400,
      );
    }
    const url = new URL(c.req.url);
    url.pathname = '/' + url.pathname.split('/').slice(3).join('/');
    return forward(name.data, new Request(url, c.req.raw));
  });
  app.notFound((c) =>
    c.json(
      { error: 'Use /queues/:name/jobs or /queues/:name/workers routes' },
      404,
    ),
  );
  return app;
}
