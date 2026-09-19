#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pytest>=8,<9"]
# ///
"""Retries exercise real CLI mutations and a second execution of the same IDs."""

from pathlib import Path

import pytest

from harness import eventually

FAIL_ONCE = 'if [ ! -f "$1" ]; then touch "$1"; exit 7; fi; echo retried'


@pytest.mark.parametrize("selection", ["single", "multiple", "all"])
def test_remote_retry(harness, selection):
    count = 1 if selection == "single" else 2
    ids = [
        harness.submit("sh", "-c", FAIL_ONCE, "sh", str(harness.directory / f"attempt-{i}"))
        for i in range(count)
    ]
    # Each worker pauses after its first failure.
    workers = [harness.start_worker(f"worker-{i}") for i in range(count)]
    eventually(
        "first attempts fail",
        lambda: all(harness.api(f"/jobs/{job_id}")["status"] == "failed" for job_id in ids),
    )
    old = [harness.api(f"/jobs/{job_id}") for job_id in ids]
    args = ["--all"] if selection == "all" else ids
    result = harness.call("job", "retry", *args)
    assert result.stdout == f"Requeued {count} failed job(s).\n"
    # A completed local job proves the paused workers are still scheduling, but
    # retrying must not itself unpause remote claims.
    for i in range(count):
        state = harness.directory / f"worker-{i}"
        local_id = harness.call("--local", "true", extra_env={"JOBD_STATE_DIR": str(state)}).stdout.strip()
        eventually(
            "paused worker runs local job",
            lambda: harness.local_api(state, f"/jobs/{local_id}")["status"] == "succeeded",
        )
    for previous in old:
        job = harness.api(f"/jobs/{previous['id']}")
        assert job["status"] == "queued"
        assert job["command"] == previous["command"]
        assert job["created_at"] == previous["created_at"]
        assert job["cancel_requested"] == 0
        for field in ["started_at", "finished_at", "worker_id", "output_path", "exit_code", "error"]:
            assert job[field] is None
        assert Path(previous["output_path"]).exists()
    for worker in workers:
        harness.stop(worker)
        assert worker.returncode == 0
    harness.start_worker("restarted", state=harness.directory / "worker-0")
    eventually(
        "retried commands succeed",
        lambda: all(harness.api(f"/jobs/{job_id}")["status"] == "succeeded" for job_id in ids),
    )
    for job_id in ids:
        assert Path(harness.api(f"/jobs/{job_id}")["output_path"]).read_text() == "retried\n"
    harness.call("job", "retry", ids[0], success=False)


def test_local_retry_multiple(harness):
    state = harness.directory / "worker"
    harness.env["JOBD_STATE_DIR"] = str(state)
    harness.start_worker(state=state)
    ids = [
        harness.call(
            "--local", "sh", "-c", FAIL_ONCE, "sh", str(harness.directory / f"attempt-{i}")
        ).stdout.strip()
        for i in range(2)
    ]
    eventually(
        "local first attempts fail",
        lambda: all(harness.local_api(state, f"/jobs/{job_id}")["status"] == "failed" for job_id in ids),
    )
    gate = harness.directory / "release"
    blocker = harness.call(
        "--local", "sh", "-c", 'while [ ! -f "$1" ]; do sleep 0.1; done', "sh", str(gate)
    ).stdout.strip()
    eventually(
        "local blocker starts", lambda: harness.local_api(state, f"/jobs/{blocker}")["status"] == "running"
    )
    assert harness.call("--local", "job", "retry", *ids).stdout == "Requeued 2 failed job(s).\n"
    for job_id in ids:
        assert harness.local_api(state, f"/jobs/{job_id}")["status"] == "queued"
    gate.touch()
    eventually(
        "local retries succeed",
        lambda: all(harness.local_api(state, f"/jobs/{job_id}")["status"] == "succeeded" for job_id in ids),
    )
    assert harness.jobs() == []
