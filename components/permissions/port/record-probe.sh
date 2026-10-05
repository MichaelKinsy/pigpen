#!/bin/sh
# Re-records extensions/pi-permission-system/testdata/oracle.json from the original. Run from components/permissions with an
# installed copy of port/oracle (see port/oracle/UPSTREAM.md) as the argument; the probe is copied into that copy's test
# directory for the run and removed after. python3 port/probe/gen-cases.py first when the cases change.
set -e
pkg="$1/packages/pi-permission-system"
test -d "$pkg/node_modules" || { echo "usage: $0 <installed copy of port/oracle>" >&2; exit 2; }
here="$(pwd)"
cp port/probe/probe.test.ts "$pkg/test/zz-review-probe.test.ts"
trap 'rm -f "$pkg/test/zz-review-probe.test.ts"' EXIT
(cd "$pkg" && PROBE_IN="$here/port/probe/cases.json" PROBE_OUT="$here/extensions/pi-permission-system/testdata/oracle.json" \
  node_modules/.bin/vitest run test/zz-review-probe.test.ts)
