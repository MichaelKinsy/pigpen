#!/usr/bin/env bash
# End-to-end belt test on the sandbox herdr-core (never the default core, never a real Herdr home).
#   line: pig -> pig -> oven (test -f pigtorio-fake-1.txt) -> review -> barn
# plus a crate handed straight to a third pig. Needs: hc, jq, git, go (to build if bin is missing).
set -euo pipefail

export HC_SESSION=hfarm HC_SOCKET=/tmp/hfarm/rt/hc.sock XDG_RUNTIME_DIR=/tmp/hfarm/rt \
  XDG_STATE_HOME=/tmp/hfarm/state XDG_CONFIG_HOME=/tmp/hfarm/cfg
[ -S "$HC_SOCKET" ] || { echo "sandbox core is not running ($HC_SOCKET)"; exit 2; }

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=$ROOT/engine/bin/pigtorio
[ -x "$BIN" ] || (cd "$ROOT/engine" && go build -o bin/pigtorio ./cmd/pigtorio)

H=/tmp/hfarm/e2e-home           # the data home: NOT the sandbox Herdr home
D=$H/pigtorio
REPO=$(mktemp -d /tmp/hfarm/e2e-repo.XXXXXX)
TERMS=()
FAILED=1

cleanup() {
  for t in "${TERMS[@]:-}"; do
    [ -n "$t" ] || continue
    hc kill "$t" >/dev/null 2>&1 || true
    hc rm "$t" >/dev/null 2>&1 || true
  done
  if [ "$FAILED" = 0 ]; then rm -rf "$H" "$REPO"; else echo "kept $H and $REPO for inspection"; fi
}
trap cleanup EXIT

fail() { echo "FAIL: $*"; [ -z "${ENGINE:-}" ] || { echo "--- engine log"; hc read "$ENGINE" --lines 60 2>&1 | tail -40 || true; }; echo "--- state.json"; cat "$D/state/state.json" 2>/dev/null || true; exit 1; }
ok() { echo "ok: $*"; }

rm -rf "$H"; mkdir -p "$D/inbox" "$D/state" "$D/sessions"
git -C "$REPO" init -q -b main
git -C "$REPO" config user.email e2e@example.com
git -C "$REPO" config user.name "E2E"
git -C "$REPO" config commit.gpgsign false
echo "hello" > "$REPO/README"
git -C "$REPO" add -A && git -C "$REPO" commit -q -m init

start_pig() { # name -> address in $REPLY (runs in this shell so TERMS is kept)
  local out
  out=$(hc run --bg --json --no-parent --name "e2e-pig-$(tr A-Z a-z <<<"$1")" --cwd "$REPO" -- \
    "$BIN" fakepig --name "$1" --work 1s --session-dir "$D/sessions/$1") || fail "hc run fakepig $1: $out"
  REPLY=$(jq -r '.address // empty' <<<"$out")
  [ -n "$REPLY" ] || fail "hc run fakepig $1 gave no address: $out"
  TERMS+=("$REPLY")
}
wait_for() { # seconds description command...
  local secs=$1 what=$2; shift 2
  local end=$((SECONDS + secs))
  until "$@" >/dev/null 2>&1; do
    [ $SECONDS -lt $end ] || fail "timed out waiting for $what"
    sleep 0.3
  done
}
state() { jq -r "$1" "$D/state/state.json"; }
crate_is() { [ "$(state ".crates[] | select(.id==\"$1\") | .state")" = "$2" ]; }
pig_idle() { [ "$(hc get "$1" --json | jq -r '.status.state // empty')" = "idle" ]; }
inbox() { # name json
  printf '%s' "$2" > "$D/inbox/.$1.tmp" && mv "$D/inbox/.$1.tmp" "$D/inbox/$1.json"
}

start_pig Sandy; P1=$REPLY; start_pig Slate; P2=$REPLY; start_pig Rosie; P3=$REPLY
echo "pigs: $P1 $P2 $P3"
for p in $P1 $P2 $P3; do wait_for 15 "$p idle" pig_idle "$p"; done

cat > "$D/farm.json" <<JSON
{
  "pens": { "pen1": { "test": "test -f pigtorio-fake-1.txt", "fence": 4 } },
  "pigs": { "$P1": { "name": "Sandy", "role": "builder", "skin": "sandy" },
            "$P2": { "name": "Slate", "role": "planner", "skin": "slate" },
            "$P3": { "name": "Rosie", "role": "builder", "skin": "pink" } },
  "lines": [ { "id": "l1", "pen": "pen1", "stops": [
      { "kind": "pig", "pig": "$P1" }, { "kind": "pig", "pig": "$P2" },
      { "kind": "oven" }, { "kind": "review" }, { "kind": "barn" } ] } ]
}
JSON

out=$(hc run --bg --json --no-parent --name e2e-engine -- "$BIN" engine --home "$H") || fail "hc run engine: $out"
ENGINE=$(jq -r '.address // empty' <<<"$out"); [ -n "$ENGINE" ] || fail "engine terminal: $out"; TERMS+=("$ENGINE")
engine_up() { [ "$(state '.engine.pid // 0' 2>/dev/null)" -gt 0 ]; }
wait_for 15 "engine heartbeat" engine_up
ok "engine running in terminal $ENGINE (pid $(state .engine.pid))"

# a second engine on the same home exits 0 with a message
second=$("$BIN" engine --home "$H" 2>&1) || fail "second engine exited non-zero: $second"
grep -q "already running" <<<"$second" || fail "second engine did not say it is locked: $second"
ok "second engine refused politely"

# an empty done-when is refused
"$BIN" crate add --home "$H" --title "Nameless" --done-when "" >/dev/null
acked() { [ "$(state '[.acks[] | select(.ok==false)] | length')" -ge 1 ]; }
wait_for 10 "refusal ack" acked
[ "$(state '.crates | length')" = 0 ] || fail "a crate without done-when was accepted"
ok "crate without done-when refused: $(state '.acks[0].error')"

# the belt
"$BIN" crate add --home "$H" --title "Add the fake file" --text "Create the file." \
  --done-when "pigtorio-fake-1.txt exists" --pen pen1 >/dev/null
has_crate() { [ "$(state '.crates | length')" = 1 ]; }
wait_for 10 "crate on the board" has_crate
C1=$(state '.crates[0].id')
[ "$(state '.crates[0].state')" = board ] || fail "new crate is not on the board"
ok "crate $C1 on the board"

inbox e2e-run "{\"op\":\"crate.run\",\"id\":\"$C1\",\"line\":\"l1\"}"
crate_holding() { crate_is "$1" holding; }
wait_for 90 "$C1 at Review" crate_holding "$C1"
[ "$(state ".crates[] | select(.id==\"$C1\") | .at.kind")" = review ] || fail "holding but not at review"
test -f "$REPO/pigtorio-fake-1.txt" || fail "the pig wrote no file"
[ "$(state ".crates[] | select(.id==\"$C1\") | .tokens")" -gt 0 ] || fail "no tokens counted"
state ".crates[] | select(.id==\"$C1\") | .report" | grep -q "Goal:" || fail "no hand-off report"
ok "crate $C1 reached Review with a report ($(state ".crates[] | select(.id==\"$C1\") | .tokens") tokens); oven log: '$(state ".crates[] | select(.id==\"$C1\") | .log")'"

inbox e2e-approve "{\"op\":\"review.approve\",\"id\":\"$C1\"}"
crate_shipped() { crate_is "$1" shipped; }
wait_for 30 "$C1 shipped" crate_shipped "$C1"
COMMIT=$(state ".crates[] | select(.id==\"$C1\") | .commit")
[ -n "$COMMIT" ] || fail "shipped without a commit"
[ "$(git -C "$REPO" log -1 --format=%s)" = "Add the fake file" ] || fail "commit subject is wrong"
[ "$(git -C "$REPO" rev-parse --short HEAD)" = "$COMMIT" ] || fail "state commit is not HEAD"
[ -z "$(git -C "$REPO" status --porcelain)" ] || fail "tree not clean after ship"
git -C "$REPO" show --stat --format=%s HEAD | sed 's/^/    /'
ok "crate $C1 shipped as commit $COMMIT"

# each hop is in crates.jsonl
echo "crates.jsonl for $C1:"
jq -c "select(.crate==\"$C1\") | [.event,.pig,.detail]" "$D/crates.jsonl" | sed 's/^/    /'
got=$(jq -r "select(.crate==\"$C1\") | .event" "$D/crates.jsonl" | paste -sd, -)
want="added,moved,done,moved,done,moved,oven-green,moved,holding,approved,moved,shipped"
[ "$got" = "$want" ] || fail "events: got $got, want $want"
[ "$(jq -r "select(.crate==\"$C1\" and .event==\"moved\") | .pig // \"\"" "$D/crates.jsonl" | paste -sd, -)" = "$P1,$P2,,," ] || fail "moved hops are not pig,pig,oven,review,barn"
ok "crates.jsonl shows every hop"
[ "$(state .stats.trufflesOut24h)" = 1 ] && [ "$(state .stats.cratesIn24h)" = 1 ] && [ "$(state .stats.tokens24h)" -gt 0 ] || fail "stats: $(jq -c .stats "$D/state/state.json")"
ok "stats: $(jq -c .stats "$D/state/state.json")"

# a crate handed straight to one pig holds with its report
"$BIN" crate add --home "$H" --title "Hand me" --done-when "a hand-off" --text "Just reply." >/dev/null
has_two() { [ "$(state '.crates | length')" = 2 ]; }
wait_for 10 "second crate" has_two
C2=$(state '.crates[1].id')
inbox e2e-hand "{\"op\":\"crate.hand\",\"id\":\"$C2\",\"to\":\"$P3\"}"
wait_for 60 "$C2 holding" crate_holding "$C2"
state ".crates[] | select(.id==\"$C2\") | .report" | grep -q "Goal:" || fail "handed crate has no report"
[ "$(state ".crates[] | select(.id==\"$C2\") | .line // \"\"")" = "" ] || fail "handed crate is on a line"
ok "crate $C2 handed to $P3 is holding with its report"

FAILED=0
echo "E2E PASS"
