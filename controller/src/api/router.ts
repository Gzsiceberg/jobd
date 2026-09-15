import { Hono } from 'hono';
import { z } from 'zod';
import { ApiError } from './errors';
import type { Scheduler } from '../jobs/scheduler';

const text = z.string().trim().min(1).max(4096);
const owner = z.object({ worker_id: text });
const submission = z.object({
  command: z
    .array(z.string().max(4096))
    .min(1)
    .max(256)
    .refine(
      (argv) => argv[0].trim().length > 0,
      'Executable must not be empty',
    ),
});
const registration = owner.extend({ hostname: text });
const progress = owner.extend({ progress: z.number().min(0).max(1) });
const completion = owner.extend({ exit_code: z.literal(0) });
const failure = owner.extend({
  exit_code: z
    .number()
    .int()
    .refine((code) => code !== 0)
    .nullable(),
  error: text,
});

/** HTTP validation; the scheduler owns persistence and transactions. */
export function createApi(scheduler: Scheduler) {
  const app = new Hono();
  app.onError((error, c) => {
    if (error instanceof z.ZodError)
      return c.json({ error: error.issues }, 400);
    if (error instanceof SyntaxError)
      return c.json({ error: 'Invalid JSON' }, 400);
    if (error instanceof ApiError)
      return c.json({ error: error.message }, error.status);
    console.error(error);
    return c.json({ error: 'Internal server error' }, 500);
  });
  app.notFound((c) => c.json({ error: 'Route not found' }, 404));
  app.post('/jobs', async (c) => {
    const body = submission.parse(await c.req.json<unknown>());
    return c.json(scheduler.submit(body.command), 201);
  });
  app.get('/jobs/:id', (c) => c.json(scheduler.job(c.req.param('id'))));
  app.post('/workers/register', async (c) => {
    const body = registration.parse(await c.req.json<unknown>());
    return c.json(scheduler.register(body.worker_id, body.hostname));
  });
  app.post('/workers/:id/heartbeat', async (c) => {
    z.object({}).parse(await c.req.json<unknown>());
    return c.json(scheduler.heartbeat(c.req.param('id')));
  });
  app.post('/workers/:id/claim', async (c) => {
    z.object({}).parse(await c.req.json<unknown>());
    return c.json({ job: scheduler.claim(c.req.param('id')) });
  });
  app.post('/jobs/:id/progress', async (c) => {
    const body = progress.parse(await c.req.json<unknown>());
    return c.json(
      scheduler.progress(c.req.param('id'), body.worker_id, body.progress),
    );
  });
  app.post('/jobs/:id/complete', async (c) => {
    const body = completion.parse(await c.req.json<unknown>());
    return c.json(
      scheduler.finish(
        c.req.param('id'),
        body.worker_id,
        true,
        body.exit_code,
        null,
      ),
    );
  });
  app.post('/jobs/:id/fail', async (c) => {
    const body = failure.parse(await c.req.json<unknown>());
    return c.json(
      scheduler.finish(
        c.req.param('id'),
        body.worker_id,
        false,
        body.exit_code,
        body.error,
      ),
    );
  });
  return app;
}
