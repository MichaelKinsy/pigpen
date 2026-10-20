#!/bin/bash
# go test with the pinned SDK workspace: gotest.sh <args to go test>, from the module dir.
set -u
cd /work/components/permissions/extensions/pi-permission-system
printf 'go 1.26\nuse /work/components/permissions/extensions/pi-permission-system\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "${PIG_SDK_DIR:-/pigsrc/extensions/sdk}" > /tmp/go.work.bp
export GOWORK=/tmp/go.work.bp
go test "$@" 2>&1 | tail -60
echo "== done"
