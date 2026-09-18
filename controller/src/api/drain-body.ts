import type { MiddlewareHandler } from 'hono';

/** Finish unread uploads before returning a response across a workerd boundary. */
export const drainBody: MiddlewareHandler = async (c, next) => {
  await next();
  const request = c.req.raw;
  if (request.body && !request.bodyUsed) {
    // Discard incrementally rather than buffering an unused body in memory.
    await request.body.pipeTo(new WritableStream());
  }
};
