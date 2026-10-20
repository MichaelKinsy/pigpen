#!/bin/sh
# Records the original's node dumps for an arbitrary list of inputs.
#   [DIFF_MODE=full|hash] sh port/bashparse-diff/run-dump.sh <installed copy of port/oracle> <in.json (array of strings)> <out>
# full (default): one JSON line per input; compare with BP_DUMP=<out> go test -run TestDiffDump -v ./bashparse
# hash: one FNV hash per line; compare with BP_FUZZ_IN=<in.json> BP_FUZZ_HASHES=<out> go test -run TestCompareFuzzMore ./bashparse
# Several can run at once: each copies the dump test under its own name.
set -e
pkg="$1/packages/pi-permission-system"
here="$(pwd)/port/bashparse-diff"
name="zz-bashparse-dump-$$.test.ts"
cp "$here/dump.test.ts" "$pkg/test/$name"
trap 'rm -f "$pkg/test/$name"' EXIT
(cd "$pkg" && DIFF_MODE="${DIFF_MODE:-full}" DIFF_IN="$2" DIFF_OUT="$3" node_modules/.bin/vitest run "test/$name" 2>&1 | grep -E "Error|passed|failed")
