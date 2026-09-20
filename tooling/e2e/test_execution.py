#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pytest>=8,<9"]
# ///
from pathlib import Path
import time

from harness import ROOT, check, eventually


def test_execution_output_elapsed_and_safeguards(harness):
    call = harness.call
    api = harness.api
    submit = harness.submit
    jobs = harness.jobs
    elapsed = harness.elapsed
    start = harness.start
    stop = harness.stop
    directory = harness.directory
    worker = harness.worker
    output_paths = harness.output_paths

    call("env", "set", "JOBD_TEST_SECRET=queue-secret")
    check(call("env", "list").stdout.strip() == "JOBD_TEST_SECRET", "admin env access failed")
    gate_a = directory / "release-a"
    a = submit(
        "sh",
        "-c",
        'set -eu; test "$JOBD_TEST_SECRET" = queue-secret; test "${JOBD_WORKER_TOKEN+x}" != x; test "${JOBD_MASTER_KEY+x}" != x; printf "hello stdout\\n"; printf "hello stderr\\n" >&2; while [ ! -f "$1" ]; do sleep 0.1; done',
        "sh",
        str(gate_a),
    )
    gate_f = directory / "release-f"
    f = submit(
        "sh",
        "-c",
        'printf "failure output\\n"; while [ ! -f "$1" ]; do sleep 0.1; done; exit 7',
        "sh",
        str(gate_f),
    )
    workers = []
    for name in ["worker-a", "worker-b"]:
        workers.append(
            start(
                name,
                [
                    str(worker),
                    "--state-dir",
                    str(directory / name),
                    "--poll-interval",
                    "0.1",
                    "--heartbeat-interval",
                    "0.2",
                ],
                ROOT,
            )
        )
    eventually(
        "both workers running with output paths",
        lambda: all(
            (j := api(f"/jobs/{job_id}"))["status"] == "running" and j["output_path"] for job_id in [a, f]
        ),
    )
    running = [api(f"/jobs/{job_id}") for job_id in [a, f]]
    check(len({j["worker_id"] for j in running}) == 2, "jobs not assigned to separate workers")
    for j in running:
        output_paths.add(j["output_path"])
        eventually("actual command output", lambda j=j: Path(j["output_path"]).stat().st_size > 0)
        result = call("-o", j["id"])
        check(result.stdout.strip() == j["output_path"], "incorrect output path")
        check(
            j["hostname"] in result.stderr and j["worker_id"] in result.stderr, "missing output host/worker"
        )
        check(Path(j["output_path"]).stat().st_mode & 0o077 == 0, "output permissions not private")
        line = next(line for line in call().stdout.splitlines() if line.split()[0] == j["id"])
        check(
            all(text in line for text in ["running", j["hostname"], j["worker_id"][:8], j["output_path"]]),
            "listing missing running metadata",
        )
    eventually("nonzero running elapsed time", lambda: elapsed(a) not in ("-", "0s"))
    before = elapsed(a)
    eventually("running elapsed time increases", lambda: elapsed(a) != before)
    latest = api("/jobs/latest?kind=run")
    check(call("-o").stdout.strip() == latest["output_path"], "-o default did not select last run")
    check("hello stdout" in Path(api(f"/jobs/{a}")["output_path"]).read_text(), "stdout not captured")
    check("hello stderr" in Path(api(f"/jobs/{a}")["output_path"]).read_text(), "stderr not captured")

    queued = submit("echo", "queued")
    for args in [("-r", a), ("-U", a, queued), ("-U", queued, a)]:
        check("409" in call(*args, success=False).stderr, f"Running job mutation accepted: {args}")
    # Batch urgent reports per-target failures instead of an HTTP conflict.
    result = call("-u", a, success=False)
    check(result.stdout.strip() == "Prioritized 0 queued job(s).", "incorrect urgent success count")
    check(
        f"0 succeeded, 1 failed: {a}: Only queued jobs can be reordered" in result.stderr,
        "urgent did not report the running job rejection",
    )
    check(api(f"/jobs/{a}")["status"] == "running", "urgent changed the running job")
    check(api(f"/jobs/{queued}")["status"] == "queued", "urgent changed the queued job")
    call("-C")
    check(len(jobs()) == 3, "-C removed queued/running jobs")
    call("-r")
    check(api(f"/jobs/{a}")["status"] == "running", "running job changed")
    gate_a.touch()
    eventually("successful job", lambda: api(f"/jobs/{a}")["status"] == "succeeded")
    check(api(f"/jobs/{a}")["exit_code"] == 0, "success exit code incorrect")
    call("-C")
    check([j["id"] for j in jobs()] == [f], "-C must preserve running jobs while clearing finished ones")
    gate_f.touch()
    eventually("failed job", lambda: api(f"/jobs/{f}")["status"] == "failed")
    check(api(f"/jobs/{f}")["exit_code"] == 7, "failure exit code incorrect")
    check("failed" in call().stdout, "failure not listed")
    finished_elapsed = elapsed(f)
    check(finished_elapsed != "-", "finished job missing duration")
    time.sleep(1.1)
    check(elapsed(f) == finished_elapsed, "finished duration kept increasing")
    check(call("-o").stdout.strip() == api(f"/jobs/{f}")["output_path"], "finished output unavailable")
    call("-r", f)  # Removing a finished job is allowed.
    check(jobs() == [], "finished removal failed")
    for process in workers:
        stop(process)
        check(process.returncode == 0, f"Worker shutdown failed: {process.returncode}")
    check(all(Path(path).exists() for path in output_paths), "queue removal deleted log files")


def test_failure_pauses_remote_but_local_continues_until_restart(harness):
    call = harness.call
    api = harness.api
    submit = harness.submit
    jobs = harness.jobs
    start = harness.start
    stop = harness.stop
    local_api = harness.local_api
    directory = harness.directory
    worker = harness.worker
    output_paths = harness.output_paths

    # Verify that queue edits change actual execution order, not just the table.
    execution_order = directory / "execution-order"
    ordered_ids = [
        submit("sh", "-c", 'printf "%s\\n" "$1" >> "$2"; exit "$3"', "sh", label, str(execution_order), code)
        for label, code in [("one", "0"), ("two", "9"), ("three", "0")]
    ]
    call("-u", ordered_ids[2])
    call("-U", ordered_ids[2], ordered_ids[1])
    ordered_state = directory / "worker-order"
    ordered_command = [
        str(worker),
        "--state-dir",
        str(ordered_state),
        "--poll-interval",
        "0.1",
        "--heartbeat-interval",
        "0.2",
    ]
    ordered_worker = start("worker-order", ordered_command, ROOT)
    eventually("ordered job fails", lambda: api(f"/jobs/{ordered_ids[1]}")["status"] == "failed")
    failed = api(f"/jobs/{ordered_ids[1]}")
    output_paths.add(failed["output_path"])
    local_id = call(
        "--local",
        "sh",
        "-c",
        "sleep 0.5; echo local-after-failure",
        extra_env={"JOBD_STATE_DIR": str(ordered_state)},
    ).stdout.strip()
    eventually(
        "local work continues after remote failure",
        lambda: local_api(ordered_state, f"/jobs/{local_id}")["status"] == "succeeded",
    )
    output_paths.add(local_api(ordered_state, f"/jobs/{local_id}")["output_path"])
    check(
        all(api(f"/jobs/{job_id}")["status"] == "queued" for job_id in [ordered_ids[0], ordered_ids[2]]),
        "worker claimed remote work after failure",
    )
    check(execution_order.read_text().splitlines() == ["two"], "remote execution continued after failure")
    check(ordered_worker.poll() is None, "failed job stopped worker")
    stop(ordered_worker)
    check(ordered_worker.returncode == 0, "paused worker shutdown failed")
    ordered_worker = start("worker-order-restarted", ordered_command, ROOT)
    eventually(
        "reordered execution after restart",
        lambda: all(api(f"/jobs/{job_id}")["status"] in ("succeeded", "failed") for job_id in ordered_ids),
    )
    check(
        execution_order.read_text().splitlines() == ["two", "one", "three"],
        "worker ignored edited queue order",
    )
    check(api(f"/jobs/{ordered_ids[1]}")["exit_code"] == 9, "ordered failure code incorrect")
    for job_id in ordered_ids:
        output_paths.add(api(f"/jobs/{job_id}")["output_path"])
    stop(ordered_worker)
    check(ordered_worker.returncode == 0, "ordered worker shutdown failed")
    call("-C")
    check(jobs() == [], "-C did not clear both succeeded and failed jobs")
    check(all(Path(path).exists() for path in output_paths), "-C deleted output files")
