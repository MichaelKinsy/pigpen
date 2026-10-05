#!/usr/bin/env bash
# bash 3.2 compatible (macOS default).
# Usage: benchmark.sh <run-dir>
#
# Scans raw.ndjson from a pi experiment run and outputs a structured
# bench-result.json with a pass/fail verdict on frustrated/cycling language.
#
# Two pattern families:
#   frustrate_patterns — explicit frustration/cycling/apologist language
#   meta_patterns — meta-commentary about the loop ("help me break this loop")

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUN_DIR="${1:?usage: benchmark.sh <run-dir>}"
RAW="${RUN_DIR}/raw.ndjson"
OUT="${RUN_DIR}/bench-result.json"

[[ -f "${RAW}" ]] || { echo "Missing ${RAW}" >&2; exit 2; }

# Patterns where the agent expresses frustration/cycling/apology
FRUSTRATE_PATTERNS=(
	"going in circles"
	"round and round"
	"apologis"
	"going around"
	"feel like we(('| a)re|'re)"
	"we keep going"
	"we seem to be"
	"this is getting"
	"you seem"
)

# Patterns where the agent comments on being stuck in a loop/meta-iteration
META_PATTERNS=(
	"help me break"
	"break this loop"
	"break out of this loop"
	"stuck"
	"overcomplicat"
	"stop proposing"
	"i'll stop"
	"i keep iterat"
	"tooling isn't connect"
	"i'm stuck"
	"give me a signal"
	"tell me what"
	"reset"
	"not quite right"
	"still not quite"
)

# Build a jq filter for all assistant text
JQ_FILTER='[. | .. | objects | select(.type == "message_end" and .message.role == "assistant") | .message.content // [] | map(select(.type == "text") | .text) | .[]] | join(" ")'

ALL_TEXT="$(jq -s "${JQ_FILTER}" < "${RAW}")"

# Count matches per pattern, then aggregate
MATCHES_JSON="{"
FIRST=true
TOTAL_FRUSTRATE=0
TOTAL_META=0
VERDICT="PASS"

for pattern in "${FRUSTRATE_PATTERNS[@]}"; do
	COUNT="$(echo "${ALL_TEXT}" | grep -ioE "${pattern}" | wc -l | tr -d ' ')" || COUNT=0
	if [[ "${COUNT}" -gt 0 ]]; then
		VERDICT="FAIL"
		TOTAL_FRUSTRATE=$((TOTAL_FRUSTRATE + COUNT))
	fi
	if [[ "${FIRST}" == true ]]; then
		FIRST=false
	else
		MATCHES_JSON+=","
	fi
	MATCHES_JSON+="$(printf '\n  "frustrate/%s": %d' "${pattern}" "${COUNT}")"
done

for pattern in "${META_PATTERNS[@]}"; do
	COUNT="$(echo "${ALL_TEXT}" | grep -ioE "${pattern}" | wc -l | tr -d ' ')" || COUNT=0
	if [[ "${COUNT}" -gt 0 ]]; then
		VERDICT="FAIL"
		TOTAL_META=$((TOTAL_META + COUNT))
	fi
	if [[ "${FIRST}" == true ]]; then
		FIRST=false
	else
		MATCHES_JSON+=","
	fi
	MATCHES_JSON+="$(printf '\n  "meta/%s": %d' "${pattern}" "${COUNT}")"
done
MATCHES_JSON+=$'\n}'

# Count total assistant messages
TOTAL_MSGS=$(jq -s '[. | .. | objects | select(.type == "message_end" and .message.role == "assistant")] | length' < "${RAW}")

TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

FRUSTRATE_LIST=$(printf '"%s",' "${FRUSTRATE_PATTERNS[@]}" | sed 's/,$//')
META_LIST=$(printf '"%s",' "${META_PATTERNS[@]}" | sed 's/,$//')

cat > "${OUT}" <<JSON
{
  "case": "C19-iteration-frustration-benchmark",
  "run_at": "${TIMESTAMP}",
  "run_dir": "${RUN_DIR}",
  "total_assistant_messages": ${TOTAL_MSGS},
  "patterns_frustrate": [${FRUSTRATE_LIST}],
  "patterns_meta": [${META_LIST}],
  "pattern_matches": ${MATCHES_JSON},
  "stats": {
    "total_frustrate_hits": ${TOTAL_FRUSTRATE},
    "total_meta_hits": ${TOTAL_META},
    "any_hits": $((TOTAL_FRUSTRATE + TOTAL_META))
  },
  "has_cycling_language": $([ "${VERDICT}" = "FAIL" ] && echo true || echo false),
  "verdict": "${VERDICT}"
}
JSON

echo "Wrote ${OUT}"
echo
jq '.' "${OUT}"
