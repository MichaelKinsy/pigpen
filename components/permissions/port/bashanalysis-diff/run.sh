#!/bin/sh
# Regenerates the inputs of the bash analysis differential and records the original's answers.
# Run from components/permissions inside an installed copy of port/oracle (see port/oracle/UPSTREAM.md):
#   sh port/bashanalysis-diff/run.sh <installed copy of port/oracle> <work dir>
# Writes <work>/in.json and <work>/oracle.jsonl; gzip the latter into
# extensions/pi-permission-system/bashanalysis/testdata/oracle.jsonl.gz and the former's size into the test's
# expectations. The probe is copied into the oracle copy for the run and removed after.
# Corpus: every string of the original's tests under test/access-intent/bash and test/handlers plus
# port/probe/cases.json's commands (extract-corpus.mjs of the parse differential), the generated inputs of
# gen-extra.mjs, and a deterministic sample of error-recovery variants (every 7th prefix, every 11th deletion).
set -e
oracle="$1"; work="$2"
pkg="$oracle/packages/pi-permission-system"
test -d "$pkg/node_modules" || { echo "usage: $0 <installed copy of port/oracle> <work dir>" >&2; exit 2; }
here="$(pwd)/port/bashanalysis-diff"
parse="$(pwd)/port/bashparse-diff"
mkdir -p "$work"
node "$parse/extract-corpus.mjs" "$pkg" port/probe/cases.json "$work/corpus.json"
node "$parse/gen-variants.mjs" "$work/corpus.json" "$work/variants.json" 200 120
node "$here/gen-extra.mjs" "$work/extra.json"
node -e '
const fs=require("fs");const w=process.argv[1];
const c=JSON.parse(fs.readFileSync(w+"/corpus.json"));const v=JSON.parse(fs.readFileSync(w+"/variants.json"));
const e=JSON.parse(fs.readFileSync(w+"/extra.json"));
const seen=new Set(c.a);const out=[...c.a];
for(const s of e){if(!seen.has(s)){seen.add(s);out.push(s);}}
v.prefix.forEach((s,i)=>{if(i%7===0&&!seen.has(s)){seen.add(s);out.push(s);}});
v.del.forEach((s,i)=>{if(i%11===0&&!seen.has(s)){seen.add(s);out.push(s);}});
fs.writeFileSync(w+"/in.json",JSON.stringify(out));console.log("inputs="+out.length);' "$work"
cp "$here/probe.test.ts" "$pkg/test/zz-bashanalysis-probe.test.ts"
trap 'rm -f "$pkg/test/zz-bashanalysis-probe.test.ts"' EXIT
(cd "$pkg" && DIFF_IN="$work/in.json" DIFF_OUT="$work/oracle.jsonl" node_modules/.bin/vitest run test/zz-bashanalysis-probe.test.ts)
