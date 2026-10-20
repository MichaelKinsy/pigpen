#!/bin/bash
# The remaining proof of the bashparse lane, run in the tern-ci container (one detached job):
#   PP_DIR=<dir> ~/pp-perm-run.sh "bash /work/components/permissions/port/bashparse-diff/proof.sh" > log
set -u
cd /work/components/permissions/extensions/pi-permission-system
printf 'go 1.26\nuse /work/components/permissions/extensions/pi-permission-system\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "${PIG_SDK_DIR:-/pigsrc/extensions/sdk}" > /tmp/go.work.bp
export GOWORK=/tmp/go.work.bp
echo "== go version / sdk"; go version; echo "${PIG_SDK_DIR:-unset}"
echo "== (1) vet"
for os in linux windows darwin; do echo "vet GOOS=$os"; GOOS=$os go vet ./... 2>&1 | tail -5; done
echo "== (1) go test -race -count=1 ./..."
go test -race -count=1 ./... 2>&1 | tail -30
echo "== (2) fuzz-more comparison"
for n in 1 2 3 4; do
  if test -f /work/bpwork/hash-fuzzmore$n.txt; then
    BP_FUZZ_IN=/work/bpwork/in-fuzzmore$n.json BP_FUZZ_HASHES=/work/bpwork/hash-fuzzmore$n.txt go test -count=1 -run TestCompareFuzzMore -v ./bashparse 2>&1 | grep -E "fuzz-more|FAIL|ok|differs" | head -12
  fi
done
echo "== (3) mutations (the bp-* ones)"
python3 - <<'PY'
import json
m = json.load(open("port/mutations.json"))
json.dump([x for x in m if x["name"].startswith("bp-")], open("/tmp/bpmut.json", "w"))
PY
/home/kinsy/pigeq-bin-bp mutate --unit-only --go extensions/pi-permission-system --mutations /tmp/bpmut.json --sdk-dir "${PIG_SDK_DIR:-/pigsrc}" --jobs 4 2>&1 | tail -25
cd /work/components/permissions/extensions/pi-permission-system
echo "== (4) performance (interleaved rounds, one pinned CPU set; see bench-rounds.sh)"
bash /work/components/permissions/port/bashparse-diff/bench-rounds.sh 12 4
cd /work/components/permissions/extensions/pi-permission-system
echo "== binary size"
cat > /tmp/sz_main.go <<'EOF'
package main

import (
	"fmt"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

func main() { p, _ := bashparse.GetParser(); fmt.Println(p.Parse("echo hi").Root.Type()) }
EOF
mkdir -p bashparse/zzsize && cp /tmp/sz_main.go bashparse/zzsize/main.go
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /tmp/sz_with ./bashparse/zzsize && ls -l /tmp/sz_with
printf 'package main\nfunc main() { println("hi") }\n' > bashparse/zzsize/main.go
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /tmp/sz_without ./bashparse/zzsize && ls -l /tmp/sz_without
rm -rf bashparse/zzsize
echo "== done"
