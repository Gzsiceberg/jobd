# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Submit with: jobd uv run /absolute/path/on/worker/to/examples/progress.py"""
import json
import os
import socket
import time


def report_progress(fraction):
    """ACK means buffered locally; timer/threshold uploads follow (not crash-durable)."""
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
        sock.settimeout(15)
        sock.connect(os.environ["JOBD_PROGRESS_SOCKET"])
        sock.sendall((json.dumps({"progress": fraction}) + "\n").encode())
        with sock.makefile("rb") as reader:
            line = reader.readline(4096)
        if not line:
            raise RuntimeError("Worker closed the progress socket without an acknowledgement")
        response = json.loads(line)
        if not response["ok"]:
            raise RuntimeError(response["error"])


if __name__ == "__main__":
    for step in range(1, 11):
        time.sleep(1)  # Replace with a unit of your actual work.
        report_progress(step / 10)
        print(f"Completed step {step}/10", flush=True)
