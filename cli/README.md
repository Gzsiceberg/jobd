# jobd CLI

[Overview](../README.md) · [Installation](../docs/installation.md) · [Worker](../worker/README.md) · [Controller API](../controller/README.md#api)

A tsp-style client for submitting jobs, with Cobra subcommands for management.

## Source layout

The CLI remains a single Go package, organized by responsibility:

- `main.go`: process entry point and exit handling.
- `root.go`: Cobra root, shared options, help, and command registration.
- `dispatch.go`: direct submission, short actions, and management dispatch.
- `cmd_job.go`, `cmd_env.go`, `cmd_worker.go`, `cmd_auth.go`: command definitions and argument parsing.
- `jobs.go`, `env.go`, `worker.go`: shared operations, independent of Cobra.
- `client.go`: controller HTTP transport and authentication.
- `local.go`: local worker socket connection.
- `list.go`, `format.go`: job listings and display formatting.

Keep command handlers thin: call shared operations rather than implementing HTTP requests in command files. Shortcuts and explicit job commands use the same operations. End-to-end dispatch tests live in `dispatch_test.go`; operation and formatting tests sit alongside their respective files.

## Build

Requires Go 1.27.1. From the repository root:

```sh
(cd cli && go build -o jobd .)
./cli/jobd --help
```

Examples below use the installed `jobd`. For source builds, use `./cli/jobd`.

## Configuration

- `JOBD_CONTROLLER`: defaults to `https://jobd-controller.aflashsheng.workers.dev`.
- `JOBD_QUEUE`: defaults to `default`.
- `JOBD_MASTER_KEY`: admin credential for all controller operations, including env and worker-token generation. Takes precedence when both credentials are set, except for `auth verify-worker-token`.
- `JOBD_WORKER_TOKEN`: generated, expiring worker token for job reads and worker operations in its authorized queue. Cannot submit/manage jobs or env. No key file is written.

Set the endpoint and queue only through `JOBD_CONTROLLER` and `JOBD_QUEUE`; there are no `--controller` or `--queue` flags. For example, `JOBD_QUEUE=batch jobd job list`. For direct submission and short actions, put `--local` **before** the action or executable; everything after the executable is passed through unchanged. Use HTTPS outside localhost.

When both credentials are missing or blank, job commands select local mode. Listings warn to set the key and restart the worker. Environment management never falls back to local mode.

## Generate a worker token

In a trusted admin shell with `JOBD_MASTER_KEY` set:

```sh
export JOBD_WORKER_TOKEN="$(JOBD_QUEUE=batch jobd auth create-worker-token --duration 24h)"
```

The controller issues a token for `JOBD_QUEUE` (default `default`). `--duration` is required: whole seconds from `1s` through `720h` (30 days), using Go duration syntax such as `24h` or `168h`, not `7d`. HTTPS is required, including localhost. Token goes to stdout; expiry goes to stderr. Only send the generated token to the worker machine; never copy `JOBD_MASTER_KEY` there.

Worker tokens allow listing/inspecting jobs and the full worker lifecycle, not job submission or administration. They are stateless and cannot be individually revoked before expiry. All requests fail after expiry; renew manually before expiry and restart workers while idle. For persistent storage, use a private `0600` file outside the repository, loaded by your service manager. No key file is created automatically.

## Verify a worker token

```sh
jobd auth verify-worker-token
```

Checks `JOBD_WORKER_TOKEN` against the controller for `JOBD_QUEUE`, even when `JOBD_MASTER_KEY` is set. Prints the queue, UTC expiry, and remaining lifetime (for example, `23h59m58s`). Remaining time uses the CLI machine's clock, rounded down to whole seconds and clamped to zero. HTTPS is required, including localhost. Missing, invalid, expired, or wrong-queue tokens fail with a nonzero exit status. The token is never printed. This verifies authentication, not worker connectivity.

## Restart the local worker

Start or restart a [detached worker](../worker/README.md#background-worker):

```sh
export JOBD_WORKER_TOKEN='your-generated-worker-token'
jobd worker restart
```

The worker inherits the CLI's directory and environment except `JOBD_MASTER_KEY`, which the CLI strips before launching it. `JOBD_CONTROLLER` and `JOBD_QUEUE` apply. No key file is written. Restart cancels active work and waits for shutdown before starting a replacement.

Use `jobd worker stop` to stop it, or `jobd worker start` to start it only if needed. Logs append to `<state-dir>/worker.log`; arrange rotation yourself. It survives SSH disconnects, but not crashes or reboots.

Install `jobd-worker` beside `jobd` or in `PATH`. Remote queue commands never auto-start a worker.

## Pause remote assignments

```sh
jobd worker list                    # worker name and token time remaining (admin only)
jobd worker pause a1b2c3d4
jobd worker resume a1b2c3d4
jobd worker pause a1b2c3d4 e5f6a7b8
jobd worker resume a1b2c3d4 e5f6a7b8
```

IDs/prefixes are sent in one server-side batch. Valid workers are updated even if another ID is missing or ambiguous. Successful workers are printed; failures identify each rejected target and make the CLI exit nonzero. Duplicate IDs are processed once; confirmations also deduplicate prefixes resolving to the same worker. The batch is not atomic.

Requires `JOBD_MASTER_KEY`. Use a full worker ID or the prefix shown by `jobd -l`. Resolution includes all registered workers in `JOBD_QUEUE`, including idle/offline workers: exact IDs win, ambiguous prefixes fail and show matching IDs without changing anything.

Pause prevents new controller assignments, not execution of an already assigned job. Current work finishes; heartbeats and local fallback jobs continue. A job assigned just before pause may start after confirmation. Pause is durable across worker/controller restarts until explicitly resumed; repeated pause/resume is safe. Confirmation shows the full worker ID and hostname. `--local` is not supported.

Resume only clears this administrative pause. It does not clear the worker's separate pause after a failed remote job; that still requires restarting the worker. `worker stop` remains a local daemon shutdown that cancels active work.

## Commands

```sh
jobd                       # list; same as -l
jobd echo hello            # submit argv; print job ID
jobd sh -c 'echo hi; sleep 60'
jobd -o [ID]               # output path; default: last started
jobd -r [ID]               # remove queued/finished job; default: last added
jobd -k [ID]               # request cancellation; default: last started
jobd -u [ID...]            # move queued jobs first in argument order; default: last added
jobd -U ID1 ID2            # swap queued jobs
jobd -C                    # clear finished records
```

Brackets mark optional IDs; omit the brackets when typing. Defaults select remaining jobs.

`jobd worker list` lists all registered workers in the selected queue, including offline workers. It shows a unique `WORKER_ID` prefix (as in `jobd -l`), hostname as `WORKER_NAME`, and a relative duration under `TOKEN_EXPIRED_TIME` (for example, `1h2m0s remaining` or `expired 5m0s ago`). Expiry is unknown until the controller observes a successful token-authenticated heartbeat. This command requires `JOBD_MASTER_KEY` and does not start a local worker.

Controller job IDs are numeric and per queue. They start at 1 and are not reused after deletion. JSON uses decimal strings. Worker IDs are UUIDs.

Commands are argv arrays. Use `sh -c` for shell syntax. Full tsp flag compatibility is not supported.

### Management and name collisions

The reserved roots are **`env`, `worker`, `job`, `help`, and `completion`**. They always select management, even if the remaining arguments are invalid. For example, `jobd env typo` fails; it never queues a job.

Use `--` to force submission, including reserved names or executables starting with a dash:

```sh
jobd -- env                 # submit the system's env command
jobd -- worker restart     # submit an executable named worker
jobd echo --help            # --help belongs to echo, not jobd
JOBD_QUEUE=batch jobd -- env # select queue, then force submission
```

All job operations also have explicit forms that share the same implementation and defaults:

| Short form | Explicit form |
| --- | --- |
| `jobd echo hello` | `jobd job submit -- echo hello` |
| `jobd -l` | `jobd job list` |
| `jobd -C` | `jobd job clear` |
| `jobd -o [ID]` | `jobd job output [ID]` |
| `jobd -r [ID]` | `jobd job remove [ID]` |
| `jobd -k [ID]` | `jobd job cancel [ID]` |
| `jobd -u [ID...]` | `jobd job urgent [ID...]` |
| `jobd -U ID1 ID2` | `jobd job swap ID1 ID2` |

Use `jobd --help`, `jobd help env`, or `jobd env set --help` for command help. `jobd env`, `jobd worker`, and `jobd job` show group help without performing operations. Generate shell completion with `jobd completion bash` (also supports Zsh, Fish, and PowerShell). Persistent `config` commands are not implemented yet.

Batch retry, urgent, and worker pause/resume accept 1–100 IDs per request. Deploy the controller and update local workers before upgrading the CLI; these commands require the new batch endpoints. Single-target endpoints remain available for older clients. Malformed batch payloads are rejected before processing; valid payloads return per-target results. A lost response can leave outcomes unknown; the CLI does not automatically replay batches.

## Prioritize queued jobs

`jobd -u 3 5 2` (or `jobd job urgent 3 5 2`) moves queued jobs to the front in argument order: 3, then 5, then 2. Without IDs, it selects the last-added job. This also works with `--local`.

IDs are sent in one server-side batch. The server processes them backward so successful targets retain argument order at the front. Invalid targets are skipped and reported; other targets still succeed. Duplicate IDs are processed once. The CLI prints the success count and exits nonzero if any target failed. The batch is not atomic; local workers may claim jobs between individual updates.

## Remove all non-running jobs

```sh
JOBD_QUEUE=batch jobd job remove --all
jobd --local job remove --all
```

The command displays the target and asks you to type `yes` and press Enter. Nothing is removed on an empty answer, mismatch, or input error. There is no `--yes` bypass, and `--all` cannot be combined with a job ID. Without either `JOBD_MASTER_KEY` or `JOBD_WORKER_TOKEN`, the target is the local queue, as with other job actions; the prompt explicitly identifies it.

This atomically removes **queued and finished** records in the selected queue. Running jobs are kept and counted in the result. Secrets, worker registrations, output files, and job ID sequences are preserved. The deletion applies to jobs present when the operation executes, including submissions made while the prompt was open. Jobs claimed before deletion are kept as running. No requests or worker startup occur until confirmation; failed requests are not retried automatically.

`jobd job clear` / `jobd -C` still removes only finished records without this prompt.

## Retry failed jobs

```sh
jobd job retry 123          # Requeue one failed job
jobd job retry 42 43 44     # Requeue multiple failed jobs in order
jobd job retry {42..56}     # Bash/Zsh range expansion
jobd job retry --all        # Requeue all failed jobs in the selected queue
jobd --local job retry --all
```

IDs are sent in one server-side batch and processed in argument order. Invalid targets are skipped and reported; other targets still succeed. Duplicate IDs are processed once. The CLI prints the success count and exits nonzero if any target failed. IDs cannot be combined with `--all`, which retains its dedicated atomic operation.

Retries keep the same IDs and commands, append jobs to the back of the queue, and clear previous execution details. Existing log files remain on disk. Only failed jobs are eligible; queued, running and successful jobs are unchanged. Remote retries require `JOBD_MASTER_KEY`. Retrying does not resume remote claims on a paused worker: use `jobd worker restart` on that worker.

## Per-queue environment secrets

First configure the controller's [encryption key](../controller/README.md#queue-environment-secrets). Then:

```sh
JOBD_QUEUE=batch jobd env set API_KEY=XXX KEY=SSS
JOBD_QUEUE=batch jobd env list
JOBD_QUEUE=batch jobd env delete API_KEY
```

`env set KEY[=VALUE] [KEY[=VALUE] ...]` sets one or more secrets. For a bare name, such as `jobd env set API_KEY`, type its value on stdin and press Enter; the line ending is not stored. Terminal input is hidden (no echo). After Enter, a preview shows the character count and, for values longer than 12 characters, the first and last four characters. These fragments remain visible in terminal scrollback. `Save? [Y/n]` accepts Enter or `y` to confirm; `n` lets you re-enter the value. All interactive values must be confirmed before any upload. Piped input is also supported without previews or confirmation. Multiple bare names read one value each, and can be mixed with `KEY=VALUE` arguments. An empty line sets an empty value; EOF without a value is an error. Quote assignments containing spaces or shell metacharacters, for example `jobd env set 'MESSAGE=hello world'`. Empty values (`KEY=`) and additional `=` characters in values are supported. `--stdin` is no longer supported. Values passed as arguments may appear in shell history and process listings; the CLI never prints their full values. Maximum value size is 4096 UTF-8 bytes; NUL is forbidden. All assignments are validated before uploading, then saved sequentially; a failed request does not roll back earlier updates. Listing shows names only.

These actions require `JOBD_MASTER_KEY` and HTTPS, including during local development; they never fall back to local mode. Queue values override inherited variables in controller job processes without changing the worker environment. Changes affect subsequent claims, not running jobs. Local jobs receive no queue secrets. Do not print secrets from jobs: output files are not redacted. Admin key holders are trusted across all queues; worker token holders can receive secrets through claims in their authorized queue.

## Local fallback jobs

```sh
jobd --local sh -c 'echo background work'
jobd --local -l
jobd --local -k local-1
jobd --local -U local-1 local-2
```

All job actions work with `--local`. IDs use `local-N`. Local jobs stay off the controller, but appear in combined CLI listings.

The CLI starts a worker on demand as the same user. Existing workers keep their settings. Match `JOBD_STATE_DIR` (default `~/.local/state/jobd-worker`). The CLI uses an owner-only Unix socket, without TCP or an API key. Only the worker opens queue storage. `JOBD_QUEUE` and `JOBD_CONTROLLER` do not select local queues.

Local jobs use the worker's directory and environment. Controller work takes priority. A failed controller job pauses further remote claims until the worker restarts, while local work continues after the result is reported. Local failures do not pause remote claims. Otherwise, local work starts immediately after a successful empty controller claim. Request failures can delay it. Once started, a local job runs to completion. Without a key, local work runs directly without controller requests.

The queue defaults to memory. Restart loses pending jobs and history, but keeps output logs. For persistence:

```sh
export JOBD_LOCAL_PERSIST=true
jobd worker restart
```

Persistent pending jobs survive restart. Previously running jobs fail, without replay. Switching modes does not migrate jobs. Memory mode preserves but ignores an existing database. See [worker storage details](../worker/README.md#local-fallback-queue).

## Listing jobs

With an API key, `jobd` and `jobd -l` show remote jobs first, then local fallback jobs. Combined listings never start a local worker. A missing local worker is skipped.

If one queue fails, the other is shown with a warning. If both fail, listing returns an error. `jobd --local -l` shows only local jobs. Without a key, listing uses local mode and starts the worker if needed.

Actions still target one queue. Use `jobd --local -k local-1` for local jobs and `jobd -k 42` for remote jobs. Default IDs never span queues.

```text
ID  STATE  ELAPSED  HOST  WORKER  EXIT  OUTPUT  COMMAND
```

Numeric IDs are remote jobs. `local-N` IDs are local fallback jobs.

- `WORKER`: first 8 characters of the ID. Colliding prefixes expand within the listing. Full IDs remain unchanged.
- `ELAPSED`: whole seconds from assignment to now or completion. Includes launch/reporting delays. Queued jobs show `-`. Rerun to refresh.
- `COMMAND`: Bash/Zsh quoting, capped at 60 characters with `...`. Truncation affects display only. Only untruncated commands can be copied for execution.

Match workers by hostname and `~/.local/state/jobd-worker/worker-id`. Remote rows show controller assignments, not live process checks. Listing, clearing, or removing jobs first fails running/cancelling jobs whose worker has not refreshed its heartbeat for two minutes, with `Worker disconnected; outcome unknown`. It releases assignments without retrying or stopping remote processes. `jobd -C` therefore removes stale job records directly, without a preceding list; output files remain untouched. Until that threshold, disconnected workers may still look running. Idle workers have no running row.

Lists use pages of 100, not a snapshot. Concurrent changes can affect pagination.

## Output and cancellation

`-o` prints the path on stdout and the host on stderr. Files stay on that host; nothing is downloaded. Older jobs may lack output metadata. Removing records does not delete logs.

`-k` confirms a request, not process termination. See [cancellation behavior](../worker/README.md#remote-cancellation).

Mutations are not retried automatically. If a reply is lost, inspect the queue before resubmitting, especially for submissions and swaps.
