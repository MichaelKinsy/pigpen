#!/bin/sh
# Usage: PIG_SDK_DIR=<PiG checkout>/extensions/sdk sh port/sdkcheck/run.sh   (go on PATH)
# Copies this check to a temp dir, points the SDK module at a PiG checkout and builds it.
set -eu
: "${PIG_SDK_DIR:?set PIG_SDK_DIR to <PiG checkout>/extensions/sdk}"
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp "$here/sdkcheck.go" "$tmp/"
sed "s|=> ../..|=> $root|" "$here/go.mod" > "$tmp/go.mod"
cd "$tmp"
go mod edit -replace "github.com/MichaelKinsy/PiG/extensions/sdk=$PIG_SDK_DIR"
GOFLAGS=-mod=mod go vet ./... && echo "sdkcheck: sdk.ModelRegistry satisfies pigmodel.Registry"
