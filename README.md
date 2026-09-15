# jobd

A small pull-based job scheduler: remote VMs execute commands from named task queues.

```text
Cloudflare Controller
        |
        | HTTP
        |
   Worker Daemon
        |
        v
   Local Process
```

- `controller/`: TypeScript, Hono HTTP routes, Zod input validation. Each uniquely named queue gets its own SQLite-backed Durable Object, with isolated jobs, worker records and atomic claims. Cloudflare integration lives in `src/index.ts`.
- `worker/`: Python/uv daemon; one command at a time, stable local identity, periodic heartbeats, HTTP retries and SIGINT/SIGTERM process-group cleanup.
- `tooling/eslint/base.mjs` and `tsconfig.base.json`: copied from Tau; all config references are local. `controller/tsconfig.eslint.json` extends the controller config, which extends the shared base.

Lifecycle: `queued -> running -> succeeded | failed`.

## Local development

Requires Node.js 24 (`.nvmrc`), pnpm 12.4.1, Python 3.14 and uv (`worker/.python-version` pins uv to 3.14). Workers target Linux/POSIX VMs.

```sh
nvm install && nvm use     # if using nvm
corepack enable
pnpm install
pnpm dev                    # http://localhost:8787, local persistent SQLite
```

In another terminal:

```sh
cd worker
uv sync
uv run jobd-worker --controller http://localhost:8787 --queue default
```

Submit and inspect a job:

```sh
curl -s http://localhost:8787/queues/default/jobs \
  -H 'Content-Type: application/json' \
  -d '{"command":["echo","hello from jobd"]}'

curl -s http://localhost:8787/queues/default/jobs/JOB_ID
```

Commands are argv arrays, not shell strings. Explicit shell usage is possible with `["sh", "-c", "..."]`. Output goes to the worker's stdout/stderr; no logs are stored centrally. Progress is a fraction from 0 to 1; arbitrary commands have no inferred intermediate progress. The daemon reports 0 at start and completion sets 1 on success.

The worker stores its ID in `~/.local/state/jobd-worker`. Use `--state-dir PATH` for separate daemons; never copy an identity to another VM. Poll and heartbeat intervals default to 2 and 10 seconds (`--poll-interval`, `--heartbeat-interval`). Ctrl-C stops the worker and terminates its active process group.

## API

All routes below are prefixed with `/queues/:name` (for example, `/queues/default/jobs`). All request bodies are JSON. IDs in paths come from returned records.

Queue names are unique within the controller deployment and must match `[a-z0-9][a-z0-9_-]{0,62}`. Queues are created on first use; the same name always selects the same Durable Object. A worker serves one queue, selected with `--queue` or `JOBD_QUEUE` (default: `default`). Use separate daemons and state directories to serve multiple queues. There is no queue registry or cross-queue claiming.

Unprefixed API routes are not served.

| Method | Path | Body |
| --- | --- | --- |
| POST | `/jobs` | `{"command":["echo","hello"]}` |
| GET | `/jobs/:id` | — |
| POST | `/workers/register` | `{"worker_id":"...","hostname":"vm-1"}` |
| POST | `/workers/:id/heartbeat` | `{}` |
| POST | `/workers/:id/claim` | `{}` |
| POST | `/jobs/:id/progress` | `{"worker_id":"...","progress":0.5}` |
| POST | `/jobs/:id/complete` | `{"worker_id":"...","exit_code":0}` |
| POST | `/jobs/:id/fail` | `{"worker_id":"...","exit_code":1,"error":"failed"}` |

Claim returns `{"job":null}` when idle or `{"job":{...}}`. Retrying a claim returns the worker's existing assignment; terminal reports are retry-safe. Failures before launch may use `exit_code: null`. Worker busy/idle status and current job are derived from assignments, not trusted heartbeat payloads.

## Checks

```sh
pnpm typecheck
pnpm lint
pnpm test
pnpm format
cd worker
uv run pytest
uv run ruff check .
uv run ruff format --check .
```

## Deployment and limits

`pnpm --filter jobd-controller exec wrangler login`, then `pnpm --filter jobd-controller deploy`. Configure a protected route first; `workers_dev` is deliberately disabled. **There is no authentication or sandbox: anyone with API access can execute commands on your VMs. Do not expose this controller publicly. Run workers as an unprivileged user.**

This is a scaffold, not a production scheduler. Each queue has separate SQLite `jobs` and `workers` tables in its own Durable Object. Operations read/update individual rows; claiming and completion update both tables atomically. An index selects queued jobs in submission order. Only each command's argv array is JSON-encoded. There are no leases, stale-worker recovery, automatic job retries, priorities or advanced scheduling. Network errors are retried; failed jobs are not requeued. On restart, a worker marks its previous assignment failed rather than replaying an unknown outcome. Lost shutdown reports or permanently lost VMs can leave jobs running. Execution is not exactly-once, and results are not durably buffered on the worker.
