#!/bin/sh
# Real-host proof: a fused pig-warden Binary, a scripted model that asks for `git push --force origin main`, warden
# enabled offline. Expects the call to be HELD with the agent-facing message and the status line to count it.
# Usage: PIG_WARDEN_BIN=/path/to/pig-warden sh smoke.sh   (runs entirely in a temporary directory; no network)
set -eu
BIN=${PIG_WARDEN_BIN:?set PIG_WARDEN_BIN to a pig-warden Binary}
here=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d); trap 'kill $LLM 2>/dev/null || true; rm -rf "$T"' EXIT
mkdir -p "$T/agent" "$T/home" "$T/pighome" "$T/work"
cat > "$T/agent/models.json" <<'JSON'
{"providers":{"eq-llm":{"baseUrl":"http://127.0.0.1:18765/v1","api":"openai-completions","apiKey":"k","models":[{"id":"eq-1","name":"eq-1","reasoning":false,"input":["text"],"contextWindow":100000,"maxTokens":4096,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}
JSON
python3 "$here/fakellm.py" 2>"$T/llm.log" & LLM=$!
sleep 1
cd "$T/work"; git init -q .
out=$( (printf '{"type":"prompt","message":"tidy up the readme","id":"1"}\n'; sleep 10) |
  env -i PATH="$PATH" HOME="$T/home" PIG_HOME="$T/pighome" PIG_CODING_AGENT_DIR="$T/agent" PI_CODING_AGENT_DIR="$T/agent" PIGPEN_WARDEN_ENABLED=1 \
  "$BIN" --mode rpc --no-session --offline --provider eq-llm --model eq-1 2>&1 )
echo "$out" | grep -q 'pi-warden held this bash call before it ran: destructive: git force push' || { echo "FAIL: not held"; exit 1; }
echo "$out" | grep -q 'warden: offline · steer · 1 checked, 1 held' || { echo "FAIL: status line"; exit 1; }
echo "PASS: force push held, agent told why, status line counted it"
