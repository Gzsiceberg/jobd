import { DurableObject } from 'cloudflare:workers';
import { createApi } from './api/router';
import { createQueueApi } from './api/queues';
import { Scheduler } from './jobs/scheduler';
import { QueueSecrets } from './secrets';

interface Env {
  JOBD_MASTER_KEY?: string;
  SCHEDULER: DurableObjectNamespace<SchedulerObject>;
}

/** Cloudflare adapter: one SQLite-backed object per uniquely named queue. */
export class SchedulerObject extends DurableObject<Env> {
  private api;

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    this.api = createApi(
      new Scheduler(ctx.storage),
      new QueueSecrets(ctx.storage, ctx.id.toString(), env.JOBD_MASTER_KEY),
    );
  }

  fetch(request: Request) {
    return this.api.fetch(request);
  }
}

export default {
  fetch(request: Request, env: Env) {
    return createQueueApi(
      (name, queueRequest) =>
        env.SCHEDULER.get(env.SCHEDULER.idFromName(name)).fetch(queueRequest),
      env.JOBD_MASTER_KEY,
    ).fetch(request);
  },
} satisfies ExportedHandler<Env>;
