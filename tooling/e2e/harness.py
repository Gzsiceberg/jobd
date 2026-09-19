#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Real CLI/API access and owned-process lifecycle; no mocked services."""

import http.client
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[2]


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def eventually(description, predicate, timeout=30):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = predicate()
        if result:
            return result
        time.sleep(0.1)
    raise AssertionError(f"Timed out: {description}")


def stop(process):
    if process.poll() is None:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=20)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)


class Harness:
    """One test's queue, mutable environment, processes, state and output logs."""

    stop = staticmethod(stop)

    def __init__(self, services, directory):
        self.services = services
        self.directory = directory
        self.cli = services.cli
        self.worker = services.worker
        self.address = services.address
        self.tls = services.tls
        self.queue = "test-" + uuid.uuid4().hex
        self.env = services.env | {
            "JOBD_QUEUE": self.queue,
            "JOBD_STATE_DIR": str(directory / "local"),
        }
        self.processes = []
        self.output_paths = set()
        self.detached_states = set()
        self.states = set()
        self.queues = {self.queue}

    def track_outputs(self, value):
        if isinstance(value, dict):
            if value.get("output_path"):
                self.output_paths.add(value["output_path"])
            for child in value.values():
                self.track_outputs(child)
        elif isinstance(value, list):
            for child in value:
                self.track_outputs(child)
        return value

    def api(self, path, queue=None):
        queue = queue or self.env["JOBD_QUEUE"]
        self.queues.add(queue)
        request = urllib.request.Request(
            f"{self.address}/queues/{queue}{path}",
            headers={"Authorization": "Bearer " + self.services.env["JOBD_MASTER_KEY"]},
        )
        with urllib.request.urlopen(request, timeout=2, context=self.tls) as response:
            return self.track_outputs(json.load(response))

    def local_api(self, state, path):
        self.states.add(state)
        connection = http.client.HTTPConnection("local", timeout=2)
        connection.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        connection.sock.settimeout(2)
        try:
            connection.sock.connect(str(state / "local/control.sock"))
            connection.request("GET", path)
            response = connection.getresponse()
            check(response.status == 200, f"Local API {path}: {response.status}")
            return self.track_outputs(json.load(response))
        finally:
            connection.close()

    def call(self, *args, success=True, extra_env=None):
        env = self.env | (extra_env or {})
        self.queues.add(env["JOBD_QUEUE"])
        state = Path(env["JOBD_STATE_DIR"])
        self.states.add(state)
        # CLI operations may auto-start a local daemon. Always own its cleanup.
        self.detached_states.add(state)
        result = subprocess.run(
            [str(self.cli), *args],
            env=env,
            capture_output=True,
            text=True,
            timeout=20,
        )
        check(
            (result.returncode == 0) == success,
            f"jobd {args}: exit={result.returncode}\n{result.stdout}\n{result.stderr}",
        )
        return result

    def jobs(self):
        return self.api("/jobs")["jobs"]

    def submit(self, *args):
        job_id = self.call(*args).stdout.strip()
        check(self.api(f"/jobs/{job_id}")["command"] == list(args), "submission argv changed")
        return job_id

    def order(self, *expected):
        check([job["id"] for job in self.jobs()] == list(expected), f"Unexpected order: {self.jobs()}")
        listed = self.call("-l").stdout.splitlines()
        check([line.split()[0] for line in listed[1:]] == list(expected), f"CLI order: {listed}")

    def elapsed(self, job_id):
        lines = self.call("-l").stdout.splitlines()
        check(lines[0].split()[2] == "ELAPSED", "missing elapsed column")
        return next(line.split()[2] for line in lines[1:] if line.split()[0] == job_id)

    def start(self, name, command, cwd=ROOT):
        env = self.env.copy()
        if command[0] == str(self.worker):
            env["JOBD_WORKER_TOKEN"] = self.call(
                "auth", "create-worker-token", "--duration", "1h"
            ).stdout.strip()
            env.pop("JOBD_MASTER_KEY", None)
            if "--state-dir" in command:
                self.states.add(Path(command[command.index("--state-dir") + 1]))
        log = self.directory / f"{name}.log"
        with log.open("w") as stream:
            process = subprocess.Popen(
                command,
                cwd=cwd,
                env=env,
                stdin=subprocess.DEVNULL,
                stdout=stream,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        self.processes.append((name, process, log))
        return process

    def start_worker(self, name="worker", state=None):
        state = state or self.directory / name
        process = self.start(
            name,
            [
                str(self.worker),
                "--state-dir",
                str(state),
                "--poll-interval",
                "0.1",
                "--heartbeat-interval",
                "0.2",
            ],
        )

        def ready():
            check(process.poll() is None, f"{name} exited during startup")
            return (state / "local/control.sock").exists()

        eventually(f"{name} socket", ready)
        return process

    def snapshot(self):
        """Collect outputs and diagnostics even after a partially executed test."""
        snapshot = {}
        for queue in sorted(self.queues):
            try:
                snapshot[queue] = self.api("/jobs", queue)
            except Exception as error:
                snapshot[queue] = str(error)
        for state in sorted(self.states):
            if (state / "local/control.sock").exists():
                try:
                    snapshot[str(state)] = self.local_api(state, "/jobs")
                except Exception as error:
                    snapshot[str(state)] = str(error)
        return json.dumps(snapshot, indent=2)

    def diagnostics(self):
        parts = [self.snapshot()]
        for name, _, log in self.processes:
            parts.append(f"--- {name} ---\n{log.read_text()[-8000:]}")
        parts.append("--- controller ---\n" + self.services.log.read_text()[-8000:])
        return "\n".join(parts)

    def close(self):
        errors = []
        self.snapshot()
        for _, process, _ in reversed(self.processes):
            try:
                stop(process)
            except Exception as error:
                errors.append(error)
        for state in self.detached_states:
            try:
                subprocess.run(
                    [str(self.cli), "worker", "stop"],
                    env=self.env | {"JOBD_STATE_DIR": str(state)},
                    capture_output=True,
                    timeout=40,
                    check=True,
                )
            except Exception as error:
                errors.append(error)
        self.snapshot()
        for path in self.output_paths:
            try:
                Path(path).unlink(missing_ok=True)
            except Exception as error:
                errors.append(error)
        if errors:
            raise ExceptionGroup("E2E cleanup failed", errors)
