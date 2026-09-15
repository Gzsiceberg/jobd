#!/bin/sh
# Removes only checksum-tracked jobd binaries. Never deletes worker state or logs.
set -eu

bin_dir=${JOBD_BIN_DIR:-${HOME:?HOME must be set}/.local/bin}
if [ -z "${JOBD_BIN_DIR:-}" ]; then
    case "$0" in
        */jobd-uninstall) bin_dir=$(dirname "$0") ;;
    esac
fi
manifest=.jobd-install.sha256
files='jobd jobd-worker jobd-uninstall'
fail() { printf 'jobd uninstall: %s\n' "$*" >&2; exit 1; }
while [ "$#" -gt 0 ]; do
    case "$1" in
        --bin-dir)
            [ "$#" -ge 2 ] && [ -n "$2" ] || fail '--bin-dir requires a value'
            bin_dir=$2; shift 2 ;;
        -h|--help)
            printf '%s\n' 'Usage: jobd-uninstall [--bin-dir DIR]' \
                'Removes managed binaries only. Stop workers yourself; state and logs are preserved.'
            exit 0 ;;
        *) fail "unknown argument: $1" ;;
    esac
done
for tool in sha256sum awk; do
    command -v "$tool" >/dev/null 2>&1 || fail "missing $tool"
done
if [ ! -d "$bin_dir" ]; then
    printf 'No jobd installation in %s\n' "$bin_dir"
    exit 0
fi
bin_dir=$(cd "$bin_dir" && pwd -P)
umask 077
mkdir "$bin_dir/.jobd-install.lock" 2>/dev/null || fail "another install/uninstall is active (lock: $bin_dir/.jobd-install.lock)"
cleanup() { rmdir "$bin_dir/.jobd-install.lock"; }
trap cleanup 0
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
if [ ! -e "$bin_dir/$manifest" ] && [ ! -L "$bin_dir/$manifest" ]; then
    printf 'No managed jobd installation in %s; nothing removed.\n' "$bin_dir"
    exit 0
fi
[ -f "$bin_dir/$manifest" ] && [ ! -L "$bin_dir/$manifest" ] || fail 'invalid installation manifest'
for name in $files; do
    if [ -e "$bin_dir/$name" ] || [ -L "$bin_dir/$name" ]; then
        [ -f "$bin_dir/$name" ] && [ ! -L "$bin_dir/$name" ] || fail "refusing to remove non-regular file: $bin_dir/$name"
        expected=$(awk -v name="$name" '$2 == name { print $1 }' "$bin_dir/$manifest")
        actual=$(sha256sum < "$bin_dir/$name" | awk '{print $1}')
        [ "$actual" = "$expected" ] || fail "installed file was modified: $bin_dir/$name (move it aside before uninstalling)"
    fi
done
# Validation above completes before removing anything, including this script itself.
for name in $files; do rm -f "$bin_dir/$name"; done
rm -f "$bin_dir/$manifest"
printf 'Removed jobd binaries from %s\n' "$bin_dir"
printf '%s\n' 'Worker identity/state and job output logs were preserved. Running workers were not stopped.'
