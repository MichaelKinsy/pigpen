#!/usr/bin/env bash
# Run the perf basket for one harness and join client timings with node-side memory.
#
#   NAME=pig AGENT=kagent/pig-basket URL=http://localhost:8083 \
#   TARGETS='worker:ns=kagent,sel=ate.dev/worker-pool=kagent-default' \
#   ./perf.sh
#
# Run it ON THE NODE that hosts the pods (cgroup files are read directly). Repeat per harness, interleaved
# (pig, claude, codex, pi-go, pig, claude, ...) rather than in blocks, so slow drift on the shared host hits
# every harness alike. Defaults follow RESULTS.md: 3 warmup + 20 measured iterations.
set -euo pipefail
: "${NAME:?harness name}" "${AGENT:?namespace/name}" "${URL:?controller URL}" "${TARGETS:?collector --target specs, ';' separated}"
OUT=${OUT:-./results/$(date -u +%Y%m%dT%H%M%SZ)-$NAME}
REPEAT=${REPEAT:-20}; WARMUP=${WARMUP:-3}; CONCURRENCY=${CONCURRENCY:-1}
IDLE_WAIT=${IDLE_WAIT:-90s}   # long enough for idle settlement to suspend the actor, so turn 2 measures resume
HOLD=${HOLD:-30s}
HERE=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$OUT"

# gate on host noise: refuse to start when the host already has CPU pressure above MAX_PSI percent
MAX_PSI=${MAX_PSI:-30}
psi=$(awk -F'avg10=' 'NR==1{split($2,a," "); print a[1]}' /proc/pressure/cpu)
awk -v p="$psi" -v m="$MAX_PSI" 'BEGIN{exit !(p+0 > m+0)}' && { echo "host cpu PSI avg10=$psi > $MAX_PSI, not starting" >&2; exit 3; }

targets=()
IFS=';' read -ra specs <<<"$TARGETS"
for s in "${specs[@]}"; do targets+=(--target "$s"); done
SUDO=${SUDO:-}   # set SUDO=sudo with PSS=1
pssflag=(); [ "${PSS:-0}" = 1 ] && pssflag=(--pss)

START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
$SUDO python3 "$HERE/collect.py" run --out "$OUT/mem.jsonl" "${targets[@]}" "${pssflag[@]}" --interval 1 &
COL=$!
trap 'kill -INT $COL 2>/dev/null || true' EXIT
sleep 5   # idle baseline before the first request

"${BASKET:-$HERE/basket}" -url "$URL" -agent "$AGENT" -repeat "$REPEAT" -warmup "$WARMUP" -concurrency "$CONCURRENCY" \
  -idle-wait "$IDLE_WAIT" -hold "$HOLD" ${BASKET_ARGS:-} -out "$OUT/basket.json" || echo "basket reported failures" >&2

kill -INT $COL; wait $COL || true; trap - EXIT
if [ -n "${LOG_SOURCES:-}" ]; then
  srcs=(); IFS=';' read -ra ls <<<"$LOG_SOURCES"; for s in "${ls[@]}"; do srcs+=(--source "$s"); done
  python3 "$HERE/collect.py" logs --out "$OUT/substrate-logs.json" "${srcs[@]}" --since-time "$START" || true
fi
python3 "$HERE/collect.py" report --mem "$OUT/mem.jsonl" --basket "$OUT/basket.json" | tee "$OUT/report.md"
