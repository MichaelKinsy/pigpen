#!/bin/sh
# Re-records extensions/pi-permission-system/testdata/paths-oracle.json and native-target.json from the original. Run from
# components/permissions with an installed copy of port/oracle (see port/oracle/UPSTREAM.md) as the argument; the probe is copied
# into that copy's test directory for the run and removed after.
set -e
pkg="$1/packages/pi-permission-system"
test -d "$pkg/node_modules" || { echo "usage: $0 <installed copy of port/oracle>" >&2; exit 2; }
here="$(pwd)"
cp port/probe/paths-probe.test.ts "$pkg/test/zz-paths-probe.test.ts"
trap 'rm -f "$pkg/test/zz-paths-probe.test.ts"' EXIT
(cd "$pkg" && PROBE_OUT="$here/extensions/pi-permission-system/testdata/paths-oracle.json" \
  PROBE_NATIVE_OUT="$here/extensions/pi-permission-system/testdata/native-target.json" \
  node_modules/.bin/vitest run test/zz-paths-probe.test.ts)
