#!/bin/sh
# One run of the handlers/service latency probe under the original (ns per call, printed as a BENCH line). Run from components/permissions
# with an installed copy of port/oracle as the argument; interleave with `go test -bench Handlers -run xxx -benchmem` for the port.
set -e
pkg="$1/packages/pi-permission-system"
cp port/probe/handlers-bench.test.ts "$pkg/test/zz-handlers-bench.test.ts"
trap 'rm -f "$pkg/test/zz-handlers-bench.test.ts"' EXIT
(cd "$pkg" && node_modules/.bin/vitest run test/zz-handlers-bench.test.ts 2>&1 | grep '^BENCH')
