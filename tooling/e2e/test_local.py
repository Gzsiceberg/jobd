#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pytest>=8,<9"]
# ///
from pathlib import Path

from harness import ROOT, check, eventually


def test_local_priority_and_persistence(harness):
    call = harness.call
    api = harness.api
    submit = harness.submit
    jobs = harness.jobs
    start = harness.start
    stop = harness.stop
    env = harness.env
    directory = harness.directory
    worker = harness.worker
    output_paths = harness.output_paths

    str(ROOT / "tooling/e2e_cancel_job.py")
    local_state = directory / "worker-local"
    remote_marker = directory / "remote-first"
    env["JOBD_STATE_DIR"] = str(local_state)
    env["JOBD_LOCAL_PERSIST"] = "true"
    local_options = ("--local",)
    remote_id = submit("touch", str(remote_marker))
    local_worker_command = [
        str(worker),
        "--state-dir",
        str(local_state),
        "--poll-interval",
        "0.1",
        "--heartbeat-interval",
        "0.2",
    ]
    local_worker = start("worker-local-first", local_worker_command, ROOT)
    eventually("local worker socket", lambda: (local_state / "local/control.sock").exists())
    local_id = call(
        *local_options,
        "sh",
        "-c",
        'set -eu; test -f "$1"; test "${JOBD_WORKER_TOKEN+x}" != x; printf "local output\\n"',
        "sh",
        str(remote_marker),
        extra_env={"JOBD_WORKER_TOKEN": ""},
    ).stdout.strip()
    check(local_id.startswith("local-"), "local submission ID")
    eventually("controller job runs before local", lambda: api(f"/jobs/{remote_id}")["status"] == "succeeded")
    output_paths.add(api(f"/jobs/{remote_id}")["output_path"])
    eventually(
        "local job completes without idle delay",
        lambda: "succeeded" in call(*local_options, "-l").stdout,
        timeout=10,
    )
    combined = call("-l").stdout.splitlines()
    combined_ids = [row.split()[0] for row in combined[1:]]
    check(combined_ids == [remote_id, local_id], "combined listing lost or duplicated jobs")
    stop(local_worker)
    local_worker = start("worker-local-restarted", local_worker_command, ROOT)
    eventually("restarted local worker socket", lambda: (local_state / "local/control.sock").exists())
    check("succeeded" in call(*local_options, "-l").stdout, "persistent local result lost after restart")
    path = call(*local_options, "-o", local_id).stdout.strip()
    output_paths.add(path)
    check(Path(path).read_text() == "local output\n", "local output mismatch")
    check(len(jobs()) == 1, "local job was sent to controller")

    stop(local_worker)
    check(local_worker.returncode == 0, "local worker shutdown failed")


def test_local_cancellation_and_ordering(harness):
    call = harness.call
    local_api = harness.local_api
    directory = harness.directory
    output_paths = harness.output_paths
    local_state = directory / "worker-local"
    harness.env["JOBD_STATE_DIR"] = str(local_state)
    local_options = ("--local",)
    fixture = str(ROOT / "tooling/e2e_cancel_job.py")
    local_worker = harness.start_worker(state=local_state)

    # With no controller job available, local work uses the same cancellation/execution flow.
    work = directory / "local-cancellation"
    work.mkdir()
    cancel_id = call(*local_options, "uv", "run", fixture, str(work)).stdout.strip()
    eventually("local cancellation job starts", lambda: (work / "ready.json").exists())
    output_paths.add(call(*local_options, "-o").stdout.strip())
    for action in ["-r", "-u"]:
        call(*local_options, action, cancel_id, success=False)
    local_order = directory / "local-order"
    pending = [
        call(
            *local_options, "sh", "-c", 'printf "%s\\n" "$1" >> "$2"', "sh", label, str(local_order)
        ).stdout.strip()
        for label in ["a", "b", "c"]
    ]
    call(*local_options, "-u", pending[2])
    call(*local_options, "-U", pending[2], pending[1])
    call(*local_options, "-u")  # Last added is still c, not the reordered tail.
    call(*local_options, "-r")  # Remove c; remaining order is b then a.
    call(*local_options, "-k")  # Last run is the local cancellation job.
    check("cancelling" in call(*local_options, "-l").stdout, "local cancellation not visible")

    def local_finished():
        rows = {row.split()[0]: row.split()[1] for row in call(*local_options, "-l").stdout.splitlines()[1:]}
        return rows.get(cancel_id) == "failed" and all(rows.get(j) == "succeeded" for j in pending[:2])

    eventually("local cancellation and reordered execution", local_finished)
    check(local_order.read_text() == "b\na\n", "local reorder/default IDs did not affect execution")
    for job_id in pending[:2]:
        output_paths.add(call(*local_options, "-o", job_id).stdout.strip())
    check(
        local_api(local_state, f"/jobs/{cancel_id}")["error"] == "Job cancelled by user",
        "missing local cancellation reason",
    )
    check(harness.jobs() == [], "local result sent to controller")
    call(*local_options, "-C")
    check(len(call(*local_options, "-l").stdout.splitlines()) == 1, "local clear left finished jobs")
    harness.stop(local_worker)
    check(local_worker.returncode == 0, "local worker shutdown failed")


def test_local_only_autostart_and_memory_restart(harness):
    call = harness.call
    env = harness.env
    directory = harness.directory
    output_paths = harness.output_paths
    detached_states = harness.detached_states

    # No credentials: both binaries automatically select local-only mode.
    env.pop("JOBD_WORKER_TOKEN", None)
    env.pop("JOBD_MASTER_KEY")
    env.pop("JOBD_LOCAL_PERSIST", None)
    env["JOBD_CONTROLLER"] = "invalid-unused-controller"
    no_key_state = directory / "worker-no-key"
    env["JOBD_STATE_DIR"] = str(no_key_state)
    detached_states.add(no_key_state)
    call("-l")  # Starts a detached worker.
    check((no_key_state / "local/control.sock").exists(), "CLI did not start worker")
    for args in [(), ("-l",)]:
        listed = call(*args)
        check(
            "JOBD_WORKER_TOKEN" in listed.stderr and "jobd worker restart" in listed.stderr,
            "no-key listing did not explain controller setup",
        )
        check("Warning" not in listed.stdout, "warning polluted job listing")
    no_key_id = call("sh", "-c", 'test "${JOBD_WORKER_TOKEN+x}" != x; echo no-key-output').stdout.strip()
    check(no_key_id.startswith("local-"), "no-key submission was not local")
    eventually(
        "no-key job completes without idle delay", lambda: "succeeded" in call("-l").stdout, timeout=10
    )
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
