# Local development and testing

[Overview](../README.md) · [Controller](../controller/README.md) · [CLI](../cli/README.md) · [Worker](../worker/README.md) · [Releases](releases.md)

Run commands from the repository root.

## Prerequisites

Node.js 24, pnpm 12.4.1, Go 1.27.1+, OpenSSL and uv. Workers target Linux. Python scripts declare requirements inline.

## Start the controller

```sh
nvm install && nvm use     # optional: if using nvm
corepack enable
pnpm install
export JOBD_API_KEY="$(openssl rand -base64 32)"
(umask 077; printf 'JOBD_API_KEY=%s\n' "$JOBD_API_KEY" > controller/.dev.vars)
pnpm dev                  # localhost:8787; persistent local SQLite
```

Never use a production key for development.

## Start a worker

In another terminal:

```sh
set -a
. ./controller/.dev.vars
set +a
(cd worker && go build -o jobd-worker .)
./worker/jobd-worker --controller http://localhost:8787 --queue default
```

## Submit and inspect a job

Use a terminal with the same key exported:

```sh
curl -fsS http://localhost:8787/queues/default/jobs \
  -H "Authorization: Bearer $JOBD_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"command":["echo","hello from jobd"]}'

curl -fsS http://localhost:8787/queues/default/jobs/JOB_ID \
  -H "Authorization: Bearer $JOBD_API_KEY"
```

Or use the [CLI](../cli/README.md).

## Checks

```sh
pnpm typecheck
pnpm lint
pnpm test
pnpm format
(cd worker && go test -race ./... && go vet ./... && test -z "$(gofmt -l *.go)")
(cd cli && go test -race ./... && go vet ./... && test -z "$(gofmt -l *.go)")
```

Shared config: [ESLint](../tooling/eslint/base.mjs) and [TypeScript](../tsconfig.base.json).

## End-to-end test

```sh
uv run tooling/e2e.py
```

Requires the prerequisites and installed pnpm dependencies. Builds temporary binaries. Runs real workers and a local Wrangler controller with isolated storage and a test key.

Covers:

- Authentication, CLI actions, default IDs, ordering, isolation and pagination.
- Output files and invalid operations.
- Progress buffering, thresholds, timing and final reports.
- Process-group cancellation, socket cleanup and worker reuse.
- Local/remote parity, controller priority and the 30-second fallback delay.
- Memory and persistent queues across restart.
- Detached auto-start, restart and stop.

The test cleans up its processes, state and identified logs. Existing workers and controller state are not used.

## Installer tests

```sh
uv run tooling/test-install.py
```

Uses isolated directories and local assets. See [packaging and installer tests](releases.md#local-packaging-and-installer-tests).
