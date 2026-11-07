#!/usr/bin/env bash
# demo.sh: a 60 to 120 second terminal walk-through of PiG running as a kagent harness on a Kind cluster (hermetic mock model).
# Needs kubectl pointed at the cluster, the gateway forwarded to $GW, the built `basket` binary, and a measurement file
# (results/baseline-*/analysis.json from analyze.py) for the last screen. Meant to be recorded in a real pty (see record.sh).
#
# Agent names are variables so the same script rehearses against the baseline agents:
#   A_PIG=claude-basket A_PIG_APPROVE=claude-approve ./demo.sh
# SPEED=0.5 halves every pause (quick check), SPEED=1.4 (default) gives an 80 s clip.
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
: "${KUBECONFIG:=$HOME/.kube/config}"; export KUBECONFIG
NS=${NS:-kagent}
GW=${GW:-http://localhost:8083}
BASKET=${BASKET:-$HERE/basket}
A_PIG=${A_PIG:-pig-basket}
A_PIG_APPROVE=${A_PIG_APPROVE:-pig-approve}
SPEED=${SPEED:-1.4}
MEMJSON=${MEMJSON:-$(ls -td "$HERE"/results/baseline-*/analysis.json 2>/dev/null | head -1)}
STATE=$(mktemp "${TMPDIR:-/tmp}/demo-state.XXXXXX")
trap 'rm -f "$STATE"' EXIT

cols=$(stty size 2>/dev/null | awk '{print $2}'); cols=${cols:-110}
B=$'\033[1m'; R=$'\033[0m'; G=$'\033[32m'; Y=$'\033[33m'; D=$'\033[2m'; W=$'\033[97m'; BG=$'\033[44m'

p() { sleep "$(awk -v s="$1" -v k="$SPEED" 'BEGIN{printf "%.2f", s*k}')"; }
banner() {
  local t=" $1"
  printf '\n%s%s%s%-*s%s\n' "$BG" "$B" "$W" "$cols" "$t" "$R"
}
say() { printf '%s%s%s\n' "$W" "$1" "$R"; }
type_out() {
  local s=$1 i
  printf '%s$ %s' "$G" "$R"
  for ((i = 0; i < ${#s}; i++)); do printf '%s' "${s:i:1}"; sleep "$(awk -v k="$SPEED" 'BEGIN{printf "%.3f", 0.018*k}')"; done
  printf '\n'
}
# run "text shown" command args...  (the command really runs)
run() { local shown=$1; shift; type_out "$shown"; "$@"; }

a2a_send() { # agent text [context]
  "$BASKET" -mode send -live -url "$GW" -agent "$NS/$1" -header 'x-user-id: admin@kagent.dev' -text "$2" ${3:+-context "$3"} -state "$STATE"
}
a2a_approve() { "$BASKET" -mode approve -live -url "$GW" -agent "$NS/$1" -header 'x-user-id: admin@kagent.dev' -state "$STATE"; }
ctx() { sed -n 's/.*"context_id": "\([^"]*\)".*/\1/p' "$STATE" | head -1; }
worker() { kubectl -n "$NS" get pods -l ate.dev/worker-pool=kagent-default; }
get_agents() {
  kubectl get harness,agent -n "$NS" | grep -E 'NAME|pig|claude|codex' |
    awk '/\/pig(-basket|-approve)? /{print "\033[32m" $0 "\033[0m"; next}{print}'
}

clear
banner "kagent harness demo: PiG next to Claude Code, Codex and pi-go"
say "Real kagent + Agent Substrate (gVisor actors) on a Kind cluster."
say "Every model reply comes from one deterministic mock server, no real LLM."
p 5

clear
banner "1/5  Harnesses and agents in the cluster"
run "kubectl get harness,agent -n $NS" get_agents
p 6

clear
banner "2/5  [basket] task through the PiG agent, via the kagent A2A gateway"
say "A shell tool call, then an MCP tool call, then the final answer, streamed:"
run "a2a send $A_PIG \"[basket] run the perf basket\"" a2a_send "$A_PIG" "[basket] run the perf basket"
p 5

MARK="[demo-$RANDOM]"
clear
banner "3/5  [approve]: the task parks in input-required"
say "The MCP tool is protected, so the agent waits for a human decision."
run "a2a send $A_PIG_APPROVE \"$MARK [approve] deploy\"" a2a_send "$A_PIG_APPROVE" "$MARK [approve] deploy"
CTX=$(ctx)
p 4
banner "    Now replace the worker pod hosting the actor while the task waits"
run "kubectl get pods -l ate.dev/worker-pool" worker
OLD=$(kubectl -n "$NS" get pods -l ate.dev/worker-pool=kagent-default -o jsonpath='{.items[0].metadata.name}')
run "kubectl delete pod $OLD" kubectl -n "$NS" delete pod "$OLD" --wait=true
run "kubectl wait --for=condition=Ready pod -l ate.dev/worker-pool" kubectl -n "$NS" wait --for=condition=Ready pod -l ate.dev/worker-pool=kagent-default --timeout=120s
run "kubectl get pods -l ate.dev/worker-pool" worker
p 3
banner "    Approve: the actor restores from its snapshot in a fresh process and the task completes"
run "a2a approve $A_PIG_APPROVE" a2a_approve "$A_PIG_APPROVE"
p 5

clear
banner "4/5  [recall]: the restarted session still knows what was said before"
say "First message of this session carried the marker $MARK"
run "a2a send $A_PIG_APPROVE \"[recall]\"   # same session" a2a_send "$A_PIG_APPROVE" "[recall]" "$CTX"
p 5

clear
banner "5/5  Memory per actor (gVisor sandbox cgroup), same task, same mock model"
if [ -n "$MEMJSON" ] && [ -f "$MEMJSON" ]; then
  run "python3 mem_table.py results/.../analysis.json" python3 "$HERE/mem_table.py" "$MEMJSON"
else
  say "(no measurement file found)"
fi
p 9
banner "Done. PiG runs as a native kagent harness: task, approval, restart-resume, MCP, memory."
p 4
