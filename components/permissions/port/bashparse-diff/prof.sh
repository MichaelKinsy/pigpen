#!/bin/bash
# CPU and allocation profile of the Go parse, then the bp-* mutations.
set -u
cd /work/components/permissions/extensions/pi-permission-system
printf 'go 1.26\nuse /work/components/permissions/extensions/pi-permission-system\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "${PIG_SDK_DIR:-/pigsrc/extensions/sdk}" > /tmp/go.work.bp
export GOWORK=/tmp/go.work.bp
cd bashparse
echo "== cpu profile"
go test -run XXX -bench 'BenchmarkParse/script-5k' -benchtime 3s -cpuprofile /tmp/cpu.out -memprofile /tmp/mem.out -memprofilerate 1 -o /tmp/bp2.test . 2>&1 | grep -E "^Bench"
go tool pprof -top -nodecount 35 /tmp/bp2.test /tmp/cpu.out 2>&1 | head -50
echo "== alloc profile"
go tool pprof -sample_index=alloc_space -top -nodecount 20 /tmp/bp2.test /tmp/mem.out 2>&1 | head -30
echo "== mutations (the bp-* ones)"
cd /work/components/permissions
python3 - <<'PY'
import json
m = json.load(open("port/mutations.json"))
json.dump([x for x in m if x["name"].startswith("bp-")], open("/tmp/bpmut.json", "w"))
PY
/work/pigeq-bin mutate --unit-only --go extensions/pi-permission-system --mutations /tmp/bpmut.json --sdk-dir "${PIG_SDK_DIR:-/pigsrc}" --jobs 4 2>&1 | tail -25
echo "== done"
