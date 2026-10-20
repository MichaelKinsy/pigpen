#!/bin/sh
# Probes how deep each construct can nest before the original's parse traps (see depth.test.ts).
#   sh port/bashparse-diff/run-depth.sh <installed copy of port/oracle>
set -e
pkg="$1/packages/pi-permission-system"
here="$(pwd)/port/bashparse-diff"
cp "$here/depth.test.ts" "$pkg/test/zz-bashparse-depth.test.ts"
trap 'rm -f "$pkg/test/zz-bashparse-depth.test.ts"' EXIT
(cd "$pkg" && node_modules/.bin/vitest run test/zz-bashparse-depth.test.ts 2>&1 | grep -E "DEPTH|Error|passed|failed")
