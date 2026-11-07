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
# Output: $OUT/{runs/,mem.jsonl (the raw 0.25 s samples),logs-*.txt,gate.log,pressure.tsv,schedule.tsv}; analyze.py makes the tables.
#
# Host-pressure gate. Before every invocation the script waits until the host CPU pressure (PSI some avg10) is at most MAX_PSI,
# for at most GATE_WAIT seconds. What happens then depends on GATE_MODE:
#   proceed (default)  run the session anyway; it is recorded as proceeded-despite-pressure in gate.log and pressure.tsv, and
#                      analyze.py lists every such session in its report
#   refuse             stop the whole schedule with exit status 3 (no partial session is started)
# pressure.tsv has one line per session: tag, agent, psi, seconds waited, over (1 = proceeded above MAX_PSI).
#
# Where it runs (all overridable, the defaults are the proof cluster):
#   SUBSTRATE_NS  namespace of the Substrate API server and atelet            (ate-system)
#   AGENT_NS      namespace of the agents, the worker pool and the mock        (kagent)
#   POOL          worker pool whose pod hosts the Actors (label ate.dev/worker-pool)   (kagent-default)
#   MOCK_SEL      label selector of the mock model server pod                  (app.kubernetes.io/name=kagent-mock)
#   CONTROLLER_SEL  label selector of the kagent controller pod                (app.kubernetes.io/component=controller)
#   AGENT_GROUP   A2A namespace segment, as in /agents/<AGENT_GROUP>/<agent>   (= AGENT_NS)
#   USER_HEADER   header sent with every request                               (x-user-id: admin@kagent.dev)
set -euo pipefail
SUBSTRATE_NS=${SUBSTRATE_NS:-ate-system}   # kagent install puts Substrate here; check with: kubectl get pods -A | grep atelet
AGENT_NS=${AGENT_NS:-kagent}
POOL=${POOL:-kagent-default}
MOCK_SEL=${MOCK_SEL:-app.kubernetes.io/name=kagent-mock}
CONTROLLER_SEL=${CONTROLLER_SEL:-app.kubernetes.io/component=controller}
AGENT_GROUP=${AGENT_GROUP:-$AGENT_NS}
USER_HEADER=${USER_HEADER:-x-user-id: admin@kagent.dev}
GATE_MODE=${GATE_MODE:-proceed}
case "$GATE_MODE" in proceed|refuse) ;; *) echo "GATE_MODE must be proceed or refuse" >&2; exit 2;; esac
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
: >"$OUT/gate.log"; : >"$OUT/schedule.tsv"; : >"$OUT/pressure.tsv"

pids=()
cleanup() { for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT

python3 "$HERE/collect.py" run --out "$OUT/mem.jsonl" --interval "$INTERVAL" \
  --target "worker:ns=$AGENT_NS,sel=ate.dev/worker-pool=$POOL" \
  --target "controller:ns=$AGENT_NS,sel=$CONTROLLER_SEL" \
  --target "mock:ns=$AGENT_NS,sel=$MOCK_SEL" &
pids+=($!)
kubectl -n "$SUBSTRATE_NS" logs -f -l app=ate-api-server --prefix=false --max-log-requests=10 --since-time="$START" >"$OUT/logs-ateapi.txt" 2>/dev/null &
pids+=($!)
kubectl -n "$SUBSTRATE_NS" logs -f -l app=atelet --all-containers --prefix=false --max-log-requests=10 --since-time="$START" >"$OUT/logs-atelet.txt" 2>/dev/null &
pids+=($!)
sleep "${SETTLE_S:-6}"   # idle baseline (SETTLE_S and SETTLE_END_S exist for the offline test)

psi() { awk -F'avg10=' 'NR==1{split($2,a," "); print a[1]}' "${PSI_FILE:-/proc/pressure/cpu}"; }
gate() {  # $1 = "<tag> <agent>"
  local waited=0 p
  while :; do
    p=$(psi)
    if awk -v p="$p" -v m="$MAX_PSI" 'BEGIN{exit !(p+0 <= m+0)}'; then
      echo "$(date -u +%T) psi=$p ok waited=${waited}s $1" >>"$OUT/gate.log"
      printf '%s\t%s\t%s\t%s\t0\n' "${1% *}" "${1#* }" "$p" "$waited" >>"$OUT/pressure.tsv"
      return 0
    fi
    if [ "$waited" -ge "$GATE_WAIT" ]; then
      printf '%s\t%s\t%s\t%s\t1\n' "${1% *}" "${1#* }" "$p" "$waited" >>"$OUT/pressure.tsv"
      if [ "$GATE_MODE" = refuse ]; then
        echo "$(date -u +%T) psi=$p OVER GATE after ${waited}s, refusing $1" | tee -a "$OUT/gate.log" >&2
        exit 3
      fi
      echo "$(date -u +%T) psi=$p OVER GATE after ${waited}s, PROCEEDED DESPITE PRESSURE $1" >>"$OUT/gate.log"
      return 0
    fi
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
    "${BASKET:-$HERE/basket/basket}" -url "$URL" -agent "$AGENT_GROUP/$agent" -scenario "$scen" -header "$USER_HEADER" \
      -repeat 1 -warmup 0 -idle-wait "$IDLE_WAIT" -timeout 180s -out "$OUT/runs/$tag$idx-$agent.json" 2>>"$OUT/basket.err" \
      || echo "basket failed: $tag$idx $agent" >>"$OUT/basket.err"
    printf '%s\t%s\t%s\t%s\t%s\n' "$tag$idx" "$agent" "$scen" "$t0" "$(date +%s.%N)" >>"$OUT/schedule.tsv"
  done
done

sleep "${SETTLE_END_S:-3}"
cleanup; sleep 1
python3 "$HERE/collect.py" logs --out "$OUT/logs.json" --file "$OUT/logs-ateapi.txt" --file "$OUT/logs-atelet.txt"
echo "done $OUT"
