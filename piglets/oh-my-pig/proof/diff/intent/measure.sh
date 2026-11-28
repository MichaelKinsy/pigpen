#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Michael Kinsy
# Measures internal/intent against the full corpus: fingerprints internal/intent, runs both
# stages (PrepareArguments only, then with PiG's host validator) and keeps the result only
# when the fingerprint did not change during the runs. Writes $DIFF_OUT/results/.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "$here/../../../../.." && pwd)"
ext_dir="${EXT_DIR:-$repo_root/components/oh-my-pig/extensions/oh-my-pig}"
T="$ext_dir/internal/intent"
DIFF_OUT="${DIFF_OUT:-/tmp/omp-intent-diff}"
corpus="${1:-$DIFF_OUT/corpus.jsonl}"
tries="${2:-1}"

if [ ! -f "$corpus" ]; then
  echo "error: corpus not found: $corpus (run $here/make-corpus.sh first)" >&2
  exit 2
fi

fp() {
  (cd "$T" && shasum -a 256 intent.go schema.go validate.go | cut -c1-16,65- && wc -c intent.go schema.go validate.go | head -3)
}

mkdir -p "$DIFF_OUT/results"
for n in $(seq 1 "$tries"); do
  before="$(fp)"
  set +e
  "$here/run-port.sh" "$corpus" "$DIFF_OUT/results/port-prepare-only.jsonl" > "$DIFF_OUT/results/port-prepare-only.txt" 2>&1
  rc1=$?
  "$here/run-port.sh" --host "$corpus" "$DIFF_OUT/results/port-with-host.jsonl" > "$DIFF_OUT/results/port-with-host.txt" 2>&1
  rc2=$?
  set -e
  after="$(fp)"
  if [ "$before" = "$after" ] && [ "$rc1" -eq 0 ] && [ "$rc2" -eq 0 ] \
    && grep -q '^ok ' "$DIFF_OUT/results/port-prepare-only.txt" \
    && grep -q '^ok ' "$DIFF_OUT/results/port-with-host.txt"; then
    {
      date "+measured %Y-%m-%d %H:%M:%S"
      echo "corpus $(shasum -a 256 "$corpus" | cut -c1-16) ($(wc -l < "$corpus" | tr -d ' ') cases)"
      echo "$after"
    } > "$DIFF_OUT/results/tree-fingerprint.txt"
    for f in port-prepare-only port-with-host; do
      echo "######## $f"
      grep -E "stage:|== |^ +[0-9]+  |not register|answered by" "$DIFF_OUT/results/$f.txt" | sed 's/^ *//'
    done > "$DIFF_OUT/results/summary.txt"
    echo "measured on try $n -> $DIFF_OUT/results/summary.txt"
    exit 0
  fi
  echo "try $n: not stable or not building ($(grep -m1 -E 'undefined|FAIL|error' "$DIFF_OUT/results/port-prepare-only.txt" "$DIFF_OUT/results/port-with-host.txt" 2>/dev/null | head -n1 | cut -c1-140))" >&2
  [ "$n" -lt "$tries" ] && sleep 40
done
exit 1
