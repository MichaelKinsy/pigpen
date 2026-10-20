#!/bin/bash
# The performance measurement of port/PORT.md, one command. Run it on a quiet host, pinned to a fixed CPU set (see "Performance" in
# PORT.md for the environment the recorded numbers came from). It needs go, node (the runtime PiG hosts TypeScript extensions on), a
# PiG binary and its SDK, and four checkouts' worth of input:
#
#   NEW   a checkout of this branch (components/goal, components/pitui)
#   OLD   a checkout of the previous release (origin/goal-honest-refusal: components/goal)
#   PIG   the pig binary (PiG v0.5.0), PIG_SDK_DIR its SDK (the extension modules require it), PIG_SOURCE_ROOT its source (Piglet builds)
#   OUT   where the raw output goes
#   CPUS  the CPU set the measured processes may use (taskset); ROUNDS  interleaved rounds (default 12)
#   PIG_SDK_DIR and PIG_SOURCE_ROOT as above; go, node and git on PATH
#
#   NEW=/w OLD=/old PIG=/work/bin/pig OUT=/scratch/out CPUS=64-71 ROUNDS=12 bash port/perf/run.sh
#
# Stages (each writes under $OUT; a stage can be repeated alone with STAGES="size rpc" and so on):
#   size   extension binary sizes (go build, as PiG builds an extension) and the Piglet Binaries (single extension, fused)
#   bench  in-process benchmarks, interleaved by round: the port (go test -bench), the original (bench-original.mjs), the previous
#          release (old_bench_test.go.txt copied into OLD; it has only the commands it implemented)
#   rpc    cold start and tool/command round trips through a real pig (rpc.mjs), interleaved by round
# Then: python3 port/perf/summarize.py $OUT > port/perf/results.md
set -eu
: "${NEW:?}" "${OLD:?}" "${PIG:?}" "${OUT:?}" "${CPUS:?}"
ROUNDS=${ROUNDS:-12}
NSCALE=${NSCALE:-1}   # divides the iteration counts (debugging the harness only)
STAGES=${STAGES:-size bench rpc}
PERF=$NEW/components/goal/port/perf
mkdir -p "$OUT/bin" "$OUT/bench" "$OUT/rpc"
pin="taskset -c $CPUS"
: "${PIG_SDK_DIR:?}"

cat > "$OUT/new.work" <<EOF
go 1.26.0

use (
	$NEW/components/goal/extensions/pi-goal-x
	$NEW/components/pitui
)

replace github.com/MichaelKinsy/PiG/extensions/sdk => $PIG_SDK_DIR
replace github.com/MichaelKinsy/pigpen/components/pitui v0.0.0 => $NEW/components/pitui
EOF
cat > "$OUT/old.work" <<EOF
go 1.26.0

use (
	$OLD/components/goal/extensions/pi-goal-x
)

replace github.com/MichaelKinsy/PiG/extensions/sdk => $PIG_SDK_DIR
EOF
# The release has no perf_bench_test.go of its own: the port's old_bench_test.go.txt is copied in (untracked there).
cp "$PERF/old_bench_test.go.txt" "$OLD/components/goal/extensions/pi-goal-x/perf_bench_test.go"

if [[ " $STAGES " == *" size "* ]]; then
  # The extension as PiG hosts it is a program PiG links around `func Extension() *sdk.Extension` ("factory path"); the cached result is
  # $PIG_HOME/cache/cells/go/<hash>/runner (summarize.py reports those sizes from the rpc stage's PIG_HOME). For the breakdown the same
  # shape is built here: a main that runs the extension, with PiG's build flags (builder.go: -trimpath -ldflags "-s -w"), and once with
  # symbols.
  for v in new old; do
    if [ $v = new ]; then tree=$NEW; else tree=$OLD; fi
    mkdir -p "$OUT/wrap-$v"
    printf 'module perfwrap\n\ngo 1.26.0\n' > "$OUT/wrap-$v/go.mod"
    cat > "$OUT/wrap-$v/main.go" <<EOT
package main

import (
	"os"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

func main() {
	if err := goal.Extension().Run(); err != nil {
		os.Exit(1)
	}
}
EOT
    sed "s#^use (#use (\n\t$OUT/wrap-$v#" "$OUT/$v.work" > "$OUT/$v-wrap.work"
    (cd "$OUT/wrap-$v" && GOWORK=$OUT/$v-wrap.work GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags "-s -w" -o "$OUT/bin/ext-$v" . \
      && GOWORK=$OUT/$v-wrap.work GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -o "$OUT/bin/ext-$v-sym" . \
      && go list -deps . | sort > "$OUT/bin/deps-$v.txt" \
      && go tool nm -size "$OUT/bin/ext-$v-sym" > "$OUT/bin/ext-$v.nm")
    (readelf -SW "$OUT/bin/ext-$v-sym" 2>/dev/null || true) > "$OUT/bin/sections-$v.txt"
  done
  ls -l "$NEW"/components/pitui/wordsegmenter/*.bin > "$OUT/bin/pitui-dictionaries.txt"
  du -sb "$NEW/components/goal/port/oracle/extensions" | cut -f1 > "$OUT/bin/orig-ts-bytes"
  # Piglet Binaries: the port, the previous release and the control (an extension that registers nothing).
  (cd "$NEW" && node components/goal/port/perf/build-piglets.mjs "$NEW" new "$OUT/pl" && node components/goal/port/perf/build-piglets.mjs "$OLD" old "$OUT/pl" && node components/goal/port/perf/build-piglets.mjs "$NEW" control "$OUT/pl" --control)
  ls -l "$OUT/bin" "$OUT/pl/bin" > "$OUT/sizes.txt"
fi

if [[ " $STAGES " == *" bench "* ]]; then
  (cd "$NEW/components/goal/extensions/pi-goal-x" && GOWORK=$OUT/new.work go test -c -o "$OUT/bin/new.test" .)
  (cd "$OLD/components/goal/extensions/pi-goal-x" && GOWORK=$OUT/old.work go test -c -o "$OUT/bin/old.test" .)
  # Operations by cost: the count is the same for both sides. (RenderPush exists only in the port: the original lays out in the host.)
  declare -A GROUPS_N=([key]=20000 [light]=3000 [heavy]=300 [big]=300)
  declare -A GROUP_OPS=(
    [key]="KeyPlain KeyToggleExpand"
    [light]="ToolGetGoalSummary ToolGetGoalTasks CmdGoalList CmdGoalStatus CmdGoalStatusVerbose CmdGoalRefresh RenderDashboardCompact RenderDashboardExpanded"
    [heavy]="SessionStart ToolCreateGoal ToolUpdateGoalPause ToolSetGoalTasks ToolUpdateGoalTask CmdGoalDirect SchedulerTick"
    [big]="BigLedgerSessionStart BigLedgerGetGoalSummary BigLedgerGoalStatus BigLedgerRender BigLedgerKeyToggleExpand BigLedgerUpdateGoalTask")
  for round in $(seq 0 "$ROUNDS"); do   # round 0 is a warm-up and is not summarized
    for g in key light heavy big; do
      n=$(( ${GROUPS_N[$g]} / NSCALE ))
      ops=${GROUP_OPS[$g]}
      re="^Benchmark($(echo "$ops" | tr ' ' '|'))\$"
      [ "$g" = light ] && re="^Benchmark($(echo "$ops RenderPush" | tr ' ' '|'))\$"
      $pin nice -n 10 "$OUT/bin/new.test" -test.run '^$' -test.bench "$re" -test.benchtime "${n}x" -test.benchmem -test.cpu 4 > "$OUT/bench/go-new.$round.$g.txt"
      $pin nice -n 10 node --experimental-strip-types --expose-gc --min-semi-space-size=1024 --max-semi-space-size=1024 "$PERF/bench-original.mjs" --n "$n" --only "$(echo "$ops" | tr ' ' ',')" > "$OUT/bench/orig.$round.$g.txt"
      case $g in
        key) ;;
        light) $pin nice -n 10 "$OUT/bin/old.test" -test.run '^$' -test.bench '^Benchmark(CmdGoalList)$' -test.benchtime "${n}x" -test.benchmem -test.cpu 4 > "$OUT/bench/go-old.$round.$g.txt" ;;
        heavy) $pin nice -n 10 "$OUT/bin/old.test" -test.run '^$' -test.bench '^Benchmark(SessionStart|CmdGoalDirect)$' -test.benchtime "${n}x" -test.benchmem -test.cpu 4 > "$OUT/bench/go-old.$round.$g.txt" ;;
        big) ;;
      esac
    done
  done
fi

if [[ " $STAGES " == *" rpc "* ]]; then
  export PATH=/usr/local/bin:$PATH
  mkdir -p "$OUT/pighome"
  $pin nice -n 10 node "$PERF/rpc.mjs" --pig "$PIG" --cpus "$CPUS" --runs "$ROUNDS" --home-root "$OUT/pighome" --no-flow old,old-piglet \
    control=none new="$NEW/components/goal/extensions/pi-goal-x" old="$OLD/components/goal/extensions/pi-goal-x" \
    orig="$NEW/components/goal/port/oracle/extensions/goal.ts" \
    control-piglet=bin:"$OUT/pl/bin/control" new-piglet=bin:"$OUT/pl/bin/new" old-piglet=bin:"$OUT/pl/bin/old" > "$OUT/rpc/rpc.json" 2> "$OUT/rpc/rpc.err"
fi
