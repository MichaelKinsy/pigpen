#!/usr/bin/env bash
# images.sh OUT.json  : compressed size, layer count and registry pull time (crane) of each harness image.
# Needs kubectl (KUBECONFIG), crane and jq. The Claude Code and Codex images are kagent's own harness images (defaults below are the
# v1.0.0-rc3 digests; override with CLAUDE_IMAGE / CODEX_IMAGE). pi-go and PiG are read from the live Harness objects.
set -euo pipefail
: "${TMPDIR:=/tmp}"
OUT=${1:?out.json}
declare -A REF=(
  [claude]="${CLAUDE_IMAGE:-ghcr.io/kagent-dev/kagent/claude-harness@sha256:216613bf155d3d437765675144fee19c9045cb21cd2e481189f2dcae36c24b3b}"
  [codex]="${CODEX_IMAGE:-ghcr.io/kagent-dev/kagent/codex-harness@sha256:84c3275e36c0d98472a8b930a628e47e08277e7ce4438908e2a44fd1d8a9efd2}"
)
REF[pi-go]=$(kubectl -n kagent get harness "${PIGO_HARNESS:-pigo-basket}" -o jsonpath='{.spec.workload.image}')
REF[pig]=$(kubectl -n kagent get harness "${PIG_HARNESS:-pig}" -o jsonpath='{.spec.workload.image}')
tmp=$(mktemp -d "$TMPDIR/img.XXXXXX")
echo '[]' >"$tmp/o.json"
for name in claude codex pi-go pig; do
  ref=${REF[$name]}; flags=(--platform linux/amd64); [[ $ref == localhost:* ]] && flags+=(--insecure)
  m=$(crane manifest "${flags[@]}" "$ref")
  layers=$(echo "$m" | jq '[.layers[].size]|length'); bytes=$(echo "$m" | jq '[.layers[].size]|add')
  s=$(date +%s.%N); crane pull "${flags[@]}" "$ref" "$tmp/x.tar" >/dev/null 2>&1; e=$(date +%s.%N)
  rm -f "$tmp/x.tar"
  jq --arg n "$name" --arg r "$ref" --argjson l "$layers" --argjson b "$bytes" --arg p "$(echo "$e - $s" | bc -l | xargs printf '%.1f')" \
     '. + [{name:$n, ref:$r, layers:$l, compressed_mib:(($b/1048576*10|round)/10), pull_s:($p|tonumber)}]' "$tmp/o.json" >"$tmp/o2.json" && mv "$tmp/o2.json" "$tmp/o.json"
done
cp "$tmp/o.json" "$OUT"; rm -rf "$tmp"; cat "$OUT"
