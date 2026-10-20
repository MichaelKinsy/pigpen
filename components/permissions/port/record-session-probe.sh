#!/bin/sh
# Re-records extensions/pi-permission-system/testdata/session-oracle.json from the original. Run from components/permissions with an
# installed copy of port/oracle (see port/oracle/UPSTREAM.md) as the argument; the probe is copied into that copy's test directory for
# the run and removed after. Regenerate the cases first with python3 port/probe/gen-session-cases.py (from port/probe).
set -e
pkg="$1/packages/pi-permission-system"
test -d "$pkg/node_modules" || { echo "usage: $0 <installed copy of port/oracle>" >&2; exit 2; }
here="$(pwd)"
cp port/probe/session-probe.test.ts "$pkg/test/zz-session-probe.test.ts"
trap 'rm -f "$pkg/test/zz-session-probe.test.ts"' EXIT
(cd "$pkg" && PROBE_SESSION_CASES="$here/extensions/pi-permission-system/testdata/session-cases.json" \
  PROBE_SESSION_OUT="$here/extensions/pi-permission-system/testdata/session-oracle.json" \
  node_modules/.bin/vitest run test/zz-session-probe.test.ts)
