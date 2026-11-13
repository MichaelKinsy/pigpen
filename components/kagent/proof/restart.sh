#!/usr/bin/env bash
# Worker pod replacement series. Per run: start a session (variant idle: remember a marker; variant approval: park an approval),
# delete the worker pod BY EXACT NAME, wait for the new pod to be Ready, then send the follow-up and record the FIRST attempt exactly
# (success, JSON-RPC error code, HTTP status, empty reply, timeout, wrong answer) and then retry the same message with backoff,
# recording every attempt and the time until success.
#   idle      `[recall] go` on the same context; success = the answer names the marker
#   approval  approve the parked approval; success = APPROVE-DONE, then `[recall] go` must still name the marker
#
#   AGENT=pig-basket APPROVE_AGENT=pig-approve N=10 ./restart.sh        # results/restart-<date>/{runs.jsonl,summary.json,summary.md}
#
# DELETES THE WORKER POD of pool $POOL N times per variant; run it only on a cluster you own and tell other users first.
# Env: AGENT, APPROVE_AGENT (a variant runs only when its agent is set), N=10, URL=http://localhost:8083, AGENT_NS=kagent, POOL=kagent-default,
#      USER_HEADER, OUT, BASKET, READY_TIMEOUT=180 (seconds), ATTEMPT_TIMEOUT=30s, RETRY_MAX=3m, BACKOFF=500ms, BACKOFF_MAX=5s.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
N=${N:-10}; URL=${URL:-http://localhost:8083}; AGENT_NS=${AGENT_NS:-kagent}; POOL=${POOL:-kagent-default}
USER_HEADER=${USER_HEADER:-x-user-id: admin@kagent.dev}
OUT=${OUT:-$HERE/results/restart-$(date -u +%Y%m%d)}
BASKET=${BASKET:-$HERE/basket/basket}
READY_TIMEOUT=${READY_TIMEOUT:-180}   # seconds
[ -n "${AGENT:-}${APPROVE_AGENT:-}" ] || { echo "set AGENT and/or APPROVE_AGENT" >&2; exit 2; }
mkdir -p "$OUT"; : >"$OUT/runs.jsonl"
bk() { "$BASKET" -url "$URL" -header "$USER_HEADER" "$@"; }
retry_flags=(-attempt-timeout "${ATTEMPT_TIMEOUT:-30s}" -retry-max "${RETRY_MAX:-3m}" -backoff "${BACKOFF:-500ms}" -backoff-max "${BACKOFF_MAX:-5s}")
pods() { kubectl -n "$AGENT_NS" get pods -l "ate.dev/worker-pool=$POOL" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'; }

# replace_pod: deletes the only worker pod by exact name, waits for a different Ready pod; sets OLD, NEW, READY_S
replace_pod() {
  mapfile -t cur < <(pods)
  [ "${#cur[@]}" = 1 ] || { echo "expected exactly one worker pod in pool $POOL, found ${#cur[@]}" >&2; return 1; }
  OLD=${cur[0]}
  local t0; t0=$(date +%s.%N)
  kubectl -n "$AGENT_NS" delete pod "$OLD" --wait=true >/dev/null
  local deadline=$((SECONDS + READY_TIMEOUT)) p
  while ((SECONDS < deadline)); do
    mapfile -t cur < <(pods)
    for p in "${cur[@]}"; do
      [ "$p" != "$OLD" ] || continue
      if kubectl -n "$AGENT_NS" wait --for=condition=Ready "pod/$p" --timeout=5s >/dev/null 2>&1; then
        NEW=$p; READY_S=$(echo "$(date +%s.%N) - $t0" | bc); return 0
      fi
    done
    sleep 1
  done
  echo "no new Ready worker pod within ${READY_TIMEOUT}s" >&2; return 1
}

run_variant() {  # $1 = idle|approval, $2 = agent
  local variant=$1 agent=$2 i marker st rs rc ok
  for ((i = 1; i <= N; i++)); do
    marker="[run-$RANDOM$RANDOM]"; st="$OUT/$variant-$i.state.json"; rs="$OUT/$variant-$i.series.json"; rc="$OUT/$variant-$i.recall.json"
    if [ "$variant" = idle ]; then
      bk -mode send -agent "$AGENT_NS/$agent" -text "$marker remember this" -state "$st" -timeout 120s >/dev/null
    else
      bk -mode send -agent "$AGENT_NS/$agent" -text "$marker [approve] park" -state "$st" -timeout 120s >/dev/null
    fi
    ctx=$(jq -r .context_id "$st")
    if ! replace_pod; then echo "{\"variant\":\"$variant\",\"run\":$i,\"marker\":\"$marker\",\"pod_error\":true,\"series\":{\"first_attempt\":{\"outcome\":\"error\"},\"first_attempt_success\":false,\"eventual_success\":false}}" >>"$OUT/runs.jsonl"; continue; fi
    ok=null
    if [ "$variant" = idle ]; then
      bk -mode recall -agent "$AGENT_NS/$agent" -context "$ctx" -text '[recall] go' -expect "$marker" "${retry_flags[@]}" -out "$rs" 2>>"$OUT/basket.err" || true
    else
      bk -mode resume -agent "$AGENT_NS/$agent" -state "$st" -expect APPROVE-DONE "${retry_flags[@]}" -out "$rs" 2>>"$OUT/basket.err" || true
      if bk -mode recall -agent "$AGENT_NS/$agent" -context "$ctx" -text '[recall] go' -expect "$marker" "${retry_flags[@]}" -out "$rc" 2>>"$OUT/basket.err"; then ok=true; else ok=false; fi
    fi
    if [ ! -s "$rs" ]; then
      echo "{\"variant\":\"$variant\",\"run\":$i,\"marker\":\"$marker\",\"old_pod\":\"$OLD\",\"new_pod\":\"${NEW:-}\",\"pod_ready_s\":${READY_S:-null},\"recall_ok\":$ok,\"series\":{\"first_attempt\":{\"outcome\":\"error\"},\"first_attempt_success\":false,\"eventual_success\":false,\"error\":\"basket wrote no result\"}}" >>"$OUT/runs.jsonl"
      continue
    fi
    jq -c --arg v "$variant" --argjson run "$i" --arg m "$marker" --arg old "$OLD" --arg new "${NEW:-}" --argjson ready "${READY_S:-null}" --argjson rok "$ok" \
      '{variant:$v, run:$run, marker:$m, old_pod:$old, new_pod:$new, pod_ready_s:$ready, recall_ok:$rok, series:.}' "$rs" >>"$OUT/runs.jsonl"
  done
}

[ -z "${AGENT:-}" ] || run_variant idle "$AGENT"
[ -z "${APPROVE_AGENT:-}" ] || run_variant approval "$APPROVE_AGENT"
python3 "$HERE/series.py" summary --kind restart --in "$OUT/runs.jsonl" --out-json "$OUT/summary.json" --out-md "$OUT/summary.md"
