#!/bin/sh
# Interleaved measurement of changeset computation: Go port (go test -run TestPerfCompute) against the
# oracle under Node, 11 rounds each, median of the per-round medians. Usage:
#   run.sh <repository> <oracle src/pi directory> <ahp extension directory>
set -eu
repo=$1; oracle=$2; ext=$3
( cd "$ext" && go test -c -o /tmp/ahp-changeset.test ./internal/changeset )
for round in 1 2 3 4 5 6 7 8 9 10 11; do
  echo "go   $(AHP_PERF_REPO=$repo /tmp/ahp-changeset.test -test.run '^TestPerfCompute$' -test.v | grep '^PERF ' | cut -c6-)"
  echo "node $(node "$(dirname "$0")/compute.mjs" "$oracle" "$repo")"
done
