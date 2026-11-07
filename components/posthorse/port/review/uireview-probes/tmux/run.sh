#!/bin/sh
# uireview: run tmux scenarios (Pi with the original's cards vs PiG with the port's) into the reviewer's scratch.
#   sh run.sh <scenario-dir> <fixtures.json> [SCENARIO...]   (inside the container; default: all scenarios of the dir)
set -u
T=/home/kinsy/ph-work/pigpen/components/posthorse/port/tmux
PKG=/home/kinsy/ph-work/pigpen/components/posthorse
OUT=/home/kinsy/ph-work/scratch/uireview/tmux
DIR=$1; FIX=$2; shift 2
mkdir -p "$OUT"
python3 "$T/build_probe.py" "$PKG/extensions/posthorse" "$OUT/probe/posthorse-probe" || exit 1
export POSTHORSE_TMUX_PI=$PIGEQ_PI POSTHORSE_TMUX_PIG=$(command -v pig) POSTHORSE_TMUX_FIXTURES=$FIX
export POSTHORSE_TMUX_ORIGINAL=$T/probe/probe.ts POSTHORSE_TMUX_PORT=$OUT/probe/posthorse-probe
SCN="$*"
if [ -z "$SCN" ]; then SCN=$(cd "$DIR" && ls | sed 's/\.json$//'); fi
for s in $SCN; do
  echo "== $s"
  python3 "$T/compare.py" "$DIR/$s.json" "$OUT/$s" both
done
