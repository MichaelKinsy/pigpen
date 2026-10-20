#!/bin/sh
# Records the libc character classes the original's scanner applies (see wctype.test.ts).
#   sh port/bashparse-diff/run-wctype.sh <installed copy of port/oracle> <out.json>
set -e
pkg="$1/packages/pi-permission-system"
here="$(pwd)/port/bashparse-diff"
cp "$here/wctype.test.ts" "$pkg/test/zz-bashparse-wctype.test.ts"
trap 'rm -f "$pkg/test/zz-bashparse-wctype.test.ts"' EXIT
(cd "$pkg" && WCTYPE_OUT="$2" node_modules/.bin/vitest run test/zz-bashparse-wctype.test.ts 2>&1 | grep -E "Error|passed|failed")
