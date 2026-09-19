#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pytest>=8,<9"]
# ///

import pytest

from helpers import INSTALL, corrupt_archive


@pytest.mark.parametrize("kind", ["checksum", "missing-license", "traversal", "symlink"])
def test_unsafe_archive_is_rejected(sandbox, kind):
    s = sandbox
    target = s.root / "unrelated"
    target.write_text("keep")
    if kind == "checksum":
        s.archive.write_bytes(s.archive.read_bytes() + b"tamper")
    else:
        corrupt_archive(s.archive, kind, target)
    custom = s.root / "custom"
    s.run(INSTALL, "--bin-dir", str(custom), success=False)
    for name in ["jobd", "jobd-worker", "jobd-uninstall", "jobd-LICENSE"]:
        assert not (custom / name).exists(), "unsafe archive installed"
    assert not list(s.root.rglob("escape")), "archive escaped its staging directory"
    assert target.read_text() == "keep"


def test_arm64_asset_selection(sandbox):
    arm = sandbox.root / "arm-bin"
    sandbox.run(INSTALL, "--bin-dir", str(arm), extra={"JOBD_TEST_ARCH": "aarch64"})
    assert int.from_bytes((arm / "jobd").read_bytes()[18:20], "little") == 183
    sandbox.run(arm / "jobd-uninstall")
