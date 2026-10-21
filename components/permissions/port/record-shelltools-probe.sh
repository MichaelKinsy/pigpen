#!/bin/sh
# Re-records extensions/pi-permission-system/testdata/shelltools-oracle.json from the original. Run from components/permissions with an
# installed copy of port/oracle (see port/oracle/UPSTREAM.md) as the argument; the probe is copied into that copy's test directory for the
# run and removed after. Regenerate the cases first with python3 port/probe/gen-shelltools-cases.py.
set -e
pkg="$1/packages/pi-permission-system"
test -d "$pkg/node_modules" || { echo "usage: $0 <installed copy of port/oracle>" >&2; exit 2; }
here="$(pwd)"
cp port/probe/shelltools-probe.test.ts "$pkg/test/zz-shelltools-probe.test.ts"
trap 'rm -f "$pkg/test/zz-shelltools-probe.test.ts"' EXIT
(cd "$pkg" && PROBE_SHELLTOOLS_CASES="$here/extensions/pi-permission-system/testdata/shelltools-cases.json" \
  PROBE_SHELLTOOLS_OUT="$here/extensions/pi-permission-system/testdata/shelltools-oracle.json" \
  node_modules/.bin/vitest run test/zz-shelltools-probe.test.ts)
