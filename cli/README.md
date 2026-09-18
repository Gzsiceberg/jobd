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

Start or restart a [detached worker](../worker/README.md#background-worker):

```sh
export JOBD_API_KEY='your-controller-key'
jobd --restart
```

The worker inherits the CLI's directory and environment. Controller and queue flags apply. No key file is written. Restart cancels active work and waits for shutdown before starting a replacement.

Use `jobd --stop` to stop it. Logs append to `<state-dir>/worker.log`; arrange rotation yourself. It survives SSH disconnects, but not crashes or reboots. Start it again with a local command or `--restart`.

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

Commands are argv arrays. Use `sh -c` for shell syntax. Use `-- COMMAND...` for executables starting with a dash. Full tsp flag compatibility is not supported.

## Local fallback jobs

```sh
jobd --local sh -c 'echo background work'
jobd --local -l
jobd --local -k local-1
jobd --local -U local-1 local-2
```

All job actions work with `--local`. IDs use `local-N`. Local jobs stay off the controller, but appear in combined CLI listings.

The CLI starts a worker on demand as the same user. Existing workers keep their settings. Match `JOBD_STATE_DIR` (default `~/.local/state/jobd-worker`). The CLI uses an owner-only Unix socket, without TCP or an API key. Only the worker opens queue storage. `--queue` and `--controller` do not select local queues.

Local jobs use the worker's directory and environment. Controller work takes priority. Local work waits for a successful empty claim and a 30-second idle period. Request failures can delay it. Once started, a local job runs to completion. Without a key, there are no controller requests or idle delay.

The queue defaults to memory. Restart loses pending jobs and history, but keeps output logs. For persistence:

```sh
export JOBD_LOCAL_PERSIST=true
jobd --restart
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
