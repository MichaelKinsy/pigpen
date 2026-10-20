#!/bin/bash
# vet and the bashparse tests (the differential included) in the container, no race detector.
#   PP_DIR=<dir> ~/pp-perm-run.sh "bash /work/components/permissions/port/bashparse-diff/quick.sh" > log
set -u
cd /work/components/permissions/extensions/pi-permission-system
printf 'go 1.26\nuse /work/components/permissions/extensions/pi-permission-system\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "${PIG_SDK_DIR:-/pigsrc/extensions/sdk}" > /tmp/go.work.bp
export GOWORK=/tmp/go.work.bp
echo "== vet"; go vet ./bashparse/... 2>&1 | tail -20
echo "== test"; go test -count=1 ./bashparse/... 2>&1 | tail -40
echo "== done"
