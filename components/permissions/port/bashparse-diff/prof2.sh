#!/bin/bash
# Tests, then CPU/allocation profile and benchmark of the Go parse (after an optimisation).
set -u
cd /work/components/permissions/extensions/pi-permission-system
printf 'go 1.26\nuse /work/components/permissions/extensions/pi-permission-system\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "${PIG_SDK_DIR:-/pigsrc/extensions/sdk}" > /tmp/go.work.bp
export GOWORK=/tmp/go.work.bp
cd bashparse
echo "== tests"; go vet ./... && go test -count=1 ./... 2>&1 | tail -15
echo "== bench"; go test -run XXX -bench BenchmarkParse -benchtime 500ms -cpuprofile /tmp/cpu.out -o /tmp/bp3.test . 2>&1 | grep -E "^Bench"
echo "== cpu"; go tool pprof -top -nodecount 30 /tmp/bp3.test /tmp/cpu.out 2>&1 | sed -n 5,40p
echo "== alloc"; go test -run XXX -bench 'BenchmarkParse/script-5k' -benchtime 300ms -memprofile /tmp/mem.out -memprofilerate 1 -o /tmp/bp4.test . >/dev/null 2>&1; go tool pprof -sample_index=alloc_space -top -nodecount 12 /tmp/bp4.test /tmp/mem.out 2>&1 | sed -n 6,20p
echo "== done"
