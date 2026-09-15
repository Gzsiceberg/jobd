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
- `cli/`: standalone Go `jobd` client with tsp-style submission, listing and queue management.
- `worker/`: Go daemon (standard library only); one command at a time, stable local identity, periodic heartbeats, HTTP retries and SIGINT/SIGTERM process-group cleanup.
- `tooling/eslint/base.mjs` and `tsconfig.base.json`: copied from Tau; all config references are local. `controller/tsconfig.eslint.json` extends the controller config, which extends the shared base.

Lifecycle: `queued -> running -> succeeded | failed`.

## Local development

Requires Node.js 24 (`.nvmrc`), pnpm 12.4.1 and Go 1.27.1 or newer. Workers target Linux VMs; deployed worker binaries do not need Go installed.

```sh
nvm install && nvm use     # if using nvm
corepack enable
pnpm install
pnpm dev                    # http://localhost:8787, local persistent SQLite
```

In another terminal:

```sh
cd worker
go build -o jobd-worker .
./jobd-worker --controller http://localhost:8787 --queue default
```

Submit and inspect a job:

```sh
curl -s http://localhost:8787/queues/default/jobs \
  -H 'Content-Type: application/json' \
  -d '{"command":["echo","hello from jobd"]}'

curl -s http://localhost:8787/queues/default/jobs/JOB_ID
```

Commands are argv arrays, not shell strings. Explicit shell usage is possible with `["sh", "-c", "..."]`. Each command's stdout and stderr are combined in a unique `/tmp/jobd-*.log` file (owner-only permissions). The Go worker logs the path when execution starts and when the job finishes. Files remain after success, failure or shutdown; the worker does not rotate or delete them, so arrange cleanup as needed. No logs are stored centrally. Progress is a fraction from 0 to 1; arbitrary commands have no inferred intermediate progress. Progress starts at 0; the worker buffers updates in memory and uploads them independently every 10 seconds or when progress increases by more than 5 percentage points since the last successful upload. Completion sets 1 on success.

The worker stores its ID in `~/.local/state/jobd-worker`. Use `--state-dir PATH` for separate daemons; never copy an identity to another VM. Poll and heartbeat intervals default to 5 and 15 seconds (`--poll-interval`, `--heartbeat-interval`, both in seconds). `JOBD_CONTROLLER`, `JOBD_QUEUE` and `JOBD_STATE_DIR` provide environment defaults; flags take precedence.

Ctrl-C or SIGTERM stops polling and sends TERM to the active process group, followed by KILL after a 5-second grace period. A final result report has a separate 10-second deadline. HTTP calls retry network failures, 429 and 5xx at the poll interval; other HTTP errors are not retried by the client. Results stay in memory until accepted, so another job is not claimed while a report is pending.

On restart, an existing assignment is marked failed with an unknown outcome rather than executed again.

### Worker layout

- `main.go`: CLI configuration, signals and startup.
- `identity_linux.go`: persistent UUID and single-daemon file lock.
- `worker.go`: register → recover → claim → execute → report, plus heartbeat lifecycle.
- `active_job.go`: synchronized active-job registration and cancellation routing.
- `job_execution.go`: execution setup and cleanup before the terminal report.
- `client.go`: typed API records and one context-aware HTTP retry loop.
- `executor_linux.go`: direct argv execution and process-group shutdown.
- `progress_linux.go`: private per-job NDJSON Unix socket, validation and local buffering acknowledgements.
- `progress_reporter_linux.go`: per-job progress buffer, socket and uploader lifecycle; closes producers and joins uploads before returning final progress.
- `progress_upload.go`: independent 10-second / greater-than-5-point upload loop, owned by the per-job reporter.

Build a standalone binary for a Linux VM (use `GOARCH=arm64` for ARM):

```sh
cd worker
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o jobd-worker .
```

## jobd CLI

Build the tsp-style client (Go 1.27.1, as declared in `cli/go.mod`):

```sh
(cd cli && go build -o jobd .)
./cli/jobd --help
```

Set `JOBD_CONTROLLER` and `JOBD_QUEUE`, or pass `--controller URL` and `--queue NAME` **before** the action/command. Defaults match the worker.

```sh
./cli/jobd                       # same as -l: list the selected queue across all machines
./cli/jobd echo hello            # submit argv directly; prints a numeric job ID
./cli/jobd sh -c 'echo hi; sleep 60'
./cli/jobd -o                    # output path of the most recently started job
./cli/jobd -o JOB_ID             # output path of a specific job
./cli/jobd -r JOB_ID             # remove queued/finished job; running jobs are rejected
./cli/jobd -k JOB_ID             # request remote cancellation (or omit ID for the last run)
./cli/jobd -u JOB_ID             # move queued job to the front
./cli/jobd -U JOB_ID1 JOB_ID2     # swap two queued jobs
./cli/jobd -C                    # delete all finished records in this queue
```

`-r` and `-u` without an ID select the most recently submitted remaining job. Job IDs are auto-incrementing numbers **per queue**, starting at 1 in an empty queue, and are never reused after deletion. They remain decimal strings in JSON for API/client compatibility. `-U` currently takes two separate IDs (for example, `-U 1 2`). Worker IDs remain UUIDs. Use `-- COMMAND ...` for executables starting with a dash. This is the requested subset of tsp, not full flag compatibility.

The list shows state, elapsed time, progress, hostname, worker ID, exit code, output path and command. `PROGRESS` is a percentage with one decimal place (`50.0%`); queued/uninstrumented running jobs start at `0.0%`, success sets `100.0%`, and failures retain their last reported value. `ELAPSED` shows time since the controller assigned a running job, or the fixed start-to-finish duration for a finished job (for example, `2h3m4s`); queued jobs show `-`. It updates when you rerun the listing, uses whole seconds and includes launch/reporting delays, not just process execution time. `COMMAND` uses copyable Bash/Zsh quoting: simple arguments stay bare (`echo hello`), spaces/metacharacters are single-quoted (`echo 'hello world'`), and control characters use ANSI-C quoting (`echo $'line1\nline2'`). Copy the command cell, not the entire row; execution still uses your shell's local environment and working directory. Find your local worker by its ID (`~/.local/state/jobd-worker/worker-id`) and hostname. This is controller assignment status, **not** a live local process probe: a disconnected worker can still appear running. Idle workers have no running row.

Workers report the output path before launching commands. `-o` prints only the path to stdout and identifies the executing host on stderr; paths are local to **that host**, not downloaded. Older workers/jobs may have no output metadata. Clearing/removing jobs does not delete logs. Mutations are not automatically retried: if a response is lost, inspect the queue before resubmitting, especially for submissions and swaps.

New queues are initialized with the complete current schema. Existing queues are expected to already use that schema and numeric job IDs; startup does not migrate schemas or rewrite IDs. IDs can have gaps from deleted jobs. List requests fetch pages of 100; concurrent queue changes can affect pagination (not a snapshot).

### Reporting progress from a job

The worker sets `JOBD_PROGRESS_SOCKET` to a unique Unix stream socket for each job. Send UTF-8 newline-delimited JSON, one message per line:

```json
{"progress":0.5}
```

Progress must be a finite number between 0 and 1. The worker returns `{"ok":true}` followed by a newline **as soon as the highest value is buffered in worker memory** (not after upload), or `{"ok":false,"error":"..."}`. Wait for the reply before sending the next message; persistent connections and one-connection-per-update both work. Decreasing values are acknowledged but cannot lower buffered or controller progress. Socket handlers never wait for HTTP and remain fast during controller outages. A separate uploader coalesces updates to the highest value and posts to `/jobs/:id/progress` on either rule: **every 10 seconds**, or **an increase greater than 5 percentage points since the last successful upload** (20% → 25% waits for the timer; 20% → 26% uploads early). Exactly 5 points does not trigger early upload. These intervals are independent of heartbeats, which no longer carry progress. Network failures are retried with one request in flight; newer values remain buffered, and a failed attempt does not advance the last-successful value. Final completion/failure/cancellation reports also include buffered progress so short jobs do not lose their last update. Unsent updates are lost if the worker crashes; local ACKs are not durable. Unknown fields, missing/null progress and invalid ranges are rejected.

See [`examples/progress.py`](examples/progress.py) for a dependency-free Python example managed with uv:

```sh
./cli/jobd uv run /absolute/path/on/worker/to/examples/progress.py
watch -n 1 ./cli/jobd -l
```

The script and uv must exist on the executing worker; submission does not upload files. Jobs do not need controller credentials or job IDs to report progress. The socket is `0600` inside a `0700` directory and is removed after execution, before the final result is sent. Processes running as the same OS user are trusted; this is not a sandbox. Messages are limited to less than 4 KiB, connections to 16 per job, and idle reads to 30 seconds. Reconnect idle connections and handle errors in the job as appropriate. Progress is not durably buffered; avoid emitting excessive updates (once per second is usually plenty).

### Remote cancellation

`jobd -k [ID]` requests cancellation of a **running** job; without an ID it selects the most recently started remaining job. For queued jobs use `-r`. The request is durable and is safe to repeat while pending. The CLI prints confirmation of the **request**, not confirmation that the process has stopped.

Heartbeat responses tell the owning worker which job to cancel. The worker sends TERM to that job's process group, waits 5 seconds, then sends KILL; it remains running and can claim another job after the final result is accepted. Processes that escape the process group are outside this mechanism. With default settings detection normally takes up to 15 seconds plus the grace period. An offline or old worker leaves cancellation pending; upgrade the controller and workers before relying on cancellation.

`jobd -l` shows `cancelling` while the API status remains `running` with `cancel_requested: 1`. The running assignment is not released early. Once the worker reports cancellation, the job becomes `failed` with error `Job cancelled by user`, retaining its progress and log. If execution finishes before cancellation takes effect, its real outcome wins. A worker restart reports an unknown prior outcome rather than pretending it confirmed termination.

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

## Checks

```sh
pnpm typecheck
pnpm lint
pnpm test
pnpm format
cd worker
go test -race ./...
go vet ./...
test -z "$(gofmt -l *.go)"
cd ../cli
go test -race ./...
go vet ./...
test -z "$(gofmt -l *.go)"
```

### End-to-end test

```sh
uv run tooling/e2e.py
```

Requires `uv`, Go, Node.js and installed pnpm dependencies. The script declares its Python requirement and uses only the standard library. It builds temporary CLI/worker binaries, starts a real local Wrangler controller with isolated storage, and runs real workers. It checks all CLI actions, default IDs, queue ordering through actual execution, output files, queue isolation, pagination and rejection of unsafe/invalid operations. Real Python jobs verify buffered NDJSON progress at 25%, 50% and 100% in the CLI, final progress on failures between uploads, the strict greater-than-5-point threshold and 10-second periodic upload even with 60-second heartbeats, cancellation of a TERM-ignoring process group, socket cleanup and worker reuse after cancellation. Test processes, state and identified output files are cleaned up; existing workers and controller state are not used.

## Deployment and limits

`pnpm --filter jobd-controller exec wrangler login`, then `pnpm --filter jobd-controller deploy`. Configure a protected route first; `workers_dev` is deliberately disabled. **There is no authentication or sandbox: anyone with API access can execute commands on your VMs. Do not expose this controller publicly. Run workers as an unprivileged user.**

This is a scaffold, not a production scheduler. Each queue has separate SQLite `jobs` and `workers` tables in its own Durable Object. Operations read/update individual rows; claiming and completion update both tables atomically. An index selects queued jobs in queue order (FIFO unless explicitly reordered). Only each command's argv array is JSON-encoded. There are no leases, stale-worker recovery, automatic job retries or advanced scheduling. Network errors are retried; failed jobs are not requeued. On restart, a worker marks its previous assignment failed rather than replaying an unknown outcome. Lost shutdown reports or permanently lost VMs can leave jobs running. Execution is not exactly-once, and results are not durably buffered on the worker.
