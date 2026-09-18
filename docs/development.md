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
export JOBD_MASTER_KEY="$(openssl rand -base64 32)"
(umask 077; printf 'JOBD_MASTER_KEY=%s\n' "$JOBD_MASTER_KEY" > controller/.dev.vars)
# Private local certificate; never disable TLS verification in clients.
mkdir -p "$HOME/.local/share/jobd-dev"
(umask 077; openssl req -x509 -newkey rsa:2048 -nodes -days 7 \
  -keyout "$HOME/.local/share/jobd-dev/key.pem" \
  -out "$HOME/.local/share/jobd-dev/cert.pem" \
  -subj '/CN=localhost' -addext 'subjectAltName=IP:127.0.0.1,DNS:localhost')
pnpm dev --local-protocol https \
  --https-key-path "$HOME/.local/share/jobd-dev/key.pem" \
  --https-cert-path "$HOME/.local/share/jobd-dev/cert.pem"
```

Never use a production key for development. Keep an existing development `JOBD_MASTER_KEY` if you need its encrypted queue secrets; regenerating it makes those values unreadable.

## Start a worker

In another terminal:

```sh
set -a
. ./controller/.dev.vars
set +a
export SSL_CERT_FILE="$HOME/.local/share/jobd-dev/cert.pem"
export JOBD_CONTROLLER=https://127.0.0.1:8787
(cd cli && go build -o jobd .)
(cd worker && go build -o jobd-worker .)
export JOBD_WORKER_TOKEN="$(./cli/jobd auth create-worker-key --duration 24h)"
env -u JOBD_MASTER_KEY ./worker/jobd-worker --controller "$JOBD_CONTROLLER" --queue default
```

## Submit and inspect a job

Use an admin terminal with `JOBD_MASTER_KEY` exported from `controller/.dev.vars`:

```sh
curl --cacert "$HOME/.local/share/jobd-dev/cert.pem" -fsS https://127.0.0.1:8787/queues/default/jobs \
  -H "Authorization: Bearer $JOBD_MASTER_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"command":["echo","hello from jobd"]}'

curl --cacert "$HOME/.local/share/jobd-dev/cert.pem" -fsS https://127.0.0.1:8787/queues/default/jobs/JOB_ID \
  -H "Authorization: Bearer $JOBD_MASTER_KEY"
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

Requires the prerequisites and installed pnpm dependencies. Builds temporary binaries. Runs real workers and a local HTTPS Wrangler controller with isolated storage, a temporary trusted certificate, and generated queue-scoped worker tokens.

Covers:

- Authentication, CLI actions, default IDs, ordering, isolation and pagination.
- Output files and invalid operations.
- Job execution, output capture and final reports.
- Process-group cancellation, socket cleanup and worker reuse.
- Local/remote parity, controller priority and immediate local execution after an empty controller claim.
- Memory and persistent queues across restart.
- Detached auto-start, restart and stop.

The test cleans up its processes, state and identified logs. Existing workers and controller state are not used.

## Installer tests

```sh
uv run tooling/test-install.py
```

Uses isolated directories and local assets. See [packaging and installer tests](releases.md#local-packaging-and-installer-tests).
