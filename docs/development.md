# Local development and testing

[Overview](../README.md) · [Controller](../controller/README.md) · [CLI](../cli/README.md) · [Worker](../worker/README.md) · [Releases](releases.md)

Commands below start from the repository root unless otherwise noted.

## Prerequisites

Requires Node.js 24 ([.nvmrc](../.nvmrc)), pnpm 12.4.1 and Go 1.27.1 or newer. Workers target Linux VMs; deployed worker binaries do not need Go installed. Python test scripts use `uv` and declare their requirements inline.

## Start the controller

```sh
nvm install && nvm use     # if using nvm
corepack enable
pnpm install
export JOBD_API_KEY="$(uv run tooling/generate-api-key.py)"
(umask 077; printf 'JOBD_API_KEY=%s\n' "$JOBD_API_KEY" > controller/.dev.vars)
pnpm dev                    # http://localhost:8787, local persistent SQLite
```

## Start a worker

In another terminal, load the same local-only key (never use a production key for development):

```sh
set -a
. ./controller/.dev.vars
set +a
cd worker
go build -o jobd-worker .
./jobd-worker --controller http://localhost:8787 --queue default
```

See the [worker guide](../worker/README.md) for configuration and lifecycle details.

## Submit and inspect a job

```sh
# In a terminal with the same JOBD_API_KEY exported:
curl -fsS http://localhost:8787/queues/default/jobs \
  -H "Authorization: Bearer $JOBD_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"command":["echo","hello from jobd"]}'

curl -fsS http://localhost:8787/queues/default/jobs/JOB_ID \
  -H "Authorization: Bearer $JOBD_API_KEY"
```

Or build and use the [jobd CLI](../cli/README.md).

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

Shared lint/type configuration lives in [tooling/eslint/base.mjs](../tooling/eslint/base.mjs) and [tsconfig.base.json](../tsconfig.base.json), originally copied from Tau; all configuration references are local. `controller/tsconfig.eslint.json` extends the controller config, which extends the shared base.

## End-to-end test

From the repository root:

```sh
uv run tooling/e2e.py
```

Requires `uv`, Go, Node.js and installed pnpm dependencies. [The script](../tooling/e2e.py) declares its Python requirement and uses only the standard library. It builds temporary CLI/worker binaries, starts a real local Wrangler controller with isolated storage and an ephemeral test key, and runs real workers. It verifies rejection of missing/incorrect credentials and authenticates all normal requests.

It checks all CLI actions, default IDs, queue ordering through actual execution, output files, queue isolation, pagination and rejection of unsafe/invalid operations. Real Python jobs verify:

- Buffered NDJSON progress at 25%, 50% and 100% in the CLI.
- Final progress on failures between uploads.
- The strict greater-than-5-point threshold and 10-second periodic upload even with 60-second heartbeats.
- Cancellation of a TERM-ignoring process group.
- Socket cleanup and worker reuse after cancellation.
- Persistent local CLI submissions across worker restart, controller priority, the real 30-second fallback delay, and raw Unix-socket commands.
- Local/remote parity for progress, cancellation, urgent/swap, default job selection, listing, output and cleanup.

Local CLI commands reuse the HTTP client over an owner-only Unix socket, with no TCP listener or API key. Job progress uses a separate NDJSON Unix socket. Queue storage belongs entirely to the worker, using SQLite through the CGO-free `modernc.org/sqlite` driver. Storage defaults to `:memory:`; `JOBD_LOCAL_PERSIST=true` opts into the database file. Tests cover both restart behaviors; there is no shared Go module.

Test processes, state and identified output files are cleaned up; existing workers and controller state are not used.

## Installer tests

```sh
uv run tooling/test-install.py
```

These tests use isolated directories and local release assets. See [release packaging](releases.md#local-packaging-and-installer-tests) for details.
