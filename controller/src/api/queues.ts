import { Hono } from 'hono';
import { z } from 'zod';

const queueName = z.string().regex(/^[a-z0-9][a-z0-9_-]{0,62}$/);

/** Route a named queue to its own storage instance; keep internal routes unchanged. */
export function createQueueApi(
  forward: (name: string, request: Request) => Response | Promise<Response>,
) {
  const app = new Hono();
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
