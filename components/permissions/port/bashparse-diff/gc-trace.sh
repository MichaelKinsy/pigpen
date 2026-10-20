#!/bin/bash
# gctrace of the pipeline benchmark on one CPU.
set -u
cd /work/components/permissions/extensions/pi-permission-system
printf 'go 1.26\nuse /work/components/permissions/extensions/pi-permission-system\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "${PIG_SDK_DIR:-/pigsrc/extensions/sdk}" > /tmp/go.work.bp
export GOWORK=/tmp/go.work.bp
go test -c -o /tmp/bp6.test ./bashparse 2>&1 | tail -3
cd bashparse
GODEBUG=gctrace=1 taskset -c 130 /tmp/bp6.test -test.run XXX -test.bench 'BenchmarkParse/pipeline' -test.benchtime 300x 2>&1 | head -40
echo "== done"
