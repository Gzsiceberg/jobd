# Go worker

[Overview](../README.md) · [Installation](../docs/installation.md) · [CLI](../cli/README.md) · [Controller](../controller/README.md)

The worker is a Linux daemon using only the Go standard library. It executes one command at a time, with stable local identity, periodic heartbeats, buffered progress uploads and process-group cleanup.

## Build and run

Commands below run from the repository root unless otherwise noted.

```sh
cd worker
go build -o jobd-worker .
./jobd-worker --controller https://jobd-controller.aflashsheng.workers.dev --queue default
```

Build a standalone binary for a Linux VM (use `GOARCH=arm64` for ARM):

```sh
cd worker
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o jobd-worker .
```

The release installer sets up, enables and starts `jobd-worker.service` for the current user. Set `JOBD_API_KEY` in the installer environment, or the worker will wait idle. See [installation](../docs/installation.md).

## Configuration and identity

Set `JOBD_API_KEY` in the worker's environment to the controller's shared key. Every API request includes it as a Bearer token. Without a key, the worker stays idle without contacting the controller and can still be stopped normally. Restart with `JOBD_API_KEY` set to resume; changing another shell's environment cannot update a running worker. No key file is used. With the user service below, `jobd --restart` imports the CLI environment and restarts the worker. Use HTTPS outside localhost.

### systemd user service

The [release installer](../docs/installation.md) generates, enables and starts the user service automatically, including custom binary paths. To update its environment and restart:

```sh
export JOBD_API_KEY='your-controller-key'
jobd --restart
```

For source builds, run the worker directly or create your own user service. Stop any manually launched worker using the same state directory first. View logs with `journalctl --user -u jobd-worker.service` and stop with `systemctl --user stop jobd-worker.service`. Restart cancels active work and gives the worker up to 30 seconds to shut down. After the systemd user manager restarts (for example, after reboot), import the key again with `jobd --restart`; otherwise the service waits for configuration. The release uninstaller stops and removes the service it manages, while preserving state and logs.

The worker stores its ID in `~/.local/state/jobd-worker`. Use `--state-dir PATH` for separate daemons; never copy an identity to another VM.

Poll and heartbeat intervals default to 5 and 15 seconds (`--poll-interval`, `--heartbeat-interval`, both in seconds). `JOBD_CONTROLLER`, `JOBD_QUEUE` and `JOBD_STATE_DIR` provide environment defaults; flags take precedence. The default controller is `https://jobd-controller.aflashsheng.workers.dev` and the default queue is `default`.

## Execution, output and shutdown

Commands are argv arrays, not shell strings. Explicit shell usage is possible with `["sh", "-c", "..."]`.

Each command's stdout and stderr are combined in a unique `/tmp/jobd-*.log` file (owner-only permissions). The worker logs the path when execution starts and when the job finishes. Files remain after success, failure or shutdown; the worker does not rotate or delete them, so arrange cleanup as needed. No logs are stored centrally.

Ctrl-C or SIGTERM stops polling and sends TERM to the active process group, followed by KILL after a 5-second grace period. A final result report has a separate 10-second deadline. HTTP calls retry network failures, 429 and 5xx at the poll interval; other HTTP errors are not retried by the client. Results stay in memory until accepted, so another job is not claimed while a report is pending.

On restart, an existing assignment is marked failed with an unknown outcome rather than executed again. See the controller's [deployment limits](../controller/README.md#deployment-and-limits) before relying on this for production scheduling.

## Reporting progress from a job

Progress is a fraction from 0 to 1; arbitrary commands have no inferred intermediate progress. Progress starts at 0, and completion sets 1 on success.

The worker sets `JOBD_PROGRESS_SOCKET` to a unique Unix stream socket for each job. Send UTF-8 newline-delimited JSON, one message per line:

```json
{"progress":0.5}
```

Progress must be a finite number between 0 and 1. The worker returns `{"ok":true}` followed by a newline **as soon as the highest value is buffered in worker memory** (not after upload), or `{"ok":false,"error":"..."}`.

Wait for the reply before sending the next message; persistent connections and one-connection-per-update both work. Decreasing values are acknowledged but cannot lower buffered or controller progress. Unknown fields, missing/null progress and invalid ranges are rejected.

### Upload rules

Socket handlers never wait for HTTP and remain fast during controller outages. A separate uploader coalesces updates to the highest value and posts to `/jobs/:id/progress` on either rule:

1. **Every 10 seconds.**
2. **An increase greater than 5 percentage points since the last successful upload.** For example, 20% → 25% waits for the timer; 20% → 26% uploads early. Exactly 5 points does not trigger early upload.

These intervals are independent of heartbeats, which do not carry progress. Network failures are retried with one request in flight; newer values remain buffered, and a failed attempt does not advance the last-successful value.

Final completion/failure/cancellation reports also include buffered progress so short jobs do not lose their last update. Unsent updates are lost if the worker crashes; local ACKs are not durable.

### Python example

See [examples/progress.py](../examples/progress.py) for a dependency-free Python example managed with uv. From the repository root:

```sh
./cli/jobd uv run /absolute/path/on/worker/to/examples/progress.py
watch -n 1 ./cli/jobd -l
```

The script and uv must exist on the executing worker; submission does not upload files. Jobs do not need controller credentials or job IDs to report progress.

### Socket permissions and limits

The socket is `0600` inside a `0700` directory and is removed after execution, before the final result is sent. Processes running as the same OS user are trusted; this is not a sandbox.

Messages are limited to less than 4 KiB, connections to 16 per job, and idle reads to 30 seconds. Reconnect idle connections and handle errors in the job as appropriate. Progress is not durably buffered; avoid emitting excessive updates (once per second is usually plenty).

## Remote cancellation

`jobd -k [ID]` requests cancellation of a **running** job; without an ID it selects the most recently started remaining job. For queued jobs use `-r`. The request is durable and is safe to repeat while pending. The CLI prints confirmation of the **request**, not confirmation that the process has stopped.

Heartbeat responses tell the owning worker which job to cancel. The worker sends TERM to that job's process group, waits 5 seconds, then sends KILL; it remains running and can claim another job after the final result is accepted. Processes that escape the process group are outside this mechanism.

With default settings detection normally takes up to 15 seconds plus the grace period. An offline or old worker leaves cancellation pending; upgrade the controller and workers before relying on cancellation.

`jobd -l` shows `cancelling` while the API status remains `running` with `cancel_requested: 1`. The running assignment is not released early. Once the worker reports cancellation, the job becomes `failed` with error `Job cancelled by user`, retaining its progress and log. If execution finishes before cancellation takes effect, its real outcome wins. A worker restart reports an unknown prior outcome rather than pretending it confirmed termination.

## Source layout

- [main.go](main.go): CLI configuration, signals and startup.
- [identity_linux.go](identity_linux.go): persistent UUID and single-daemon file lock.
- [worker.go](worker.go): register → recover → claim → execute → report, plus heartbeat lifecycle.
- [active_job.go](active_job.go): synchronized active-job registration and cancellation routing.
- [job_execution.go](job_execution.go): execution setup and cleanup before the terminal report.
- [client.go](client.go): typed API records and one context-aware HTTP retry loop.
- [executor_linux.go](executor_linux.go): direct argv execution and process-group shutdown.
- [progress_linux.go](progress_linux.go): private per-job NDJSON Unix socket, validation and local buffering acknowledgements.
- [progress_reporter_linux.go](progress_reporter_linux.go): per-job progress buffer, socket and uploader lifecycle; closes producers and joins uploads before returning final progress.
- [progress_upload.go](progress_upload.go): independent 10-second / greater-than-5-point upload loop, owned by the per-job reporter.
