# jobd CLI

[Overview](../README.md) · [Installation](../docs/installation.md) · [Worker](../worker/README.md) · [Controller API](../controller/README.md#api)

The Go `jobd` client provides tsp-style submission, inspection and management of jobs across workers in a selected controller queue.

## Build

Commands below run from the repository root. Requires Go 1.27.1, as declared in [go.mod](go.mod).

```sh
(cd cli && go build -o jobd .)
./cli/jobd --help
```

If installed from a release, use `jobd` instead of `./cli/jobd`.

## Configuration

Set `JOBD_CONTROLLER` and `JOBD_QUEUE`, or pass `--controller URL` and `--queue NAME` **before** the action/command. Defaults match the worker: `https://jobd-controller.aflashsheng.workers.dev` and `default`.

Set `JOBD_API_KEY` to the controller's shared key. The CLI sends it as a Bearer token on every controller API request. When it is missing or blank, queue actions automatically use local mode; `jobd -l` (and the default listing) warns on stderr to set `JOBD_API_KEY` and run `jobd --restart`. Use HTTPS outside localhost. Keys are environment-only; no key file is written.

## Restart the local worker

After [installing the systemd user service](../worker/README.md#systemd-user-service), run:

```sh
export JOBD_API_KEY='your-controller-key'
jobd --restart
```

This imports `JOBD_API_KEY`, the selected controller and queue (including CLI flags), `JOBD_STATE_DIR` (or its default), and `JOBD_LOCAL_PERSIST` (default `false`) into the systemd user manager, then runs `systemctl --user restart jobd-worker.service`. No controller API request is made. An unset key clears the imported key and starts the replacement worker in local-only mode. Restarting cancels any running job; it does not replay it.

The environment remains in the user manager's memory and may be inherited by other user services; use a dedicated worker account where appropriate. It is not persisted across user-manager restarts. Unit-level environment overrides take precedence, so do not set these variables in the service unit if you want CLI environment updates to apply.

## Commands

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

`-r` and `-u` without an ID select the most recently submitted remaining job. Job IDs are auto-incrementing numbers **per queue**, starting at 1 in an empty queue, and are never reused after deletion. They remain decimal strings in JSON for API/client compatibility. IDs can have gaps from deleted jobs. `-U` currently takes two separate IDs (for example, `-U 1 2`). Worker IDs remain UUIDs.

Use `-- COMMAND ...` for executables starting with a dash. This is a subset of tsp, not full flag compatibility. Commands are argv arrays, not shell strings; use an explicit shell for shell syntax.

## Local fallback jobs

```sh
jobd --local sh -c 'echo background work'
jobd --local -l
jobd --local -o local-1     # output path; omit ID for the last started local job
jobd --local -r local-1     # remove a queued or finished job; default: last added
jobd --local -k local-1     # cancel a running job; default: last run
jobd --local -u local-1     # move a queued job first; default: last added
jobd --local -U local-1 local-2  # swap two queued jobs
jobd --local -C             # clear finished records, keeping output files
```

The CLI reuses its HTTP client over the worker's owner-only Unix-domain socket, without TCP or an API key. Only the worker accesses the queue, which uses in-memory SQLite by default. Local jobs use the worker's working directory and environment, just like controller jobs. Commands are argv arrays; use `sh -c` explicitly for shell syntax.

The worker checks the controller first. After 30 seconds since the first empty controller poll, it may start the next local job. A local job runs to completion even if controller work arrives meanwhile. Controller request failures do not reset the idle timer. A successful empty claim is still required before starting local work, so retries block execution while the controller is unreachable. Without `JOBD_API_KEY`, the worker instead runs local jobs without the idle delay or any controller requests. The CLI also selects local mode automatically when its key is missing; `--local` still selects local jobs when a key is configured.

Set `JOBD_STATE_DIR` to match the worker's state directory (default `~/.local/state/jobd-worker`). By default, restart discards queued jobs and history, but not output log files. To persist the queue, export `JOBD_LOCAL_PERSIST=true` and run `jobd --restart`. Submission, listing and management require the worker to be running; an API key is not required. The CLI reports a connection error if the worker is stopped. There is one local queue per state directory; `--queue` and `--controller` do not select it. In persistent mode, jobs left running after a restart are marked failed, never replayed automatically. Switching storage modes does not migrate jobs; memory mode ignores and preserves any existing database.

Local jobs use the same job model, actions, defaults, listing columns and progress/cancellation behavior as controller jobs. They have `local-N` IDs and do not appear in controller listings. Progress is reported over the same per-job socket and saved locally, not sent to the controller. Output stays in the worker's `/tmp` logs. A normal worker shutdown cancels the local process group and records failure.

## Listing jobs

`jobd` and `jobd -l` show these columns:

```text
ID  STATE  ELAPSED  PROGRESS  HOST  WORKER  EXIT  OUTPUT  COMMAND
```

- `PROGRESS`: percentage with one decimal place (`50.0%`). Queued/uninstrumented running jobs start at `0.0%`, success sets `100.0%`, and failures retain their last reported value. See [reporting progress](../worker/README.md#reporting-progress-from-a-job).
- `ELAPSED`: time since the controller assigned a running job, or the fixed start-to-finish duration for a finished job (for example, `2h3m4s`); queued jobs show `-`. It updates when you rerun the listing, uses whole seconds and includes launch/reporting delays, not just process execution time.
- `COMMAND`: copyable Bash/Zsh quoting. Simple arguments stay bare (`echo hello`), spaces/metacharacters are single-quoted (`echo 'hello world'`), and control characters use ANSI-C quoting (`echo $'line1\nline2'`). Copy the command cell, not the entire row; execution still uses your shell's local environment and working directory.

Find your local worker by its ID (`~/.local/state/jobd-worker/worker-id`) and hostname. This is controller assignment status, **not** a live local process probe: a disconnected worker can still appear running. Idle workers have no running row.

List requests fetch pages of 100; concurrent queue changes can affect pagination (not a snapshot).

## Output and cancellation

Workers report the output path before launching commands. `-o` prints only the path to stdout and identifies the executing host on stderr; paths are local to **that host**, not downloaded. Older workers/jobs may have no output metadata. Clearing/removing jobs does not delete logs.

`-k` requests cancellation, rather than confirming that the process has stopped. See the worker's [remote cancellation behavior](../worker/README.md#remote-cancellation) for timing, pending state and final outcomes.

Mutations are not automatically retried: if a response is lost, inspect the queue before resubmitting, especially for submissions and swaps.
