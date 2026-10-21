#!/bin/sh
# Re-records extensions/pi-permission-system/testdata/intent-oracle.json from the original. Run from components/permissions with an
# installed copy of port/oracle as the argument; the probe is copied into that copy's test directory for the run and removed after.
set -e
pkg="$1/packages/pi-permission-system"
test -d "$pkg/node_modules" || { echo "usage: $0 <installed copy of port/oracle>" >&2; exit 2; }
here="$(pwd)"
cp port/probe/intent-probe.test.ts "$pkg/test/zz-intent-probe.test.ts"
trap 'rm -f "$pkg/test/zz-intent-probe.test.ts"' EXIT
(cd "$pkg" && PROBE_INTENT_OUT="$here/extensions/pi-permission-system/testdata/intent-oracle.json" node_modules/.bin/vitest run test/zz-intent-probe.test.ts)
