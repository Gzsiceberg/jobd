#!/bin/sh
# Removes checksum-tracked binaries and the user service. Preserves state and logs.
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
                'Stops/removes the managed user service and binaries; state and logs are preserved.'
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
service_locked=0
cleanup() {
    [ "$service_locked" = 0 ] || rmdir "$service_dir/.jobd-service.lock"
    rmdir "$bin_dir/.jobd-install.lock"
}
trap cleanup 0
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
if [ ! -e "$bin_dir/$manifest" ] && [ ! -L "$bin_dir/$manifest" ]; then
    printf 'No managed jobd installation in %s; nothing removed.\n' "$bin_dir"
    exit 0
fi
[ -f "$bin_dir/$manifest" ] && [ ! -L "$bin_dir/$manifest" ] || fail 'invalid installation manifest'
# Older installations did not bundle a license; do not touch unrelated files.
if [ -n "$(awk '$2 == "jobd-LICENSE" { print $1 }' "$bin_dir/$manifest")" ]; then
    files="$files jobd-LICENSE"
fi
for name in $files; do
    if [ -e "$bin_dir/$name" ] || [ -L "$bin_dir/$name" ]; then
        [ -f "$bin_dir/$name" ] && [ ! -L "$bin_dir/$name" ] || fail "refusing to remove non-regular file: $bin_dir/$name"
        expected=$(awk -v name="$name" '$2 == name { print $1 }' "$bin_dir/$manifest")
        actual=$(sha256sum < "$bin_dir/$name" | awk '{print $1}')
        [ "$actual" = "$expected" ] || fail "installed file was modified: $bin_dir/$name (move it aside before uninstalling)"
    fi
done
service_path=$(awk '/^# service-path: / { print substr($0,17) }' "$bin_dir/$manifest")
if [ -n "$service_path" ]; then
    case "$service_path" in /*/jobd-worker.service) ;; *) fail 'invalid service path in manifest' ;; esac
    command -v systemctl >/dev/null 2>&1 || fail 'missing systemctl'
    service_dir=$(dirname "$service_path")
    mkdir -p "$service_dir"
    mkdir "$service_dir/.jobd-service.lock" 2>/dev/null || fail 'another service install/uninstall is active'
    service_locked=1
    if [ -e "$service_path" ] || [ -L "$service_path" ]; then
        [ -f "$service_path" ] && [ ! -L "$service_path" ] || fail 'service unit must be a regular file, not a symlink'
        expected=$(awk '$2 == "jobd-worker.service" { print $1 }' "$bin_dir/$manifest")
        actual=$(sha256sum < "$service_path" | awk '{print $1}')
        [ "$actual" = "$expected" ] || fail "service unit was modified: $service_path (move it aside before uninstalling)"
        # Validate everything before stopping or removing anything.
        systemctl --user stop jobd-worker.service || fail 'could not stop user service; nothing removed'
        systemctl --user disable jobd-worker.service || fail 'could not disable user service; nothing removed'
        rm -f "$service_path"
    else
        # A missing unit may still be cached/running in the user manager.
        systemctl --user show-environment >/dev/null || fail 'cannot reach systemd user manager'
        if systemctl --user is-active --quiet jobd-worker.service; then
            systemctl --user stop jobd-worker.service || fail 'could not stop user service'
        fi
        systemctl --user disable jobd-worker.service || fail 'could not disable user service'
    fi
    systemctl --user daemon-reload || fail 'could not reload user manager; rerun uninstall'
fi
# Validation above completes before removing anything, including this script itself.
for name in $files; do rm -f "$bin_dir/$name"; done
rm -f "$bin_dir/$manifest"
printf 'Removed jobd binaries from %s\n' "$bin_dir"
printf '%s\n' 'Managed user service removed. Worker state and logs preserved; manually started workers were not stopped.'
