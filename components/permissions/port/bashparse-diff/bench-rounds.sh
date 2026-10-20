#!/bin/bash
# Interleaved latency rounds, Go parse vs the original's WASM parser, on one pinned CPU set.
#   PP_DIR=<dir> ~/pp-perm-run.sh "bash /work/components/permissions/port/bashparse-diff/bench-rounds.sh [rounds] [ncpus]" > log
# Picks the least busy CPUs of the container's set (sampled over 2 s), prints the load, then alternates
# one Go round and one WASM round on that set. Summarise with: python3 bench-summary.py log
set -u
ROUNDS=${1:-10}
NCPU=${2:-4}
cd /work/components/permissions/extensions/pi-permission-system
printf 'go 1.26\nuse /work/components/permissions/extensions/pi-permission-system\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "${PIG_SDK_DIR:-/pigsrc/extensions/sdk}" > /tmp/go.work.bp
export GOWORK=/tmp/go.work.bp
go test -c -o /tmp/bp.test ./bashparse 2>&1 | tail -3
SET=$(python3 - "$NCPU" <<'PY'
import sys, time, os
def snap():
    d = {}
    for l in open("/proc/stat"):
        if l.startswith("cpu") and l[3].isdigit():
            f = l.split(); v = list(map(int, f[1:])); d[int(f[0][3:])] = (v[3] + v[4], sum(v))
    return d
allowed = sorted(os.sched_getaffinity(0))
a = snap(); time.sleep(2); b = snap()
busy = {c: 1 - (b[c][0] - a[c][0]) / max(1, b[c][1] - a[c][1]) for c in allowed if c in b}
best = sorted(busy, key=lambda c: busy[c])[: int(sys.argv[1])]
sys.stderr.write("LOAD cpus=%s busy=%s mean_busy_all=%.2f\n" % (sorted(best), [round(busy[c], 2) for c in sorted(best)], sum(busy.values()) / len(busy)))
print(",".join(map(str, sorted(best))))
PY
)
echo "CPUSET $SET"; cat /proc/loadavg
cd bashparse
taskset -c "$SET" /tmp/bp.test -test.run TestColdStart -test.v 2>&1 | grep COLD
for r in $(seq 1 "$ROUNDS"); do
  echo "GO round $r"; taskset -c "$SET" /tmp/bp.test -test.run XXX -test.bench BenchmarkParse -test.benchtime 300ms 2>&1 | grep -E "^Benchmark"
  echo "WASM round $r"; taskset -c "$SET" node /work/components/permissions/port/bashparse-diff/bench.mjs /work/components/permissions/port/oracle/packages/pi-permission-system /work/components/permissions/port/bashparse-diff/bench-inputs.json 2>&1 | tail -1
done
cat /proc/loadavg
echo "== done"
