#!/bin/sh
# uireview: run real-mode scenarios (original index.ts under Pi, the whole port under PiG) and print the persisted tool
# `details` of every session file, in file byte order, so the member order Pi and PiG write can be compared.
#   sh run-real.sh SCENARIO...   (inside the container)
set -u
T=/home/kinsy/ph-work/pigpen/components/posthorse/port/tmux
PKG=/home/kinsy/ph-work/pigpen/components/posthorse
OUT=/home/kinsy/ph-work/scratch/uireview/tmux-real
mkdir -p "$OUT"
export POSTHORSE_TMUX_PI=$PIGEQ_PI POSTHORSE_TMUX_PIG=$(command -v pig) POSTHORSE_TMUX_FIXTURES=$T/fixtures.json
export POSTHORSE_TMUX_ORIGINAL=$PKG/port/oracle/index.ts POSTHORSE_TMUX_PORT=$PKG/extensions/posthorse
for s in "$@"; do
  echo "== $s"
  python3 "$T/compare.py" "$T/scenarios-real/$s.json" "$OUT/$s" both
  for h in pi pig; do
    echo "-- $h session details"
    find "$OUT/$s/$h/agent" -name '*.jsonl' | sort | while read f; do
      grep -o '"details":{[^}]*}[^,]*' "$f" | grep -v '"details":{}' | sed 's/^/   /'
    done
  done
done
