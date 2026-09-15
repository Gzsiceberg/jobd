#!/bin/sh
# Build reproducible standalone Linux release archives; no system installation.
set -eu
[ "$#" = 2 ] || { echo 'Usage: sh tooling/package-release.sh vX.Y.Z OUTPUT_DIR' >&2; exit 1; }
version=$1
printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$' || { echo 'Invalid release tag' >&2; exit 1; }
root=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
mkdir -p "$2"
out=$(CDPATH='' cd -- "$2" && pwd)
stage=$(mktemp -d)
trap 'rm -rf "$stage"' 0
trap 'exit 130' INT
trap 'exit 143' TERM
for arch in amd64 arm64; do
    (cd "$root/cli" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags='-s -w' -o "$stage/jobd" .)
    (cd "$root/worker" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags='-s -w' -o "$stage/jobd-worker" .)
    cp "$root/uninstall.sh" "$stage/jobd-uninstall"
    chmod 755 "$stage/jobd" "$stage/jobd-worker" "$stage/jobd-uninstall"
    tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -czf "$out/jobd_${version}_linux_${arch}.tar.gz" \
        -C "$stage" jobd jobd-worker jobd-uninstall
done
cp "$root/install.sh" "$root/uninstall.sh" "$out/"
(cd "$out" && sha256sum "jobd_${version}_linux_amd64.tar.gz" "jobd_${version}_linux_arm64.tar.gz" install.sh uninstall.sh > SHA256SUMS)
printf 'Release assets: %s\n' "$out"
