#!/bin/sh
# Protocol differential: the oracle's AhpHost + ChangesetService under Node (its npm dependencies, but not
# Pi, installed in a scratch directory), then the Go host's transcript compared with it.
# Usage: run.sh <ahp component directory> <output file>
set -eux
comp=$1; out=$2
node -v
o=$(mktemp -d)
export HOME=$o/home; mkdir -p "$HOME"
cp -r "$comp/proof/oracle/src" "$o/src"
cd "$o"
npm init -y >/dev/null
npm pkg set type=module >/dev/null
npm install --no-save --ignore-scripts --no-audit --no-fund @microsoft/agent-host-protocol@0.9.0 chokidar@5 mime@4.1.1 2>&1 | tail -3
ws=$(mktemp -d)
node "$comp/proof/differential/oracle-run.mjs" "$o" "$ws" > "$o/transcript.json"
cd "$comp/extensions/ahp"
AHP_DIFF_EXPECT="$o/transcript.json" AHP_DIFF_WORKSPACE="$ws" AHP_DIFF_OUT="$out" go test ./internal/changeset -run TestDifferential -count=1 -v
