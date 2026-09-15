# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Job fixture launched by the real worker during tooling/e2e.py."""
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import time

mode, root = sys.argv[1:]
root = Path(root)
child = None
if mode == "cancel":
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    child = subprocess.Popen([
        sys.executable, "-c",
        "import signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); print('ready',flush=True); time.sleep(120)",
    ], stdout=subprocess.PIPE, text=True)
    assert child.stdout.readline().strip() == "ready"

with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
    sock.settimeout(15)
    sock.connect(os.environ["JOBD_PROGRESS_SOCKET"])
    reader = sock.makefile("rb")

    def send(message):
        sock.sendall((json.dumps(message) + "\n").encode())
        return json.loads(reader.readline())

    if mode == "quick-fail":
        assert send({"progress": 0.04})["ok"] is True
        sys.exit(7)  # Final report must preserve progress below the threshold before the next timer tick.

    if mode == "timed":
        assert send({"progress": 0.05})["ok"] is True
        (root / "ready").touch()
        while not (root / "half").exists():
            time.sleep(0.05)
        assert send({"progress": 0.11})["ok"] is True
        (root / "half-acked").touch()
        while not (root / "finish").exists():
            time.sleep(0.05)
        sys.exit(0)

    assert send({"progress": 2})["ok"] is False
    assert send({"progress": 0.25})["ok"] is True
    (root / "ready.json").write_text(json.dumps({
        "socket": os.environ["JOBD_PROGRESS_SOCKET"], "pid": os.getpid(),
        "child_pid": child.pid if child else None,
    }))
    while not (root / "half").exists():
        time.sleep(0.05)
    assert send({"progress": 0.5})["ok"] is True
    assert send({"progress": 0.1})["ok"] is True  # Controller must not regress.
    (root / "half-acked").touch()
    while not (root / "finish").exists():
        time.sleep(0.05)
print("Progress job finished", flush=True)
