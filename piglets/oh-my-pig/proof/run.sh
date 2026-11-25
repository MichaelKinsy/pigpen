#!/usr/bin/env bash
# End-to-end headless Tern parity proof: runs omp and oh-my-pig on the same
# scripted OpenAI-compatible conversation in isolated headless Tern instances,
# captures screenshots, Tern DOM trees, and TSP wire recordings at every step,
# then generates /tmp/omp-proof/report.md and /tmp/omp-proof/report.json.
#
# Never touches the user's real Tern windows, HOME, ~/.omp, ~/.pig, or any
# omp process it did not start.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${OMP_PROOF_ROOT:-/tmp/omp-proof}"
OMP_BIN="${OMP_BIN:-$(command -v omp || true)}"
OH_MY_PIG_BIN="${OH_MY_PIG_BIN:-/tmp/omp-bin/oh-my-pig}"

# Guard rm -rf "$ROOT": refuse anything outside /tmp or /private/tmp or /var/tmp
case "$ROOT" in
  /tmp/*|/private/tmp/*|/var/tmp/*) ;;
  *)
    echo "error: refusing to wipe OMP_PROOF_ROOT outside /tmp: $ROOT" >&2
    exit 2
    ;;
esac

CTL="$ROOT/ctl.sock"

T() { OMP_PROOF_ROOT="$ROOT" "$SCRIPT_DIR/t.sh" "$@"; }
C() { T ctl --control "$CTL" "$@"; }
q() { C "$@" >/dev/null; }

check_free_disk() {
  python3 -B - "$1" <<'PY'
import os, shutil, sys
p = sys.argv[1]
while not os.path.exists(p) and os.path.dirname(p) != p:
    p = os.path.dirname(p)
free = shutil.disk_usage(p).free
min_b = 2 * 1024 * 1024 * 1024
if free < min_b:
    sys.stderr.write(f"error: refusing to start — only {free / (1024**3):.2f} GiB free on {p} (need at least 2.00 GiB)\n")
    sys.exit(1)
PY
}

RUN_TAG="tern-$$-$(python3 -B -c 'import uuid; print(uuid.uuid4().hex[:12])')"
LLM_PID=""
PROXY_PID=""
SERVE_PID=""
RUN_OK=0

cleanup() {
  python3 -B "$SCRIPT_DIR/harness_isolation.py" reap --tag "$RUN_TAG" >/dev/null 2>&1 || true
  if [ -n "$SERVE_PID" ] && kill -0 "$SERVE_PID" 2>/dev/null; then
    C quit >/dev/null 2>&1 || true
    kill "$SERVE_PID" 2>/dev/null || true
    wait "$SERVE_PID" 2>/dev/null || true
  fi
  if [ -n "$LLM_PID" ] && kill -0 "$LLM_PID" 2>/dev/null; then
    kill "$LLM_PID" 2>/dev/null || true
    wait "$LLM_PID" 2>/dev/null || true
  fi
  if [ -n "$PROXY_PID" ] && kill -0 "$PROXY_PID" 2>/dev/null; then
    kill "$PROXY_PID" 2>/dev/null || true
    wait "$PROXY_PID" 2>/dev/null || true
  fi
  rm -rf "$ROOT/.natives-cache"
  if [ "$RUN_OK" = "1" ] || [ "${ORACLE_KEEP:-}" != "1" ]; then
    rm -rf \
      "$ROOT/omp/home" "$ROOT/omp/tmp" "$ROOT/omp/work" \
      "$ROOT/pig/home" "$ROOT/pig/pighome" "$ROOT/pig/agent" "$ROOT/pig/tmp" "$ROOT/pig/work" \
      "$ROOT/tern-cfg" "$ROOT/ctl.sock" "$ROOT/tern-d.sock" "$ROOT/llm.url" "$ROOT/proxy.url"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP
require_bin() {
  if [ ! -x "$1" ]; then
    echo "error: required executable not found: $1" >&2
    exit 1
  fi
}

require_bin "$OMP_BIN"
. "$SCRIPT_DIR/omp_version.sh"
require_omp_reference "$OMP_BIN"
require_bin "$OH_MY_PIG_BIN"
# The Binary must be the oh-my-pig Piglet, not a stock pig: PIG_BIN, which Pigpen's scripts use for pig itself, is not read here.
command -v tern >/dev/null 2>&1 || { echo "error: tern not found on PATH" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "error: python3 not found on PATH" >&2; exit 1; }

# Check free disk space (>= 2 GiB) before preparing scratch tree under /tmp/omp-proof
check_free_disk "$ROOT"
rm -rf "$ROOT"
mkdir -p "$ROOT/tern-cfg" "$ROOT/omp" "$ROOT/pig" "$ROOT/.natives-cache"

seed_workspace() {
  local work="$1"
  mkdir -p "$work"
  cat > "$work/notes.txt" <<'EOF'
title: proof fixture
status: draft
value: 42
EOF
  cat > "$work/sample.js" <<'EOF'
console.log("hi");
EOF
}


# Start refusing proxy listener to catch & reject any non-loopback network attempt
python3 -B "$SCRIPT_DIR/harness_isolation.py" proxy \
  --url-file "$ROOT/proxy.url" \
  --log "$ROOT/proxy.log.jsonl" &
PROXY_PID=$!
for _ in $(seq 1 100); do
  [ -s "$ROOT/proxy.url" ] && break
  sleep 0.05
done
if [ ! -s "$ROOT/proxy.url" ]; then
  echo "error: refusing proxy failed to start" >&2
  exit 1
fi
PROXY_URL="$(cat "$ROOT/proxy.url")"

write_strip_env_block() {
  cat <<'EOF'
for _v in $(env | cut -d= -f1); do
  case "$_v" in
    OMP_*|PI_*|PIG_*|\
    OPENAI_*|ANTHROPIC_*|GEMINI_*|COPILOT_*|GOOGLE_*|\
    EXA_*|BRAVE_*|TAVILY_*|PERPLEXITY_*|FIRECRAWL_*|TYPESAFE_*|TINYFISH_*|KEENABLE_*|SOFYA_*|JINA_*|PARALLEL_*|KAGI_*|\
    GH_*|GLAB_*|GITHUB_*|GITLAB_*|AWS_*|AZURE_*|MISTRAL_*|GROQ_*|XAI_*|DEEPSEEK_*|OPENROUTER_*|CEREBRAS_*|HF_*|HUGGINGFACE_*|\
    SSH_*|HTTP_PROXY|http_proxy|HTTPS_PROXY|https_proxy|ALL_PROXY|all_proxy|NO_PROXY|no_proxy|FTP_PROXY|ftp_proxy)
      unset "$_v" 2>/dev/null || true
      ;;
  esac
done
EOF
}
# Start the scripted OpenAI-compatible server on an ephemeral loopback port.
python3 "$SCRIPT_DIR/llm_server.py" \
  --script "$SCRIPT_DIR/turns.json" \
  --url-file "$ROOT/llm.url" \
  --log "$ROOT/llm.log.jsonl" \
  >"$ROOT/llm.stdout" 2>"$ROOT/llm.stderr" &
LLM_PID=$!

for _ in $(seq 1 100); do
  [ -s "$ROOT/llm.url" ] && break
  sleep 0.05
done
if [ ! -s "$ROOT/llm.url" ]; then
  echo "error: scripted LLM server did not start" >&2
  cat "$ROOT/llm.stderr" >&2 || true
  exit 1
fi
LLM_URL="$(cat "$ROOT/llm.url")"
LLM_ORIGIN="${LLM_URL%/v1}"

reset_llm() {
  python3 -B -c "import urllib.request; urllib.request.urlopen(urllib.request.Request('$LLM_ORIGIN/reset', method='POST')).read()" >/dev/null
}

# Wait until llm_server has completed at least `target` turns with 0 in_flight,
# then wait for TSP output to go quiet so we never capture mid-stream or run ahead of turns.
# Exits 2 if the turn deadline expires without reaching `target`.
wait_for_turn() {
  local out="$1"
  local target="$2"
  local max_s="${3:-20}"
  local scaled_max
  scaled_max="$(python3 -B -c "print(max(float('$max_s') * 3.0, 45.0))")"
  python3 -B - "$LLM_ORIGIN" "$out/tsp.jsonl" "$target" "$scaled_max" <<'PY'
import json, os, sys, time, urllib.request
origin, tsp_path, target, max_s = sys.argv[1], sys.argv[2], int(sys.argv[3]), float(sys.argv[4])
deadline = time.monotonic() + max_s
reached = False
last_c, last_if = -1, -1
while time.monotonic() < deadline:
    try:
        with urllib.request.urlopen(f"{origin}/health", timeout=0.5) as resp:
            st = json.loads(resp.read().decode("utf-8"))
            last_c = int(st.get("completed", 0))
            last_if = int(st.get("in_flight", 0))
            if last_c >= target and last_if == 0:
                reached = True
                break
    except Exception:
        pass
    time.sleep(0.08)
if not reached:
    print(f"turn {target} not consumed before {max_s:.1f}s deadline (completed={last_c}, in_flight={last_if})", file=sys.stderr)
    raise SystemExit(2)
# Wait for TSP writes to settle (quiet for 0.5s, bounded by 4.0s)
settle_end = time.monotonic() + 4.0
last_size = os.path.getsize(tsp_path) if os.path.isfile(tsp_path) else -1
last_change = time.monotonic()
while time.monotonic() < settle_end:
    time.sleep(0.08)
    cur = os.path.getsize(tsp_path) if os.path.isfile(tsp_path) else -1
    if cur != last_size:
        last_size = cur
        last_change = time.monotonic()
    elif time.monotonic() - last_change >= 0.5:
        break
PY
}

# Wait for TSP file to grow/contain a required needle (or quiet after a UI action), bounded by max_s.
wait_for_tsp_needle() {
  local out="$1"
  local needle="$2"
  local max_s="${3:-15}"
  local start_offset="${4:-0}"
  python3 -B - "$out/tsp.jsonl" "$needle" "$max_s" "$start_offset" <<'PY'
import os, sys, time
tsp_path, needle, max_s, start_off = sys.argv[1], sys.argv[2], float(sys.argv[3]), int(sys.argv[4])
deadline = time.monotonic() + max_s
found = False
while time.monotonic() < deadline:
    if os.path.isfile(tsp_path):
        with open(tsp_path, "r", encoding="utf-8", errors="replace") as fh:
            fh.seek(start_off)
            chunk = fh.read()
        if needle in chunk:
            found = True
            break
    time.sleep(0.06)
if not found:
    print(f"expected TSP marker {needle!r} not observed within {max_s:.1f}s", file=sys.stderr)
    raise SystemExit(2)
# Brief settle for trailing ops (quiet for 0.4s, bounded by 2.5s)
settle_end = time.monotonic() + 2.5
last_size = os.path.getsize(tsp_path) if os.path.isfile(tsp_path) else -1
last_change = time.monotonic()
while time.monotonic() < settle_end:
    time.sleep(0.06)
    cur = os.path.getsize(tsp_path) if os.path.isfile(tsp_path) else -1
    if cur != last_size:
        last_size = cur
        last_change = time.monotonic()
    elif time.monotonic() - last_change >= 0.4:
        break
PY
}

wait_for_tsp_quiet() {
  local out="$1"
  local quiet_s="${2:-0.45}"
  local max_s="${3:-3.0}"
  python3 -B - "$out/tsp.jsonl" "$quiet_s" "$max_s" <<'PY'
import os, sys, time
tsp_path, quiet_s, max_s = sys.argv[1], float(sys.argv[2]), float(sys.argv[3])
end = time.monotonic() + max_s
last_size = os.path.getsize(tsp_path) if os.path.isfile(tsp_path) else -1
last_change = time.monotonic()
while time.monotonic() < end:
    time.sleep(0.06)
    cur = os.path.getsize(tsp_path) if os.path.isfile(tsp_path) else -1
    if cur != last_size:
        last_size = cur
        last_change = time.monotonic()
    elif time.monotonic() - last_change >= quiet_s:
        break
PY
}

# Write isolated configs for omp
setup_omp() {
  local base="$ROOT/omp"
  mkdir -p \
    "$base/home/.omp/agent" \
    "$base/home/.config" \
    "$base/home/.local/share" \
    "$base/home/.local/state" \
    "$base/home/.cache" \
    "$base/tmp" \
    "$base/out"
  seed_workspace "$base/work"

  cat > "$base/home/.omp/agent/models.yml" <<EOF
providers:
  proof:
    baseUrl: "$LLM_URL"
    apiKey: "proof-key"
    api: "openai-completions"
    models:
      - id: "proof-1"
        name: "Proof 1"
        reasoning: true
        thinking:
          mode: "effort"
          efforts: ["low", "medium", "high"]
        input: ["text"]
        cost:
          input: 0
          output: 0
          cacheRead: 0
          cacheWrite: 0
        contextWindow: 128000
        maxTokens: 4096
EOF

  python3 -B "$SCRIPT_DIR/harness_isolation.py" omp-config-yml > "$base/home/.omp/agent/config.yml"

  {
    echo "#!/bin/sh"
    write_strip_env_block
    cat <<EOF
export HOME="$base/home"
export PI_CODING_AGENT_DIR="$base/home/.omp/agent"
export PI_CODING_AGENT_SESSION_DIR="$base/home/.omp/agent/sessions"
export PI_CONFIG_DIR=".omp"
export PI_CONFIG_FILES=""
export OMP_PROFILE=""
export PI_PROFILE=""
export OMP_SKIP_SETUP=1
export OMP_AUTORESEARCH_DB_DIR="$base/tmp/autoresearch"
export OMP_WORKTREE_DIR="$base/tmp/wt"
export OMP_GITHUB_CACHE_DB="$base/tmp/github-cache.db"
export CLAUDE_CONFIG_DIR="$base/home/.claude"
export COPILOT_HOME="$base/home/.copilot"
export PI_AI_CODEX_CLIENT_VERSION="0.1.0"
export PI_NO_TITLE=1
export PI_TUI_NATIVE=1
export PI_TUI_TSP_RECORD="$base/out/tsp.jsonl"
export PI_NOTIFICATIONS=off
export OTEL_SDK_DISABLED=true
export XDG_CONFIG_HOME="$base/home/.config"
export XDG_DATA_HOME="$base/home/.local/share"
export XDG_STATE_HOME="$base/home/.local/state"
export XDG_CACHE_HOME="$base/home/.cache"
export TMPDIR="$base/tmp"
export PI_NATIVES_DIR="$ROOT/.natives-cache"
if [ -n "${ORACLE_TOOL_DIRS:-}" ]; then
  export PATH="${ORACLE_TOOL_DIRS}:\$PATH"
fi
export GIT_TERMINAL_PROMPT=0
export ORACLE_RUN_TAG="$RUN_TAG"
export HTTP_PROXY="$PROXY_URL"
export http_proxy="$PROXY_URL"
export HTTPS_PROXY="$PROXY_URL"
export https_proxy="$PROXY_URL"
export ALL_PROXY="$PROXY_URL"
export all_proxy="$PROXY_URL"
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="127.0.0.1,localhost,::1"
cd "$base/work"
echo "env: TERM_PROGRAM=\${TERM_PROGRAM:-} TERN_PANE=\${TERN_PANE:-}" > "$base/out/launch.txt"
python3 -B "$SCRIPT_DIR/harness_isolation.py" sandbox-exec -- \\
  "$OMP_BIN" \\
  --model proof/proof-1 \\
  --thinking medium \\
  --no-session \\
  --offline \\
  --no-extensions \\
  --no-skills \\
  --no-rules \\
  --no-context-files \\
  --no-title \\
  --auto-approve \\
  --tools read,write,bash,edit,grep,glob,todo,ask,eval,task,wait,web_search,lsp,ast_edit \\
  2>"$base/out/stderr.log"
echo "exit=\$?" >> "$base/out/launch.txt"
EOF
  } > "$base/launch.sh"
  chmod +x "$base/launch.sh"
}

# Write isolated configs for oh-my-pig
setup_pig() {
  local base="$ROOT/pig"
  mkdir -p \
    "$base/home/.config" \
    "$base/home/.local/share" \
    "$base/home/.local/state" \
    "$base/home/.cache" \
    "$base/pighome" \
    "$base/agent" \
    "$base/tmp" \
    "$base/out"
  seed_workspace "$base/work"

  cat > "$base/agent/models.json" <<EOF
{
  "providers": {
    "proof": {
      "baseUrl": "$LLM_URL",
      "apiKey": "proof-key",
      "api": "openai-completions",
      "models": [
        {
          "id": "proof-1",
          "name": "Proof 1",
          "reasoning": true,
          "input": ["text"],
          "cost": { "input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0 },
          "contextWindow": 128000,
          "maxTokens": 4096
        }
      ]
    }
  }
}
EOF

  cat > "$base/agent/settings.json" <<'EOF'
{
  "lastChangelogVersion": "1.5.2",
  "defaultThinkingLevel": "medium"
}
EOF
  python3 -B "$SCRIPT_DIR/harness_isolation.py" pig-config-yml > "$base/agent/config.yml"

  cat > "$base/agent/trust.json" <<EOF
{
  "$base/work": true
}
EOF

  {
    echo "#!/bin/sh"
    write_strip_env_block
    cat <<EOF
export HOME="$base/home"
export USERPROFILE="$base/home"
export PIG_HOME="$base/pighome"
export PI_HOME="$base/pighome"
export PIG_CODING_AGENT_DIR="$base/agent"
export PI_CODING_AGENT_DIR="$base/agent"
export PIG_CODING_AGENT_SESSION_DIR="$base/agent/sessions"
export PI_CODING_AGENT_SESSION_DIR="$base/agent/sessions"
export PIG_OFFLINE=1
export PI_OFFLINE=1
export PI_NO_RULES=1
export TERN_TSP_RECORD="$base/out/tsp.jsonl"
export XDG_CONFIG_HOME="$base/home/.config"
export XDG_DATA_HOME="$base/home/.local/share"
export XDG_STATE_HOME="$base/home/.local/state"
export XDG_CACHE_HOME="$base/home/.cache"
export TMPDIR="$base/tmp"
if [ -n "${ORACLE_TOOL_DIRS:-}" ]; then
  export PATH="${ORACLE_TOOL_DIRS}:\$PATH"
fi
export GIT_TERMINAL_PROMPT=0
export ORACLE_RUN_TAG="$RUN_TAG"
export HTTP_PROXY="$PROXY_URL"
export http_proxy="$PROXY_URL"
export HTTPS_PROXY="$PROXY_URL"
export https_proxy="$PROXY_URL"
export ALL_PROXY="$PROXY_URL"
export all_proxy="$PROXY_URL"
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="127.0.0.1,localhost,::1"
cd "$base/work"
echo "env: TERM_PROGRAM=\${TERM_PROGRAM:-} TERN_PANE=\${TERN_PANE:-}" > "$base/out/launch.txt"
python3 -B "$SCRIPT_DIR/harness_isolation.py" sandbox-exec -- \\
  "$OH_MY_PIG_BIN" \\
  --model proof/proof-1 \\
  --thinking medium \\
  --no-session \\
  --approve \\
  2>"$base/out/stderr.log"
echo "exit=\$?" >> "$base/out/launch.txt"
EOF
  } > "$base/launch.sh"
  chmod +x "$base/launch.sh"
}

start_tern() {
  local out="$1"
  local log="$2"
  rm -f "$CTL" "$ROOT/tern-d.sock"
  mkdir -p "$ROOT/tern-cfg" "$out"
  T serve --control "$CTL" --out "$out" >"$log" 2>&1 &
  SERVE_PID=$!
  for _ in $(seq 1 200); do
    if [ -S "$CTL" ] && C stats >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done
  if ! C stats >/dev/null 2>&1; then
    echo "error: headless tern failed to start (see $log)" >&2
    cat "$log" >&2 || true
    exit 1
  fi
  q size 1280 800
  q ready
}

stop_tern() {
  if [ -n "$SERVE_PID" ] && kill -0 "$SERVE_PID" 2>/dev/null; then
    C quit >/dev/null 2>&1 || true
    wait "$SERVE_PID" 2>/dev/null || true
    SERVE_PID=""
  fi
  python3 -B "$SCRIPT_DIR/harness_isolation.py" reap --tag "$RUN_TAG" || true
}

# Snapshot DOM tree, screenshot, and TSP recording line count at a named step.
capture_step() {
  local out="$1"
  local step="$2"
  q shot "$step"
  C tree > "$out/tree-${step}.json"
  if [ -f "$out/tsp.jsonl" ]; then
    wc -l < "$out/tsp.jsonl" | tr -d ' ' > "$out/tsp-lines-${step}.txt"
  else
    echo "0" > "$out/tsp-lines-${step}.txt"
  fi
}

send_prompt_expect() {
  local out="$1"
  local text="$2"
  local target_completed="$3"
  local max_s="${4:-20}"
  q type "\"$text\""
  q key Enter
  wait_for_turn "$out" "$target_completed" "$max_s"
}

run_lane() {
  local name="$1"
  local base="$ROOT/$name"
  local out="$base/out"
  local lane_rc=0

  reset_llm
  start_tern "$out" "$ROOT/serve-${name}.log"

  q run "\"$base/launch.sh\""
  # Evidence-based startup wait: wait up to 45s for the native surface (.sf-main + .sf-dock)
  # AND TSP welcome card (`omp.welcome`) + quiet settle before typing the first prompt.
  local ready_ok=0
  for _ in $(seq 1 225); do
    if [ -s "$out/tsp.jsonl" ] && grep -q '"omp.welcome"' "$out/tsp.jsonl" 2>/dev/null && C tree "[class*='sf-main']" >/dev/null 2>&1; then
      local cnt
      cnt="$(C tree ".sf-main" | python3 -c "import json,sys; print(json.load(sys.stdin).get('count',0))" 2>/dev/null || echo 0)"
      if [ "$cnt" -ge 1 ]; then
        ready_ok=1
        break
      fi
    fi
    sleep 0.2
  done
  if [ "$ready_ok" -ne 1 ]; then
    echo "capture incomplete on ${name}: native surface / welcome card never became ready within 45s" >&2
    stop_tern
    return 2
  fi
  wait_for_tsp_quiet "$out" 0.55 4.0
  capture_step "$out" "00-started"

  # Turn 0: Plain Markdown reply
  send_prompt_expect "$out" "Show a short markdown example." 1 20 || { stop_tern; return 2; }
  capture_step "$out" "01-markdown"

  # Turn 1: Delayed thinking + text -> wait for live `omp.working` evidence before mid-turn snapshot, then settled
  local tsp_off
  tsp_off="$(wc -c < "$out/tsp.jsonl" | tr -d ' ')"
  q type '"Compute 20 + 22 and explain briefly."'
  q key Enter
  # Wait for `omp.working` frame (bounded by 10s; turn 1 has a 2.2s delay_ms so working row appears promptly)
  python3 -B - "$out/tsp.jsonl" "$tsp_off" <<'PY'
import os, sys, time
tsp_path, start_off = sys.argv[1], int(sys.argv[2])
deadline = time.monotonic() + 10.0
while time.monotonic() < deadline:
    if os.path.isfile(tsp_path):
        with open(tsp_path, "r", encoding="utf-8", errors="replace") as fh:
            fh.seek(start_off)
            chunk = fh.read()
        if '"omp.working"' in chunk:
            time.sleep(0.15)
            break
    time.sleep(0.05)
PY
  capture_step "$out" "02a-working"
  wait_for_turn "$out" 2 25 || { stop_tern; return 2; }
  capture_step "$out" "02b-thinking"

  # Turns 2-3: read tool call + reply
  send_prompt_expect "$out" "Read notes.txt." 4 20 || { stop_tern; return 2; }
  capture_step "$out" "03-tool-read"

  # Turns 4-5: bash tool call + reply
  send_prompt_expect "$out" "Run a short printf command in bash." 6 20 || { stop_tern; return 2; }
  capture_step "$out" "04-tool-bash"

  # Turns 6-7: edit tool call + reply
  send_prompt_expect "$out" "Update status in notes.txt from draft to verified." 8 20 || { stop_tern; return 2; }
  capture_step "$out" "05-tool-edit"

  # Turns 8-9: failing read tool call + reply
  send_prompt_expect "$out" "Read does-not-exist-proof.txt." 10 20 || { stop_tern; return 2; }
  capture_step "$out" "06-tool-error"

  # Turns 10-12: todo init + todo done + reply (0 open items -> no stop-time reminder)
  send_prompt_expect "$out" "Initialize and complete a short todo list." 13 25 || { stop_tern; return 2; }
  capture_step "$out" "07-tool-todo"

  # Turns 13-14: ask interactive form (wait for "Choose a mode:" evidence before Enter)
  tsp_off="$(wc -c < "$out/tsp.jsonl" | tr -d ' ')"
  q type '"Ask me to choose a mode."'
  q key Enter
  wait_for_turn "$out" 14 20 || { stop_tern; return 2; }
  wait_for_tsp_needle "$out" "Choose a mode:" 15 "$tsp_off" || { stop_tern; return 2; }
  capture_step "$out" "08a-ask-form"
  q key Enter
  wait_for_turn "$out" 15 20 || { stop_tern; return 2; }
  capture_step "$out" "08b-ask-submitted"

  # Turns 15-16: eval tool call + reply
  send_prompt_expect "$out" "Run a short python eval." 17 25 || { stop_tern; return 2; }
  capture_step "$out" "09-tool-eval"

  # Turns 17-19: task + wait + reply
  send_prompt_expect "$out" "Spawn a proof subagent and wait for it." 20 30 || { stop_tern; return 2; }
  capture_step "$out" "10-tool-task"

  # Turns 20-21: web_search offline error card + reply
  send_prompt_expect "$out" "Run a web search offline." 22 20 || { stop_tern; return 2; }
  capture_step "$out" "11-tool-web-search"

  # Turns 22-25: write + grep + glob + reply
  send_prompt_expect "$out" "Write generated.txt and search workspace files." 26 25 || { stop_tern; return 2; }
  capture_step "$out" "12-tool-write-search"

  # Turns 26-29: lsp status + ast_edit + reject + reply
  send_prompt_expect "$out" "Check LSP status and preview an AST edit." 30 25 || { stop_tern; return 2; }
  capture_step "$out" "13-tool-lsp-ast"

  # Turns 30-31: long foldable bash output, then toggle expand with Ctrl+O (wait for TSP update + settle)
  send_prompt_expect "$out" "Print 30 numbered lines." 32 25 || { stop_tern; return 2; }
  capture_step "$out" "14a-long-folded"
  tsp_off="$(wc -c < "$out/tsp.jsonl" | tr -d ' ')"
  q key Control+o
  wait_for_tsp_needle "$out" '"collapsed":false' 10 "$tsp_off" || wait_for_tsp_quiet "$out" 0.5 3.0
  capture_step "$out" "14b-long-expanded"

  # Turn 32: simulated provider HTTP 400 error turn
  send_prompt_expect "$out" "Trigger a simulated provider error." 33 20 || { stop_tern; return 2; }
  capture_step "$out" "15-provider-error"

  # Slash command: /plan (wait for "Plan" indicator in TSP before capturing)
  tsp_off="$(wc -c < "$out/tsp.jsonl" | tr -d ' ')"
  q type '"/plan"'
  q key Enter
  wait_for_tsp_needle "$out" "Plan" 10 "$tsp_off" || { stop_tern; return 2; }
  capture_step "$out" "16-plan-mode"

  # Slash menu autocomplete preview (type / without Enter, wait for TSP quiet, then Escape)
  q type '"/"'
  wait_for_tsp_quiet "$out" 0.5 3.0
  capture_step "$out" "17a-slash-menu"
  q key Escape
  wait_for_tsp_quiet "$out" 0.3 1.5
  q key BackSpace
  wait_for_tsp_quiet "$out" 0.45 2.5
  capture_step "$out" "17b-slash-closed"

  # Narrow viewport resize (800x600)
  q size 800 600
  wait_for_tsp_quiet "$out" 0.55 3.5
  capture_step "$out" "18-narrow-80"

  # Record server-side completion metadata for pre-compare validity verification
  python3 -B - "$LLM_ORIGIN" "$out/capture-meta.json" "$name" <<'PY'
import json, sys, urllib.request
origin, out_path, side = sys.argv[1], sys.argv[2], sys.argv[3]
with urllib.request.urlopen(f"{origin}/health", timeout=2.0) as resp:
    st = json.loads(resp.read().decode("utf-8"))
with open(out_path, "w", encoding="utf-8") as fh:
    json.dump({"side": side, "completed_turns": int(st.get("completed", 0)), "in_flight": int(st.get("in_flight", 0)), "expected_turns": 33}, fh, indent=2)
    fh.write("\n")
PY

  # Clean exit
  q type '"/quit"'
  q key Enter
  sleep 1.0
  stop_tern
  return "$lane_rc"
}

setup_omp
setup_pig

OMP_LANE_RC=0
PIG_LANE_RC=0
echo "Running omp in headless Tern..."
run_lane omp || OMP_LANE_RC=$?
echo "Running oh-my-pig in headless Tern..."
run_lane pig || PIG_LANE_RC=$?

if [ "$OMP_LANE_RC" -ne 0 ] || [ "$PIG_LANE_RC" -ne 0 ]; then
  echo "capture incomplete (Tern): omp_rc=$OMP_LANE_RC pig_rc=$PIG_LANE_RC" >&2
  python3 -B - "$ROOT/report.json" "$OMP_LANE_RC" "$PIG_LANE_RC" <<'PY'
import json, sys
out_path, orc, prc = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
with open(out_path, "w", encoding="utf-8") as fh:
    json.dump({"capture_valid": False, "verdict": None, "omp_rc": orc, "pig_rc": prc, "error": f"capture incomplete: omp_rc={orc}, pig_rc={prc}"}, fh, indent=2)
    fh.write("\n")
PY
  exit 2
fi

echo "Comparing captured trees and TSP wire frames..."
COMPARE_RC=0
COMPARE_ARGS=()
if [ "${ORACLE_ALLOW_DIFFS:-}" = "1" ]; then
  COMPARE_ARGS+=(--allow-diffs)
fi
python3 -B "$SCRIPT_DIR/compare.py" \
  --omp-dir "$ROOT/omp/out" \
  --pig-dir "$ROOT/pig/out" \
  --md-out "$ROOT/report.md" \
  --json-out "$ROOT/report.json" \
  "${COMPARE_ARGS[@]}" || COMPARE_RC=$?
python3 -B "$SCRIPT_DIR/harness_isolation.py" check-pids --tag "$RUN_TAG"
if [ -s "$ROOT/proxy.log.jsonl" ]; then
  echo "ERROR: refusing proxy recorded non-loopback network attempt(s):" >&2
  cat "$ROOT/proxy.log.jsonl" >&2
  exit 1
fi
RUN_OK=1
rm -rf \
  "$ROOT/.natives-cache" \
  "$ROOT/omp/home" "$ROOT/omp/tmp" "$ROOT/omp/work" \
  "$ROOT/pig/home" "$ROOT/pig/pighome" "$ROOT/pig/agent" "$ROOT/pig/tmp" "$ROOT/pig/work" \
  "$ROOT/tern-cfg" "$ROOT/ctl.sock" "$ROOT/tern-d.sock" "$ROOT/llm.url"
OUT_SIZE="$(du -sh "$ROOT" 2>/dev/null | awk '{print $1}')"
echo "Done: $ROOT/report.md and $ROOT/report.json (output size: $OUT_SIZE, compare_rc=$COMPARE_RC)"
exit "$COMPARE_RC"
