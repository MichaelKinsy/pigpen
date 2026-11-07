#!/usr/bin/env bash
# Offline test of reject.sh and restart.sh: the real basket binary against its fake gateway, a stub kubectl.
# Needs: a built basket ($BASKET, default ./basket), jq, bc, python3, curl-free. Run from this directory.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
BASKET=${BASKET:-$HERE/basket/basket}
T=$(mktemp -d); mkdir "$T/bin"
cleanup() { [ -z "${FAKE:-}" ] || kill "$FAKE" 2>/dev/null || true; rm -rf "$T"; }
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
PORT=${PORT:-18931}
cat >"$T/bin/kubectl" <<'S'
#!/bin/bash
# stub: pods are worker-<n>; deleting one makes worker-<n+1>; `logs` prints a mock log line for a rejected [approve] run
n=$(cat "$STUB_DIR/n" 2>/dev/null || echo 1)
case "$*" in
  *"get pods"*) echo "worker-$n" ;;
  *"delete pod worker-$n"*) echo $((n + 1)) >"$STUB_DIR/n" ;;
  *"delete pod"*) echo "stub: wrong pod name: $*" >&2; exit 1 ;;
  *" wait "*) ;;
  *" logs "*)
    rt="Tool call was rejected by user. Reason: ${STUB_REASON}"
    sha=$(printf '%s' "$rt" | sha256sum | cut -d' ' -f1)
    printf '{"step":"final","results":"%s","text":"APPROVE-DONE %s"}\n' "$rt" "$sha" ;;
esac
S
chmod +x "$T/bin/kubectl"
export STUB_DIR="$T" PATH="$T/bin:$PATH" STUB_REASON='stub reason' URL="http://127.0.0.1:$PORT"
echo 1 >"$T/n"
"$BASKET" -serve-fake "127.0.0.1:$PORT" 2>/dev/null & FAKE=$!
sleep 1

# reject series: every run rejected with the reason, reason and sha visible in the (stubbed) mock log
OUT="$T/rej" AGENT=a N=2 REASON='stub reason' BASKET="$BASKET" "$HERE/reject.sh" >/dev/null || fail "reject.sh failed: $(cat "$T/rej/basket.err" 2>/dev/null)"
[ "$(jq -s 'map(select(.pass)) | length' "$T/rej/runs.jsonl")" = 2 ] || fail "reject runs: $(cat "$T/rej/runs.jsonl")"
jq -e '.pass == 2 and .reason_visible == 2' "$T/rej/summary.json" >/dev/null || fail "reject summary"
# a reason that never reached the model must fail the series
set +e; STUB_REASON='other' OUT="$T/rej2" AGENT=a N=1 REASON='stub reason' BASKET="$BASKET" "$HERE/reject.sh" >/dev/null 2>&1; rc=$?; set -e
[ "$rc" != 0 ] || fail "reject.sh passed although the logged reason differs"

kill "$FAKE"; wait "$FAKE" 2>/dev/null || true
"$BASKET" -serve-fake "127.0.0.1:$PORT" -fake-faults 'rpc:-32004,empty' 2>/dev/null & FAKE=$!
sleep 1
# restart series: faults are queued for the follow-ups of the first idle run: rpc -32004, then an empty reply, then success
OUT="$T/rs" AGENT=a APPROVE_AGENT=b N=2 BASKET="$BASKET" ATTEMPT_TIMEOUT=5s RETRY_MAX=30s BACKOFF=20ms BACKOFF_MAX=50ms READY_TIMEOUT=10 "$HERE/restart.sh" >/dev/null || fail "restart.sh failed: $(cat "$T/rs/basket.err")"
[ "$(awk 'END{print NR}' "$T/rs/runs.jsonl")" = 4 ] || fail "run count: $(cat "$T/rs/runs.jsonl")"
jq -e 'select(.variant=="idle" and .run==1) | .series.first_attempt.rpc_code == -32004 and .series.attempts_to_success == 3 and .old_pod=="worker-1" and .new_pod=="worker-2"' "$T/rs/runs.jsonl" >/dev/null || fail "idle run 1: $(jq -c 'select(.variant=="idle" and .run==1)' "$T/rs/runs.jsonl")"
jq -e '.variants.idle | .runs == 2 and .first_attempt_success == 1 and .eventual_success == 2' "$T/rs/summary.json" >/dev/null || fail "idle summary"
jq -e '.variants.approval | .first_attempt_success == 2 and .eventual_success == 2 and .recall_after_resume_ok == 2' "$T/rs/summary.json" >/dev/null || fail "approval summary: $(cat "$T/rs/summary.json")"
[ "$(cat "$T/n")" = 5 ] || fail "expected 4 pod deletions by exact name, counter $(cat "$T/n")"
echo ok
