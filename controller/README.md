# Controller

[Overview](../README.md) · [Development](../docs/development.md) · [CLI](../cli/README.md) · [Worker](../worker/README.md)

TypeScript, Hono and Zod. Each queue has a SQLite-backed Cloudflare Durable Object. Claims and completion are atomic. Integration: [src/index.ts](src/index.ts).

## API

Prefix every route with `/queues/:name`. Bodies are JSON. Unprefixed routes are not served.

Queue names match `[a-z0-9][a-z0-9_-]{0,62}`. First use creates the queue. Names select separate storage. Worker tokens are scoped to one queue; the admin key authorizes every queue.

Each worker serves one queue (`--queue` or `JOBD_QUEUE`; default `default`). Use separate daemons and state directories for more queues. There is no queue registry or cross-queue claiming.

Failed jobs can be requeued with `POST /jobs/:id/retry` or `POST /jobs/retry-all` (under the queue prefix, requiring the master key). Both return `{ "retried": N }`. Retries preserve IDs, commands and creation timestamps, clear execution details and cancellation flags, and append jobs behind queued work. `retry-all` is atomic and affects only failed jobs; single-job retries reject other states with 409. Log files are not deleted. Retrying does not clear a worker's failure-triggered pause (restart that worker) or an administrative pause (`jobd worker resume ID`).

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
| POST   | `/workers/:id/heartbeat`   | `{}`                                                   |
| POST   | `/workers/:id/claim`       | `{}`                                                                |
| POST   | `/jobs/:id/complete`       | `{"worker_id":"...","exit_code":0}`                    |
| POST   | `/jobs/:id/fail`           | `{"worker_id":"...","exit_code":1,"error":"failed"}` |

- Claim returns `{"job":null}` or `{"job":{...}}`. Retrying returns the existing assignment.
- Terminal reports are retry-safe, except reports for jobs expired by stale-worker cleanup are rejected with 409. Pre-launch failures may use `exit_code: null`.
- Heartbeats return `cancel_job_id`, or `null`. Jobs expose `cancel_requested` as 0 or 1. A request does not mean the process stopped.
- Worker status comes from assignments, not heartbeat payloads. Workers whose last heartbeat is at least two minutes old are reported as `offline`.
- `POST /jobs/remove-all` with `{}` atomically deletes queued and finished records and returns `{"removed":N,"kept_running":N}`. Healthy running jobs and their assignments, queue secrets, and job ID sequences are preserved; stale assignments are expired first. Output files are never deleted. The CLI requires typed confirmation before calling this authenticated endpoint; direct API callers are responsible for their own confirmation. Deletion uses the jobs present at execution time, not a snapshot taken at the prompt.

### Batch operations

`POST /jobs/retry`, `/jobs/urgent`, `/workers/pause`, and `/workers/resume` accept `{"ids":["ID1","ID2"]}` (1–100 nonempty IDs). These require the master key, like the corresponding single-target mutations. Valid requests return HTTP 200 with `{"succeeded":[...],"failed":[{"id":"ID","error":"reason"}]}`, including when all targets fail. Job successes are IDs; worker successes are full worker records. Malformed payloads return 400 before any mutation.

Each target has its own transaction: invalid targets do not block others or roll back successes. Repeated IDs are processed once. Retry appends successful jobs in argument order; urgent puts them at the front in argument order. Pause/resume resolve full IDs or unique prefixes and return each successful worker once. Existing single-target and retry-all endpoints remain supported. A batch response lost in transit can leave partial outcomes unknown; do not automatically replay retry batches.

### Pause remote assignments

Admin-only `POST /workers/:id/pause` and `POST /workers/:id/resume` (under the queue prefix; no body required) return the worker, including `paused` (`0` or `1`). IDs may be full IDs or literal prefixes. Exact matches win; no match returns 404; multiple matches return 409 with matching IDs and no changes. Resolution considers all registered workers and is atomic with the update.

`GET /queues/:name/workers` (admin only) returns `{ "workers": [...] }` for all registered workers, ordered by hostname and worker ID. Each worker includes nullable `token_expired_time`, the UTC expiry of the token used for its latest successful heartbeat. Only verified token claims supply this value; tokens themselves are not stored. Existing workers have `null` until their next token-authenticated heartbeat. Admin operations do not overwrite recorded expiry. Offline workers remain listed; the CLI displays expiry as a relative duration.

The durable pause flag is separate from idle/busy/offline status and survives registration, restarts, completion and stale-worker cleanup. Claims still return any existing assignment for retry recovery, but cannot create a new one while paused. Current work, heartbeats and local fallback jobs continue. A claim committed before pause can still execute afterward. Both operations are idempotent; worker tokens cannot call them. Resume does not clear the worker's separate failure-triggered pause.

### Disconnected workers

Job listing, clearing finished jobs, removing a job, and removing all jobs run stale-worker cleanup before their operation. This lets `jobd -C` clear stale jobs without a preceding list. If a worker's last heartbeat is at least two minutes old, its running/cancelling jobs become `failed` with `Worker disconnected; outcome unknown` and a null exit code. Cleanup clears cancellation flags and worker assignments, preserving job records and output paths. It affects all stale jobs in the queue, regardless of pagination. There is no alarm or background cleanup; without one of these requests, jobs remain unchanged. Registration, heartbeats and claims refresh worker liveness; reconnecting before cleanup preserves the assignment.

Expired jobs are never automatically retried. Late result/output reports are rejected and cannot change a subsequent assignment. A disconnected process may still run: timeout cleanup does not stop it or confirm its outcome. A worker whose result is rejected exits; restart it to resume accepting work. Keep heartbeat intervals well below two minutes (default: 15 seconds).

See [cancellation](../worker/README.md#remote-cancellation).

## Storage and IDs

Each queue stores jobs and workers separately. Claims follow FIFO unless reordered.

Job IDs start at 1 per queue. They are decimal strings in JSON and never reused after deletion. Worker IDs are UUIDs.

New queues get the current schema. Existing queues must already use numeric job IDs. The queue environment table is created automatically; there are no job-schema migrations.

Lists use pages of up to 100, not snapshots. Concurrent changes can affect pagination.

## Queue environment secrets

Configure the admin/encryption key before use (preserve the existing value if already configured):

```sh
openssl rand -base64 32 | pnpm --filter jobd-controller exec wrangler secret put JOBD_MASTER_KEY
```

`JOBD_MASTER_KEY` is a base64-encoded random 32-byte AES key stored as a Cloudflare Workers secret, never in SQLite or worker configuration. Keep a secure backup if you need recovery. Do not replace it while encrypted values exist: they will become unreadable. Key rotation/re-encryption is not implemented; to replace the key, pause workers, delete existing queue secrets, replace the key, and re-provision values from their authoritative source. Retain the old key for any backups that still need it.

Queue-prefixed routes (admin bearer authentication with `JOBD_MASTER_KEY` only):

| Method | Route        | Body / response                      |
| ------ | ------------ | ------------------------------------ |
| PUT    | `/env/:name` | `{"value":"..."}`; create or replace |
| GET    | `/env`       | `{"names":["API_KEY"]}`; no values   |
| DELETE | `/env/:name` | Idempotent deletion                  |

Management requires HTTPS. Names match `[A-Za-z_][A-Za-z0-9_]{0,127}`. Each queue allows 64 variables, each up to 4096 UTF-8 bytes without NUL. Empty values and newlines are preserved. Names are not encrypted.

Values use AES-256-GCM with a fresh 96-bit nonce for each write. Authenticated data binds ciphertext to the Durable Object ID and variable name. SQLite and its backups contain ciphertext, not values. Moving encrypted rows to a different object will not work.

Claims include a separate `environment` map when a job is assigned; it is not part of stored job records. Queues with secrets require HTTPS claims. Decryption failures prevent assignment, rather than running without the required environment. Responses use `Cache-Control: no-store`. Updates apply at the next successful claim (including retries), not to already-running processes. Local jobs do not receive queue secrets.

**Security boundary:** `JOBD_MASTER_KEY` grants access to all queues. Worker tokens can claim jobs and receive decrypted secrets in their authorized queue, despite being unable to manage environment settings. Anyone able to submit jobs can retrieve their secrets by executing code. Controller operators, Cloudflare, and the executing worker must be trusted. Values exist in controller/worker memory and job process environments; application output is **not redacted**. Jobs can write secrets to output files, and host administrators or sufficiently privileged processes can read them. Encryption at rest does not protect a compromised controller or worker. Deletion does not erase historical backups or revoke an API key at its provider.

## Deployment and limits

From the repository root:

```sh
pnpm --filter jobd-controller exec wrangler login
pnpm --filter jobd-controller deploy
# Only for a NEW deployment. For an existing deployment, keep its JOBD_MASTER_KEY.
export JOBD_MASTER_KEY="$(openssl rand -base64 32)"
printf '%s' "$JOBD_MASTER_KEY" | pnpm --filter jobd-controller exec wrangler secret put JOBD_MASTER_KEY
```

Use `JOBD_MASTER_KEY` only in the controller and trusted admin CLI environments, never on workers. For development, put it in `controller/.dev.vars` (gitignored). The controller accepts only the master key or signed worker tokens, not legacy shared API keys.

Every route requires a bearer credential. `JOBD_MASTER_KEY` authorizes all operations. Generated worker tokens authorize only the routes below in their named queue. Invalid, expired or missing credentials return 401; forbidden operations/queues return 403. An unset controller `JOBD_MASTER_KEY` returns 503.

### Worker tokens

Naming migration: use `jobd auth create-worker-token`, `/auth/worker-token` and `/auth/worker-token/verify`. The creation response now uses `token`, not `api_key`. Old command and route names are not supported; update CLI and controller together. `JOBD_WORKER_TOKEN` and existing unexpired tokens are unchanged.

```sh
# In a trusted admin shell with JOBD_MASTER_KEY and JOBD_CONTROLLER set:
JOBD_QUEUE=batch jobd auth create-worker-token --duration 24h
```

Workers can check their token without accessing queue storage:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $JOBD_WORKER_TOKEN" \
  "$JOBD_CONTROLLER/queues/$JOBD_QUEUE/auth/worker-token/verify"
```

The CLI provides `jobd auth verify-worker-token` to verify `JOBD_WORKER_TOKEN` and print its expiry and remaining lifetime.

`GET /queues/:name/auth/worker-token/verify` requires HTTPS and the worker token as Bearer authorization (not the master key). Returns `{"valid":true,"queue":"batch","expires_at":"..."}` (200). Missing, invalid or expired tokens return 401; a wrong queue or admin key returns 403. An unconfigured controller returns 503. Responses are not cacheable and never include the token. This checks authentication only, not worker connectivity or registration.

`POST /queues/:name/auth/worker-token` accepts `{"duration_seconds":86400}` and returns `{"token":"...","expires_at":"...","queue":"batch"}` (201). Requires admin authentication and HTTPS, including localhost. Durations must be whole seconds from 1 through 2592000 (30 days). Responses are not cacheable.

Worker tokens allow job list/detail/latest and registration, heartbeat, claim, output reporting, completion and failure. They cannot submit, delete, clear, cancel or reorder jobs, access `/env`, or issue keys. This is a worker role, not strictly read-only: claims/reporting mutate state, and claims deliver queue secrets. Tokens are queue-scoped, not tied to an individual worker identity; holders are trusted within that queue.

Worker tokens use only the compact `jw2.` format: 80–163 characters depending on queue-name length (86 for `batch`). A base64url binary payload contains issue/expiry times, a random UUID and the queue, followed by a full 32-byte HMAC-SHA-256 signature; the format implies the version and worker role. Signatures use an HKDF-derived, purpose-separated signing key from `JOBD_MASTER_KEY`. Legacy `jobd_worker_v1` tokens are no longer accepted: generate replacement tokens and restart workers when upgrading. No worker credentials are stored in SQLite. No individual revocation or automatic renewal is implemented; all requests, including heartbeat/completion, fail after expiry. Replace credentials before expiry and restart workers while idle. Changing `JOBD_MASTER_KEY` invalidates all tokens **and makes existing encrypted secrets unreadable**; do not rotate it just to revoke a worker.

Supply the generated token as client-side `JOBD_WORKER_TOKEN`. To persist it, use a file outside the repository with mode `0600`, loaded into the worker environment by your service manager. The CLI prints the token to stdout and expiry to stderr; it never saves a key file automatically. Do not log tokens.

### Migration

Rename the previous `JOBD_ENV_KEY` secret to `JOBD_MASTER_KEY`, preserving its exact value. **Do not generate a replacement value:** existing encrypted queue secrets and signed worker tokens depend on it. From a trusted admin shell containing the existing secret:

```sh
printf '%s' "$JOBD_ENV_KEY" | pnpm --filter jobd-controller exec wrangler secret put JOBD_MASTER_KEY
export JOBD_MASTER_KEY="$JOBD_ENV_KEY"
unset JOBD_ENV_KEY
pnpm --filter jobd-controller deploy
# After deploying the updated controller:
pnpm --filter jobd-controller exec wrangler secret delete JOBD_ENV_KEY
pnpm --filter jobd-controller exec wrangler secret delete JOBD_API_KEY
```

Rename the setting in controller `.dev.vars` and admin environments too. Rename client-side `JOBD_API_KEY` to `JOBD_WORKER_TOKEN` if it already contains a generated token; legacy shared keys must be replaced with generated worker tokens. Restart workers with the new variable name. There are no aliases for the old variable names. The controller must not have a `JOBD_WORKER_TOKEN` secret.

The default endpoint is `https://jobd-controller.aflashsheng.workers.dev`; `workers_dev` is enabled. Set the secret before use. Self-hosters should set their own endpoint explicitly.

**The admin key grants command execution in every queue. There is no sandbox.** Run workers unprivileged.

This is not a production scheduler:

- No execution leases, background stale-worker cleanup, automatic job retries or advanced scheduling. Stale assignments are expired only when jobs are listed, cleared or removed.
- Network errors retry; failed jobs are not requeued.
- Restart fails the worker's previous assignment without replay.
- Lost VMs or shutdown reports can leave jobs running.
- Execution is not exactly-once. Worker results are not durably buffered.
