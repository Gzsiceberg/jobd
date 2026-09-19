#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pytest>=8,<9"]
# ///
"""Build once, then give each test independently mutable assets and HOME."""

from functools import partial
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile
import tempfile
from types import SimpleNamespace

import pytest

from helpers import INSTALL, ROOT, TAG, cli, run


@pytest.fixture(scope="session")
def release_assets():
    with tempfile.TemporaryDirectory(prefix="jobd-release-test-") as tmp:
        assets = Path(tmp)
        env = {key: value for key, value in os.environ.items() if not key.startswith("JOBD_")}
        subprocess.run(
            ["sh", str(ROOT / "tooling/package-release.sh"), TAG, str(assets)], env=env, check=True
        )
        yield assets


@pytest.fixture
def sandbox(release_assets, request):
    # Keep Unix socket paths short; pytest's nested temp paths can exceed 108 bytes.
    with tempfile.TemporaryDirectory(prefix="jobd-install-test-") as tmp:
        root = Path(tmp)
        home, assets, fake = root / "home", root / "assets", root / "fake-bin"
        home.mkdir()
        shutil.copytree(release_assets, assets)  # No hard links: tests mutate archives/checksums.
        shutil.copytree(Path(__file__).parent / "fake-bin", fake)
        for file in fake.iterdir():
            file.chmod(0o755)
        native = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[
            platform.machine()
        ]
        archive = assets / f"jobd_{TAG}_linux_{native}.tar.gz"
        cleanup = root / "cleanup-bin"
        cleanup.mkdir()
        # The CLI delegates stop to its sibling worker binary. Retain both outside
        # the installation so teardown never falls back to a developer's PATH.
        with tarfile.open(archive, "r:gz") as bundle:
            for name in ["jobd", "jobd-worker"]:
                with bundle.extractfile(name) as stream:
                    (cleanup / name).write_bytes(stream.read())
                (cleanup / name).chmod(0o755)
        bins = home / ".local/bin"
        state = home / ".local/state/jobd-worker"
        env = {key: value for key, value in os.environ.items() if not key.startswith("JOBD_")}
        env.update(
            {
                "HOME": str(home),
                "PATH": str(fake) + os.pathsep + os.environ["PATH"],
                "JOBD_STATE_DIR": str(state),
                "JOBD_TEST_TAG": TAG,
                "JOBD_TEST_ASSETS": str(assets),
                "JOBD_TEST_CURL_LOG": str(root / "curl.log"),
                "JOBD_TEST_OS": "Linux",
                "JOBD_TEST_ARCH": platform.machine(),
                "JOBD_TEST_FAIL_MARKER": str(root / "mv-failed"),
                "JOBD_TEST_REAL_MV": shutil.which("mv"),
            }
        )
        case = SimpleNamespace(
            root=root,
            home=home,
            assets=assets,
            bins=bins,
            state=state,
            env=env,
            native=native,
            archive=archive,
            run=partial(run, env),
            cli=partial(cli, env, bins / "jobd"),
        )
        request.node.install_sandbox = case
        try:
            yield case
        finally:
            # Registered before test execution and independent of installed binaries.
            cli(env, cleanup / "jobd", "worker", "stop")


@pytest.fixture
def installed(sandbox):
    sandbox.run(INSTALL)
    return sandbox


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item, call):
    outcome = yield
    report = outcome.get_result()
    case = getattr(item, "install_sandbox", None)
    if report.failed and case is not None and case.root.exists():
        log = case.root / "curl.log"
        listing = sorted(str(path.relative_to(case.home)) for path in case.home.rglob("*"))
        report.sections.append(("Installer sandbox", "\n".join(listing)))
        if log.exists():
            report.sections.append(("Fake curl requests", log.read_text()[-8000:]))
