#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
version=${VERSION:-dev}
target_os=${GOOS:-$(go env GOOS)}
target_arch=${GOARCH:-$(go env GOARCH)}
dist_dir=${DIST_DIR:-dist}
case "$version" in
  .*|*[!a-zA-Z0-9._-]*) echo "VERSION must be a filename-safe version, such as v0.1.0." >&2; exit 2 ;;
esac

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT HUP INT TERM
mkdir -p "$stage/bin" "$stage/api/proto" "$dist_dir"
CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
  go build -trimpath -ldflags="-s -w" -o "$stage/bin/" ./cmd/...
cp docs/binary-usage.md "$stage/README.md"
cp api/openapi.yaml "$stage/api/"
cp -R api/proto/user "$stage/api/proto/"

archive="go-server_${version}_${target_os}_${target_arch}.tar.gz"
tar -czf "$dist_dir/$archive" -C "$stage" bin api README.md
(
  cd "$dist_dir"
  sha256sum "$archive" > "$archive.sha256"
)
echo "$dist_dir/$archive"
