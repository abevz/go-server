#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
mode=${1:-generate}
case "$mode" in
  generate|--check) ;;
  *) echo "Usage: $0 [--check]" >&2; exit 2 ;;
esac

# Pin protoc as well as its plugins: its version appears in generated headers.
if [ "$(protoc --version)" != "libprotoc 36.1" ]; then
  echo "Generation requires protoc reporting libprotoc 36.1." >&2
  exit 1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir -p "$work/bin" "$work/out/cmd/openapi"

GOBIN="$work/bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
GOBIN="$work/bin" go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.21.1
GOBIN="$work/bin" go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.1

protoc -I api/proto \
  --plugin="protoc-gen-go=$work/bin/protoc-gen-go" \
  --plugin="protoc-gen-connect-go=$work/bin/protoc-gen-connect-go" \
  --go_out="$work/out" --go_opt=module=github.com/abevz/go-server \
  --connect-go_out="$work/out" --connect-go_opt=module=github.com/abevz/go-server \
  api/proto/user/v1/user.proto
"$work/bin/oapi-codegen" --config api/oapi-codegen.yaml \
  -o "$work/out/cmd/openapi/openapi_gen.go" api/openapi.yaml

for path in gen/user/v1/user.pb.go gen/user/v1/userv1connect/user.connect.go cmd/openapi/openapi_gen.go; do
  if [ "$mode" = "--check" ]; then
    diff -u "$path" "$work/out/$path"
  else
    mkdir -p "$(dirname "$path")"
    cp "$work/out/$path" "$path"
  fi
done
