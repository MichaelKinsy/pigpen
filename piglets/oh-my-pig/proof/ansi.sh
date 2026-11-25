#!/usr/bin/env bash
# Plain-PTY (no Tern) ANSI rendering proof: runs omp and oh-my-pig on the same
# scripted conversation (turns.json) in a fixed-size pseudo-terminal with all
# TERN_* / TERM_PROGRAM variables unset and TERM=xterm-256color, captures each
# program's final VT screen + scrollback as text, and writes a side-by-side diff.
#
# Network isolation: starts a loopback refusing proxy (HTTP_PROXY/HTTPS_PROXY/
# ALL_PROXY + NO_PROXY=127.0.0.1,localhost,::1) and runs children under macOS
# sandbox-exec loopback-only where available; fails if any non-loopback network
# attempt is logged.
#
# Process hygiene: tags every child with a unique ORACLE_RUN_TAG, sweeps all
# tagged PIDs (including __omp_worker_daemon) after each side and on
# EXIT/INT/TERM/HUP, and fails if any tagged process remains.
#
# Environment overrides:
#   OMP_BIN         Path to omp binary (default: omp on PATH)
#   OH_MY_PIG_BIN   Path to oh-my-pig binary (default: /tmp/omp-bin/oh-my-pig)
#   ANSI_MODE       both | omp-only | pig-only (auto-detected if unset)
#   ANSI_OUT        Output directory (default: /tmp/omp-proof2/ansi; must be under /tmp or /var/tmp)
#   ANSI_ROWS       PTY rows (default: 40)
#   ANSI_COLS       PTY columns (default: 120)
#   ORACLE_KEEP     Set to 1 to keep temp homes/workspaces on failure

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${ANSI_OUT:-/tmp/omp-proof2/ansi}"
OMP_BIN="${OMP_BIN:-$(command -v omp || true)}"
OH_MY_PIG_BIN="${OH_MY_PIG_BIN:-/tmp/omp-bin/oh-my-pig}"
ROWS="${ANSI_ROWS:-40}"
COLS="${ANSI_COLS:-120}"

# Guard rm -rf "$ROOT": refuse anything outside /tmp or /private/tmp or /var/tmp
case "$ROOT" in
  /tmp/*|/private/tmp/*|/var/tmp/*) ;;
  *)
    echo "error: refusing to wipe ANSI_OUT outside /tmp: $ROOT" >&2
    exit 2
    ;;
esac

MODE="${ANSI_MODE:-}"
if [ -z "$MODE" ]; then
  if [ -n "$OH_MY_PIG_BIN" ] && [ -x "$OH_MY_PIG_BIN" ]; then
    MODE="both"
  else
    MODE="omp-only"
  fi
fi

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

check_free_disk "$ROOT"

RUN_TAG="ansi-$$-$(python3 -B -c 'import uuid; print(uuid.uuid4().hex[:12])')"
LLM_PID=""
PROXY_PID=""
RUN_OK=0

cleanup() {
  # Reap every tagged descendant first (e.g. __omp_worker_daemon)
  python3 -B "$SCRIPT_DIR/harness_isolation.py" reap --tag "$RUN_TAG" >/dev/null 2>&1 || true
  if [ -n "$LLM_PID" ] && kill -0 "$LLM_PID" 2>/dev/null; then
    kill "$LLM_PID" 2>/dev/null || true
    wait "$LLM_PID" 2>/dev/null || true
    LLM_PID=""
  fi
  if [ -n "$PROXY_PID" ] && kill -0 "$PROXY_PID" 2>/dev/null; then
    kill "$PROXY_PID" 2>/dev/null || true
    wait "$PROXY_PID" 2>/dev/null || true
    PROXY_PID=""
  fi
  rm -rf "$ROOT/.natives-cache"
  if [ "$RUN_OK" = "1" ] || [ "${ORACLE_KEEP:-}" != "1" ]; then
    rm -rf "$ROOT/omp" "$ROOT/pig" "$ROOT/llm.url" "$ROOT/proxy.url"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

rm -rf "$ROOT"
mkdir -p "$ROOT/omp" "$ROOT/pig" "$ROOT/.natives-cache"
ROOT_REAL="$(python3 -B -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$ROOT")"

seed_workspace() {
  local work="$1"
  mkdir -p "$work"
  cat > "$work/notes.txt" <<'EOF'
project: oh-my-pig
status: draft
owner: proof-harness
EOF
  cat > "$work/sample.js" <<'EOF'
console.log("hi");
EOF
}

# Start refusing proxy listener to catch & reject any non-loopback network attempt
python3 -B "$SCRIPT_DIR/harness_isolation.py" proxy \
  --url-file "$ROOT_REAL/proxy.url" \
  --log "$ROOT_REAL/proxy.log.jsonl" &
PROXY_PID=$!

for _ in $(seq 1 100); do
  [ -s "$ROOT_REAL/proxy.url" ] && break
  sleep 0.05
done
if [ ! -s "$ROOT_REAL/proxy.url" ]; then
  echo "error: refusing proxy failed to start" >&2
  exit 1
fi
PROXY_URL="$(cat "$ROOT_REAL/proxy.url")"

# Start scripted OpenAI-compatible server on an ephemeral loopback port.
python3 -B "$SCRIPT_DIR/llm_server.py" \
  --script "$SCRIPT_DIR/turns.json" \
  --url-file "$ROOT_REAL/llm.url" \
  --log "$ROOT_REAL/llm.log.jsonl" \
  >"$ROOT_REAL/llm.stdout" 2>"$ROOT_REAL/llm.stderr" &
LLM_PID=$!

for _ in $(seq 1 100); do
  [ -s "$ROOT_REAL/llm.url" ] && break
  sleep 0.05
done
if [ ! -s "$ROOT_REAL/llm.url" ]; then
  echo "error: llm_server.py failed to start" >&2
  cat "$ROOT_REAL/llm.stderr" >&2 || true
  exit 1
fi
LLM_URL="$(cat "$ROOT_REAL/llm.url")"
LLM_ORIGIN="${LLM_URL%/v1}"

reset_llm() {
  python3 -B -c "import urllib.request; urllib.request.urlopen(urllib.request.Request('$LLM_ORIGIN/reset', method='POST')).read()" >/dev/null
}

# Steps driven by evidence: llm_server /health completed turn cursor (`expect_next`, 0 in_flight)
# + PTY quiet + visible precondition/postcondition markers (`wait_for_visible_any`, `expect_visible_any`)
# + program prompt/editor marker ready. Never types on a blind timer.
STEPS_JSON='[
  {"send": "Show a short markdown example.", "expect_next": 1, "wait": 20.0},
  {"send": "Compute 20 + 22 and explain briefly.", "expect_next": 2, "wait": 25.0},
  {"send": "Read notes.txt.", "expect_next": 4, "wait": 20.0},
  {"send": "Run a short printf command in bash.", "expect_next": 6, "wait": 20.0},
  {"send": "Update status in notes.txt from draft to verified.", "expect_next": 8, "wait": 20.0},
  {"send": "Read does-not-exist-proof.txt.", "expect_next": 10, "wait": 20.0},
  {"send": "Initialize and complete a short todo list.", "expect_next": 13, "wait": 25.0},
  {"send": "Ask me to choose a mode.", "expect_next": 14, "wait": 20.0, "expect_visible_any": ["Choose a mode:"], "require_prompt_ready": false},
  {"enter": true, "wait_for_visible_any": ["Choose a mode:"], "expect_next": 15, "wait": 20.0},
  {"send": "Run a short python eval.", "expect_next": 17, "wait": 25.0},
  {"send": "Spawn a proof subagent and wait for it.", "expect_next": 20, "wait": 30.0},
  {"send": "Run a web search offline.", "expect_next": 22, "wait": 20.0},
  {"send": "Write generated.txt and search workspace files.", "expect_next": 26, "wait": 25.0},
  {"send": "Check LSP status and preview an AST edit.", "expect_next": 30, "wait": 25.0},
  {"send": "Print 30 numbered lines.", "expect_next": 32, "wait": 25.0, "snapshot": "folded"},
  {"raw_keys": "\u000f", "wait": 10.0, "expect_visible_any": ["Tool output expansion: enabled", "Tool output: expanded", "fold_line_30"], "snapshot": "expanded"},
  {"send": "Trigger a simulated provider error.", "expect_next": 33, "wait": 20.0, "expect_visible_any": ["Proof simulated provider error"], "snapshot": "error"},
  {"send": "/plan", "enter": true, "wait": 10.0, "expect_visible_any": ["Plan mode enabled."], "snapshot": "plan"},
  {"resize": {"cols": 80, "rows": 24}, "wait": 10.0, "snapshot": "narrow80"}
]'
# Emit env-stripping shell snippet (removes prefixes cleanly, not just a static list)
write_strip_env_block() {
  cat <<'EOF'
for _v in $(env | cut -d= -f1); do
  case "$_v" in
    TERN_*|TERM_PROGRAM*|TMUX*|STY|ZELLIJ*|KITTY_*|TERMINFO|\
    NO_COLOR|FORCE_COLOR|CLICOLOR|CLICOLOR_FORCE|COLORTERM|NODE_DISABLE_COLORS|CI|TF_BUILD|AGENT|OMPCODE|\
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

setup_omp() {
  local base="$ROOT_REAL/omp"
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
        cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }
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
export PI_TUI_NATIVE=0
export PI_NOTIFICATIONS=off
export OTEL_SDK_DISABLED=true
export XDG_CONFIG_HOME="$base/home/.config"
export XDG_DATA_HOME="$base/home/.local/share"
export XDG_STATE_HOME="$base/home/.local/state"
export XDG_CACHE_HOME="$base/home/.cache"
export TMPDIR="$base/tmp"
export PI_NATIVES_DIR="$ROOT_REAL/.natives-cache"
if [ -n "${ORACLE_TOOL_DIRS:-}" ]; then
  export PATH="${ORACLE_TOOL_DIRS}:\$PATH"
fi
export GIT_TERMINAL_PROMPT=0
export TERM=xterm-256color
export COLORTERM=truecolor
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
exec "$OMP_BIN" \\
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
EOF
  } > "$base/launch.sh"
  chmod +x "$base/launch.sh"
}

setup_pig() {
  local base="$ROOT_REAL/pig"
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
export XDG_CONFIG_HOME="$base/home/.config"
export XDG_DATA_HOME="$base/home/.local/share"
export XDG_STATE_HOME="$base/home/.local/state"
export XDG_CACHE_HOME="$base/home/.cache"
export TMPDIR="$base/tmp"
if [ -n "${ORACLE_TOOL_DIRS:-}" ]; then
  export PATH="${ORACLE_TOOL_DIRS}:\$PATH"
fi
export GIT_TERMINAL_PROMPT=0
export TERM=xterm-256color
export COLORTERM=truecolor
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
exec "$OH_MY_PIG_BIN" \\
  --model proof/proof-1 \\
  --thinking medium \\
  --no-session \\
  --tui-mode regular \\
  --approve \\
  2>"$base/out/stderr.log"
EOF
  } > "$base/launch.sh"
  chmod +x "$base/launch.sh"
}

OMP_RUN_RC=0
PIG_RUN_RC=0
if [ "$MODE" = "both" ] || [ "$MODE" = "omp-only" ]; then
  [ -n "$OMP_BIN" ] && [ -x "$OMP_BIN" ] || { echo "error: omp binary not found" >&2; exit 1; }
  . "$SCRIPT_DIR/omp_version.sh"
  require_omp_reference "$OMP_BIN"
  setup_omp
  reset_llm
  echo "Running omp in plain PTY (${COLS}x${ROWS}, no Tern)..."
  python3 -B "$SCRIPT_DIR/ansi_capture.py" run \
    --launch "$ROOT_REAL/omp/launch.sh" \
    --steps-json "$STEPS_JSON" \
    --side omp \
    --rows "$ROWS" \
    --cols "$COLS" \
    --startup-wait 45.0 \
    --llm-origin "$LLM_ORIGIN" \
    --run-tag "$RUN_TAG" \
    --screen-out "$ROOT_REAL/omp.screen.txt" \
    --scrollback-out "$ROOT_REAL/omp.scrollback.txt" \
    --raw-out "$ROOT_REAL/omp.raw.bin" \
    --meta-out "$ROOT_REAL/omp.meta.json" || OMP_RUN_RC=$?
  python3 -B "$SCRIPT_DIR/harness_isolation.py" reap --tag "$RUN_TAG"
fi

if [ "$MODE" = "both" ] || [ "$MODE" = "pig-only" ]; then
  [ -n "$OH_MY_PIG_BIN" ] && [ -x "$OH_MY_PIG_BIN" ] || { echo "error: oh-my-pig binary not found: $OH_MY_PIG_BIN" >&2; exit 1; }
  setup_pig
  reset_llm
  echo "Running oh-my-pig in plain PTY (${COLS}x${ROWS}, no Tern)..."
  python3 -B "$SCRIPT_DIR/ansi_capture.py" run \
    --launch "$ROOT_REAL/pig/launch.sh" \
    --steps-json "$STEPS_JSON" \
    --side pig \
    --rows "$ROWS" \
    --cols "$COLS" \
    --startup-wait 45.0 \
    --llm-origin "$LLM_ORIGIN" \
    --run-tag "$RUN_TAG" \
    --screen-out "$ROOT_REAL/pig.screen.txt" \
    --scrollback-out "$ROOT_REAL/pig.scrollback.txt" \
    --raw-out "$ROOT_REAL/pig.raw.bin" \
    --meta-out "$ROOT_REAL/pig.meta.json" || PIG_RUN_RC=$?
  python3 -B "$SCRIPT_DIR/harness_isolation.py" reap --tag "$RUN_TAG"
fi

# Verify no tagged process leaked and no non-loopback network attempt happened
python3 -B "$SCRIPT_DIR/harness_isolation.py" check-pids --tag "$RUN_TAG"
if [ -s "$ROOT_REAL/proxy.log.jsonl" ]; then
  echo "ERROR: refusing proxy recorded non-loopback network attempt(s):" >&2
  cat "$ROOT_REAL/proxy.log.jsonl" >&2
  exit 1
fi

RUN_OK=1
rm -rf "$ROOT_REAL/.natives-cache" "$ROOT_REAL/omp" "$ROOT_REAL/pig" "$ROOT_REAL/llm.url" "$ROOT_REAL/proxy.url"
OUT_SIZE="$(du -sh "$ROOT_REAL" 2>/dev/null | awk '{print $1}')"
DIFF_RC=0
if [ "$MODE" = "both" ]; then
    DIFF_ARGS=()
    if [ "${ORACLE_ALLOW_DIFFS:-}" = "1" ]; then
        DIFF_ARGS+=(--allow-diffs)
    fi
    if [ "${ORACLE_ALLOW_TIMING:-}" = "1" ]; then
        DIFF_ARGS+=(--allow-timing)
    fi
  python3 -B "$SCRIPT_DIR/ansi_capture.py" diff \
    --omp-screen "$ROOT_REAL/omp.screen.txt" \
    --pig-screen "$ROOT_REAL/pig.screen.txt" \
    --omp-scrollback "$ROOT_REAL/omp.scrollback.txt" \
    --pig-scrollback "$ROOT_REAL/pig.scrollback.txt" \
    --omp-meta "$ROOT_REAL/omp.meta.json" \
    --pig-meta "$ROOT_REAL/pig.meta.json" \
    --steps-json "$STEPS_JSON" \
    --omp-raw "$ROOT_REAL/omp.raw.bin" \
    --pig-raw "$ROOT_REAL/pig.raw.bin" \
    --screen-regions \
    --json-out "$ROOT_REAL/diff.json" \
    --out "$ROOT_REAL/diff.txt" \
    --width "$COLS" \
    "${DIFF_ARGS[@]}" || DIFF_RC=$?
  python3 -B "$SCRIPT_DIR/ansi_capture.py" diff \
    --omp-screen "$ROOT_REAL/omp.scrollback.txt" \
    --pig-screen "$ROOT_REAL/pig.scrollback.txt" \
    --omp-scrollback "$ROOT_REAL/omp.scrollback.txt" \
    --pig-scrollback "$ROOT_REAL/pig.scrollback.txt" \
    --omp-meta "$ROOT_REAL/omp.meta.json" \
    --pig-meta "$ROOT_REAL/pig.meta.json" \
    --steps-json "$STEPS_JSON" \
    --omp-raw "$ROOT_REAL/omp.raw.bin" \
    --pig-raw "$ROOT_REAL/pig.raw.bin" \
    --json-out "$ROOT_REAL/scrollback-diff.json" \
    --out "$ROOT_REAL/scrollback-diff.txt" \
    --width "$COLS" \
    "${DIFF_ARGS[@]}" || DIFF_RC=$?
  OUT_SIZE="$(du -sh "$ROOT_REAL" 2>/dev/null | awk '{print $1}')"
  if [ "$OMP_RUN_RC" -eq 2 ] || [ "$PIG_RUN_RC" -eq 2 ] || [ "$DIFF_RC" -eq 2 ]; then
    echo "Done (CAPTURE INCOMPLETE): omp_rc=$OMP_RUN_RC pig_rc=$PIG_RUN_RC diff_rc=$DIFF_RC (output size: $OUT_SIZE)" >&2
    exit 2
  fi
  if [ "$OMP_RUN_RC" -ne 0 ] || [ "$PIG_RUN_RC" -ne 0 ]; then
    DIFF_RC=1
  fi
  echo "Done: $ROOT_REAL/omp.screen.txt, $ROOT_REAL/pig.screen.txt, $ROOT_REAL/diff.txt, $ROOT_REAL/scrollback-diff.txt (output size: $OUT_SIZE, diff_rc=$DIFF_RC)"
elif [ "$MODE" = "omp-only" ]; then
  echo "Done (omp-only): $ROOT_REAL/omp.screen.txt, $ROOT_REAL/omp.scrollback.txt (output size: $OUT_SIZE)"
  exit "$OMP_RUN_RC"
else
  echo "Done (pig-only): $ROOT_REAL/pig.screen.txt, $ROOT_REAL/pig.scrollback.txt (output size: $OUT_SIZE)"
  exit "$PIG_RUN_RC"
fi
exit "$DIFF_RC"
