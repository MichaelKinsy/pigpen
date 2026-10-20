#!/bin/sh
# One run of the gate core latency probe under the original (ns per tool_call, printed as a BENCH line). Run from components/permissions
# with an installed copy of port/oracle as the argument; interleave with `go test -bench GateCore -run xxx -benchmem` for the port.
set -e
pkg="$1/packages/pi-permission-system"
cp port/probe/gatecore-bench.test.ts "$pkg/test/zz-gatecore-bench.test.ts"
trap 'rm -f "$pkg/test/zz-gatecore-bench.test.ts"' EXIT
(cd "$pkg" && node_modules/.bin/vitest run test/zz-gatecore-bench.test.ts 2>&1 | grep '^BENCH')
