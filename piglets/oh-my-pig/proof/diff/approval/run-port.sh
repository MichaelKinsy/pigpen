#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Michael Kinsy
# Usage: run-port.sh [corpus.jsonl] [results.jsonl]
# Runs go/zz_approval_ompdiff_test.go over the corpus on the joined extension WITHOUT writing
# in the tree: the test is overlaid onto the module root (`go test -overlay`) with its own
# identifiers, using a temporary go.mod pointing the SDK replace to $PIG_SRC/extensions/sdk.
# OMP_APPROVAL_DIFF_HIDE=internal/approval/x.go,... leaves files out of the build.
# Report: $OMP_APPROVAL_DIFF_REPORT (default $DIFF_OUT/results/report.txt), route: <report>.route
set -euo pipefail
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE PIG_SOURCE_ROOT

here="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "$here/../../../../.." && pwd)"
ext_dir="${EXT_DIR:-$repo_root/components/oh-my-pig/extensions/oh-my-pig}"
PIG_SRC="${PIG_SRC:-$HOME/work/pig-080-link13}"
DIFF_OUT="${DIFF_OUT:-/tmp/omp-approval-diff}"

abspath() {
  case "$1" in
    /*) printf '%s' "$1" ;;
    "") ;;
    *)  printf '%s/%s' "$PWD" "$1" ;;
  esac
}

corpus="$(abspath "${1:-$DIFF_OUT/corpus.jsonl}")"
results="$(abspath "${2:-}")"
report="$(abspath "${OMP_APPROVAL_DIFF_REPORT:-$DIFF_OUT/results/report.txt}")"

if [ ! -f "$corpus" ]; then
  echo "error: corpus not found: $corpus (run $here/make-corpus.sh first)" >&2
  exit 2
fi
if [ ! -d "$ext_dir" ]; then
  echo "error: extension module not found: $ext_dir" >&2
  exit 2
fi
if [ ! -d "$PIG_SRC/extensions/sdk" ]; then
  echo "error: PIG_SRC=$PIG_SRC does not contain extensions/sdk (set PIG_SRC to your PiG checkout)" >&2
  exit 2
fi

mkdir -p "$(dirname "$report")"
[ -n "$results" ] && mkdir -p "$(dirname "$results")"

MODDIR="$(mktemp -d)"
trap 'rm -rf "$MODDIR"' EXIT

cd "$ext_dir"
mod="$MODDIR/go.mod"
cp go.mod "$mod"
[ -f go.sum ] && cp go.sum "${mod%.mod}.sum"
printf '\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "$PIG_SRC/extensions/sdk" >> "$mod"

sed \
  -e 's/ompApprDiff/zzScratchApprDiff/g' \
  -e 's/OmpApprDiff/ZzScratchApprDiff/g' \
  -e 's/TestZZOmpApproval/TestZZScratchOmpApproval/g' \
  "$here/go/zz_approval_ompdiff_test.go" > "$MODDIR/t.go"

fake=""
if grep -q 'result = map\[string\]any{"value": ans}' "$ext_dir/fakehost_test.go"; then
  sed -e 's|result = map\[string\]any{"value": ans}|result = map[string]any{"ok": true, "selected": ans}|' "$ext_dir/fakehost_test.go" > "$MODDIR/fakehost.go"
  fake=",\"$ext_dir/fakehost_test.go\":\"$MODDIR/fakehost.go\""
fi

hide=""
for f in $(printf '%s' "${OMP_APPROVAL_DIFF_HIDE:-}" | tr ',' ' '); do
  [ -n "$f" ] && hide="$hide,\"$ext_dir/$f\":\"\""
done

printf '{"Replace":{"%s/zz_scratch_approval_ompdiff_test.go":"%s/t.go"%s%s}}\n' "$ext_dir" "$MODDIR" "$fake" "$hide" > "$MODDIR/overlay.json"

set +e
GOMAXPROCS="${OMP_DIFF_GOMAXPROCS:-4}" \
OMP_APPROVAL_DIFF_CORPUS="$corpus" \
OMP_APPROVAL_DIFF_OUT="$results" \
OMP_APPROVAL_DIFF_REPORT="$report" \
OMP_APPROVAL_DIFF_EXAMPLES="${OMP_APPROVAL_DIFF_EXAMPLES:-3}" \
OMP_APPROVAL_DIFF_SECTIONS="${OMP_APPROVAL_DIFF_SECTIONS:-}" \
OMP_APPROVAL_DIFF_FAIL="${OMP_APPROVAL_DIFF_FAIL:-}" \
GOWORK=off GOFLAGS=-mod=mod \
  nice -n 10 go test -modfile="$mod" -overlay="$MODDIR/overlay.json" -count=1 -timeout 30m \
  -run 'TestZZScratchOmpApproval(Differential|UnwrapRoute)$' . 2>&1 | grep -v '^=== \|^    --- \|^--- PASS'
rc=${PIPESTATUS[0]}
set -e
exit "$rc"
