#!/usr/bin/env bash
# Interleaved baseline schedule against kagent agents through the A2A gateway, with node-side memory and Substrate logs.
#
#   AGENTS='claude-basket:basket codex-basket:basket pigo-basket:basket pig-basket:basket claude-approve:approve codex-approve:approve pig-approve:approve' \
#   ROUNDS=10 WARM=2 ./baseline.sh
#
# Runs ON the Kubernetes node that hosts the worker pod (the cgroup files are read directly), with kubectl pointed at the cluster
# (KUBECONFIG) and the gateway reachable at $URL (kubectl -n kagent port-forward svc/kagent-controller 8083:8083). One basket session per
# invocation (fresh actor), agents rotated each round so no harness always goes first, a host CPU-pressure gate before every invocation, the
# cgroup sampler running the whole time, and the Substrate ateapi/atelet logs streamed to files (container logs rotate).
# Output: $OUT/{runs/,mem.jsonl,logs-*.txt,gate.log,schedule.tsv}; analyze.py makes the tables. Substrate namespace: $SUBSTRATE_NS.
set -euo pipefail
SUBSTRATE_NS=${SUBSTRATE_NS:-ate-system}   # kagent install puts Substrate here; check with: kubectl get pods -A | grep atelet
HERE=$(cd "$(dirname "$0")" && pwd)
: "${AGENTS:?AGENTS=name:scenario ...}"
ROUNDS=${ROUNDS:-10}; WARM=${WARM:-2}; IDLE_WAIT=${IDLE_WAIT:-4s}; MAX_PSI=${MAX_PSI:-35}; GATE_WAIT=${GATE_WAIT:-180}
URL=${URL:-http://localhost:8083}; INTERVAL=${INTERVAL:-0.25}
OUT=${OUT:-$HERE/results/baseline-$(date -u +%Y%m%d)}
mkdir -p "$OUT/runs"
read -ra AG <<<"$AGENTS"
N=${#AG[@]}
START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
echo "$START" >"$OUT/start.txt"
: >"$OUT/gate.log"; : >"$OUT/schedule.tsv"

pids=()
cleanup() { for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT

python3 "$HERE/collect.py" run --out "$OUT/mem.jsonl" --interval "$INTERVAL" \
  --target 'worker:ns=kagent,sel=ate.dev/worker-pool=kagent-default' \
  --target 'controller:ns=kagent,sel=app.kubernetes.io/component=controller' \
  --target 'mock:ns=kagent,sel=app.kubernetes.io/name=kagent-mock' &
pids+=($!)
kubectl -n "$SUBSTRATE_NS" logs -f -l app=ate-api-server --prefix=false --max-log-requests=10 --since-time="$START" >"$OUT/logs-ateapi.txt" 2>/dev/null &
pids+=($!)
kubectl -n "$SUBSTRATE_NS" logs -f -l app=atelet --all-containers --prefix=false --max-log-requests=10 --since-time="$START" >"$OUT/logs-atelet.txt" 2>/dev/null &
pids+=($!)
sleep 6   # idle baseline

psi() { awk -F'avg10=' 'NR==1{split($2,a," "); print a[1]}' /proc/pressure/cpu; }
gate() {
  local waited=0 p
  while :; do
    p=$(psi)
    if awk -v p="$p" -v m="$MAX_PSI" 'BEGIN{exit !(p+0 <= m+0)}'; then echo "$(date -u +%T) psi=$p ok waited=${waited}s $1" >>"$OUT/gate.log"; return 0; fi
    [ "$waited" -ge "$GATE_WAIT" ] && { echo "$(date -u +%T) psi=$p OVER GATE after ${waited}s, running anyway $1" >>"$OUT/gate.log"; return 0; }
    sleep 10; waited=$((waited + 10))
  done
}

for ((round = 1; round <= WARM + ROUNDS; round++)); do
  tag=r; idx=$((round - WARM)); [ "$round" -le "$WARM" ] && { tag=w; idx=$round; }
  for ((k = 0; k < N; k++)); do
    spec=${AG[$(((k + round) % N))]}
    agent=${spec%%:*}; scen=${spec##*:}
    gate "$tag$idx $agent"
    t0=$(date +%s.%N)
    "${BASKET:-$HERE/basket}" -url "$URL" -agent "kagent/$agent" -scenario "$scen" -header 'x-user-id: admin@kagent.dev' \
      -repeat 1 -warmup 0 -idle-wait "$IDLE_WAIT" -timeout 180s -out "$OUT/runs/$tag$idx-$agent.json" 2>>"$OUT/basket.err" \
      || echo "basket failed: $tag$idx $agent" >>"$OUT/basket.err"
    printf '%s\t%s\t%s\t%s\t%s\n' "$tag$idx" "$agent" "$scen" "$t0" "$(date +%s.%N)" >>"$OUT/schedule.tsv"
  done
done

sleep 3
cleanup; sleep 1
python3 "$HERE/collect.py" logs --out "$OUT/logs.json" --file "$OUT/logs-ateapi.txt" --file "$OUT/logs-atelet.txt"
echo "done $OUT"
