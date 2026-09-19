#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""TERM-ignoring process group for real worker cancellation tests."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

signal.signal(signal.SIGTERM, signal.SIG_IGN)
if sys.argv[1] == "--child":
    print("ready", flush=True)
else:
    child = subprocess.Popen(
        ["uv", "run", "--script", __file__, "--child"],
        stdout=subprocess.PIPE, text=True,
    )
    assert child.stdout.readline().strip() == "ready"
    root = Path(sys.argv[1])
    (root / "ready.json").write_text(json.dumps({
        "pid": os.getpid(), "child_pid": child.pid,
    }))
time.sleep(120)
