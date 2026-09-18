# jobd CLI

[Overview](../README.md) · [Installation](../docs/installation.md) · [Worker](../worker/README.md) · [Controller API](../controller/README.md#api)

A tsp-style client for submitting jobs, with Cobra subcommands for management.

## Source layout

The CLI remains a single Go package, organized by responsibility:

- `main.go`: process entry point and exit handling.
- `root.go`: Cobra root, shared options, help, and command registration.
- `dispatch.go`: direct submission, short actions, and management dispatch.
- `cmd_job.go`, `cmd_env.go`, `cmd_worker.go`: command definitions and argument parsing.
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
- `JOBD_API_KEY`: the controller's shared key. No key file is written.

Set the endpoint and queue only through `JOBD_CONTROLLER` and `JOBD_QUEUE`; there are no `--controller` or `--queue` flags. For example, `JOBD_QUEUE=batch jobd job list`. For direct submission and short actions, put `--local` **before** the action or executable; everything after the executable is passed through unchanged. Use HTTPS outside localhost.

A missing or blank key selects local mode for job commands. Listings warn to set the key and restart the worker. Environment management never falls back to local mode.

## Restart the local worker

Start or restart a [detached worker](../worker/README.md#background-worker):

```sh
export JOBD_API_KEY='your-controller-key'
jobd worker restart
```

The worker inherits the CLI's directory and environment. `JOBD_CONTROLLER` and `JOBD_QUEUE` apply. No key file is written. Restart cancels active work and waits for shutdown before starting a replacement.

Use `jobd worker stop` to stop it, or `jobd worker start` to start it only if needed. Logs append to `<state-dir>/worker.log`; arrange rotation yourself. It survives SSH disconnects, but not crashes or reboots.

Install `jobd-worker` beside `jobd` or in `PATH`. Remote queue commands never auto-start a worker.

## Commands

```sh
jobd                       # list; same as -l
jobd echo hello            # submit argv; print job ID
jobd sh -c 'echo hi; sleep 60'
jobd -o [ID]               # output path; default: last started
jobd -r [ID]               # remove queued/finished job; default: last added
jobd -k [ID]               # request cancellation; default: last started
jobd -u [ID]               # move queued job first; default: last added
jobd -U ID1 ID2            # swap queued jobs
jobd -C                    # clear finished records
```

Brackets mark optional IDs; omit the brackets when typing. Defaults select remaining jobs.

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
| `jobd -u [ID]` | `jobd job urgent [ID]` |
| `jobd -U ID1 ID2` | `jobd job swap ID1 ID2` |

Use `jobd --help`, `jobd help env`, or `jobd env set --help` for command help. `jobd env`, `jobd worker`, and `jobd job` show group help without performing operations. Generate shell completion with `jobd completion bash` (also supports Zsh, Fish, and PowerShell). Persistent `config` commands are not implemented yet.

## Per-queue environment secrets

First configure the controller's [encryption key](../controller/README.md#queue-environment-secrets). Then:

```sh
JOBD_QUEUE=batch jobd env set API_KEY --stdin < /secure/path/api-key
JOBD_QUEUE=batch jobd env list
JOBD_QUEUE=batch jobd env delete API_KEY
```

`env set NAME --stdin` reads the exact value from stdin, including trailing newlines. The `--stdin` flag is required. It never accepts a value argument or prints the value. Avoid typing secrets into shell commands/history; use a protected file or pipe from a secret manager. Maximum value size is 4096 UTF-8 bytes; NUL is forbidden. Listing shows names only.

These actions require `JOBD_API_KEY` and HTTPS, including during local development; they never fall back to local mode. Queue values override inherited variables in controller job processes without changing the worker environment. Changes affect subsequent claims, not running jobs. Local jobs receive no queue secrets. Do not print secrets from jobs: output files are not redacted. All holders of the shared controller key remain trusted across all queues.

## Local fallback jobs

```sh
jobd --local sh -c 'echo background work'
jobd --local -l
jobd --local -k local-1
jobd --local -U local-1 local-2
```

All job actions work with `--local`. IDs use `local-N`. Local jobs stay off the controller, but appear in combined CLI listings.

The CLI starts a worker on demand as the same user. Existing workers keep their settings. Match `JOBD_STATE_DIR` (default `~/.local/state/jobd-worker`). The CLI uses an owner-only Unix socket, without TCP or an API key. Only the worker opens queue storage. `JOBD_QUEUE` and `JOBD_CONTROLLER` do not select local queues.

Local jobs use the worker's directory and environment. Controller work takes priority. Local work waits for a successful empty claim and a 30-second idle period. Request failures can delay it. Once started, a local job runs to completion. Without a key, there are no controller requests or idle delay.

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

Match workers by hostname and `~/.local/state/jobd-worker/worker-id`. Remote rows show controller assignments, not live process checks. Disconnected workers may still look running. Idle workers have no running row.

Lists use pages of 100, not a snapshot. Concurrent changes can affect pagination.

## Output and cancellation

`-o` prints the path on stdout and the host on stderr. Files stay on that host; nothing is downloaded. Older jobs may lack output metadata. Removing records does not delete logs.

`-k` confirms a request, not process termination. See [cancellation behavior](../worker/README.md#remote-cancellation).

Mutations are not retried automatically. If a reply is lost, inspect the queue before resubmitting, especially for submissions and swaps.
