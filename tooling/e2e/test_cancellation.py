#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pytest>=8,<9"]
# ///
from pathlib import Path
import json
import time

from harness import ROOT, check, eventually


def test_remote_cancellation_and_restart(harness):
    call = harness.call
    api = harness.api
    submit = harness.submit
    start = harness.start
    stop = harness.stop
    directory = harness.directory
    worker = harness.worker
    output_paths = harness.output_paths

    cancel_worker = start(
        "worker-cancellation",
        [
            str(worker),
            "--state-dir",
            str(directory / "worker-cancellation"),
            "--poll-interval",
            "0.1",
            "--heartbeat-interval",
            "0.2",
        ],
        ROOT,
    )
    fixture = str(ROOT / "tooling/e2e_cancel_job.py")
    work = directory / "cancellation"
    work.mkdir()
    cancel_id = submit("uv", "run", fixture, str(work))
    eventually("cancellation job ready", lambda: (work / "ready.json").exists())
    details = json.loads((work / "ready.json").read_text())
    output_paths.add(api(f"/jobs/{cancel_id}")["output_path"])
    check("Cancellation requested" in call("-k", cancel_id).stdout, "cancel not requested")
    check("cancelling" in call("-l").stdout, "pending cancellation not shown")
    call("-k")  # Default selects the last run; repeat pending request is safe.
    call("-r", cancel_id, success=False)
    following = submit("echo", "worker survives cancellation")
    call("-k", following, success=False)  # Queued jobs use -r, not -k.
    eventually("remote cancellation acknowledgement", lambda: api(f"/jobs/{cancel_id}")["status"] == "failed")
    cancelled = api(f"/jobs/{cancel_id}")
    check(cancelled["error"] == "Job cancelled by user", "missing cancellation reason")
    for pid in [details["pid"], details["child_pid"]]:
        proc = Path(f"/proc/{pid}/stat")
        check(
            not proc.exists() or proc.read_text().split(")", 1)[1].split()[0] == "Z",
            "cancelled process survived SIGKILL",
        )
    check(cancel_worker.poll() is None, "cancellation stopped worker")
    time.sleep(0.5)  # Several claim intervals: cancellation must pause remote work too.
    check(api(f"/jobs/{following}")["status"] == "queued", "worker claimed after cancellation")
    stop(cancel_worker)
    check(cancel_worker.returncode == 0, "paused cancellation worker shutdown failed")
    cancel_worker = start(
        "worker-cancellation-restarted",
        [
            str(worker),
            "--state-dir",
            str(directory / "worker-cancellation"),
            "--poll-interval",
            "0.1",
            "--heartbeat-interval",
            "0.2",
        ],
        ROOT,
    )
    eventually(
        "restarted worker handles next job", lambda: api(f"/jobs/{following}")["status"] == "succeeded"
    )
    output_paths.add(api(f"/jobs/{following}")["output_path"])
    call("-k", following, success=False)
    check(cancel_worker.poll() is None, "cancellation stopped worker")
    stop(cancel_worker)
    check(cancel_worker.returncode == 0, "cancellation worker shutdown failed")
