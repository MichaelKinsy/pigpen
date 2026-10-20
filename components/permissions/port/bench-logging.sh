#!/bin/sh
# One run of the logging latency probe under the original (ns per operation, printed as a BENCH line). Run from components/permissions
# with an installed copy of port/oracle as the argument; interleave with `go test -bench . -run xxx` for the port.
set -e
pkg="$1/packages/pi-permission-system"
cp port/probe/logging-bench.test.ts "$pkg/test/zz-logging-bench.test.ts"
trap 'rm -f "$pkg/test/zz-logging-bench.test.ts"' EXIT
(cd "$pkg" && node_modules/.bin/vitest run test/zz-logging-bench.test.ts 2>&1 | grep '^BENCH')
