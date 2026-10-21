#!/bin/bash
# Interleaved medians of the five context and conscience paths that were above the original: the Go benchmarks against the
# original under Node (24), same inputs, same CPUs, alternating per round. With BASE_TREE (a repo tree of the code before
# the fixes: components/warden and components/typesafe), its benchmarks run in the same rounds.
# Run inside the container from the repo root (xd670: PW_DIR=<repo copy> ~/pp-wfin/xrun.sh "bash components/warden/port/perf/ctxcons-run.sh 10 out-dir"),
# after `npm install --ignore-scripts` in components/warden/port/oracle.
# Output: <out>/go.txt, go-base.txt, node-ctx.txt, node-ctx-steady.txt, node-cs.txt (raw); medians: python3 port/perf/ctxcons-median.py <out>
set -e
ROUNDS=${1:-10}
OUT=$(realpath -m "${2:-/tmp/ctxcons-out}")
CPUS=${CPUS:-}
pin() { if [ -n "$CPUS" ]; then taskset -c "$CPUS" "$@"; else "$@"; fi; }
. /cache/env.sh
W=$PWD/components/warden
mkdir -p "$OUT"
# build_bench <tree> <binary>: env.sh lists pitui, which older branches do not have: the same go.work without it
build_bench() {
  local tree=$1
  if [ ! -d "$tree/components/pitui" ]; then
    printf 'go 1.26.0\n\nuse (\n\t%s/components/warden/extensions/warden\n\t%s/components/typesafe\n\t/work/pig-src/extensions/sdk\n)\nreplace github.com/MichaelKinsy/pigpen/components/typesafe v0.0.0 => %s/components/typesafe\nreplace github.com/MichaelKinsy/pigpen/warden v0.0.0 => %s/components/warden/extensions/warden\n' "$tree" "$tree" "$tree" "$tree" > "$OUT/go.work.$$"
    (cd "$tree/components/warden/extensions/warden" && GOWORK=$OUT/go.work.$$ go test -c -o "$2" .)
  else
    (cd "$tree/components/warden/extensions/warden" && go test -c -o "$2" .)
  fi
}
build_bench "$PWD" "$OUT/warden.test"
[ -n "$BASE_TREE" ] && build_bench "$BASE_TREE" "$OUT/warden-base.test"
cp "$W/port/bench-conscience-original.ts" "$W/port/oracle/_bench-conscience.ts"
trap 'rm -f "$W/port/oracle/_bench-conscience.ts"' EXIT
rm -f "$OUT"/go.txt "$OUT"/go-base.txt "$OUT"/node-*.txt
GOBENCH='Benchmark(CompressOutputGeneric200KB|CompressOutputParserTsc100KB|RelevanceRenderSummary120Calls|ConscienceSanitizeDescription|ConscienceWriteAndReadIndex200Entries)$'
NODE="node --no-warnings --experimental-transform-types --import ../bench/ts-register.mjs"
gobench() { # <binary> <tree> <out file>
  (cd "$2/components/warden/extensions/warden" && pin "$1" -test.run '^$' -test.bench "$GOBENCH" -test.benchtime 1s -test.benchmem | awk '/^Benchmark/{sub(/^Benchmark/,"",$1); sub(/-[0-9]+$/,"",$1); ns=b=a=0; for(i=2;i<NF;i++){if($(i+1)=="ns/op")ns=$i; if($(i+1)=="B/op")b=$i; if($(i+1)=="allocs/op")a=$i}; print $1, ns, b, a}') >> "$3"
}
for i in $(seq 1 "$ROUNDS"); do
  gobench "$OUT/warden.test" "$PWD" "$OUT/go.txt"
  [ -n "$BASE_TREE" ] && gobench "$OUT/warden-base.test" "$BASE_TREE" "$OUT/go-base.txt"
  (cd "$W/port/oracle" && BENCH_ONLY='CompressOutput|RelevanceRenderSummary' pin $NODE ../bench/bench-original.ts) >> "$OUT/node-ctx.txt"
  (cd "$W/port/oracle" && BENCH_ITERS=300 BENCH_ONLY='CompressOutput|RelevanceRenderSummary' pin $NODE ../bench/bench-original.ts) >> "$OUT/node-ctx-steady.txt"
  (cd "$W/port/oracle" && BENCH_ONLY='sanitizeDescription|writeIndex' pin $NODE --import ./tests/setup-env.ts ./_bench-conscience.ts) >> "$OUT/node-cs.txt"
done
echo done
