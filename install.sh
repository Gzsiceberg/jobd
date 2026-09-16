#!/bin/sh
# Public GitHub release installer. Uses HTTPS curl downloads; never uses sudo.
set -eu

repo=${JOBD_REPO:-Gzsiceberg/jobd}
version=${JOBD_VERSION:-latest}
bin_dir=${JOBD_BIN_DIR:-${HOME:?HOME must be set}/.local/bin}
manifest=.jobd-install.sha256
files='jobd jobd-worker jobd-uninstall jobd-LICENSE'
archive_files='jobd jobd-worker jobd-uninstall LICENSE'
download() {
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --retry 3 --connect-timeout 15 --max-time 120 "$@"
}
fail() { printf 'jobd install: %s\n' "$*" >&2; exit 1; }
usage() {
    printf '%s\n' 'Usage: sh install.sh [--version vX.Y.Z] [--bin-dir DIR]' \
        'Defaults: latest release, ~/.local/bin; JOBD_REPO can select a fork.' \
        'Requires curl and a running systemd user manager; no GitHub login needed.' \
        'Installs, enables and restarts jobd-worker.service for the current user.'
}
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version|--bin-dir)
            [ "$#" -ge 2 ] && [ -n "$2" ] || fail "$1 requires a value"
            case "$1" in --version) version=$2 ;; --bin-dir) bin_dir=$2 ;; esac
            shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) fail "unknown argument: $1" ;;
    esac
done
for tool in curl tar sha256sum mktemp awk grep systemctl; do
    command -v "$tool" >/dev/null 2>&1 || fail "missing $tool (install prerequisites first)"
done
[ "$(uname -s)" = Linux ] || fail 'only Linux is supported (the worker requires Linux)'
systemctl --user show-environment >/dev/null || fail 'cannot reach systemd user manager; run from a user login session'
service_dir=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user
case "$service_dir" in /*) ;; *) fail 'XDG_CONFIG_HOME must be absolute' ;; esac
case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) fail 'supported architectures: amd64, arm64' ;;
esac
printf '%s\n' "$repo" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' || fail 'invalid JOBD_REPO'
if [ "$version" = latest ]; then
    release_url=$(download --output /dev/null --write-out '%{url_effective}' "https://github.com/$repo/releases/latest") \
        || fail 'cannot find latest public release; check repository visibility and release availability'
    version=${release_url##*/}
fi
printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$' || fail 'version must be a release tag such as v0.1.0'
asset="jobd_${version}_linux_${arch}.tar.gz"

umask 077
tmp=$(mktemp -d)
stage=
locked=0
installing=0
committed=0
changed=
service_locked=0
service_changed=0
service_tmp=
cleanup() {
    result=$?
    trap - 0 HUP INT TERM
    rollback_failed=0
    if [ "$installing" = 1 ] && [ "$committed" = 0 ]; then
        for name in $changed; do
            if [ -f "$stage/backup/$name" ]; then
                mv -f "$stage/backup/$name" "$bin_dir/$name" || { result=1; rollback_failed=1; }
            else
                rm -f "$bin_dir/$name" || { result=1; rollback_failed=1; }
            fi
        done
    fi
    if [ "$service_changed" = 1 ] && [ "$committed" = 0 ]; then
        if [ -f "$stage/backup/jobd-worker.service" ]; then
            mv -f "$stage/backup/jobd-worker.service" "$service_path" || { result=1; rollback_failed=1; }
        else
            rm -f "$service_path" || { result=1; rollback_failed=1; }
        fi
    fi
    [ -z "$service_tmp" ] || rm -f "$service_tmp"
    [ "$service_locked" = 0 ] || rmdir "$service_dir/.jobd-service.lock"
    if [ "$rollback_failed" = 1 ]; then
        printf 'Rollback incomplete; recovery files retained in %s\n' "$stage" >&2
    else
        [ -z "$stage" ] || rm -rf "$stage"
    fi
    [ "$locked" = 0 ] || rmdir "$bin_dir/.jobd-install.lock"
    rm -rf "$tmp"
    exit "$result"
}
trap cleanup 0
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

for name in "$asset" SHA256SUMS; do
    download --output "$tmp/$name" "https://github.com/$repo/releases/download/$version/$name" \
        || fail 'release download failed; check public release availability'
done
expected=$(awk -v name="$asset" '$2 == name { print $1 }' "$tmp/SHA256SUMS")
printf '%s\n' "$expected" | grep -Eq '^[a-f0-9]{64}$' && [ "${#expected}" = 64 ] || fail 'missing or ambiguous release checksum'
checksum() { sha256sum < "$1" | awk '{print $1}'; }
[ "$(checksum "$tmp/$asset")" = "$expected" ] || fail 'release checksum mismatch; nothing installed'

# Only the binaries, uninstaller and license are allowed; reject paths and links.
listing=$(tar -tzf "$tmp/$asset") || fail 'invalid release archive'
[ "$(printf '%s\n' "$listing" | LC_ALL=C sort)" = "$(printf '%s\n' $archive_files | LC_ALL=C sort)" ] || fail 'unexpected files in release archive'
tar -tvzf "$tmp/$asset" | awk 'substr($0,1,1) != "-" { bad=1 } END { exit bad }' || fail 'release archive contains non-regular files'
mkdir "$tmp/unpacked"
tar -xzf "$tmp/$asset" -C "$tmp/unpacked" --no-same-owner --no-same-permissions
for name in $archive_files; do
    [ -f "$tmp/unpacked/$name" ] && [ ! -L "$tmp/unpacked/$name" ] || fail "invalid release file: $name"
done
mv "$tmp/unpacked/LICENSE" "$tmp/unpacked/jobd-LICENSE"

mkdir -p "$bin_dir"
bin_dir=$(cd "$bin_dir" && pwd -P)
mkdir "$bin_dir/.jobd-install.lock" 2>/dev/null || fail "another install/uninstall is active (lock: $bin_dir/.jobd-install.lock)"
locked=1
[ ! -L "$bin_dir/$manifest" ] || fail 'manifest must not be a symlink'
if [ -e "$bin_dir/$manifest" ]; then
    [ -f "$bin_dir/$manifest" ] || fail 'invalid installation manifest'
fi
for name in $files; do
    if [ -e "$bin_dir/$name" ] || [ -L "$bin_dir/$name" ]; then
        [ -f "$bin_dir/$name" ] && [ ! -L "$bin_dir/$name" ] || fail "refusing to overwrite non-regular file: $bin_dir/$name"
        [ -f "$bin_dir/$manifest" ] || fail "refusing to overwrite unmanaged file: $bin_dir/$name"
        expected=$(awk -v name="$name" '$2 == name { print $1 }' "$bin_dir/$manifest")
        [ "$(checksum "$bin_dir/$name")" = "$expected" ] || fail "installed file was modified: $bin_dir/$name (move it aside before reinstalling)"
    fi
done
mkdir -p "$service_dir"
service_dir=$(cd "$service_dir" && pwd -P)
service_path=$service_dir/jobd-worker.service
mkdir "$service_dir/.jobd-service.lock" 2>/dev/null || fail 'another service install/uninstall is active'
service_locked=1
case "$bin_dir$service_path" in
    *'
'*) fail 'installation paths must not contain newlines' ;;
esac
if printf '%s' "$bin_dir$service_path" | LC_ALL=C grep -q '[[:cntrl:]]'; then
    fail 'installation paths must not contain control characters'
fi
old_service_path=
if [ -f "$bin_dir/$manifest" ]; then
    old_service_path=$(awk '/^# service-path: / { print substr($0,17) }' "$bin_dir/$manifest")
fi
[ -z "$old_service_path" ] || [ "$old_service_path" = "$service_path" ] || fail 'service location changed; uninstall the previous installation first'
if [ -e "$service_path" ] || [ -L "$service_path" ]; then
    [ -f "$service_path" ] && [ ! -L "$service_path" ] || fail 'service unit must be a regular file, not a symlink'
    [ "$old_service_path" = "$service_path" ] || fail "refusing to overwrite unmanaged service: $service_path"
    expected=$(awk '$2 == "jobd-worker.service" { print $1 }' "$bin_dir/$manifest")
    [ "$(checksum "$service_path")" = "$expected" ] || fail "service unit was modified: $service_path (move it aside before reinstalling)"
fi
stage=$(mktemp -d "$bin_dir/.jobd-stage.XXXXXX")
mkdir "$stage/backup"
printf '# release: %s platform: linux/%s repository: %s\n' "$version" "$arch" "$repo" > "$stage/$manifest"
for name in $files; do
    cp "$tmp/unpacked/$name" "$stage/$name"
    case "$name" in jobd-LICENSE) chmod 644 "$stage/$name" ;; *) chmod 755 "$stage/$name" ;; esac
    printf '%s  %s\n' "$(checksum "$stage/$name")" "$name" >> "$stage/$manifest"
done
# Quote ExecStart for systemd (not a shell), including literal % and $.
worker_exec=$(printf '%s' "$bin_dir/jobd-worker" | awk '
    { for (i=1; i<=length($0); i++) {
        c=substr($0,i,1)
        if (c == "\\" || c == "\"") printf "\\%s", c
        else if (c == "%" || c == "$") printf "%s%s", c, c
        else printf "%s", c
    } }')
service_tmp=$(mktemp "$service_dir/.jobd-unit.XXXXXX")
cat > "$service_tmp" <<EOF
[Unit]
Description=jobd worker

[Service]
Type=simple
ExecStart="$worker_exec"
Restart=on-failure
RestartSec=5
TimeoutStopSec=30
KillSignal=SIGTERM

[Install]
WantedBy=default.target
EOF
chmod 644 "$service_tmp"
printf '# service-path: %s\n' "$service_path" >> "$stage/$manifest"
printf '%s  jobd-worker.service\n' "$(checksum "$service_tmp")" >> "$stage/$manifest"
[ ! -f "$service_path" ] || cp -p "$service_path" "$stage/backup/jobd-worker.service"
for name in $files "$manifest"; do
    [ ! -f "$bin_dir/$name" ] || cp -p "$bin_dir/$name" "$stage/backup/$name"
done
installing=1
for name in $files "$manifest"; do
    changed="$changed $name"
    mv -f "$stage/$name" "$bin_dir/$name"
done
service_changed=1
mv -f "$service_tmp" "$service_path"
service_tmp=
committed=1
# Service-manager failures leave a complete tracked installation for retry/uninstall.
systemctl --user daemon-reload || fail 'installed files, but daemon-reload failed; rerun the installer'
JOBD_API_KEY=${JOBD_API_KEY:-}
JOBD_CONTROLLER=${JOBD_CONTROLLER:-https://jobd-controller.aflashsheng.workers.dev}
JOBD_QUEUE=${JOBD_QUEUE:-default}
JOBD_STATE_DIR=${JOBD_STATE_DIR:-$HOME/.local/state/jobd-worker}
export JOBD_API_KEY JOBD_CONTROLLER JOBD_QUEUE JOBD_STATE_DIR
systemctl --user import-environment JOBD_API_KEY JOBD_CONTROLLER JOBD_QUEUE JOBD_STATE_DIR \
    || fail 'installed files, but environment import failed; rerun the installer'
systemctl --user enable jobd-worker.service || fail 'installed files, but service enable failed; rerun the installer'
systemctl --user restart jobd-worker.service || fail 'installed files, but service restart failed; inspect journalctl --user -u jobd-worker.service'
printf 'Installed jobd and jobd-worker %s in %s\n' "$version" "$bin_dir"
case ":${PATH:-}:" in
    *":$bin_dir:"*) ;;
    *) printf 'Add %s to PATH in your shell configuration.\n' "$bin_dir" ;;
esac
printf '%s\n' 'Enabled and restarted jobd-worker.service for the current user.' \
    'Without JOBD_API_KEY the worker waits. Set it and run jobd --restart to resume.' \
    "Uninstall with: $bin_dir/jobd-uninstall"
