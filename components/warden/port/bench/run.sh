#!/bin/bash
# Interleaved medians of the security lane's benchmarks: Go port (go test -bench) against the original (node with type transformation).
# Run inside the imladris wrapper from components/warden:  bash port/bench/run.sh [runs]
set -e
RUNS=${1:-10}
cd "$(dirname "$0")/../.."
(cd extensions/warden && go test -c -o /tmp/warden-sec-bench.test . >/dev/null)

rm -f /tmp/sec-bench-go.txt /tmp/sec-bench-node.txt
for i in $(seq 1 "$RUNS"); do
  (cd extensions/warden && /tmp/warden-sec-bench.test -test.run '^$' -test.bench 'Benchmark(MaskSecrets64KB|FindSecrets64KB|RunawayStream|RestatedShare|BuildProseRequest|RunawayMessageUpdateHandler|Redact64KB|Redact64KBPreviousRelease|RedactReply2KB|RedactReply2KBPreviousRelease)$' -test.benchtime 300ms -test.benchmem | awk '/^Benchmark/{sub(/^Benchmark/,"",$1); sub(/-[0-9]+$/,"",$1); print $1, $3, $5, $7}') >> /tmp/sec-bench-go.txt
  (cd port/oracle && node --no-warnings --experimental-transform-types --import ../bench/ts-register.mjs ../bench/security-bench.ts) >> /tmp/sec-bench-node.txt
done
python3 - <<'PY'
import statistics, collections
def load(p):
    d = collections.defaultdict(list)
    for line in open(p):
        f = line.split()
        d[f[0]].append([float(x) for x in f[1:]])
    return d
go, node = load('/tmp/sec-bench-go.txt'), load('/tmp/sec-bench-node.txt')
print("case | Go ns/op | Go B/op | Go allocs/op | original (Node) ns/op | Go / original")
for name in ["MaskSecrets64KB", "FindSecrets64KB", "RunawayStream", "RestatedShare", "BuildProseRequest", "RunawayMessageUpdateHandler", "Redact64KB", "Redact64KBPreviousRelease", "RedactReply2KB", "RedactReply2KBPreviousRelease"]:
    if name not in node:
        g = statistics.median(r[0] for r in go[name]); b = statistics.median(r[1] for r in go[name]); a = statistics.median(r[2] for r in go[name])
        print(f"{name} | {g:.0f} | {b:.0f} | {a:.0f} | (no counterpart) | -")
        continue
    g = statistics.median(r[0] for r in go[name]); b = statistics.median(r[1] for r in go[name]); a = statistics.median(r[2] for r in go[name])
    n = statistics.median(r[0] for r in node[name])
    print(f"{name} | {g:.0f} | {b:.0f} | {a:.0f} | {n:.0f} | {g/n:.2f}x")
PY
