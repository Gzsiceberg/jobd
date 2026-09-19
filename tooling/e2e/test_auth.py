#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pytest>=8,<9"]
# ///
import urllib.error
import urllib.request

from harness import check


def test_authentication_and_worker_scope(harness):
    call = harness.call
    env = harness.env
    address = harness.address
    tls = harness.tls

    for authorization in [None, "Bearer wrong-key"]:
        request = urllib.request.Request(
            f"{address}/queues/{env['JOBD_QUEUE']}/jobs",
            headers={} if authorization is None else {"Authorization": authorization},
        )
        try:
            urllib.request.urlopen(request, timeout=2, context=tls).close()
            raise AssertionError("Controller accepted invalid credentials")
        except urllib.error.HTTPError as error:
            check(error.code == 401, "expected unauthorized response")
    env["JOBD_WORKER_TOKEN"] = call("auth", "create-worker-token", "--duration", "1h").stdout.strip()
    worker_only = {"JOBD_MASTER_KEY": ""}
    call("job", "list", extra_env=worker_only)
    for args in [
        ("echo", "forbidden"),
        ("-C",),
        ("-r", "1"),
        ("env", "list"),
        ("auth", "create-worker-token", "--duration", "1h"),
    ]:
        call(*args, success=False, extra_env=worker_only)
    call("job", "list", success=False, extra_env=worker_only | {"JOBD_QUEUE": harness.queue + "-other"})
