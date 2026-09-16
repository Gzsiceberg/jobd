#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
"""Build real release binaries and test shell installers with a fake public curl transport.
Run: uv run tooling/test-install.py. Never touches the real HOME or GitHub releases.
"""
import hashlib
import io
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
TAG = "v0.0.0-test"


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def main():
    with tempfile.TemporaryDirectory(prefix="jobd-install-test-") as tmp:
        root = Path(tmp)
        home, assets, fake = root / "home", root / "assets", root / "fake-bin"
        for directory in [home, assets, fake]:
            directory.mkdir()
        subprocess.run(["sh", str(ROOT / "tooling/package-release.sh"), TAG, str(assets)], check=True)
        (fake / "gh").write_text('#!/bin/sh\necho "gh must not be used" >&2\nexit 1\n')
        (fake / "curl").write_text('''#!/bin/sh
set -eu
printf '%s\\n' "$*" >> "$JOBD_TEST_CURL_LOG"
[ "${JOBD_TEST_CURL_FAIL:-0}" = 0 ] || exit 22
output=; write_out=; protocols=0
while [ "$#" -gt 0 ]; do
 case "$1" in
  --fail|--silent|--show-error|--location) shift ;;
  --proto|--proto-redir) [ "$2" = '=https' ]; protocols=$((protocols + 1)); shift 2 ;;
  --retry|--connect-timeout|--max-time) shift 2 ;;
  --output) output=$2; shift 2 ;;
  --write-out) write_out=$2; shift 2 ;;
  https://github.com/*) url=$1; shift ;;
  *) exit 1 ;;
 esac
done
[ "$protocols" = 2 ]
case "$url" in
 https://github.com/Gzsiceberg/jobd/releases/latest)
  [ "$output" = /dev/null ] && [ "$write_out" = '%{url_effective}' ]
  printf 'https://github.com/Gzsiceberg/jobd/releases/tag/%s' "$JOBD_TEST_TAG" ;;
 "https://github.com/Gzsiceberg/jobd/releases/download/$JOBD_TEST_TAG/"*)
  cp "$JOBD_TEST_ASSETS/${url##*/}" "$output" ;;
 *) exit 1 ;;
esac
''')
        (fake / "uname").write_text('''#!/bin/sh
case "$1" in -s) printf '%s\\n' "$JOBD_TEST_OS" ;; -m) printf '%s\\n' "$JOBD_TEST_ARCH" ;; *) exit 1 ;; esac
''')
        (fake / "systemctl").write_text('''#!/bin/sh
set -eu
printf '%s\\n' "$*" >> "$JOBD_TEST_SYSTEMCTL_LOG"
[ "$1" = --user ]
[ "$2" != "${JOBD_TEST_SYSTEMCTL_FAIL:-}" ] || exit 1
if [ "$2" = import-environment ]; then
    printf '%s\\n' "${JOBD_API_KEY:-}" > "$JOBD_TEST_IMPORTED_KEY"
fi
''')
        actual_mv = shutil.which("mv")
        (fake / "mv").write_text(f'''#!/bin/sh
if [ "${{JOBD_TEST_MV_FAIL:-0}}" = 1 ] && [ ! -e "$JOBD_TEST_FAIL_MARKER" ]; then
 case "$2" in
  */backup/*) ;;
  */.jobd-stage.*/jobd-worker) touch "$JOBD_TEST_FAIL_MARKER"; exit 1 ;;
 esac
fi
if [ "${{JOBD_TEST_UNIT_MV_FAIL:-0}}" = 1 ]; then
 case "$2" in */.jobd-unit.*) exit 1 ;; esac
fi
exec '{actual_mv}' "$@"
''')
        for file in fake.iterdir():
            file.chmod(0o755)
        env = {k: v for k, v in os.environ.items() if not k.startswith("JOBD_")}
        env.update({"HOME": str(home), "XDG_CONFIG_HOME": str(home / ".config"),
                    "JOBD_TEST_SYSTEMCTL_LOG": str(root / "systemctl.log"),
                    "JOBD_TEST_IMPORTED_KEY": str(root / "imported-key"), "PATH": str(fake) + os.pathsep + os.environ["PATH"],
                    "JOBD_TEST_TAG": TAG, "JOBD_TEST_ASSETS": str(assets),
                    "JOBD_TEST_CURL_LOG": str(root / "curl.log"), "JOBD_TEST_OS": "Linux",
                    "JOBD_TEST_ARCH": platform.machine(), "JOBD_TEST_FAIL_MARKER": str(root / "mv-failed")})

        def run(script, *args, success=True, extra=None, pipe=False):
            result = subprocess.run(["sh", "-s", "--", *args] if pipe else ["sh", str(script), *args],
                                    input=Path(script).read_text() if pipe else None,
                                    env=env | (extra or {}), capture_output=True, text=True, timeout=30)
            check((result.returncode == 0) == success,
                  f"{script} {args}: exit {result.returncode}\n{result.stdout}\n{result.stderr}")
            return result

        install, uninstall = ROOT / "install.sh", ROOT / "uninstall.sh"
        run(install, "--help")
        run(uninstall, "--help")
        run(install, "--version", success=False)
        run(install, "--bin-dir", success=False)
        run(install, "--unknown", success=False)
        run(install, "--version", "../../evil", success=False)
        run(install, success=False, extra={"JOBD_TEST_OS": "Darwin"})
        run(install, success=False, extra={"JOBD_TEST_ARCH": "riscv64"})
        run(install, success=False, extra={"JOBD_TEST_CURL_FAIL": "1"})
        run(install, success=False, extra={"JOBD_TEST_TAG": "not-a-version"})
        run(install, success=False, extra={"JOBD_TEST_SYSTEMCTL_FAIL": "show-environment"})
        check(not (home / ".local/bin").exists(), "failed downloads/validation created an installation")
        print("PASS help, validation, unsupported platforms, download failure", flush=True)

        bins = home / ".local/bin"
        state = home / ".local/state/jobd-worker"
        state.mkdir(parents=True)
        (state / "worker-id").write_text("preserve-worker-identity")
        run(install, pipe=True, extra={"JOBD_API_KEY": "test-secret"})
        unit = home / ".config/systemd/user/jobd-worker.service"
        unit_before = unit.read_bytes()
        check(f'ExecStart="{bins}/jobd-worker"' in unit.read_text(), "wrong service binary path")
        check("test-secret" not in unit.read_text(), "secret persisted in unit")
        check((root / "imported-key").read_text().strip() == "test-secret", "key not imported")
        calls = (root / "systemctl.log").read_text()
        check("--user enable jobd-worker.service" in calls and "--user restart jobd-worker.service" in calls, "service not enabled/started")
        check("test-secret" not in calls, "secret passed as command argument")
        for name in ["jobd", "jobd-worker", "jobd-uninstall"]:
            check((bins / name).stat().st_mode & 0o777 == 0o755, "wrong executable mode")
        for name in ["jobd", "jobd-worker"]:
            result = subprocess.run([str(bins / name), "--help"], env=env, capture_output=True, timeout=10)
            check(result.returncode == 0, f"installed {name} cannot execute")
        native = "amd64" if platform.machine() in ("x86_64", "amd64") else "arm64"
        check(f"jobd_{TAG}_linux_{native}.tar.gz" in (root / "curl.log").read_text(), "wrong architecture asset requested")
        check(not (home / ".profile").exists(), "installer edited shell configuration")
        check((bins / "jobd-LICENSE").read_bytes().startswith((ROOT / "LICENSE").read_bytes()), "installed license missing or incorrect")
        check("modernc.org/sqlite@" in (bins / "jobd-LICENSE").read_text(), "SQLite license notices missing")
        check((bins / "jobd-LICENSE").stat().st_mode & 0o777 == 0o644, "wrong license mode")
        print("PASS anonymous public downloads, bundled MIT license, real binaries execute", flush=True)

        before = {file.name: file.read_bytes() for file in bins.iterdir() if file.is_file()}
        next_tag = "v0.0.1-test"
        next_asset = assets / f"jobd_{next_tag}_linux_{native}.tar.gz"
        shutil.copyfile(assets / f"jobd_{TAG}_linux_{native}.tar.gz", next_asset)
        with (assets / "SHA256SUMS").open("a") as sums_file:
            sums_file.write(hashlib.sha256(next_asset.read_bytes()).hexdigest() + "  " + next_asset.name + "\n")
        run(install, "--version", next_tag, extra={"JOBD_TEST_TAG": next_tag})
        check(next_tag in (bins / ".jobd-install.sha256").read_text(), "upgrade version not recorded")
        run(install, "--version", TAG)  # Explicit downgrade.
        run(install, "--version", TAG)  # Idempotent reinstall.
        check(before == {file.name: file.read_bytes() for file in bins.iterdir() if file.is_file()}, "reinstall changed artifacts")
        run(install, "--version", TAG, success=False, extra={"JOBD_TEST_MV_FAIL": "1"})
        check(before == {file.name: file.read_bytes() for file in bins.iterdir() if file.is_file()}, "failed install did not roll back")
        check(not (bins / ".jobd-install.lock").exists(), "lock leaked")
        check(unit.read_bytes() == unit_before, "rollback changed service")
        run(install, success=False, extra={"JOBD_TEST_UNIT_MV_FAIL": "1"})
        check(unit.read_bytes() == unit_before, "unit replacement failure did not restore service")
        check(before == {file.name: file.read_bytes() for file in bins.iterdir() if file.is_file()}, "unit failure did not restore binaries/manifest")
        check((root / "imported-key").read_text().strip() == "", "missing key did not clear imported key")
        unit.write_text("locally modified unit")
        run(install, success=False)
        run(uninstall, success=False)
        check((bins / "jobd").exists(), "modified service caused partial uninstall")
        unit.write_bytes(unit_before)
        run(uninstall, success=False, extra={"JOBD_TEST_SYSTEMCTL_FAIL": "stop"})
        check(unit.exists() and (bins / "jobd").exists(), "stop failure removed files")
        run(install, success=False, extra={"JOBD_TEST_SYSTEMCTL_FAIL": "restart"})
        check(unit.exists() and (bins / ".jobd-install.sha256").exists(), "restart failure lost tracked installation")
        run(install)

        print("PASS explicit version, upgrade/downgrade, reinstall and rollback on replacement failure", flush=True)

        (bins / "jobd-LICENSE").write_text("modified license")
        run(install, success=False)
        run(uninstall, success=False)
        (bins / "jobd-LICENSE").write_bytes(before["jobd-LICENSE"])
        (bins / "jobd").write_bytes(b"locally modified")
        run(install, success=False)
        run(uninstall, success=False)
        check((bins / "jobd-worker").exists(), "uninstall partially deleted before validation")
        (bins / "jobd").write_bytes(before["jobd"])
        (bins / "jobd").chmod(0o755)
        (bins / "unrelated").write_text("keep")
        run(bins / "jobd-uninstall")
        check(not (bins / "jobd").exists() and not (bins / "jobd-worker").exists(), "binaries not removed")
        check(not unit.exists(), "service unit not removed")
        check(not (bins / "jobd-LICENSE").exists(), "managed license not removed")
        calls = (root / "systemctl.log").read_text()
        check("--user stop jobd-worker.service" in calls and "--user disable jobd-worker.service" in calls, "service not stopped/disabled")
        check((state / "worker-id").read_text() == "preserve-worker-identity", "uninstall deleted worker state")
        check((bins / "unrelated").read_text() == "keep", "uninstall deleted unrelated files")
        run(uninstall)
        print("PASS modified-file protection, uninstall, repeated uninstall, state preservation", flush=True)

        custom = root / "custom bin"
        unit.write_text("unmanaged service")
        run(install, "--bin-dir", str(custom), success=False)
        check(unit.read_text() == "unmanaged service", "unmanaged unit overwritten")
        unit.unlink()
        unit.symlink_to(bins / "unrelated")
        run(install, "--bin-dir", str(custom), success=False)
        unit.unlink()
        run(install, "--bin-dir", str(custom), "--version", TAG)
        check(f'ExecStart="{custom}/jobd-worker"' in unit.read_text(), "custom service path not quoted")
        run(custom / "jobd-uninstall")  # Must infer its own custom installation directory.
        check(not (custom / "jobd").exists(), "custom uninstall used the wrong directory")
        (custom / "jobd").write_text("not ours")
        run(install, "--bin-dir", str(custom), success=False)
        run(uninstall, "--bin-dir", str(custom))
        check((custom / "jobd").read_text() == "not ours", "unmanaged binary was changed")
        (custom / "jobd").unlink()
        (custom / "jobd").symlink_to(bins / "unrelated")
        run(install, "--bin-dir", str(custom), success=False)
        check((bins / "unrelated").read_text() == "keep", "installer followed a binary symlink")
        (custom / "jobd").unlink()
        (custom / ".jobd-install.lock").mkdir()
        run(install, "--bin-dir", str(custom), success=False)
        run(uninstall, "--bin-dir", str(custom), success=False)
        (custom / ".jobd-install.lock").rmdir()
        print("PASS custom paths with spaces, unmanaged-file protection, symlinks, concurrent lock", flush=True)

        archive = assets / f"jobd_{TAG}_linux_{native}.tar.gz"
        original, sums = archive.read_bytes(), (assets / "SHA256SUMS").read_text()
        archive.write_bytes(original + b"tamper")
        run(install, "--bin-dir", str(custom), success=False)
        archive.write_bytes(original)
        # A legacy archive without LICENSE is intentionally rejected.
        with tarfile.open(fileobj=io.BytesIO(original), mode="r:gz") as source:
            with tarfile.open(archive, "w:gz") as bundle:
                for item in source.getmembers():
                    if item.name != "LICENSE":
                        bundle.addfile(item, source.extractfile(item))
        (assets / "SHA256SUMS").write_text(hashlib.sha256(archive.read_bytes()).hexdigest() + "  " + archive.name + "\n")
        run(install, "--bin-dir", str(custom), success=False)
        check(not (custom / "jobd").exists(), "archive without license was installed")
        for link in [False, True]:
            with tarfile.open(archive, "w:gz") as bundle:
                for name in ["jobd", "jobd-worker", "jobd-uninstall", "LICENSE"]:
                    item = tarfile.TarInfo(name if link or name != "jobd" else "../escape")
                    if link and name == "jobd":
                        item.type, item.linkname = tarfile.SYMTYPE, str(bins / "unrelated")
                        bundle.addfile(item)
                    else:
                        item.size = 1
                        bundle.addfile(item, io.BytesIO(b"x"))
            (assets / "SHA256SUMS").write_text(hashlib.sha256(archive.read_bytes()).hexdigest() + "  " + archive.name + "\n")
            run(install, "--bin-dir", str(custom), success=False)
            check(not (custom / "jobd").exists(), "unsafe archive installed")
        archive.write_bytes(original)
        (assets / "SHA256SUMS").write_text(sums)
        print("PASS checksum mismatch, archive traversal and link rejection", flush=True)

        special = root / ('special % $ " ' + chr(92) + ' bin')
        run(install, "--bin-dir", str(special))
        quoted = str(special / "jobd-worker").replace(chr(92), chr(92) * 2).replace('"', chr(92) + '"').replace('%', '%%').replace('$', '$$')
        check(f'ExecStart="{quoted}"' in unit.read_text(), "systemd metacharacters not escaped")
        run(special / "jobd-uninstall")
        print("PASS systemd ExecStart escaping", flush=True)

        # Uninstall still supports older manifests without a managed license.
        run(install, "--bin-dir", str(custom))
        manifest = custom / ".jobd-install.sha256"
        manifest.write_text("\n".join(line for line in manifest.read_text().splitlines()
                                    if not line.endswith("  jobd-LICENSE")) + "\n")
        (custom / "jobd-LICENSE").write_text("unmanaged legacy file")
        run(custom / "jobd-uninstall")
        check((custom / "jobd-LICENSE").read_text() == "unmanaged legacy file", "legacy uninstall removed unrelated license")
        print("PASS legacy manifest uninstall", flush=True)

        arm = root / "arm-bin"
        run(install, "--bin-dir", str(arm), extra={"JOBD_TEST_ARCH": "aarch64"})
        check(int.from_bytes((arm / "jobd").read_bytes()[18:20], "little") == 183, "not an ARM64 ELF binary")
        run(arm / "jobd-uninstall")
        print("PASS ARM64 archive selection; all installer checks passed", flush=True)


if __name__ == "__main__":
    main()
