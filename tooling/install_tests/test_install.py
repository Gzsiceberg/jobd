#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pytest>=8,<9"]
# ///

import shutil
import subprocess

import pytest

from helpers import INSTALL, ROOT, TAG, UNINSTALL, snapshot, update_checksum


def test_help(sandbox):
    sandbox.run(INSTALL, "--help")
    sandbox.run(UNINSTALL, "--help")
    assert not sandbox.bins.exists()


@pytest.mark.parametrize(
    "args,extra",
    [
        (("--version",), {}),
        (("--bin-dir",), {}),
        (("--unknown",), {}),
        (("--version", "../../evil"), {}),
        ((), {"JOBD_TEST_OS": "Darwin"}),
        ((), {"JOBD_TEST_ARCH": "riscv64"}),
        ((), {"JOBD_TEST_CURL_FAIL": "1"}),
        ((), {"JOBD_TEST_TAG": "not-a-version"}),
    ],
)
def test_invalid_install_does_not_create_binaries(sandbox, args, extra):
    sandbox.run(INSTALL, *args, success=False, extra=extra)
    assert not sandbox.bins.exists()


def test_public_pipe_install_and_real_binaries(sandbox):
    s = sandbox
    s.state.mkdir(parents=True)
    identity = "00000000-0000-4000-8000-000000000001\n"
    (s.state / "worker-id").write_text(identity)
    s.run(INSTALL, pipe=True, extra={"JOBD_WORKER_TOKEN": "test-secret", "JOBD_LOCAL_PERSIST": "true"})
    assert not (s.state / "local/control.sock").exists(), "installer started a worker"
    assert (s.state / "worker-id").read_text() == identity
    for name in ["jobd", "jobd-worker", "jobd-uninstall"]:
        assert (s.bins / name).stat().st_mode & 0o777 == 0o755
    for name in ["jobd", "jobd-worker"]:
        result = subprocess.run([str(s.bins / name), "--help"], env=s.env, capture_output=True, timeout=10)
        assert result.returncode == 0, f"installed {name} cannot execute: {result.stderr}"
    assert f"jobd_{TAG}_linux_{s.native}.tar.gz" in (s.root / "curl.log").read_text()
    assert not (s.home / ".profile").exists(), "installer edited shell configuration"
    license = s.bins / "jobd-LICENSE"
    assert license.read_bytes().startswith((ROOT / "LICENSE").read_bytes())
    assert "modernc.org/sqlite@" in license.read_text()
    assert license.stat().st_mode & 0o777 == 0o644


def test_upgrade_downgrade_reinstall_and_rollback(installed):
    s = installed
    before = snapshot(s.bins)
    next_tag = "v0.0.1-test"
    next_asset = s.assets / f"jobd_{next_tag}_linux_{s.native}.tar.gz"
    shutil.copyfile(s.archive, next_asset)
    update_checksum(next_asset)
    s.run(INSTALL, "--version", next_tag, extra={"JOBD_TEST_TAG": next_tag})
    assert next_tag in (s.bins / ".jobd-install.sha256").read_text()
    s.run(INSTALL, "--version", TAG)
    s.run(INSTALL, "--version", TAG)
    assert snapshot(s.bins) == before
    s.run(INSTALL, "--version", TAG, success=False, extra={"JOBD_TEST_MV_FAIL": "1"})
    assert (s.root / "mv-failed").exists(), "replacement failure was not exercised"
    assert snapshot(s.bins) == before, "failed install did not roll back"
    assert not (s.bins / ".jobd-install.lock").exists()


@pytest.mark.parametrize("directory", ["custom bin", 'special % $ " \\ bin'])
def test_custom_paths_and_self_locating_uninstaller(sandbox, directory):
    custom = sandbox.root / directory
    sandbox.run(INSTALL, "--bin-dir", str(custom), "--version", TAG)
    sandbox.run(custom / "jobd-uninstall")
    assert not (custom / "jobd").exists()
    assert not (custom / "jobd-worker").exists()


@pytest.mark.parametrize("kind", ["unmanaged", "symlink", "lock"])
def test_installation_conflicts(sandbox, kind):
    s = sandbox
    custom = s.root / "custom bin"
    custom.mkdir()
    target = s.root / "unrelated"
    target.write_text("keep")
    binary = custom / "jobd"
    if kind == "unmanaged":
        binary.write_text("not ours")
    elif kind == "symlink":
        binary.symlink_to(target)
    else:
        (custom / ".jobd-install.lock").mkdir()
    s.run(INSTALL, "--bin-dir", str(custom), success=False)
    if kind == "unmanaged":
        s.run(UNINSTALL, "--bin-dir", str(custom))
        assert binary.read_text() == "not ours"
    elif kind == "lock":
        s.run(UNINSTALL, "--bin-dir", str(custom), success=False)
        assert (custom / ".jobd-install.lock").is_dir()
    else:
        assert binary.is_symlink()
    assert target.read_text() == "keep"
