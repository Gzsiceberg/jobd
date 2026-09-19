#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pytest>=8,<9"]
# ///
import subprocess

from harness import check


def test_help_and_empty_defaults(harness):
    call = harness.call

    for flag in ["-h", "--help"]:
        check(
            "-U ID1 ID2" in call(flag, extra_env={"JOBD_CONTROLLER": "invalid"}).stdout,
            "help missing actions",
        )
    check(call().stdout == call("-l").stdout, "default action is not list")
    for flag in ["-o", "-r", "-u", "-k"]:
        call(flag, success=False)
    call("-C")


def test_ordering_defaults_and_queue_isolation(harness):
    call = harness.call
    api = harness.api
    submit = harness.submit
    order = harness.order
    elapsed = harness.elapsed
    address = harness.address

    a = submit("echo", "first")
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

    # Environment selectors, queue isolation, and -- delimiter.
    other_queue = harness.queue + "-other"
    selectors = {"JOBD_CONTROLLER": address, "JOBD_QUEUE": other_queue}
    other = call("--", "echo", "isolated", extra_env=selectors).stdout.strip()
    check(api(f"/jobs/{other}", other_queue)["command"] == ["echo", "isolated"], "-- changed argv")
    check(other in call("job", "list", extra_env=selectors).stdout, "environment selectors failed")
    check("isolated" not in call().stdout and other == "1", "queue isolation/per-queue IDs failed")
    check(other in call(extra_env={"JOBD_QUEUE": other_queue}).stdout, "queue env ignored")
    call("-r", other, extra_env=selectors)


def test_pagination_and_invalid_arguments(harness):
    call = harness.call
    submit = harness.submit
    jobs = harness.jobs

    # Exercise pagination with real submissions, not mocked API responses.
    ids = [submit("echo", str(i)) for i in range(101)]
    listing = call().stdout.splitlines()[1:]
    check([line.split()[0] for line in listing] == ids, "listing pagination lost/reordered jobs")
    call("-u", ids[-1])
    call("-U", ids[-1], ids[0])
    for job_id in ids:
        call("-r", job_id)
    check(jobs() == [], "pagination cleanup failed")

    for args in [
        ("-U", "one"),
        ("-C", "extra"),
        ("-l", "extra"),
        ("-o", "a", "b"),
        ("-r", "a", "b"),
        ("-u", "a", "b"),
        ("-k", "a", "b"),
        ("-k", "missing"),
        ("--",),
        ("-unknown",),
        ("--controller",),
        ("--queue",),
        ("--queue", "INVALID", "-l"),
        ("-o", "missing"),
        ("-r", "missing"),
        ("-u", "missing"),
        ("-U", "missing", "also-missing"),
    ]:
        call(*args, success=False)


def test_command_quoting(harness):
    call = harness.call
    api = harness.api
    submit = harness.submit

    # Copy the actual COMMAND table cell into Bash, and verify argv survives.
    copy_args = [
        "hello world",
        "",
        "it's fine",
        "$HOME",
        "$(printf injected)",
        "`printf injected`",
        "*.go",
        "~",
        "a;b",
        "line1\nline2",
        "a\tb",
        "\\path\\",
    ]
    for arg in copy_args:
        args = [arg]
        copy_id = submit("sh", "-c", 'printf "%s\\0" "$@"', "sh", *args)
        row = next(line for line in call("-l").stdout.splitlines() if line.split()[0] == copy_id)
        command = row.split(None, 7)[7]
        pasted = subprocess.run(["bash", "-c", command], capture_output=True, timeout=10)
        check(pasted.returncode == 0, f"Pasted command failed: {pasted.stderr!r}")
        check(
            pasted.stdout == b"".join(arg.encode() + b"\0" for arg in args),
            "Copied COMMAND changed arguments",
        )
        call("-r", copy_id)
    long_id = submit("echo", "x" * 200)
    row = next(line for line in call("-l").stdout.splitlines() if line.split()[0] == long_id)
    command = row.split(None, 7)[7]
    check(len(command) == 60 and command.endswith("..."), "long command was not truncated")
    check(api(f"/jobs/{long_id}")["command"] == ["echo", "x" * 200], "truncation changed stored command")
    call("-r", long_id)
