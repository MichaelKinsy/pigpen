#!/bin/bash
# Interleaved medians of the five context and conscience paths that were above the original: the Go benchmarks against the
# original under Node (24), same inputs, same CPUs, alternating per round.
# Run inside the container from the repo root (xd670: PW_DIR=<repo copy> ~/pp-wfin/xrun.sh "bash components/warden/port/perf/ctxcons-run.sh 10 out-dir"),
# after `. /cache/env.sh` (done below) and `npm install --ignore-scripts` in components/warden/port/oracle.
# Output: <out>/go.txt, <out>/node-ctx.txt, <out>/node-ctx-steady.txt, <out>/node-cs.txt (raw); medians: python3 port/perf/ctxcons-median.py <out>
set -e
ROUNDS=${1:-10}
OUT=$(realpath -m "${2:-/tmp/ctxcons-out}")
CPUS=${CPUS:-}
pin() { if [ -n "$CPUS" ]; then taskset -c "$CPUS" "$@"; else "$@"; fi; }
. /cache/env.sh
W=$PWD/components/warden
mkdir -p "$OUT"
if [ ! -d "$PWD/components/pitui" ]; then
  # env.sh lists pitui, which older branches do not have: the same go.work without it
  printf 'go 1.26.0\n\nuse (\n\t%s/extensions/warden\n\t%s/components/typesafe\n\t/work/pig-src/extensions/sdk\n)\nreplace github.com/MichaelKinsy/pigpen/components/typesafe v0.0.0 => %s/components/typesafe\nreplace github.com/MichaelKinsy/pigpen/warden v0.0.0 => %s/extensions/warden\n' "$W" "$PWD" "$PWD" "$W" > "$OUT/go.work"
  export GOWORK=$OUT/go.work
fi
(cd "$W/extensions/warden" && go test -c -o "$OUT/warden.test" .)
cp "$W/port/bench-conscience-original.ts" "$W/port/oracle/_bench-conscience.ts"
trap 'rm -f "$W/port/oracle/_bench-conscience.ts"' EXIT
rm -f "$OUT"/go.txt "$OUT"/node-*.txt
GOBENCH='Benchmark(CompressOutputGeneric200KB|CompressOutputParserTsc100KB|RelevanceRenderSummary120Calls|ConscienceSanitizeDescription|ConscienceWriteAndReadIndex200Entries)$'
NODE="node --no-warnings --experimental-transform-types --import ../bench/ts-register.mjs"
for i in $(seq 1 "$ROUNDS"); do
  (cd "$W/extensions/warden" && pin "$OUT/warden.test" -test.run '^$' -test.bench "$GOBENCH" -test.benchtime 1s -test.benchmem | awk '/^Benchmark/{sub(/^Benchmark/,"",$1); sub(/-[0-9]+$/,"",$1); ns=b=a=0; for(i=2;i<NF;i++){if($(i+1)=="ns/op")ns=$i; if($(i+1)=="B/op")b=$i; if($(i+1)=="allocs/op")a=$i}; print $1, ns, b, a}') >> "$OUT/go.txt"
  (cd "$W/port/oracle" && BENCH_ONLY='CompressOutput|RelevanceRenderSummary' pin $NODE ../bench/bench-original.ts) >> "$OUT/node-ctx.txt"
  (cd "$W/port/oracle" && BENCH_ITERS=300 BENCH_ONLY='CompressOutput|RelevanceRenderSummary' pin $NODE ../bench/bench-original.ts) >> "$OUT/node-ctx-steady.txt"
  (cd "$W/port/oracle" && BENCH_ONLY='sanitizeDescription|writeIndex' pin $NODE --import ./tests/setup-env.ts ./_bench-conscience.ts) >> "$OUT/node-cs.txt"
done
echo done
