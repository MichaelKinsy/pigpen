#!/usr/bin/env bash
# Offline test of baseline.sh's host-pressure gate and parameterised selectors, with stub kubectl/python3/basket.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir "$T/bin"
printf '#!/bin/sh\necho "kubectl $*" >>"$STUB_LOG"\nexit 0\n' >"$T/bin/kubectl"
printf '#!/bin/sh\necho "python3 $*" >>"$STUB_LOG"\nexit 0\n' >"$T/bin/python3"
printf '#!/bin/sh\necho "basket $*" >>"$STUB_LOG"\nexit 0\n' >"$T/basket"
chmod +x "$T/bin/kubectl" "$T/bin/python3" "$T/basket"
export STUB_LOG="$T/stub.log" PATH="$T/bin:$PATH" BASKET="$T/basket" SETTLE_S=0 SETTLE_END_S=0
export AGENTS='a-basket:basket b-basket:basket' ROUNDS=1 WARM=0 MAX_PSI=35 GATE_WAIT=0
fail() { echo "FAIL: $*" >&2; exit 1; }

echo 'some avg10=90.00 avg60=0.00 avg300=0.00 total=0' >"$T/psi-high"
echo 'some avg10=5.00 avg60=0.00 avg300=0.00 total=0' >"$T/psi-low"

# 1. over the gate, default mode: proceeds and records every session as proceeded despite pressure
OUT="$T/o1" PSI_FILE="$T/psi-high" "$HERE/baseline.sh" >/dev/null
[ "$(awk -F'\t' '$5==1{n++} END{print n+0}' "$T/o1/pressure.tsv")" = 2 ] || fail "proceeded sessions not recorded: $(cat "$T/o1/pressure.tsv")"
grep -q 'PROCEEDED DESPITE PRESSURE' "$T/o1/gate.log" || fail "gate.log lacks the proceeded marker"
[ "$(awk 'END{print NR}' "$T/o1/schedule.tsv")" = 2 ] || fail "sessions did not run"

# 2. over the gate, GATE_MODE=refuse: exit 3 before any session starts
set +e; OUT="$T/o2" PSI_FILE="$T/psi-high" GATE_MODE=refuse "$HERE/baseline.sh" >/dev/null 2>&1; rc=$?; set -e
[ "$rc" = 3 ] || fail "refuse mode exit $rc, want 3"
[ ! -s "$T/o2/schedule.tsv" ] || fail "a session ran in refuse mode"

# 3. under the gate: recorded with over=0, nothing proceeded despite pressure
OUT="$T/o3" PSI_FILE="$T/psi-low" "$HERE/baseline.sh" >/dev/null
[ "$(awk -F'\t' '$5==0{n++} END{print n+0}' "$T/o3/pressure.tsv")" = 2 ] || fail "ok sessions not recorded"

# 4. namespace, pool, selectors and agent group come from the environment; defaults are the proof cluster's
: >"$STUB_LOG"
OUT="$T/o4" PSI_FILE="$T/psi-low" AGENT_NS=ns9 POOL=pool9 MOCK_SEL=app=m9 CONTROLLER_SEL=app=c9 USER_HEADER='x-user-id: u9' "$HERE/baseline.sh" >/dev/null
grep -q 'worker:ns=ns9,sel=ate.dev/worker-pool=pool9' "$STUB_LOG" || fail "pool/namespace not applied"
grep -q 'mock:ns=ns9,sel=app=m9' "$STUB_LOG" && grep -q 'controller:ns=ns9,sel=app=c9' "$STUB_LOG" || fail "selectors not applied"
grep -q -- '-agent ns9/a-basket' "$STUB_LOG" && grep -q -- '-header x-user-id: u9' "$STUB_LOG" || fail "agent group/header not applied"
: >"$STUB_LOG"
OUT="$T/o5" PSI_FILE="$T/psi-low" "$HERE/baseline.sh" >/dev/null
grep -q 'worker:ns=kagent,sel=ate.dev/worker-pool=kagent-default' "$STUB_LOG" && grep -q -- '-agent kagent/a-basket' "$STUB_LOG" || fail "defaults changed"
echo ok
