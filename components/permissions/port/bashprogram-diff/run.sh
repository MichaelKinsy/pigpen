#!/bin/sh
# Regenerates the inputs of the BashProgram differential and records the original's answers.
# Run from components/permissions inside an installed copy of port/oracle (see port/oracle/UPSTREAM.md):
#   sh port/bashprogram-diff/run.sh <installed copy of port/oracle> <work dir>
# Writes <work>/in.json and <work>/oracle.jsonl; gzip the latter into
# extensions/pi-permission-system/testdata/bashprogram-oracle.jsonl.gz. The probe is copied into the oracle copy for the run and
# removed after.
# Corpus: every input of the bash analysis differential (extensions/pi-permission-system/bashanalysis/testdata/oracle.jsonl.gz:
# the strings of the original's bash and gate tests, port/probe/cases.json, generated wrapper, command, redirect, sed, awk and option
# shapes, error-recovery variants) plus gen-paths.mjs's path-bearing commands (redirects, cd chains, wrappers, heredocs, env
# assignments, globs, symlinks, HOME and PWD spellings).
set -e
oracle="$1"; work="$2"
pkg="$oracle/packages/pi-permission-system"
test -d "$pkg/node_modules" || { echo "usage: $0 <installed copy of port/oracle> <work dir>" >&2; exit 2; }
here="$(pwd)/port/bashprogram-diff"
analysis="$(pwd)/extensions/pi-permission-system/bashanalysis/testdata/oracle.jsonl.gz"
mkdir -p "$work"
node "$here/gen-paths.mjs" "$work/paths.json"
node -e '
const fs=require("fs"),zlib=require("zlib");
const lines=zlib.gunzipSync(fs.readFileSync(process.argv[1])).toString("utf8").split("\n").filter(Boolean);
const seen=new Set(),out=[];
for(const l of lines){const s=JSON.parse(l).s;if(!seen.has(s)){seen.add(s);out.push(s);}}
for(const s of JSON.parse(fs.readFileSync(process.argv[2]+"/paths.json"))){if(!seen.has(s)){seen.add(s);out.push(s);}}
fs.writeFileSync(process.argv[2]+"/in.json",JSON.stringify(out));console.log("inputs="+out.length);' "$analysis" "$work"
cp "$here/probe.test.ts" "$pkg/test/zz-bashprogram-probe.test.ts"
trap 'rm -f "$pkg/test/zz-bashprogram-probe.test.ts"' EXIT
(cd "$pkg" && DIFF_IN="$work/in.json" DIFF_OUT="$work/oracle.jsonl" node_modules/.bin/vitest run test/zz-bashprogram-probe.test.ts)
