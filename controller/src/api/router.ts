import { Hono } from 'hono';
import { z } from 'zod';
import { ApiError } from './errors';
import { drainBody } from './drain-body';
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
const completion = owner.extend({
  exit_code: z.literal(0),
  progress: z.number().min(0).max(1).optional(),
});
const failure = owner.extend({
  exit_code: z
    .number()
    .int()
    .refine((code) => code !== 0)
    .nullable(),
  error: text,
  progress: z.number().min(0).max(1).optional(),
});

/** HTTP validation; the scheduler owns persistence and transactions. */
export function createApi(scheduler: Scheduler) {
  const app = new Hono();
  app.use('*', drainBody);
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
  app.get('/jobs', (c) => {
    const limit = z.coerce
      .number()
      .int()
      .min(1)
      .max(100)
      .parse(c.req.query('limit') ?? 100);
    const offset = z.coerce
      .number()
      .int()
      .min(0)
      .parse(c.req.query('offset') ?? 0);
    return c.json({ jobs: scheduler.list(limit, offset) });
  });
  app.get('/jobs/latest', (c) =>
    c.json(
      scheduler.latest(
        z.enum(['added', 'run']).parse(c.req.query('kind') ?? 'added'),
      ),
    ),
  );
  app.post('/jobs/clear', (c) => {
    scheduler.clear();
    return c.json({ ok: true });
  });
  app.post('/jobs/swap', async (c) => {
    const body = z
      .object({ first: text, second: text })
      .parse(await c.req.json<unknown>());
    scheduler.reorder(body.first, body.second);
    return c.json({ ok: true });
  });
  app.delete('/jobs/:id', (c) => {
    scheduler.remove(c.req.param('id'));
    return c.json({ ok: true });
  });
  app.post('/jobs/:id/cancel', (c) =>
    c.json(scheduler.cancel(c.req.param('id'))),
  );
  app.post('/jobs/:id/urgent', (c) => {
    scheduler.reorder(c.req.param('id'));
    return c.json({ ok: true });
  });
  app.post('/jobs/:id/output', async (c) => {
    const body = owner
      .extend({ output_path: text })
      .parse(await c.req.json<unknown>());
    return c.json(
      scheduler.output(c.req.param('id'), body.worker_id, body.output_path),
    );
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
        body.progress,
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
        body.progress,
      ),
    );
  });
  return app;
}
