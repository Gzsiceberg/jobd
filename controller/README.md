# Controller

[Overview](../README.md) · [Development](../docs/development.md) · [CLI](../cli/README.md) · [Worker](../worker/README.md)

TypeScript, Hono and Zod. Each queue has a SQLite-backed Cloudflare Durable Object. Claims and completion are atomic. Integration: [src/index.ts](src/index.ts).

## API

Prefix every route with `/queues/:name`. Bodies are JSON. Unprefixed routes are not served.

Queue names match `[a-z0-9][a-z0-9_-]{0,62}`. First use creates the queue. Names select separate storage, not separate permissions.

Each worker serves one queue (`--queue` or `JOBD_QUEUE`; default `default`). Use separate daemons and state directories for more queues. There is no queue registry or cross-queue claiming.

| Method | Path                       | Body / notes                                                        |
| ------ | -------------------------- | ------------------------------------------------------------------- |
| POST   | `/jobs`                    | `{"command":["echo","hello"]}`                                      |
| GET    | `/jobs/:id`                | —                                                                   |
| GET    | `/jobs?limit=100&offset=0` | Limit 1–100; returns `{jobs:[...]}`                                 |
| GET    | `/jobs/latest?kind=added`  | Kind: `added` or `run`                                              |
| POST   | `/jobs/clear`              | `{}`                                                                |
| DELETE | `/jobs/:id`                | Rejects running jobs                                                |
| POST   | `/jobs/:id/urgent`         | `{}`; queued only                                                   |
| POST   | `/jobs/:id/cancel`         | `{}`; running only; returns job                                     |
| POST   | `/jobs/swap`               | `{"first":"ID1","second":"ID2"}`; queued only                       |
| POST   | `/jobs/:id/output`         | `{"worker_id":"...","output_path":"/tmp/jobd-....log"}`             |
| POST   | `/workers/register`        | `{"worker_id":"...","hostname":"vm-1"}`                             |
| POST   | `/workers/:id/heartbeat`   | `{}`; no progress                                                   |
| POST   | `/workers/:id/claim`       | `{}`                                                                |
| POST   | `/jobs/:id/progress`       | `{"worker_id":"...","progress":0.5}`                                |
| POST   | `/jobs/:id/complete`       | `{"worker_id":"...","exit_code":0,"progress":1}`                    |
| POST   | `/jobs/:id/fail`           | `{"worker_id":"...","exit_code":1,"error":"failed","progress":0.5}` |

- Claim returns `{"job":null}` or `{"job":{...}}`. Retrying returns the existing assignment.
- Terminal reports are retry-safe. Their `progress` field is optional. Pre-launch failures may use `exit_code: null`.
- Heartbeats return `cancel_job_id`, or `null`. Jobs expose `cancel_requested` as 0 or 1. A request does not mean the process stopped.
- Worker status comes from assignments, not heartbeat payloads.

See [progress](../worker/README.md#reporting-progress-from-a-job) and [cancellation](../worker/README.md#remote-cancellation).

## Storage and IDs

Each queue stores jobs and workers separately. Claims follow FIFO unless reordered.

Job IDs start at 1 per queue. They are decimal strings in JSON and never reused after deletion. Worker IDs are UUIDs.

New queues get the current schema. Existing queues must already use numeric job IDs. The queue environment table is created automatically; there are no job-schema migrations.

Lists use pages of up to 100, not snapshots. Concurrent changes can affect pagination.

## Queue environment secrets

Configure a **separate** encryption key before setting queue secrets:

```sh
openssl rand -base64 32 | pnpm --filter jobd-controller exec wrangler secret put JOBD_ENV_KEY
```

`JOBD_ENV_KEY` is a base64-encoded random 32-byte AES key stored as a Cloudflare Workers secret, never in SQLite or worker configuration. Keep a secure backup if you need recovery. Do not replace it while encrypted values exist: they will become unreadable. Key rotation/re-encryption is not implemented; to replace the key, pause workers, delete existing queue secrets, replace the key, and re-provision values from their authoritative source. Retain the old key for any backups that still need it.

Queue-prefixed routes (same shared bearer authentication):

| Method | Route        | Body / response                      |
| ------ | ------------ | ------------------------------------ |
| PUT    | `/env/:name` | `{"value":"..."}`; create or replace |
| GET    | `/env`       | `{"names":["API_KEY"]}`; no values   |
| DELETE | `/env/:name` | Idempotent deletion                  |

Management requires HTTPS. Names match `[A-Za-z_][A-Za-z0-9_]{0,127}`. Each queue allows 64 variables, each up to 4096 UTF-8 bytes without NUL. Empty values and newlines are preserved. Names are not encrypted.

Values use AES-256-GCM with a fresh 96-bit nonce for each write. Authenticated data binds ciphertext to the Durable Object ID and variable name. SQLite and its backups contain ciphertext, not values. Moving encrypted rows to a different object will not work.

Claims include a separate `environment` map when a job is assigned; it is not part of stored job records. Queues with secrets require HTTPS claims. Decryption failures prevent assignment, rather than running without the required environment. Responses use `Cache-Control: no-store`. Updates apply at the next successful claim (including retries), not to already-running processes. Local jobs do not receive queue secrets.

**Security boundary:** the existing API key grants access to all queues. Anyone able to submit jobs can retrieve their secrets by executing code. Controller operators, Cloudflare, and the executing worker must be trusted. Values exist in controller/worker memory and job process environments; application output is **not redacted**. Jobs can write secrets to output files, and host administrators or sufficiently privileged processes can read them. Encryption at rest does not protect a compromised controller or worker. Deletion does not erase historical backups or revoke an API key at its provider.

## Deployment and limits

From the repository root:

```sh
pnpm --filter jobd-controller exec wrangler login
pnpm --filter jobd-controller deploy
export JOBD_API_KEY="$(openssl rand -base64 32)"
printf '%s' "$JOBD_API_KEY" | pnpm --filter jobd-controller exec wrangler secret put JOBD_API_KEY
```

Use this same key in the CLI and workers. For development, put it in `controller/.dev.vars` (gitignored).

Every route requires `Authorization: Bearer <JOBD_API_KEY>`. Invalid or missing credentials return 401. An unset controller key returns 503.

The default endpoint is `https://jobd-controller.aflashsheng.workers.dev`; `workers_dev` is enabled. Set the secret before use. Self-hosters should set their own endpoint explicitly.

**The shared key grants command execution in every queue. There is no sandbox.** Run workers unprivileged.

This is not a production scheduler:

- No leases, stale-worker recovery, automatic job retries or advanced scheduling.
- Network errors retry; failed jobs are not requeued.
- Restart fails the worker's previous assignment without replay.
- Lost VMs or shutdown reports can leave jobs running.
- Execution is not exactly-once. Worker results are not durably buffered.
