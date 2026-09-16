# Controller

[Overview](../README.md) · [Local development](../docs/development.md) · [CLI](../cli/README.md) · [Worker](../worker/README.md)

The controller uses TypeScript, Hono HTTP routes and Zod input validation. Each uniquely named queue gets its own SQLite-backed Cloudflare Durable Object, with isolated jobs, worker records and atomic claims. Cloudflare integration lives in [src/index.ts](src/index.ts).

## API

All routes below are prefixed with `/queues/:name` (for example, `/queues/default/jobs`). All request bodies are JSON. IDs in paths come from returned records.

Queue names are unique within the controller deployment and must match `[a-z0-9][a-z0-9_-]{0,62}`. Queues are created on first use; the same name always selects the same Durable Object. A worker serves one queue, selected with `--queue` or `JOBD_QUEUE` (default: `default`). Use separate daemons and state directories to serve multiple queues. There is no queue registry or cross-queue claiming.

Unprefixed API routes are not served.

| Method | Path | Body |
| --- | --- | --- |
| POST | `/jobs` | `{"command":["echo","hello"]}` |
| GET | `/jobs/:id` | — |
| GET | `/jobs?limit=100&offset=0` | — (limit 1–100; returns `{jobs:[...]}`) |
| GET | `/jobs/latest?kind=added` | — (`added` or `run`) |
| POST | `/jobs/clear` | `{}` |
| DELETE | `/jobs/:id` | — (rejects running jobs) |
| POST | `/jobs/:id/urgent` | `{}` (queued only) |
| POST | `/jobs/:id/cancel` | `{}` (running only; records intent, returns job) |
| POST | `/jobs/swap` | `{"first":"ID1","second":"ID2"}` (queued only) |
| POST | `/jobs/:id/output` | `{"worker_id":"...","output_path":"/tmp/jobd-....log"}` |
| POST | `/workers/register` | `{"worker_id":"...","hostname":"vm-1"}` |
| POST | `/workers/:id/heartbeat` | `{}` (no progress) |
| POST | `/workers/:id/claim` | `{}` |
| POST | `/jobs/:id/progress` | `{"worker_id":"...","progress":0.5}` |
| POST | `/jobs/:id/complete` | `{"worker_id":"...","exit_code":0,"progress":1}` (progress optional) |
| POST | `/jobs/:id/fail` | `{"worker_id":"...","exit_code":1,"error":"failed","progress":0.5}` (progress optional) |

Heartbeat replies include `cancel_job_id` (the assigned job to cancel, or `null`). Job records expose `cancel_requested` as 0 or 1; a pending request alone does not imply the process stopped.

Claim returns `{"job":null}` when idle or `{"job":{...}}`. Retrying a claim returns the worker's existing assignment; terminal reports are retry-safe. Failures before launch may use `exit_code: null`. Worker busy/idle status and current job are derived from assignments, not trusted heartbeat payloads.

See the worker guide for [progress reporting](../worker/README.md#reporting-progress-from-a-job) and [remote cancellation](../worker/README.md#remote-cancellation).

## Storage and IDs

Job IDs are auto-incrementing numbers per queue, represented as decimal strings in JSON. They start at 1 for an empty queue and are never reused after deletion, so IDs can have gaps. Worker IDs are UUIDs.

New queues are initialized with the complete current schema. Existing queues are expected to already use that schema and numeric job IDs; startup does not migrate schemas or rewrite IDs.

List requests fetch pages of up to 100; concurrent queue changes can affect pagination (not a snapshot).

## Deployment and limits

From the repository root:

```sh
pnpm --filter jobd-controller exec wrangler login
pnpm --filter jobd-controller deploy
pnpm --filter jobd-controller exec wrangler secret put JOBD_API_KEY
```

Every HTTP route requires `Authorization: Bearer <JOBD_API_KEY>`. Missing or incorrect credentials return 401; an unset controller key returns 503 (no unauthenticated fallback). Use the same strong random key in the controller, CLI and workers. For local development, set `JOBD_API_KEY` in `controller/.dev.vars` (gitignored). Include the Authorization header in raw HTTP requests.

Configure a protected HTTPS route first; `workers_dev` is deliberately disabled. **There is no execution sandbox: anyone holding the shared key can execute commands on your VMs in any queue. Run workers as an unprivileged user.**

This is a scaffold, not a production scheduler. Each queue has separate SQLite `jobs` and `workers` tables in its own Durable Object. Operations read/update individual rows; claiming and completion update both tables atomically. An index selects queued jobs in queue order (FIFO unless explicitly reordered). Only each command's argv array is JSON-encoded.

There are no leases, stale-worker recovery, automatic job retries or advanced scheduling. Network errors are retried; failed jobs are not requeued. On restart, a worker marks its previous assignment failed rather than replaying an unknown outcome. Lost shutdown reports or permanently lost VMs can leave jobs running. Execution is not exactly-once, and results are not durably buffered on the worker.
