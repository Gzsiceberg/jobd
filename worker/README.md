# Go worker

[Overview](../README.md) · [Installation](../docs/installation.md) · [CLI](../cli/README.md) · [Controller](../controller/README.md)

A Linux daemon. Runs one command at a time.

## Queue environment

Controller claims deliver the queue's [encrypted-at-rest environment secrets](../controller/README.md#queue-environment-secrets) over HTTPS. The worker keeps decrypted values only in memory and injects them into the assigned job process, overriding inherited values. It does not save them in job records or local storage, or modify its own environment. Local jobs never receive queue secrets. `JOBD_` names are allowed, but `JOBD_WORKER_TOKEN` and `JOBD_MASTER_KEY` are stripped from job environments.

Changes apply on subsequent claims, not to running processes. Job code can read these values and may expose them through output or network requests; output files are not redacted. The worker host and submitted code must be trusted. Never configure the controller's `JOBD_MASTER_KEY` on workers.

## Build and run

From the repository root:

```sh
(cd worker && go build -o jobd-worker .)
./worker/jobd-worker --controller https://jobd-controller.aflashsheng.workers.dev --queue default
```

For a standalone Linux binary, set `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`. Use `arm64` for ARM.

## Configuration and identity

| Setting | Default | Flag |
| --- | --- | --- |
| `JOBD_CONTROLLER` | `https://jobd-controller.aflashsheng.workers.dev` | `--controller` |
| `JOBD_QUEUE` | `default` | `--queue` |
| `JOBD_STATE_DIR` | `~/.local/state/jobd-worker` | `--state-dir` |
| Poll interval | 5 seconds | `--poll-interval` |
| Heartbeat interval | 15 seconds | `--heartbeat-interval` |

Flags override environment values. Interval flags use seconds.

Set `JOBD_WORKER_TOKEN` to a generated worker token for this queue (`jobd auth create-worker-token --duration 24h` in a trusted admin shell with `JOBD_MASTER_KEY`). Never configure the admin key on workers. Tokens expire; replace them before expiry and restart while idle. No automatic renewal or individual revocation is implemented. Store persistent tokens in a private `0600` file outside the repository, loaded by your service manager. Without it, the worker runs only local jobs. Restart to apply environment changes. Use HTTPS outside localhost.

The state directory holds the worker ID. Use separate directories for separate daemons. Never copy an identity to another VM.

### Background worker

Local CLI commands start a detached worker on demand. Remote commands do not. To start a controller worker explicitly:

```sh
export JOBD_WORKER_TOKEN='your-generated-worker-token'
jobd worker restart
tail -f ~/.local/state/jobd-worker/worker.log
jobd worker stop
```

The worker inherits the starting CLI's directory and environment. No key file is written. Restart applies new settings, cancels active work and waits up to 30 seconds for shutdown.

The worker survives SSH disconnects. It does not restart itself after a crash or reboot. The next local command starts it again. For unattended controller workers, use your container's startup command or a supervisor.

Source builds can run in the foreground. For CLI auto-start, put `jobd-worker` beside `jobd` or in `PATH`.

The worker owns its lifecycle:

```sh
jobd-worker --start     # start if absent; wait until healthy
jobd-worker --restart   # stop fully, then start with current settings
jobd-worker --stop      # stop and wait for cleanup
```

The CLI delegates to these commands. Without an action flag, `jobd-worker` runs in the foreground.

### Health check

`GET /health` returns `200` with `{"status":"ok"}`. During shutdown it returns `503`. This checks the local HTTP handler, not controller connectivity or job success.

```sh
curl --unix-socket "${JOBD_STATE_DIR:-$HOME/.local/state/jobd-worker}/local/control.sock" http://local/health
```

Worker lifecycle commands use this endpoint to check readiness. An unhealthy response is an error, not a reason to launch another worker.

## Execution, output and shutdown

- Commands are argv arrays. Use `sh -c` for shell syntax.
- Jobs use the worker's user, directory and environment, except `JOBD_WORKER_TOKEN` and `JOBD_MASTER_KEY`. They are **not sandboxed**.
- Combined stdout/stderr goes to owner-only `/tmp/jobd-*.log` files. Paths are logged. Files stay on the worker; arrange cleanup yourself.
- Ctrl-C or SIGTERM stops polling. The active process group gets TERM, then KILL after 5 seconds. Final reporting has a separate 10-second deadline.
- Network errors, 429 and 5xx retry at the poll interval. Other HTTP errors do not.
- Results stay in memory until accepted. Pending reports block new claims.
- Restart marks an existing assignment failed with an unknown outcome. It does not replay it. See [deployment limits](../controller/README.md#deployment-and-limits).

## Local fallback queue

Use `jobd --local COMMAND...` as the worker's user. Match `JOBD_STATE_DIR`. The CLI starts the worker if absent.

The CLI connects to `<state-dir>/local/control.sock`. The socket is `0600`; its directory is `0700`. There is no TCP listener or API key. Only the worker accesses SQLite. Transactions make claims and edits atomic.

`jobd --local job remove --all` asks for confirmation before atomically deleting queued and finished local records. The local socket endpoint is `POST /jobs/remove-all`; it returns `removed` and `kept_running` counts. Running jobs and their cancellation state remain intact, output files are kept, and job IDs are never reset.

Controller jobs take priority. Local work starts immediately after a successful empty controller claim. Request failures block local work until a claim succeeds. Without a key, local work runs directly without controller requests.

A local job runs to completion before the next controller claim. Heartbeats continue. Results stay local. Execution and cancellation match controller jobs.

| `JOBD_LOCAL_PERSIST` | Storage | On restart |
| --- | --- | --- |
| `false` or `0` (default) | SQLite `:memory:` | Queue/history lost; IDs may repeat |
| `true` or `1` | `<state-dir>/local/queue.db` | Pending jobs survive; running jobs fail |

Invalid values are rejected. Export the setting and run `jobd worker restart` to apply it. Modes do not migrate jobs. Memory mode preserves but ignores an existing database. Re-enabling persistence loads it. Stop the worker before backing it up.

Logs, identity and the daemon lock stay on disk in both modes. See [local CLI commands](../cli/README.md#local-fallback-jobs).

## Remote cancellation

`jobd -k [ID]` requests cancellation of a running job. Without an ID, it selects the last-started remaining job. Use `-r` for queued jobs.

The request is durable and safe to repeat while pending. **Confirmation means requested, not stopped.** Heartbeats deliver it to the worker. Detection normally takes up to 15 seconds, then TERM and a 5-second grace before KILL. Escaped process groups are not covered.

Offline or old workers leave cancellation pending. Upgrade both controller and workers before relying on it.

The CLI shows `cancelling`; the API keeps `running` with `cancel_requested: 1`. The assignment stays held until reporting finishes. Confirmed cancellation becomes `failed` with `Job cancelled by user`. Logs remain. If execution finishes first, its real outcome wins. Restart reports an unknown prior outcome.

## Source layout

| Files | Role |
| --- | --- |
| `main.go`, `daemon.go`, `identity_linux.go` | Configuration, daemon lifecycle, identity and lock |
| `worker.go`, `active_job.go` | Worker lifecycle and cancellation |
| `job.go`, `job_execution.go`, `executor_linux.go` | Job model and execution |
| `client.go` | API client and retries |
| `local_queue.go`, `local_socket.go` | Local scheduling, SQLite and socket API |
