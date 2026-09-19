#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Small helpers for real installer processes and private release fixtures."""

import hashlib
import io
from pathlib import Path
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[2]
TAG = "v0.0.0-test"
INSTALL = ROOT / "install.sh"
UNINSTALL = ROOT / "uninstall.sh"


def run(env, script, *args, success=True, extra=None, pipe=False):
    result = subprocess.run(
        ["sh", "-s", "--", *args] if pipe else ["sh", str(script), *args],
        input=Path(script).read_text() if pipe else None,
        env=env | (extra or {}),
        capture_output=True,
        text=True,
        timeout=30,
    )
    assert (result.returncode == 0) == success, (
        f"{script} {args}: exit {result.returncode}\n{result.stdout}\n{result.stderr}"
    )
    return result


def cli(env, binary, *args):
    result = subprocess.run(
        [str(binary), *args],
        env=env,
        capture_output=True,
        text=True,
        timeout=40,
    )
    assert result.returncode == 0, f"CLI {args}: {result.stdout}\n{result.stderr}"
    return result


def snapshot(directory):
    return {file.name: file.read_bytes() for file in directory.iterdir() if file.is_file()}


def update_checksum(archive):
    """Replace only this archive's checksum, preserving the other assets."""
    sums = archive.parent / "SHA256SUMS"
    lines = [line for line in sums.read_text().splitlines() if line.split()[-1] != archive.name]
    lines.append(hashlib.sha256(archive.read_bytes()).hexdigest() + "  " + archive.name)
    sums.write_text("\n".join(lines) + "\n")


def corrupt_archive(archive, kind, target):
    """Craft signed-by-the-fixture archives that must fail content validation."""
    if kind == "missing-license":
        original = archive.read_bytes()
        with tarfile.open(fileobj=io.BytesIO(original), mode="r:gz") as source:
            with tarfile.open(archive, "w:gz") as bundle:
                for item in source.getmembers():
                    if item.name != "LICENSE":
                        bundle.addfile(item, source.extractfile(item))
    else:
        with tarfile.open(archive, "w:gz") as bundle:
            for name in ["jobd", "jobd-worker", "jobd-uninstall", "LICENSE"]:
                item = tarfile.TarInfo("../escape" if kind == "traversal" and name == "jobd" else name)
                if kind == "symlink" and name == "jobd":
                    item.type, item.linkname = tarfile.SYMTYPE, str(target)
                    bundle.addfile(item)
                else:
                    item.size = 1
                    bundle.addfile(item, io.BytesIO(b"x"))
    update_checksum(archive)
