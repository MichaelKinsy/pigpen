#!/usr/bin/env bash
# Plain-PTY ANSI proof script for internal/ansi.
#
# Drives an oh-my-pig binary against piglets/oh-my-pig/proof/llm_server.py
# (scripted conversation in turns.json covering markdown, thinking, read, bash,
# edit, and read error) using piglets/oh-my-pig/proof/ansi_capture.py's VT100
# screen model. Runs completely offline (no network, no real model, all homes
# under a throwaway temp dir, TERN_* and TERM_PROGRAM stripped, PIG_TUI_NATIVE=0)
# and asserts omp's welcome banner, status line, compact read rows, bash card,
# edit diff card, and absence of extension load failures.
#
# Usage:
#   internal/ansi/testdata/pty-proof.sh <path-to-oh-my-pig-binary> [output-file]
set -euo pipefail

if [ $# -lt 1 ]; then
  echo "usage: $0 <oh-my-pig-binary> [output-file]" >&2
  exit 2
fi

BIN="$1"
OUT="${2:-}"

if [ ! -x "$BIN" ]; then
  echo "pty-proof: binary not executable: $BIN" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../../../../../.." && pwd)"
PROOF_DIR="$REPO_ROOT/piglets/oh-my-pig/proof"

if [ ! -f "$PROOF_DIR/ansi_capture.py" ] || [ ! -f "$PROOF_DIR/llm_server.py" ] || [ ! -f "$PROOF_DIR/turns.json" ]; then
  echo "pty-proof: proof helpers not found under $PROOF_DIR" >&2
  exit 2
fi

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/omp-ansi-pty-proof.XXXXXX")"
TMP_REAL="$(python3 -B -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$TMP_ROOT")"
RUN_TAG="ansi-pty-proof-$$-$(python3 -B -c 'import uuid; print(uuid.uuid4().hex[:8])')"
LLM_PID=""

cleanup() {
  python3 -B "$PROOF_DIR/harness_isolation.py" reap --tag "$RUN_TAG" >/dev/null 2>&1 || true
  if [ -n "$LLM_PID" ]; then
    kill "$LLM_PID" 2>/dev/null || true
    wait "$LLM_PID" 2>/dev/null || true
    LLM_PID=""
  fi
  rm -rf "$TMP_ROOT"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

mkdir -p \
  "$TMP_REAL/home/.config" \
  "$TMP_REAL/home/.local/share" \
  "$TMP_REAL/home/.local/state" \
  "$TMP_REAL/home/.cache" \
  "$TMP_REAL/pighome" \
  "$TMP_REAL/agent" \
  "$TMP_REAL/work" \
  "$TMP_REAL/tmp" \
  "$TMP_REAL/out"

cat > "$TMP_REAL/work/notes.txt" <<'EOF'
project: oh-my-pig
status: draft
owner: proof-harness
EOF

# Start scripted loopback OpenAI-compatible server (no external network).
python3 -B "$PROOF_DIR/llm_server.py" \
  --script "$PROOF_DIR/turns.json" \
  --url-file "$TMP_REAL/llm.url" \
  --log "$TMP_REAL/llm.log.jsonl" \
  >"$TMP_REAL/llm.stdout" 2>"$TMP_REAL/llm.stderr" &
LLM_PID=$!

for _ in $(seq 1 100); do
  [ -s "$TMP_REAL/llm.url" ] && break
  sleep 0.05
done
if [ ! -s "$TMP_REAL/llm.url" ]; then
  echo "pty-proof: llm_server.py failed to start" >&2
  cat "$TMP_REAL/llm.stderr" >&2 || true
  exit 1
fi
LLM_URL="$(cat "$TMP_REAL/llm.url")"
LLM_ORIGIN="${LLM_URL%/v1}"

cat > "$TMP_REAL/agent/models.json" <<EOF
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

cat > "$TMP_REAL/agent/settings.json" <<'EOF'
{
  "lastChangelogVersion": "1.5.2",
  "defaultThinkingLevel": "medium"
}
EOF

cat > "$TMP_REAL/agent/trust.json" <<EOF
{
  "$TMP_REAL/work": true
}
EOF

cat > "$TMP_REAL/launch.sh" <<EOF
#!/bin/sh
for _v in \$(env | cut -d= -f1); do
  case "\$_v" in
    TERN_*|TERM_PROGRAM*|TMUX*|STY|ZELLIJ*|OMP_*|PI_*|PIG_*|\
    OPENAI_*|ANTHROPIC_*|GEMINI_*|COPILOT_*|GOOGLE_*|\
    HTTP_PROXY|http_proxy|HTTPS_PROXY|https_proxy|ALL_PROXY|all_proxy)
      unset "\$_v" 2>/dev/null || true
      ;;
  esac
done
export HOME="$TMP_REAL/home"
export USERPROFILE="$TMP_REAL/home"
export PIG_HOME="$TMP_REAL/pighome"
export PIG_CODING_AGENT_DIR="$TMP_REAL/agent"
export PI_CODING_AGENT_DIR="$TMP_REAL/agent"
export XDG_CONFIG_HOME="$TMP_REAL/home/.config"
export XDG_DATA_HOME="$TMP_REAL/home/.local/share"
export XDG_STATE_HOME="$TMP_REAL/home/.local/state"
export XDG_CACHE_HOME="$TMP_REAL/home/.cache"
export TMPDIR="$TMP_REAL/tmp"
export TEMP="$TMP_REAL/tmp"
export TMP="$TMP_REAL/tmp"
export PIG_TUI_NATIVE=0
export PI_TUI_NATIVE=0
export TERN_TSP=0
export TERM=xterm-256color
export ORACLE_RUN_TAG="$RUN_TAG"
export NO_PROXY="127.0.0.1,localhost,::1"
cd "$TMP_REAL/work"
exec "$BIN" \
  --model proof/proof-1 \
  --thinking medium \
  --no-session \
  --offline \
  2>"$TMP_REAL/out/stderr.log"
EOF
chmod +x "$TMP_REAL/launch.sh"

STEPS_JSON='[
  {"send": "Show a short markdown example.", "expect_next": 1, "wait": 10.0},
  {"send": "Compute 20 + 22 and explain briefly.", "expect_next": 2, "wait": 12.0},
  {"send": "Read notes.txt.", "expect_next": 4, "wait": 10.0},
  {"send": "Run a short printf command in bash.", "expect_next": 6, "wait": 10.0},
  {"send": "Update status in notes.txt from draft to verified.", "expect_next": 8, "wait": 10.0},
  {"send": "Read does-not-exist-proof.txt.", "expect_next": 10, "wait": 10.0}
]'

SCREEN_OUT="$TMP_REAL/out/screen.txt"
SCROLL_OUT="$TMP_REAL/out/scrollback.txt"

python3 -B "$PROOF_DIR/ansi_capture.py" run \
  --launch "$TMP_REAL/launch.sh" \
  --steps-json "$STEPS_JSON" \
  --rows 40 \
  --cols 120 \
  --startup-wait 2.5 \
  --screen-out "$SCREEN_OUT" \
  --scrollback-out "$SCROLL_OUT" \
  --llm-origin "$LLM_ORIGIN" \
  --run-tag "$RUN_TAG" >/dev/null

# Verify expected omp terminal markers, tool cards, and clean execution.
python3 -B - "$SCREEN_OUT" "$SCROLL_OUT" "$TMP_REAL/out/stderr.log" <<'PY'
import sys

screen = open(sys.argv[1], encoding="utf-8").read()
scroll = open(sys.argv[2], encoding="utf-8").read()
stderr = open(sys.argv[3], encoding="utf-8").read()

errors = []
if not screen.strip():
    errors.append("captured screen is empty")
if "Failed to load extension" in scroll or "Failed to load extension" in stderr:
    errors.append("extension load failure detected")
if "RenderOverflowError" in scroll or "RenderOverflowError" in stderr:
    errors.append("RenderOverflowError detected")

# Assert omp welcome banner, status line, compact read rows, bash card, and edit card.
checks = [
    ("welcome pi logo", "████████████", scroll),
    ("status line pi brand", "π", screen),
    ("compact read success row", "✔ Read notes.txt", scroll),
    ("compact read error row", "✘ Read does-not-exist-proof.txt", scroll),
    ("bash framed output divider", "Output", scroll),
    ("bash output content", "alpha", scroll),
    ("edit diff card header", "✎ Edit:", scroll),
    ("edit diff stats brackets", "⟦+1/-1⟧", scroll),
    ("edit diff gutter", "status: verified", scroll),
]
for label, needle, haystack in checks:
    if needle not in haystack:
        errors.append(f"missing {label!r}: expected {needle!r}")

# Ensure no row in the captured screen exceeds 120 columns.
for idx, line in enumerate(screen.splitlines(), 1):
    if len(line) > 120:
        errors.append(f"screen line {idx} exceeds 120 chars ({len(line)})")

if errors:
    for e in errors:
        sys.stderr.write(f"pty-proof FAIL: {e}\n")
    sys.exit(1)
PY

if [ -n "$OUT" ]; then
  cp "$SCREEN_OUT" "$OUT"
else
  cat "$SCREEN_OUT"
fi
