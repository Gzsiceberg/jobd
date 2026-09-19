#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pytest>=8,<9"]
# ///
"""Session services are shared; every scenario owns its queue and worker state."""

import base64
import os
from pathlib import Path
import secrets
import socket
import ssl
import subprocess
import tempfile
from types import SimpleNamespace
import urllib.error

import pytest

from harness import Harness, ROOT, check, eventually, stop


@pytest.fixture(scope="session")
def services():
    with tempfile.TemporaryDirectory(prefix="jobd-e2e-") as tmp:
        directory = Path(tmp)
        env = {key: value for key, value in os.environ.items() if not key.startswith("JOBD_")}
        env.update({"WRANGLER_SEND_METRICS": "false", "CI": "true"})
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        address = f"https://127.0.0.1:{port}"
        cert, cert_key = directory / "cert.pem", directory / "key.pem"
        subprocess.run(
            [
                "openssl",
                "req",
                "-x509",
                "-newkey",
                "rsa:2048",
                "-nodes",
                "-keyout",
                str(cert_key),
                "-out",
                str(cert),
                "-days",
                "1",
                "-subj",
                "/CN=localhost",
                "-addext",
                "subjectAltName=IP:127.0.0.1,DNS:localhost",
            ],
            check=True,
            capture_output=True,
        )
        env.update(
            {
                "JOBD_CONTROLLER": address,
                "JOBD_MASTER_KEY": base64.b64encode(secrets.token_bytes(32)).decode(),
                "SSL_CERT_FILE": str(cert),
            }
        )
        dev_vars = directory / "controller.env"
        dev_vars.touch(mode=0o600)
        dev_vars.write_text("JOBD_MASTER_KEY=" + env["JOBD_MASTER_KEY"] + "\n")
        cli, worker = directory / "jobd", directory / "jobd-worker"
        for module, binary in [("cli", cli), ("worker", worker)]:
            subprocess.run(["go", "build", "-o", str(binary), "."], cwd=ROOT / module, env=env, check=True)
        log = directory / "controller.log"
        with log.open("w") as stream:
            controller = subprocess.Popen(
                [
                    "pnpm",
                    "exec",
                    "wrangler",
                    "dev",
                    "--local",
                    "--ip",
                    "127.0.0.1",
                    "--port",
                    str(port),
                    "--inspector-port",
                    "0",
                    "--persist-to",
                    str(directory / "controller-state"),
                    "--env-file",
                    str(dev_vars),
                    "--local-protocol",
                    "https",
                    "--https-key-path",
                    str(cert_key),
                    "--https-cert-path",
                    str(cert),
                ],
                cwd=ROOT / "controller",
                env=env,
                stdin=subprocess.DEVNULL,
                stdout=stream,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        shared = SimpleNamespace(
            cli=cli,
            worker=worker,
            address=address,
            env=env,
            log=log,
            tls=ssl.create_default_context(cafile=str(cert)),
        )
        probe = Harness(shared, directory)
        try:

            def ready():
                check(controller.poll() is None, "Controller exited during startup")
                try:
                    return probe.jobs() == []
                except (urllib.error.URLError, TimeoutError):
                    return False

            eventually("controller startup", ready, timeout=60)
            yield shared
        except BaseException:
            print(log.read_text()[-8000:])
            raise
        finally:
            stop(controller)


@pytest.fixture
def harness(services, request):
    # Short paths avoid the Unix socket path length limit on pytest's temp paths.
    with tempfile.TemporaryDirectory(prefix="jobd-case-") as tmp:
        case = Harness(services, Path(tmp))
        request.node.e2e_harness = case
        try:
            yield case
        finally:
            case.close()


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item, call):
    outcome = yield
    report = outcome.get_result()
    case = getattr(item, "e2e_harness", None)
    if report.failed and case is not None and case.directory.exists():
        report.sections.append(("E2E diagnostics", case.diagnostics()))
