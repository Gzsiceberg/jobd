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
export JOBD_WORKER_TOKEN="$(./cli/jobd auth create-worker-token --duration 24h)"
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
uv run --with 'pytest>=8,<9' pytest tooling/e2e
uv run --with 'pytest>=8,<9' pytest tooling/e2e -k cancellation   # Run one scenario group
uv run --with 'pytest>=8,<9' pytest tooling/e2e -k retry -v       # Show individual retry cases
uv run --with 'pytest>=8,<9' pytest tooling/e2e --collect-only -q # List without starting services
```

Requires the prerequisites, Python 3.12+ and installed pnpm dependencies. Run from the repository root. uv supplies pytest in an isolated environment; no wrapper script is needed. Standard pytest options work directly, including test file/node selection and `--junitxml=results.xml`.

The session fixture builds temporary binaries and starts one real local HTTPS Wrangler controller with isolated storage, a temporary trusted certificate, and generated queue-scoped worker tokens. Each test gets a unique queue, environment copy and short temporary state directory; workers are never shared between tests. Tests run sequentially and can be selected independently.

The suite lives in `tooling/e2e/`:

- `conftest.py`: session services, per-test fixtures and failure diagnostics.
- `harness.py`: real CLI/API helpers, polling, process ownership and cleanup.
- `test_auth.py`, `test_queue.py`: authentication and queue/CLI behavior.
- `test_execution.py`, `test_cancellation.py`: execution, failure pause and cancellation.
- `test_local.py`: local priority, persistence, cancellation and auto-start.
- `test_retry.py`: single/multiple/all remote retries and local retries.

Add a standalone `test_*(harness)` function rather than extending a shared scenario. Use explicit gates or readiness polling for asynchronous work; bounded observation delays are reserved for asserting that something stays unchanged. The fixture stops owned foreground and detached workers even when an assertion fails. Failed tests include queue snapshots and process logs in the pytest report.

Covers:

- Authentication, CLI actions, default IDs, ordering, isolation and pagination.
- Output files and invalid operations.
- Job execution, output capture and final reports.
- Process-group cancellation, socket cleanup, remote-claim pausing and restart recovery.
- Retrying failed jobs with the same IDs, retaining old logs and executing again.
- Local/remote parity, controller priority and immediate local execution after an empty controller claim.
- Memory and persistent queues across restart.
- Detached auto-start, restart and stop.

The test cleans up its processes, state and identified logs. Existing workers and controller state are not used.

## Installer tests

```sh
uv run --with 'pytest>=8,<9' pytest tooling/install_tests
uv run --with 'pytest>=8,<9' pytest tooling/install_tests -k rollback -v
```

Builds real release assets once per session. Every test gets copied assets, its own HOME, worker state and download log. No real GitHub downloads or changes to your installation occur.

The suite lives in `tooling/install_tests/`:

- `conftest.py`: build fixture, isolated sandboxes and teardown using a retained cleanup CLI.
- `helpers.py`: subprocess assertions, file snapshots and archive/checksum editing.
- `fake-bin/`: readable shell fixtures for curl, uname, mv failure injection and forbidden gh use.
- `test_install.py`: validation, installation, upgrades, rollback, paths and conflicts.
- `test_uninstall.py`: modified-file protection, worker shutdown and state preservation.
- `test_archives.py`: checksum/content validation and ARM64 selection.

Workers are cleaned up even if a test deletes or corrupts installed binaries. Failed tests include subprocess output, a sandbox listing and captured download requests. See [packaging and installer tests](releases.md#local-packaging-and-installer-tests).
