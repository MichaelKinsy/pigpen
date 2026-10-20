#!/bin/sh
# Regenerates the inputs of the bash parse differential and records the original's parse trees.
# Run from components/permissions inside an installed copy of port/oracle (see port/oracle/UPSTREAM.md):
#   sh port/bashparse-diff/run.sh <installed copy of port/oracle> <work dir>
# Writes <work>/corpus.json, shapes.json, variants.json and the original's dumps: full-a.jsonl (full node
# dumps of corpus a), hash-<kind>.txt for the others (b, shapes, prefix, del, fuzz). DIFF_FULL=1 records full
# dumps for b and shapes too (investigation; shapes is ~80 MB). The fuzz inputs come from the Go test
# (BP_GEN_FUZZ=<work>/in-fuzz.json go test -run TestGenFuzz ./bashparse) and are recorded when that file exists.
# ONLY=<kind> records just that kind. Then: python3 port/bashparse-diff/pack-testdata.py <work> <testdata>.
# The dump test is copied into the oracle copy for the run and removed after.
set -e
oracle="$1"; work="$2"
pkg="$oracle/packages/pi-permission-system"
test -d "$pkg/node_modules" || { echo "usage: $0 <installed copy of port/oracle> <work dir>" >&2; exit 2; }
here="$(pwd)/port/bashparse-diff"
mkdir -p "$work"
node "$here/extract-corpus.mjs" "$pkg" port/probe/cases.json "$work/corpus.json"
node "$here/gen-shapes.mjs" "$work/shapes.json"
node "$here/gen-variants.mjs" "$work/corpus.json" "$work/variants.json"
node -e '
const fs=require("fs");const w=process.argv[1];
const c=JSON.parse(fs.readFileSync(w+"/corpus.json"));const v=JSON.parse(fs.readFileSync(w+"/variants.json"));
fs.writeFileSync(w+"/in-a.json",JSON.stringify(c.a));fs.writeFileSync(w+"/in-b.json",JSON.stringify(c.b));
fs.writeFileSync(w+"/in-prefix.json",JSON.stringify(v.prefix));fs.writeFileSync(w+"/in-del.json",JSON.stringify(v.del));
fs.writeFileSync(w+"/in-shapes.json",fs.readFileSync(w+"/shapes.json"));' "$work"
cp "$here/dump.test.ts" "$pkg/test/zz-bashparse-dump.test.ts"
trap 'rm -f "$pkg/test/zz-bashparse-dump.test.ts"' EXIT
want() { test -z "$ONLY" || test "$ONLY" = "$1"; }
if want a; then
  (cd "$pkg" && DIFF_MODE=full DIFF_IN="$work/in-a.json" DIFF_OUT="$work/full-a.jsonl" node_modules/.bin/vitest run test/zz-bashparse-dump.test.ts)
fi
for k in b shapes; do
  want $k || continue
  if test -n "$DIFF_FULL"; then
    (cd "$pkg" && DIFF_MODE=full DIFF_IN="$work/in-$k.json" DIFF_OUT="$work/full-$k.jsonl" node_modules/.bin/vitest run test/zz-bashparse-dump.test.ts)
  else
    (cd "$pkg" && DIFF_MODE=hash DIFF_IN="$work/in-$k.json" DIFF_OUT="$work/hash-$k.txt" node_modules/.bin/vitest run test/zz-bashparse-dump.test.ts)
  fi
done
for k in prefix del fuzz; do
  want $k || continue
  test -f "$work/in-$k.json" || continue
  (cd "$pkg" && DIFF_MODE=hash DIFF_IN="$work/in-$k.json" DIFF_OUT="$work/hash-$k.txt" node_modules/.bin/vitest run test/zz-bashparse-dump.test.ts)
done
