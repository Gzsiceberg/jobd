import { DurableObject } from 'cloudflare:workers';
import { createApi } from './api/router';
import { createQueueApi } from './api/queues';
import { Scheduler } from './jobs/scheduler';

interface Env {
  SCHEDULER: DurableObjectNamespace<SchedulerObject>;
}

/** Cloudflare adapter: one SQLite-backed object per uniquely named queue. */
export class SchedulerObject extends DurableObject<Env> {
  private api;

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    this.api = createApi(new Scheduler(ctx.storage));
  }

  fetch(request: Request) {
    return this.api.fetch(request);
  }
}

export default {
  fetch(request: Request, env: Env) {
    return createQueueApi((name, queueRequest) =>
      env.SCHEDULER.get(env.SCHEDULER.idFromName(name)).fetch(queueRequest),
    ).fetch(request);
  },
} satisfies ExportedHandler<Env>;
