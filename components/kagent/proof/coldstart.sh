#!/usr/bin/env bash
# coldstart.sh OUT.json N  : time from applying a cloned Harness+Agent to Agent Ready=True (golden snapshot built)
# The clone reuses the image digest, so the node image cache is warm: this is golden-snapshot build time without a registry pull.
set -euo pipefail
: "${TMPDIR:=/tmp}"   # kubectl must already point at the cluster (KUBECONFIG)
OUT=${1:?out.json}; N=${2:-3}
BASES=${BASES:-"claude-basket codex-basket pigo-basket pig-basket:pig"}   # agent[:harness] (harness defaults to the agent name)
res='{}'
strip='del(.status,.metadata.uid,.metadata.resourceVersion,.metadata.creationTimestamp,.metadata.generation,.metadata.managedFields,.metadata.ownerReferences,.metadata.finalizers,.metadata.annotations)'
for spec in $BASES; do
  base=${spec%%:*}; hbase=${spec##*:}
  vals='[]'
  for ((i = 1; i <= N; i++)); do
    nm="$base-cold$i"
    kubectl -n kagent get harness "$hbase" -o json | jq --arg n "$nm" "$strip | .metadata.name=\$n" >"$TMPDIR/h.json"
    kubectl -n kagent get agent "$base" -o json | jq --arg n "$nm" "$strip | .metadata.name=\$n | .spec.harnessRef.name=\$n" >"$TMPDIR/a.json"
    t0=$(date +%s.%N)
    kubectl apply -f "$TMPDIR/h.json" -f "$TMPDIR/a.json" >/dev/null
    dt=null
    for _ in $(seq 1 600); do
      st=$(kubectl -n kagent get agent "$nm" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
      if [ "$st" = "True" ]; then dt=$(echo "$(date +%s.%N) - $t0" | bc -l | xargs printf '%.2f'); break; fi
      sleep 0.5
    done
    echo "$nm ready_after=$dt" >&2
    vals=$(echo "$vals" | jq --argjson v "$dt" '. + [$v]')
    kubectl -n kagent delete agent "$nm" --wait=true --timeout=120s >/dev/null 2>&1; kubectl -n kagent delete harness "$nm" --wait=true --timeout=120s >/dev/null 2>&1 || echo "delete $nm failed" >&2
    sleep 5
  done
  res=$(echo "$res" | jq --arg b "$base" --argjson v "$vals" '. + {($b): $v}')
done
echo "$res" >"$OUT"; cat "$OUT"
