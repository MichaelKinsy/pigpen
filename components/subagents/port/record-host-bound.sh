#!/bin/sh
# Records the host-bound scenario (get on a builtin agent) from the ORIGINAL under Pi, then replaces the absolute path of the
# original's agents directory with <oracle> so no machine path is committed. Run from components/subagents with the
# environment of the other records ($PIGEQ_PI, $PIGEQ_PIG).
set -e
pigeq record --scenarios port/host-bound/scenarios --golden port/host-bound/golden --ts port/oracle/index.ts --pi "$PIGEQ_PI" --pig "$PIGEQ_PIG" >/dev/null 2>&1 || true
oracle="$(cd port/oracle && pwd)"
for f in port/host-bound/golden/*.jsonl; do
  python3 - "$f" "$oracle" <<'PY'
import sys
p, oracle = sys.argv[1], sys.argv[2]
s = open(p).read()
open(p, "w").write(s.replace(oracle, "<oracle>"))
PY
done
mkdir -p extensions/pi-subagents/testdata && cp port/host-bound/golden/get-builtin.jsonl extensions/pi-subagents/testdata/get-builtin.jsonl
