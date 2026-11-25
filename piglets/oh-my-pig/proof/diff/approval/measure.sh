#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Michael Kinsy
# Usage: measure.sh [corpus.jsonl] [tries]
# Measures internal/approval + internal/shell/approval.go against the full corpus:
# fingerprints the approval files, runs run-port.sh, and keeps the result only when the
# fingerprint did not change during the run. Writes $DIFF_OUT/results/.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "$here/../../../../.." && pwd)"
ext_dir="${EXT_DIR:-$repo_root/components/oh-my-pig/extensions/oh-my-pig}"
DIFF_OUT="${DIFF_OUT:-/tmp/omp-approval-diff}"
corpus="${1:-$DIFF_OUT/corpus.jsonl}"
tries="${2:-1}"

if [ ! -f "$corpus" ]; then
  echo "error: corpus not found: $corpus (run $here/make-corpus.sh first)" >&2
  exit 2
fi

fp() {
  (cd "$ext_dir" && shasum -a 256 internal/approval/*.go internal/shell/approval.go | cut -c1-16,65- && wc -c internal/approval/*.go internal/shell/approval.go | sed '$d')
}

also() {
  (cd "$ext_dir" && shasum -a 256 internal/files/selector.go internal/shell/bash.go internal/shell/interceptor.go wiring.go extension.go fakehost_test.go 2>/dev/null | cut -c1-16,65-)
}

tmp="$DIFF_OUT/results/.run"
mkdir -p "$tmp"
for n in $(seq 1 "$tries"); do
  before="$(fp)"
  rm -f "$tmp"/*
  set +e
  OMP_APPROVAL_DIFF_REPORT="$tmp/report.txt" "$here/run-port.sh" "$corpus" "$tmp/port.jsonl" > "$tmp/run.txt" 2>&1
  rc=$?
  set -e
  after="$(fp)"
  if [ "$before" = "$after" ] && [ "$rc" -eq 0 ] && grep -q '^ok ' "$tmp/run.txt" && [ -s "$tmp/report.txt" ]; then
    {
      date "+measured %Y-%m-%d %H:%M:%S"
      echo "corpus $(shasum -a 256 "$corpus" | cut -c1-16) ($(wc -l < "$corpus" | tr -d ' ') cases)"
      [ -n "${OMP_APPROVAL_DIFF_HIDE:-}" ] && echo "left out of the build: $OMP_APPROVAL_DIFF_HIDE"
      echo "$after"
      echo "-- also:"
      also
    } > "$DIFF_OUT/results/tree-fingerprint.txt"
    mv "$tmp/report.txt" "$tmp/port.jsonl" "$tmp/run.txt" "$DIFF_OUT/results/"
    [ -f "$tmp/report.txt.route" ] && mv "$tmp/report.txt.route" "$DIFF_OUT/results/"
    {
      grep -E '^approval differential|^NOTE|^== |^ +[0-9]+  |^cases not equal' "$DIFF_OUT/results/report.txt"
      echo "== unwrap route"
      grep -E 'VIOLATION|^unwrap route' "$DIFF_OUT/results/report.txt.route" 2>/dev/null || true
    } > "$DIFF_OUT/results/summary.txt"
    rmdir "$tmp" 2>/dev/null || true
    echo "measured on try $n -> $DIFF_OUT/results/summary.txt"
    exit 0
  fi
  echo "try $n: not stable or not building ($(grep -m1 -E 'undefined|FAIL|error|panic' "$tmp/run.txt" 2>/dev/null | cut -c1-140))" >&2
  [ "$n" -lt "$tries" ] && sleep 40
done
exit 1
