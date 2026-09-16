# jobd CLI

[Overview](../README.md) · [Installation](../docs/installation.md) · [Worker](../worker/README.md) · [Controller API](../controller/README.md#api)

A tsp-style client for submitting and managing jobs.

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

Override the endpoint or queue with `--controller URL` or `--queue NAME`. Put flags **before** the action or command. Use HTTPS outside localhost.

A missing or blank key selects local mode. Listings warn to set the key and run `jobd --restart`.

## Restart the local worker

Requires the [installed user service](../worker/README.md#systemd-user-service):

```sh
export JOBD_API_KEY='your-controller-key'
jobd --restart
```

This imports the key, selected endpoint/queue, state directory and persistence setting into the systemd user manager. Defaults apply to unset settings. An unset key clears the old key. No controller request is made.

Restart cancels active jobs; it does not replay them. Imported values stay in memory and may reach other user services. Use a dedicated account. Reimport after the user manager restarts. Unit-level environment settings override imported values.

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

Commands are argv arrays. Use `sh -c` for shell syntax. Use `-- COMMAND...` for executables starting with a dash. Full tsp flag compatibility is not supported.

## Local fallback jobs

```sh
jobd --local sh -c 'echo background work'
jobd --local -l
jobd --local -k local-1
jobd --local -U local-1 local-2
```

All job actions work with `--local`. IDs use `local-N`. Local jobs do not appear in controller listings.

The worker must run as the same user. Match its `JOBD_STATE_DIR` (default `~/.local/state/jobd-worker`). The CLI uses an owner-only Unix socket, without TCP or an API key. Only the worker opens queue storage. `--queue` and `--controller` do not select local queues.

Local jobs use the worker's directory and environment. Controller work takes priority. Local work waits for a successful empty claim and a 30-second idle period. Request failures can delay it. Once started, a local job runs to completion. Without a key, there are no controller requests or idle delay.

The queue defaults to memory. Restart loses pending jobs and history, but keeps output logs. For persistence:

```sh
export JOBD_LOCAL_PERSIST=true
jobd --restart
```

Persistent pending jobs survive restart. Previously running jobs fail, without replay. Switching modes does not migrate jobs. Memory mode preserves but ignores an existing database. See [worker storage details](../worker/README.md#local-fallback-queue).

## Listing jobs

`jobd` and `jobd -l` show:

```text
ID  STATE  ELAPSED  PROGRESS  HOST  WORKER  EXIT  OUTPUT  COMMAND
```

- `PROGRESS`: one decimal place. Starts at `0.0%`; success sets `100.0%`. Failure keeps the last value. See [progress reporting](../worker/README.md#reporting-progress-from-a-job).
- `ELAPSED`: whole seconds from assignment to now or completion. Includes launch/reporting delays. Queued jobs show `-`. Rerun to refresh.
- `COMMAND`: copyable Bash/Zsh quoting. Copy only this cell. Local shell execution uses your local environment and directory.

Match workers by hostname and `~/.local/state/jobd-worker/worker-id`. Listings show controller assignments, not live process checks. Disconnected workers may still look running. Idle workers have no running row.

Lists use pages of 100, not a snapshot. Concurrent changes can affect pagination.

## Output and cancellation

`-o` prints the path on stdout and the host on stderr. Files stay on that host; nothing is downloaded. Older jobs may lack output metadata. Removing records does not delete logs.

`-k` confirms a request, not process termination. See [cancellation behavior](../worker/README.md#remote-cancellation).

Mutations are not retried automatically. If a reply is lost, inspect the queue before resubmitting, especially for submissions and swaps.
