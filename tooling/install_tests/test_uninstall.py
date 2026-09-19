#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pytest>=8,<9"]
# ///

import pytest

from helpers import INSTALL, UNINSTALL, snapshot


@pytest.mark.parametrize("name", ["jobd", "jobd-LICENSE"])
def test_modified_managed_files_are_protected(installed, name):
    s = installed
    (s.bins / name).write_bytes(b"locally modified")
    before = snapshot(s.bins)
    s.run(INSTALL, success=False)
    assert snapshot(s.bins) == before
    s.run(UNINSTALL, success=False)
    assert snapshot(s.bins) == before, "uninstall partially deleted before validation"


def test_uninstall_stops_worker_and_preserves_state(installed):
    s = installed
    s.state.mkdir(parents=True)
    identity = "00000000-0000-4000-8000-000000000001\n"
    (s.state / "worker-id").write_text(identity)
    (s.bins / "unrelated").write_text("keep")
    # Capture pipes too: a detached worker must not keep its parent's pipes open.
    s.cli("--local", "-l")
    assert (s.state / "local/control.sock").exists()
    s.cli("worker", "restart")
    s.run(s.bins / "jobd-uninstall")
    assert not (s.state / "local/control.sock").exists(), "uninstall left worker running"
    for name in ["jobd", "jobd-worker", "jobd-uninstall", "jobd-LICENSE"]:
        assert not (s.bins / name).exists()
    assert (s.state / "worker-id").read_text() == identity
    assert (s.bins / "unrelated").read_text() == "keep"
    s.run(UNINSTALL)
    assert (s.state / "worker-id").read_text() == identity
    assert (s.bins / "unrelated").read_text() == "keep"
