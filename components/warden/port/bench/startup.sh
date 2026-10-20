#!/bin/bash
# Interleaved registration-time medians: this tree against another tree (the previous release), inside the imladris
# wrapper. Usage: bash port/bench/startup.sh <other extensions/warden dir> [runs]
set -e
OTHER=$1
RUNS=${2:-10}
HERE="$(cd "$(dirname "$0")/../../extensions/warden" && pwd)"
cp "$HERE/startup_timing_test.go" "$OTHER/startup_timing_test.go"
trap 'rm -f "$OTHER/startup_timing_test.go"' EXIT
# The previous release needs its own go.work (the lane's resolves the warden module to this tree): pass it as the
# third argument.
declare -A GOWORKS
GOWORKS[$HERE]="$GOWORK"
GOWORKS[$OTHER]="${3:-$GOWORK}"
for i in $(seq 1 "$RUNS"); do
  for tree in "$HERE" "$OTHER"; do
    (cd "$tree" && GOWORK="${GOWORKS[$tree]}" WARDEN_STARTUP_TIMING=1 go test -count=1 -run '^TestStartupTiming$' -v . 2>&1 | awk -v tree="$tree" '/startup median/{print tree, $3, $6}')
  done
done | python3 -c "
import sys, statistics, collections
d = collections.defaultdict(list)
for line in sys.stdin:
    tree, med, best = line.split()
    d[tree].append((int(med), int(best)))
for tree, rows in d.items():
    print(tree, 'median of medians', statistics.median(r[0] for r in rows), 'us; best', min(r[1] for r in rows), 'us;', len(rows), 'runs')
"
