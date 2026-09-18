#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Real local Wrangler + Go worker + CLI smoke test; Python stdlib only.
Run from any directory: uv run tooling/e2e.py
Uses temporary binaries/state, two local workers, and a loopback controller.
"""

import http.client
import json
import os
from pathlib import Path
import signal
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def eventually(description, predicate, timeout=30):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = predicate()
        if result:
            return result
        time.sleep(0.1)
    raise AssertionError(f"Timed out: {description}")


def main():
    processes = []
    output_paths = set()
    detached_states = set()
    with tempfile.TemporaryDirectory(prefix="jobd-e2e-") as tmp:
        directory = Path(tmp)
        env = {key: value for key, value in os.environ.items() if not key.startswith("JOBD_")}
        env.update({"WRANGLER_SEND_METRICS": "false", "CI": "true"})
        port = free_port()
        address = f"http://127.0.0.1:{port}"
        env.update({"JOBD_CONTROLLER": address, "JOBD_QUEUE": "e2e", "JOBD_API_KEY": secrets.token_hex(32),
                    "JOBD_STATE_DIR": str(directory / "unused-local-state")})
        # Keep the test key separate from developer secrets and out of argv/logs.
        dev_vars = directory / "controller.env"
        dev_vars.write_text("JOBD_API_KEY=" + env["JOBD_API_KEY"] + "\n")
        dev_vars.chmod(0o600)
        cli = directory / "jobd"
        worker = directory / "jobd-worker"

        def api(path, queue=None):
            queue = queue or env["JOBD_QUEUE"]
            request = urllib.request.Request(f"{address}/queues/{queue}{path}",
                                             headers={"Authorization": "Bearer " + env["JOBD_API_KEY"]})
            with urllib.request.urlopen(request, timeout=2) as response:
                return json.load(response)

        def local_api(state, path):
            connection = http.client.HTTPConnection("local", timeout=2)
            connection.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            connection.sock.settimeout(2)
            try:
                connection.sock.connect(str(state / "local/control.sock"))
                connection.request("GET", path)
                response = connection.getresponse()
                check(response.status == 200, f"Local API {path}: {response.status}")
                return json.load(response)
            finally:
                connection.close()

        def jobs():
            return api("/jobs")['jobs']

        def call(*args, success=True, extra_env=None):
            result = subprocess.run(
                [str(cli), *args], env=env | (extra_env or {}),
                capture_output=True, text=True, timeout=20,
            )
            check((result.returncode == 0) == success,
                  f"jobd {args}: exit={result.returncode}\n{result.stdout}\n{result.stderr}")
            return result

        def elapsed(job_id):
            lines = call("-l").stdout.splitlines()
            check(lines[0].split()[2] == "ELAPSED", "missing elapsed column")
            return next(line.split()[2] for line in lines[1:] if line.split()[0] == job_id)

        def submit(*args):
            job_id = call(*args).stdout.strip()
            check(api(f"/jobs/{job_id}")["command"] == list(args), "submission argv changed")
            return job_id

        def order(*expected):
            check([job['id'] for job in jobs()] == list(expected), f"Unexpected order: {jobs()}")
            listed = call("-l").stdout.splitlines()
            check([line.split()[0] for line in listed[1:]] == list(expected), f"CLI order: {listed}")

        def start(name, command, cwd):
            log = directory / f"{name}.log"
            with log.open("w") as stream:
                process = subprocess.Popen(command, cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                                           stdout=stream, stderr=subprocess.STDOUT, start_new_session=True)
            processes.append((name, process, log))
            return process

        def stop(process):
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait(timeout=5)

        try:
            for module, binary in [("cli", cli), ("worker", worker)]:
                subprocess.run(["go", "build", "-o", str(binary), "."], cwd=ROOT / module, env=env, check=True)
            controller = start("controller", [
                "pnpm", "exec", "wrangler", "dev", "--local", "--ip", "127.0.0.1",
                "--port", str(port), "--inspector-port", "0", "--persist-to", str(directory / "controller-state"),
                "--env-file", str(dev_vars),
            ], ROOT / "controller")

            def ready():
                check(controller.poll() is None, "Controller exited during startup")
                try:
                    return api("/jobs") == {"jobs": []}
                except (urllib.error.URLError, TimeoutError):
                    return False

            eventually("controller startup", ready, timeout=60)
            for authorization in [None, "Bearer wrong-key"]:
                request = urllib.request.Request(f"{address}/queues/e2e/jobs",
                    headers={} if authorization is None else {"Authorization": authorization})
                try:
                    urllib.request.urlopen(request, timeout=2).close()
                    raise AssertionError("Controller accepted invalid credentials")
                except urllib.error.HTTPError as error:
                    check(error.code == 401, "expected unauthorized response")
            print("PASS API authentication rejects absent/incorrect keys", flush=True)
            for flag in ["-h", "--help"]:
                check("-U ID1 ID2" in call(flag, extra_env={"JOBD_CONTROLLER": "invalid"}).stdout, "help missing actions")
            check(call().stdout == call("-l").stdout, "default action is not list")
            for flag in ["-o", "-r", "-u", "-k"]:
                call(flag, success=False)
            call("-C")
            print("PASS help, default list, empty-queue defaults", flush=True)

            gate_a = directory / "release-a"
            a = submit("sh", "-c", 'printf "hello stdout\\n"; printf "hello stderr\\n" >&2; while [ ! -f "$1" ]; do sleep 0.1; done', "sh", str(gate_a))
            b = submit("echo", "b with spaces")
            c = submit("echo", "c")
            check([a, b, c] == ["1", "2", "3"], "IDs are not per-queue auto-increment numbers")
            order(a, b, c)
            check(elapsed(a) == "-", "queued job should not have elapsed time")
            call("-u", c)
            order(c, a, b)
            call("-U", c, b)
            order(b, a, c)
            call("-u")  # Last submitted, not last in current queue order.
            order(c, b, a)
            call("-r", b)
            order(c, a)
            call("-r")
            order(a)
            call("-o", a, success=False)
            print("PASS submit argv, -u/-r explicit and defaults, -U ordering", flush=True)

            # Environment selectors, queue isolation, and -- delimiter.
            selectors = {"JOBD_CONTROLLER": address, "JOBD_QUEUE": "other"}
            other = call("--", "echo", "isolated", extra_env=selectors).stdout.strip()
            check(api(f"/jobs/{other}", "other")["command"] == ["echo", "isolated"], "-- changed argv")
            check(other in call("job", "list", extra_env=selectors).stdout, "environment selectors failed")
            check("isolated" not in call().stdout and other == "1", "queue isolation/per-queue IDs failed")
            check(other in call(extra_env={"JOBD_QUEUE": "other"}).stdout, "queue env ignored")
            call("-r", other, extra_env=selectors)
            print("PASS environment selectors, --, queue isolation", flush=True)

            gate_f = directory / "release-f"
            f = submit("sh", "-c", 'printf "failure output\\n"; while [ ! -f "$1" ]; do sleep 0.1; done; exit 7', "sh", str(gate_f))
            workers = []
            for name in ["worker-a", "worker-b"]:
                workers.append(start(name, [str(worker), "--state-dir", str(directory / name),
                    "--poll-interval", "0.1", "--heartbeat-interval", "0.2"], ROOT))
            eventually("both workers running with output paths", lambda: all(
                (j := api(f"/jobs/{job_id}"))["status"] == "running" and j["output_path"]
                for job_id in [a, f]))
            running = [api(f"/jobs/{job_id}") for job_id in [a, f]]
            check(len({j['worker_id'] for j in running}) == 2, "jobs not assigned to separate workers")
            for j in running:
                output_paths.add(j['output_path'])
                eventually("actual command output", lambda j=j: Path(j['output_path']).stat().st_size > 0)
                result = call("-o", j['id'])
                check(result.stdout.strip() == j['output_path'], "incorrect output path")
                check(j['hostname'] in result.stderr and j['worker_id'] in result.stderr, "missing output host/worker")
                check(Path(j['output_path']).stat().st_mode & 0o077 == 0, "output permissions not private")
                line = next(line for line in call().stdout.splitlines() if line.split()[0] == j['id'])
                check(all(text in line for text in ["running", j['hostname'], j['worker_id'][:8], j['output_path']]), "listing missing running metadata")
            eventually("nonzero running elapsed time", lambda: elapsed(a) not in ("-", "0s"))
            before = elapsed(a)
            time.sleep(1.1)
            check(elapsed(a) != before, "running elapsed time did not increase")
            latest = api("/jobs/latest?kind=run")
            check(call("-o").stdout.strip() == latest['output_path'], "-o default did not select last run")
            check("hello stdout" in Path(api(f"/jobs/{a}")['output_path']).read_text(), "stdout not captured")
            check("hello stderr" in Path(api(f"/jobs/{a}")['output_path']).read_text(), "stderr not captured")
            print("PASS two real workers, live command metadata, -o explicit/default, combined log contents", flush=True)

            queued = submit("echo", "queued")
            for args in [("-r", a), ("-u", a), ("-U", a, queued), ("-U", queued, a)]:
                check("409" in call(*args, success=False).stderr, f"Running job mutation accepted: {args}")
            call("-C")
            check(len(jobs()) == 3, "-C removed queued/running jobs")
            call("-r")
            check(api(f"/jobs/{a}")['status'] == "running", "running job changed")
            gate_a.touch()
            eventually("successful job", lambda: api(f"/jobs/{a}")['status'] == "succeeded")
            check(api(f"/jobs/{a}")['exit_code'] == 0, "success exit code incorrect")
            call("-C")
            check([j['id'] for j in jobs()] == [f], "-C must preserve running jobs while clearing finished ones")
            gate_f.touch()
            eventually("failed job", lambda: api(f"/jobs/{f}")['status'] == "failed")
            check(api(f"/jobs/{f}")['exit_code'] == 7, "failure exit code incorrect")
            check("failed" in call().stdout, "failure not listed")
            finished_elapsed = elapsed(f)
            check(finished_elapsed != "-", "finished job missing duration")
            time.sleep(1.1)
            check(elapsed(f) == finished_elapsed, "finished duration kept increasing")
            print("PASS ELAPSED queued/running/finished behavior", flush=True)
            check(call("-o").stdout.strip() == api(f"/jobs/{f}")['output_path'], "finished output unavailable")
            call("-r", f)  # Removing a finished job is allowed.
            check(jobs() == [], "finished removal failed")
            for process in workers:
                stop(process)
                check(process.returncode == 0, f"Worker shutdown failed: {process.returncode}")
            check(all(Path(path).exists() for path in output_paths), "queue removal deleted log files")
            print("PASS running-job safeguards, successful/failed execution, -C, finished -r, retained logs", flush=True)

            # Use a fresh queue: a cancelled claim from an earlier worker may still
            # reach the controller after that worker exits (there are no leases).
            env["JOBD_QUEUE"] = "ordered"
            # Verify that queue edits change actual execution order, not just the table.
            execution_order = directory / "execution-order"
            ordered_ids = [submit("sh", "-c", 'printf "%s\\n" "$1" >> "$2"; exit "$3"',
                                  "sh", label, str(execution_order), code)
                           for label, code in [("one", "0"), ("two", "9"), ("three", "0")]]
            call("-u", ordered_ids[2])
            call("-U", ordered_ids[2], ordered_ids[1])
            ordered_worker = start("worker-order", [str(worker), "--state-dir", str(directory / "worker-order"),
                                   "--poll-interval", "0.1", "--heartbeat-interval", "0.2"], ROOT)
            eventually("reordered execution", lambda: all(
                api(f"/jobs/{job_id}")["status"] in ("succeeded", "failed") for job_id in ordered_ids))
            check(execution_order.read_text().splitlines() == ["two", "one", "three"], "worker ignored edited queue order")
            check(api(f"/jobs/{ordered_ids[1]}")["exit_code"] == 9, "ordered failure code incorrect")
            for job_id in ordered_ids:
                output_paths.add(api(f"/jobs/{job_id}")["output_path"])
            stop(ordered_worker)
            check(ordered_worker.returncode == 0, "ordered worker shutdown failed")
            call("-C")
            check(jobs() == [], "-C did not clear both succeeded and failed jobs")
            check(all(Path(path).exists() for path in output_paths), "-C deleted output files")
            print("PASS real execution follows -u/-U order; -C clears success and failure", flush=True)

            env["JOBD_QUEUE"] = "pagination"
            # Exercise pagination with real submissions, not mocked API responses.
            ids = [submit("echo", str(i)) for i in range(101)]
            listing = call().stdout.splitlines()[1:]
            check([line.split()[0] for line in listing] == ids, "listing pagination lost/reordered jobs")
            call("-u", ids[-1])
            call("-U", ids[-1], ids[0])
            for job_id in ids:
                call("-r", job_id)
            check(jobs() == [], "pagination cleanup failed")
            print("PASS 101-job listing pagination", flush=True)

            for args in [("-U", "one"), ("-C", "extra"), ("-l", "extra"), ("-o", "a", "b"),
                         ("-r", "a", "b"), ("-u", "a", "b"), ("-k", "a", "b"), ("-k", "missing"), ("--",), ("-unknown",),
                         ("--controller",), ("--queue",), ("--queue", "INVALID", "-l"),
                         ("-o", "missing"), ("-r", "missing"), ("-u", "missing"), ("-U", "missing", "also-missing")]:
                call(*args, success=False)
            print("PASS invalid arguments and nonexistent-job errors", flush=True)

            env["JOBD_QUEUE"] = "progress"
            progress_worker = start("worker-progress", [str(worker), "--state-dir", str(directory / "worker-progress"),
                                    "--poll-interval", "0.1", "--heartbeat-interval", "0.2"], ROOT)
            fixture = str(ROOT / "tooling/e2e_progress_job.py")
            for mode in ["finish", "cancel"]:
                work = directory / ("progress-" + mode)
                work.mkdir()
                progress_id = submit("uv", "run", fixture, mode, str(work))
                eventually("Python job progress socket", lambda: (work / "ready.json").exists())
                details = json.loads((work / "ready.json").read_text())
                output_paths.add(api(f"/jobs/{progress_id}")["output_path"])
                eventually("threshold uploads 25%", lambda: api(f"/jobs/{progress_id}")["progress"] == 0.25)

                check("PROGRESS" not in call("-l").stdout.splitlines()[0], "progress column should be hidden")
                (work / "half").touch()
                eventually("50% acknowledged", lambda: (work / "half-acked").exists())
                eventually("threshold uploads 50%", lambda: api(f"/jobs/{progress_id}")["progress"] == 0.5)
                if mode == "finish":
                    (work / "finish").touch()
                    eventually("progress job completion", lambda: api(f"/jobs/{progress_id}")["status"] == "succeeded")
                    check(api(f"/jobs/{progress_id}")["progress"] == 1, "success not 100%")
                    call("-k", progress_id, success=False)
                else:
                    check("Cancellation requested" in call("-k", progress_id).stdout, "cancel not requested")
                    check("cancelling" in call("-l").stdout, "pending cancellation not shown")
                    call("-k")  # Default selects the last run; repeat pending request is safe.
                    call("-r", progress_id, success=False)
                    following = submit("echo", "worker survives cancellation")
                    call("-k", following, success=False)  # Queued jobs use -r, not -k.
                    eventually("remote cancellation acknowledgement", lambda: api(f"/jobs/{progress_id}")["status"] == "failed")
                    cancelled = api(f"/jobs/{progress_id}")
                    check(cancelled["error"] == "Job cancelled by user", "missing cancellation reason")
                    check(cancelled["progress"] == 0.5, "cancelled job lost progress")
                    for pid in [details["pid"], details["child_pid"]]:
                        proc = Path(f"/proc/{pid}/stat")
                        check(not proc.exists() or proc.read_text().split(")", 1)[1].split()[0] == "Z", "cancelled process survived SIGKILL")
                    eventually("worker handles next job", lambda: api(f"/jobs/{following}")["status"] == "succeeded")
                    output_paths.add(api(f"/jobs/{following}")["output_path"])
                    check(progress_worker.poll() is None, "cancellation stopped worker")
                check(not Path(details["socket"]).exists(), "finished job progress socket retained")
            stop(progress_worker)
            check(progress_worker.returncode == 0, "progress worker shutdown failed")
            print("PASS buffered Python NDJSON progress via independent uploader: 25% -> 50% -> 100%, validation, monotonicity and socket cleanup", flush=True)
            print("PASS -k explicit/default, cancelling display, SIGKILL of TERM-ignoring process group, worker continues", flush=True)

            env["JOBD_QUEUE"] = "buffered-final"
            quick_worker = start("worker-buffered-final", [str(worker), "--state-dir", str(directory / "worker-buffered-final"),
                                  "--poll-interval", "0.1", "--heartbeat-interval", "60"], ROOT)
            quick = submit("uv", "run", fixture, "quick-fail", str(directory))
            eventually("final report uploads buffered progress", lambda: api(f"/jobs/{quick}")["status"] == "failed")
            check(api(f"/jobs/{quick}")["progress"] == 0.04, "final buffered progress lost")
            output_paths.add(api(f"/jobs/{quick}")["output_path"])
            stop(quick_worker)
            check(quick_worker.returncode == 0, "quick worker shutdown failed")
            check(f"/queues/buffered-final/jobs/{quick}/progress " not in (directory / "controller.log").read_text(), "small progress uploaded before timer/finish")
            print("PASS final failure flushes 4% before periodic upload, independent of heartbeat", flush=True)

            env["JOBD_QUEUE"] = "timed-progress"
            timed_dir = directory / "timed-progress"
            timed_dir.mkdir()
            timed_worker = start("worker-timed-progress", [str(worker), "--state-dir", str(directory / "worker-timed-progress"),
                                  "--poll-interval", "0.1", "--heartbeat-interval", "60"], ROOT)
            timed = submit("uv", "run", fixture, "timed", str(timed_dir))
            eventually("5% buffered", lambda: (timed_dir / "ready").exists())
            time.sleep(1)
            check(api(f"/jobs/{timed}")["progress"] == 0, "exactly five points uploaded early")
            eventually("10-second upload", lambda: api(f"/jobs/{timed}")["progress"] == 0.05, timeout=15)
            (timed_dir / "half").touch()
            eventually("six-point increase uploads promptly", lambda: api(f"/jobs/{timed}")["progress"] == 0.11, timeout=4)
            (timed_dir / "finish").touch()
            eventually("timed job completion", lambda: api(f"/jobs/{timed}")["status"] == "succeeded")
            output_paths.add(api(f"/jobs/{timed}")["output_path"])
            stop(timed_worker)
            check(timed_worker.returncode == 0, "timed worker shutdown failed")
            print("PASS 10-second periodic upload and strict >5-point early upload with 60-second heartbeats", flush=True)

            env["JOBD_QUEUE"] = "copy"
            # Copy the actual COMMAND table cell into Bash, and verify argv survives.
            copy_args = ["hello world", "", "it's fine", "$HOME", "$(printf injected)",
                         "`printf injected`", "*.go", "~", "a;b", "line1\nline2", "a\tb", "\\path\\"]
            for arg in copy_args:
                args = [arg]
                copy_id = submit("sh", "-c", 'printf "%s\\0" "$@"', "sh", *args)
                row = next(line for line in call("-l").stdout.splitlines() if line.split()[0] == copy_id)
                command = row.split(None, 7)[7]
                pasted = subprocess.run(["bash", "-c", command], capture_output=True, timeout=10)
                check(pasted.returncode == 0, f"Pasted command failed: {pasted.stderr!r}")
                check(pasted.stdout == b"".join(arg.encode() + b"\0" for arg in args), "Copied COMMAND changed arguments")
                call("-r", copy_id)
            long_id = submit("echo", "x" * 200)
            row = next(line for line in call("-l").stdout.splitlines() if line.split()[0] == long_id)
            command = row.split(None, 7)[7]
            check(len(command) == 60 and command.endswith("..."), "long command was not truncated")
            check(api(f"/jobs/{long_id}")["command"] == ["echo", "x" * 200], "truncation changed stored command")
            call("-r", long_id)
            print("PASS COMMAND quoting and display-only truncation", flush=True)
            env["JOBD_QUEUE"] = "local-idle"
            local_state = directory / "worker-local"
            remote_marker = directory / "remote-first"
            env["JOBD_STATE_DIR"] = str(local_state)
            env["JOBD_LOCAL_PERSIST"] = "true"
            local_options = ("--local",)
            remote_id = submit("touch", str(remote_marker))
            local_worker_command = [str(worker), "--state-dir", str(local_state),
                                    "--poll-interval", "0.1", "--heartbeat-interval", "0.2"]
            local_worker = start("worker-local-first", local_worker_command, ROOT)
            eventually("local worker socket", lambda: (local_state / "local/control.sock").exists())
            local_id = call(*local_options, "sh", "-c",
                'set -eu; test -f "$1"; test "${JOBD_API_KEY+x}" != x; printf "local output\\n"',
                "sh", str(remote_marker), extra_env={"JOBD_API_KEY": ""}).stdout.strip()
            check(local_id.startswith("local-"), "local submission ID")
            eventually("controller job runs before local", lambda: api(f"/jobs/{remote_id}")["status"] == "succeeded")
            output_paths.add(api(f"/jobs/{remote_id}")["output_path"])
            check("queued" in call(*local_options, "-l").stdout, "local job ran before idle delay")
            combined = call("-l").stdout.splitlines()
            combined_ids = [row.split()[0] for row in combined[1:]]
            check(combined_ids == [remote_id, local_id], "combined listing lost or duplicated jobs")
            stop(local_worker)
            restarted_at = time.monotonic()
            local_worker = start("worker-local-restarted", local_worker_command, ROOT)
            eventually("restarted local worker socket", lambda: (local_state / "local/control.sock").exists())
            eventually("persistent local job completes", lambda: "succeeded" in call(*local_options, "-l").stdout, timeout=45)
            check(time.monotonic() - restarted_at >= 30, "local job skipped production idle delay")
            path = call(*local_options, "-o", local_id).stdout.strip()
            output_paths.add(path)
            check(Path(path).read_text() == "local output\n", "local output mismatch")
            check(len(jobs()) == 1, "local job was sent to controller")

            # Once idle, local work uses the same progress/cancel/execution flow.
            work = directory / "local-progress"
            work.mkdir()
            progress_id = call(*local_options, "uv", "run", fixture, "cancel", str(work)).stdout.strip()
            eventually("local progress job starts", lambda: (work / "ready.json").exists())
            eventually("local progress 25%", lambda: local_api(local_state, f"/jobs/{progress_id}")["progress"] == 0.25)
            (work / "half").touch()
            eventually("local progress 50%", lambda: local_api(local_state, f"/jobs/{progress_id}")["progress"] == 0.5)
            progress_path = call(*local_options, "-o").stdout.strip()
            output_paths.add(progress_path)
            for action in ["-r", "-u"]:
                call(*local_options, action, progress_id, success=False)
            local_order = directory / "local-order"
            pending = [call(*local_options, "sh", "-c", 'printf "%s\\n" "$1" >> "$2"',
                            "sh", label, str(local_order)).stdout.strip() for label in ["a", "b", "c"]]
            call(*local_options, "-u", pending[2])
            call(*local_options, "-U", pending[2], pending[1])
            call(*local_options, "-u")  # Last added is still c, not the reordered tail.
            call(*local_options, "-r")  # Remove c; remaining order is b then a.
            call(*local_options, "-k")  # Last run is the local progress job.
            check("cancelling" in call(*local_options, "-l").stdout, "local cancellation not visible")
            def local_finished():
                rows = {row.split()[0]: row.split()[1] for row in call(*local_options, "-l").stdout.splitlines()[1:]}
                return rows.get(progress_id) == "failed" and all(rows.get(j) == "succeeded" for j in pending[:2])
            eventually("local cancellation and reordered execution", local_finished)
            check(local_order.read_text() == "b\na\n", "local reorder/default IDs did not affect execution")
            for job_id in pending[:2]:
                output_paths.add(call(*local_options, "-o", job_id).stdout.strip())
            check(local_api(local_state, f"/jobs/{progress_id}")["progress"] == 0.5, "cancel lost local progress")
            check(len(jobs()) == 1, "local progress/result sent to controller")
            call(*local_options, "-C")
            check(len(call(*local_options, "-l").stdout.splitlines()) == 1, "local clear left finished jobs")
            stop(local_worker)
            check(local_worker.returncode == 0, "local worker shutdown failed")
            print("PASS persistent local submission, controller priority, real 30-second idle delay, local progress/cancel/reorder/default IDs/output and key filtering", flush=True)
            # No credentials: both binaries automatically select local-only mode.
            env.pop("JOBD_API_KEY")
            env.pop("JOBD_LOCAL_PERSIST")
            env["JOBD_CONTROLLER"] = "invalid-unused-controller"
            no_key_state = directory / "worker-no-key"
            env["JOBD_STATE_DIR"] = str(no_key_state)
            detached_states.add(no_key_state)
            call("-l")  # Starts a detached worker.
            check((no_key_state / "local/control.sock").exists(), "CLI did not start worker")
            for args in [(), ("-l",)]:
                listed = call(*args)
                check("JOBD_API_KEY" in listed.stderr and "jobd worker restart" in listed.stderr,
                      "no-key listing did not explain controller setup")
                check("Warning" not in listed.stdout, "warning polluted job listing")
            no_key_id = call("sh", "-c", 'test "${JOBD_API_KEY+x}" != x; echo no-key-output').stdout.strip()
            check(no_key_id.startswith("local-"), "no-key submission was not local")
            eventually("no-key job completes without idle delay", lambda: "succeeded" in call("-l").stdout, timeout=10)
            path = call("-o", no_key_id).stdout.strip()
            output_paths.add(path)
            check(Path(path).read_text() == "no-key-output\n", "no-key output mismatch")
            call("worker", "restart")
            check((no_key_state / "local/control.sock").exists(), "restart did not start worker")
            check(len(call("-l").stdout.splitlines()) == 1, "memory queue survived restart")
            check(not (no_key_state / "local/queue.db").exists(), "memory queue created a database file")
            check(Path(path).read_text() == "no-key-output\n", "restart removed output log")
            call("-C")
            call("worker", "stop")
            check(not (no_key_state / "local/control.sock").exists(), "stop left socket open")
            print("PASS automatic local-only mode without API key, listing warning, immediate execution, output and memory-only restart", flush=True)
            print("All real controller/worker/CLI end-to-end checks passed.", flush=True)
        except BaseException:
            try:
                remaining = jobs()
                print(f"\nRemaining jobs: {json.dumps(remaining, indent=2)}", flush=True)
                output_paths.update(j['output_path'] for j in remaining if j.get('output_path'))
            except Exception:
                pass  # Controller may not have started.
            for name, _, log in processes:
                print(f"\n--- {name} log (last 8000 characters) ---\n{log.read_text()[-8000:]}", flush=True)
            raise
        finally:
            for state in detached_states:
                subprocess.run([str(cli), "worker", "stop"], env=env | {"JOBD_STATE_DIR": str(state)},
                               capture_output=True, timeout=40, check=True)
            for _, process, _ in reversed(processes):
                stop(process)
            # Only logs positively identified as belonging to this test's jobs.
            for path in output_paths:
                Path(path).unlink(missing_ok=True)


if __name__ == "__main__":
    main()
