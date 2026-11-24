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
CTL="$ROOT/ctl.sock"

T() { OMP_PROOF_ROOT="$ROOT" "$SCRIPT_DIR/t.sh" "$@"; }
C() { T ctl --control "$CTL" "$@"; }
q() { C "$@" >/dev/null; }

LLM_PID=""
SERVE_PID=""

cleanup() {
  if [ -n "$SERVE_PID" ] && kill -0 "$SERVE_PID" 2>/dev/null; then
    C quit >/dev/null 2>&1 || true
    kill "$SERVE_PID" 2>/dev/null || true
    wait "$SERVE_PID" 2>/dev/null || true
  fi
  if [ -n "$LLM_PID" ] && kill -0 "$LLM_PID" 2>/dev/null; then
    kill "$LLM_PID" 2>/dev/null || true
    wait "$LLM_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

require_bin() {
  if [ ! -x "$1" ]; then
    echo "error: required executable not found: $1" >&2
    exit 1
  fi
}

require_bin "$OMP_BIN"
require_bin "$OH_MY_PIG_BIN"
# The Binary must be the oh-my-pig Piglet, not a stock pig: PIG_BIN, which Pigpen's scripts use for pig itself, is not read here.
command -v tern >/dev/null 2>&1 || { echo "error: tern not found on PATH" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "error: python3 not found on PATH" >&2; exit 1; }

# Prepare clean scratch tree under /tmp/omp-proof
rm -rf "$ROOT"
mkdir -p "$ROOT/tern-cfg" "$ROOT/omp" "$ROOT/pig"

seed_workspace() {
  local work="$1"
  mkdir -p "$work"
  cat > "$work/notes.txt" <<'EOF'
title: proof fixture
status: draft
value: 42
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
  python3 -c "import urllib.request; urllib.request.urlopen(urllib.request.Request('$LLM_ORIGIN/reset', method='POST')).read()" >/dev/null
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

  cat > "$base/home/.omp/agent/config.yml" <<'EOF'
composer:
  predictions: false
EOF

  cat > "$base/launch.sh" <<EOF
#!/bin/sh
# Hermetic omp launch: every directory/profile/config env var pinned under $base.
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
export PI_NO_TITLE=1
export PI_SKIP_VERSION_CHECK=1
export PI_TUI_NATIVE=1
export PI_TUI_TSP_RECORD="$base/out/tsp.jsonl"
export PI_NOTIFICATIONS=off
export OTEL_SDK_DISABLED=true
export XDG_CONFIG_HOME="$base/home/.config"
export XDG_DATA_HOME="$base/home/.local/share"
export XDG_STATE_HOME="$base/home/.local/state"
export XDG_CACHE_HOME="$base/home/.cache"
export TMPDIR="$base/tmp"
export GIT_TERMINAL_PROMPT=0
unset OMP_AUTH_BROKER_URL OMP_AUTH_BROKER_TOKEN OPENAI_API_KEY ANTHROPIC_API_KEY ANTHROPIC_OAUTH_TOKEN GEMINI_API_KEY COPILOT_GITHUB_TOKEN
cd "$base/work"
echo "env: TERM_PROGRAM=\${TERM_PROGRAM:-} TERN_PANE=\${TERN_PANE:-}" > "$base/out/launch.txt"
"$OMP_BIN" \\
  --model proof/proof-1 \\
  --thinking medium \\
  --no-session \\
  --offline \\
  --no-lsp \\
  --no-extensions \\
  --no-skills \\
  --no-rules \\
  --no-context-files \\
  --no-title \\
  --motion none \\
  --auto-approve \\
  --tools read,bash,edit \\
  2>"$base/out/stderr.log"
echo "exit=\$?" >> "$base/out/launch.txt"
EOF
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

  cat > "$base/agent/trust.json" <<EOF
{
  "$base/work": true
}
EOF

  cat > "$base/launch.sh" <<EOF
#!/bin/sh
# Hermetic oh-my-pig launch: every PiG/Pi/XDG dir env var pinned under $base.
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
export PI_SKIP_VERSION_CHECK=1
export TERN_TSP_RECORD="$base/out/tsp.jsonl"
export XDG_CONFIG_HOME="$base/home/.config"
export XDG_DATA_HOME="$base/home/.local/share"
export XDG_STATE_HOME="$base/home/.local/state"
export XDG_CACHE_HOME="$base/home/.cache"
export TMPDIR="$base/tmp"
export GIT_TERMINAL_PROMPT=0
unset PIG_USE_PI_DIRS PIG_PIGLET_PATH PIG_PIGLET_NAME PIG_TEST_FAUX OPENAI_API_KEY ANTHROPIC_API_KEY ANTHROPIC_OAUTH_TOKEN GEMINI_API_KEY COPILOT_GITHUB_TOKEN
cd "$base/work"
echo "env: TERM_PROGRAM=\${TERM_PROGRAM:-} TERN_PANE=\${TERN_PANE:-}" > "$base/out/launch.txt"
"$OH_MY_PIG_BIN" \\
  --model proof/proof-1 \\
  --thinking medium \\
  --no-session \\
  --approve \\
  2>"$base/out/stderr.log"
echo "exit=\$?" >> "$base/out/launch.txt"
EOF
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

send_prompt() {
  local text="$1"
  local wait_s="$2"
  q type "\"$text\""
  q key Enter
  sleep "$wait_s"
}

run_lane() {
  local name="$1"
  local base="$ROOT/$name"
  local out="$base/out"

  reset_llm
  start_tern "$out" "$ROOT/serve-${name}.log"

  q run "\"$base/launch.sh\""
  # Wait for the program to open its native surface (up to 15s).
  for _ in $(seq 1 75); do
    if [ -s "$out/tsp.jsonl" ] && C tree "[class*='sf-main']" >/dev/null 2>&1; then
      local cnt
      cnt="$(C tree ".sf-main" | python3 -c "import json,sys; print(json.load(sys.stdin).get('count',0))" 2>/dev/null || echo 0)"
      if [ "$cnt" -ge 1 ]; then
        break
      fi
    fi
    sleep 0.2
  done
  sleep 1.2
  capture_step "$out" "00-started"

  # Step 1: Plain Markdown reply
  send_prompt "Show a short markdown example." 2.5
  capture_step "$out" "01-markdown"

  # Step 2: Delayed thinking + text -> capture mid-turn working state, then settled
  q type '"Compute 20 + 22 and explain briefly."'
  q key Enter
  sleep 0.9
  capture_step "$out" "02a-working"
  sleep 2.8
  capture_step "$out" "02b-thinking"

  # Step 3: read tool call
  send_prompt "Read notes.txt." 2.8
  capture_step "$out" "03-tool-read"

  # Step 4: bash tool call
  send_prompt "Run a short printf command in bash." 2.8
  capture_step "$out" "04-tool-bash"

  # Step 5: edit tool call
  send_prompt "Update status in notes.txt from draft to verified." 2.8
  capture_step "$out" "05-tool-edit"

  # Step 6: failing tool call
  send_prompt "Read does-not-exist-proof.txt." 2.8
  capture_step "$out" "06-tool-error"

  # Clean exit
  q type '"/quit"'
  q key Enter
  sleep 1.2
  stop_tern
}

setup_omp
setup_pig

echo "Running omp in headless Tern..."
run_lane omp
echo "Running oh-my-pig in headless Tern..."
run_lane pig

echo "Comparing captured trees and TSP wire frames..."
python3 "$SCRIPT_DIR/compare.py" \
  --omp-dir "$ROOT/omp/out" \
  --pig-dir "$ROOT/pig/out" \
  --md-out "$ROOT/report.md" \
  --json-out "$ROOT/report.json"

echo "Done: $ROOT/report.md and $ROOT/report.json"
