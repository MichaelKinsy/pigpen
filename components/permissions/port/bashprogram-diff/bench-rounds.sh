#!/bin/bash
# Interleaved latency rounds, Go BashProgram vs the original's BashProgram.parse under Node, on one pinned CPU set.
#   PP_DIR=<dir> ~/pp-perm2-run.sh "bash /work/components/permissions/port/bashprogram-diff/bench-rounds.sh [rounds] [ncpus]" > log
# Picks the least busy CPUs of the container's set, prints the load, then alternates one Go round and one Node round on that set.
set -u
ROUNDS=${1:-10}
NCPU=${2:-4}
W=/work/components/permissions
cd $W/extensions/pi-permission-system
printf 'go 1.26\nuse %s/extensions/pi-permission-system\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "$W" "${PIG_SDK_DIR:-/pigsrc/extensions/sdk}" > /tmp/go.work.bpb
export GOWORK=/tmp/go.work.bpb
go test -c -o /tmp/bpb.test . 2>&1 | tail -3
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
pkg=$W/port/oracle/packages/pi-permission-system
cp $W/port/bashprogram-diff/bench.test.ts $pkg/test/zz-bashprogram-bench.test.ts
trap 'rm -f $pkg/test/zz-bashprogram-bench.test.ts' EXIT
for r in $(seq 1 "$ROUNDS"); do
  echo "GO round $r"; taskset -c "$SET" /tmp/bpb.test -test.run XXX -test.bench BenchmarkBashProgram -test.benchtime 300ms 2>&1 | grep -E "^Benchmark"
  echo "NODE round $r"; (cd $pkg && BENCH_IN=$W/port/bashprogram-diff/bench-inputs.json taskset -c "$SET" node_modules/.bin/vitest run test/zz-bashprogram-bench.test.ts 2>&1 | grep '^BENCH')
done
cat /proc/loadavg
echo "== done"
