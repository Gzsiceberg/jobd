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
        'Requires curl, tar and sha256sum.' \
        'Installs binaries only. Local jobd commands start the worker on demand.'
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
for tool in curl tar sha256sum mktemp awk grep; do
    command -v "$tool" >/dev/null 2>&1 || fail "missing $tool (install prerequisites first)"
done
[ "$(uname -s)" = Linux ] || fail 'only Linux is supported (the worker requires Linux)'
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
if printf '%s' "$bin_dir" | LC_ALL=C grep -q '[[:cntrl:]]'; then
    fail 'installation paths must not contain control characters'
fi
stage=$(mktemp -d "$bin_dir/.jobd-stage.XXXXXX")
mkdir "$stage/backup"
printf '# release: %s platform: linux/%s repository: %s\n' "$version" "$arch" "$repo" > "$stage/$manifest"
for name in $files; do
    cp "$tmp/unpacked/$name" "$stage/$name"
    case "$name" in jobd-LICENSE) chmod 644 "$stage/$name" ;; *) chmod 755 "$stage/$name" ;; esac
    printf '%s  %s\n' "$(checksum "$stage/$name")" "$name" >> "$stage/$manifest"
done
for name in $files "$manifest"; do
    [ ! -f "$bin_dir/$name" ] || cp -p "$bin_dir/$name" "$stage/backup/$name"
done
installing=1
for name in $files "$manifest"; do
    changed="$changed $name"
    mv -f "$stage/$name" "$bin_dir/$name"
done
committed=1
printf 'Installed jobd and jobd-worker %s in %s\n' "$version" "$bin_dir"
case ":${PATH:-}:" in
    *":$bin_dir:"*) ;;
    *) printf 'Add %s to PATH in your shell configuration.\n' "$bin_dir" ;;
esac
printf '%s\n' 'Local commands start jobd-worker on demand. Remote commands do not.' \
    'Run jobd worker restart to start a controller worker or apply an upgrade/environment changes.' \
    "Uninstall with: $bin_dir/jobd-uninstall"
