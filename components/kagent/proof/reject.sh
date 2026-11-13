#!/usr/bin/env bash
# Rejection series: N sessions (default 10) of [approve] on an approval agent, each REJECTED with a reason. Per run it checks that
# the session still reaches its final answer, that the reason text arrived at the model (it is visible in the mock's request
# log as part of the tool result the mock was asked to answer), and that the answer's sha is the sha of exactly that logged text,
# which is the rejected path's sha (the approved path answers over the probe's output instead).
#
#   AGENT=pig-approve N=10 ./reject.sh                 # results/reject-<date>/{runs.jsonl,summary.json,summary.md}
#
# Needs kubectl (for the mock's log), the gateway at $URL, the built basket binary, python3. No cgroup access needed.
# Env: AGENT (required), N=10, URL=http://localhost:8083, AGENT_NS=kagent (also the A2A namespace segment), MOCK_DEPLOY=deploy/kagent-mock,
#      REASON='rejected by basket', EXPECT_SHA (optional: the sha the rejected path must give), USER_HEADER, OUT, BASKET, TIMEOUT=180s.
# runs.jsonl: one record per run (result, first attempt outcome: success = no gateway retry, timing). The summary lists failures.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
: "${AGENT:?AGENT=<approval agent name>}"
N=${N:-10}; URL=${URL:-http://localhost:8083}; AGENT_NS=${AGENT_NS:-kagent}
MOCK_DEPLOY=${MOCK_DEPLOY:-deploy/kagent-mock}
REASON=${REASON:-rejected by basket}
USER_HEADER=${USER_HEADER:-x-user-id: admin@kagent.dev}
OUT=${OUT:-$HERE/results/reject-$(date -u +%Y%m%d)}
BASKET=${BASKET:-$HERE/basket/basket}
mkdir -p "$OUT"; : >"$OUT/runs.jsonl"
for ((i = 1; i <= N; i++)); do
  t0=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  "$BASKET" -url "$URL" -agent "$AGENT_NS/$AGENT" -scenario approve -approve reject -reject-reason "$REASON" -header "$USER_HEADER" \
    -repeat 1 -warmup 0 -turn2=false -timeout "${TIMEOUT:-180s}" -out "$OUT/basket-$i.json" 2>>"$OUT/basket.err" || echo "basket failed: run $i" >>"$OUT/basket.err"
  sleep 1   # let the mock flush its log line
  kubectl -n "$AGENT_NS" logs "$MOCK_DEPLOY" --since-time="$t0" >"$OUT/mock-$i.log" 2>>"$OUT/basket.err" || : >"$OUT/mock-$i.log"
  args=(--basket "$OUT/basket-$i.json" --mock-log "$OUT/mock-$i.log" --reason "$REASON" --run "$i")
  [ -n "${EXPECT_SHA:-}" ] && args+=(--expect-sha "$EXPECT_SHA")
  python3 "$HERE/series.py" reject-check "${args[@]}" >>"$OUT/runs.jsonl"
done
python3 "$HERE/series.py" summary --kind reject --in "$OUT/runs.jsonl" --out-json "$OUT/summary.json" --out-md "$OUT/summary.md"
[ "$(python3 -c "import json,sys;s=json.load(open(sys.argv[1]));print(s['pass']==s['runs'])" "$OUT/summary.json")" = True ]
