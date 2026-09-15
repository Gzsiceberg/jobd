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

Set `JOBD_CONTROLLER` and `JOBD_QUEUE`, or pass `--controller URL` and `--queue NAME` **before** the action/command. Defaults match the worker: `http://localhost:8787` and `default`.

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
