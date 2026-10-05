#!/usr/bin/env bash
# Run a ported extension in a real pig, interactively, in a detached tmux session, against the scripted model
# of the equivalence harness, and capture what the terminal shows. Nothing touches the real ~/.pig: HOME,
# PIG_HOME and the agent directory are private to the run.
#
#   scripts/tmux-demo.sh <name> <extension dir> <turns.json> <step>...
#
# Environment: PIG_BIN (the pig to run), PIGEQ (the pigeq binary, for its scripted model), TMUX_BIN (tmux, default
# tmux), PATH with `go` and `node` (a source extension is built by pig on first use), DEMO_CONFIG_FILE and
# DEMO_CONFIG (optional: a file under the private ~/.config and its content), DEMO_ROOT (default: a new directory).
# A step is `type:<text>` (typed and sent with Enter), `key:<tmux key>` or `wait:<n>` (until the model has been
# asked n times) or `grab:<label>` (write the pane to <root>/pane-<label>.txt). The root is printed last.
set -euo pipefail
name=$1 ext=$2 turns=$3
shift 3
: "${PIG_BIN:?}" "${PIGEQ:?}"
tmux=${TMUX_BIN:-tmux}
root=${DEMO_ROOT:-$(mktemp -d)}
mkdir -p "$root"/{home,pighome,agent,work}
if [ -n "${DEMO_CONFIG_FILE:-}" ]; then
  mkdir -p "$root/home/$(dirname "$DEMO_CONFIG_FILE")"
  printf '%s\n' "${DEMO_CONFIG:-}" >"$root/home/$DEMO_CONFIG_FILE"
fi
# pigeq llm serves until its stdin closes: hold a pipe open for the length of the run.
exec 3< <(sleep 86400)
sleep_pid=$!
"$PIGEQ" llm --script "$turns" --log "$root/llm.log" <&3 >"$root/llm.url" 2>"$root/llm.err" &
llm_pid=$!
cleanup() { kill "$llm_pid" "$sleep_pid" 2>/dev/null || true; "$tmux" -L "demo-$name" kill-server 2>/dev/null || true; }
trap cleanup EXIT
for _ in $(seq 1 100); do [ -s "$root/llm.url" ] && break; sleep 0.1; done
base=$(head -1 "$root/llm.url")
cat >"$root/agent/models.json" <<JSON
{"providers":{"eq-llm":{"baseUrl":"$base","api":"openai-completions","apiKey":"demo-key","models":[{"id":"eq-1","name":"eq-1","reasoning":false,"input":["text"],"contextWindow":100000,"maxTokens":4096,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}
JSON
ext_abs=$(cd "$ext" && pwd)
env_prefix="HOME=$root/home PIG_HOME=$root/pighome PIG_CODING_AGENT_DIR=$root/agent PI_CODING_AGENT_DIR=$root/agent GOCACHE=${GOCACHE:-} GOMODCACHE=${GOMODCACHE:-} PIG_OFFLINE=1 TERM=xterm-256color"
"$tmux" -L "demo-$name" new-session -d -s "$name" -x 110 -y 36 -c "$root/work" \
  "env $env_prefix $PIG_BIN --provider eq-llm --model eq-1 --no-extensions --no-skills --no-prompt-templates --no-themes --no-context-files --no-session --offline -e $ext_abs; sleep 3600"
requests() { grep -c '"ch":"llm"' "$root/llm.log" 2>/dev/null || true; }
pane() { "$tmux" -L "demo-$name" capture-pane -p -t "$name"; }
# Wait for the interface to draw before typing.
for _ in $(seq 1 600); do pane | grep -q . && break; sleep 0.1; done
sleep 3
for step in "$@"; do
  case $step in
    type:*) "$tmux" -L "demo-$name" send-keys -t "$name" -l "${step#type:}"; sleep 0.3; "$tmux" -L "demo-$name" send-keys -t "$name" Enter ;;
    key:*) "$tmux" -L "demo-$name" send-keys -t "$name" "${step#key:}" ;;
    wait:*) for _ in $(seq 1 600); do [ "$(requests)" -ge "${step#wait:}" ] && break; sleep 0.1; done; sleep 2 ;;
    grab:*) sleep 1.5; pane >"$root/pane-${step#grab:}.txt" ;;
    *) echo "unknown step $step" >&2; exit 2 ;;
  esac
done
echo "$root"
