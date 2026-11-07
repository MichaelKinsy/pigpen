#!/bin/sh
# Runs card-probe scenarios under Pi and PiG and compares the screens (inside the xd6702 container):
#   ~/work/ph-tools/ph run "sh components/posthorse/port/tmux/tmux.sh [SCENARIO|all] [pi|pig|both] [probe|real]"
# Output: /home/kinsy/ph-work/scratch/UiLane/tmux/<scenario>/{pi,pig}/*.txt|.ansi, summary on stdout.
# `probe` (default) loads probe/probe.ts under Pi and the Go probe built from the port's ui*.go under PiG;
# `real` loads the original index.ts under Pi and the whole port under PiG (the scenarios then need real tool results).
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
PKG=$(cd "$HERE/../.." && pwd)
SCN=${1:-all}
WHICH=${2:-both}
MODE=${3:-probe}
OUT=/home/kinsy/ph-work/scratch/UiLane/tmux
mkdir -p "$OUT"
export POSTHORSE_TMUX_PI=$PIGEQ_PI POSTHORSE_TMUX_PIG=$(command -v pig) POSTHORSE_TMUX_FIXTURES=$HERE/fixtures.json
if [ "$MODE" = probe ]; then
  python3 "$HERE/build_probe.py" "$PKG/extensions/posthorse" "$OUT/probe/posthorse-probe"
  export POSTHORSE_TMUX_ORIGINAL=$HERE/probe/probe.ts POSTHORSE_TMUX_PORT=$OUT/probe/posthorse-probe
else
  export POSTHORSE_TMUX_ORIGINAL=$PKG/port/oracle/index.ts POSTHORSE_TMUX_PORT=$PKG/extensions/posthorse
fi
DIR=scenarios; [ "$MODE" = real ] && DIR=scenarios-real
if [ "$SCN" = all ]; then SCN=$(cd "$HERE/$DIR" && ls | sed 's/\.json$//'); fi
status=0
for s in $SCN; do
  echo "== $s"
  python3 "$HERE/compare.py" "$HERE/$DIR/$s.json" "$OUT/$s" "$WHICH" || status=1
done
exit $status
