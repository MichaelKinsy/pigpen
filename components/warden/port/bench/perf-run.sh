#!/bin/bash
# Interleaved per-call comparison of the Go port with the original under Node: the supervision, rules and guard-core
# cases of port/bench/perf-original.ts and their Go benchmarks (supervision_bench_test.go, rules_bench_test.go,
# guardcore_bench_test.go, rules_cmp_bench_test.go). Each round runs, in this order, the Go benchmarks of the tree
# under test, the Go benchmarks of a base tree (optional) and the Node harness, all on the CPU set of the container.
#
#   run inside the container from components/warden:   bash port/bench/perf-run.sh [rounds]
#   BASE=/w/.wf/base   a copy of the repo at the commit to compare with (it needs rules_cmp_bench_test.go too)
#   Round output: /tmp/perf-go.txt /tmp/perf-base.txt /tmp/perf-node.txt (ns/op), /tmp/perf-node-alloc.txt (bytes/op, three runs), then the medians table on stdout.
set -e
RUNS=${1:-10}
cd "$(dirname "$0")/../.."
CASES='WasteRecord|BuildStuckEvidence|ParseFailureTAP|TriageReportOffline|RulesParse40|RulesStoreLoadCached|RulesBuildRequest40|RulesDescribeWriteTarget|RulesDescribeEditTarget|ShellWritesHeredoc|RulesScore40|RulesEvaluateOffline|RulesEvaluateStubJudge40|EvaluateActionOrdinaryCommand|EvaluateActionWithPathRules|EvaluateActionSQLCommand|ArmingCheckWhenArmed|ArmingCheckNotArmed|ArmingArmMatching|ArmingArmNotMatching|SQLTargetLocalDelete|SQLTargetHostedWrite|MatchPatternsFiveCommands|MatchPatternsFiveCommandsUserRules|MatchPatternsWritePath|MatchPathRulesCommand|MatchPathRulesWrite'
(cd extensions/warden && go test -c -o /tmp/warden-perf.test .)
if [ -n "$BASE" ]; then
  mkdir -p /tmp/gwbase
  printf 'go 1.26.0\n\nuse (\n\t%s/components/warden/extensions/warden\n\t%s/components/typesafe\n\t/work/pig-src/extensions/sdk\n)\nreplace github.com/MichaelKinsy/pigpen/components/typesafe v0.0.0 => %s/components/typesafe\nreplace github.com/MichaelKinsy/pigpen/warden v0.0.0 => %s/components/warden/extensions/warden\n' "$BASE" "$BASE" "$BASE" "$BASE" > /tmp/gwbase/go.work
  (cd "$BASE/components/warden/extensions/warden" && GOWORK=/tmp/gwbase/go.work go test -c -o /tmp/warden-perf-base.test .)
fi
rm -f /tmp/perf-go.txt /tmp/perf-base.txt /tmp/perf-node.txt /tmp/perf-node-alloc.txt
gobench() { # binary, output file
  (cd extensions/warden && "$1" -test.run '^$' -test.bench "^Benchmark($CASES)\$" -test.benchtime 300ms -test.benchmem | awk '/^Benchmark/{sub(/^Benchmark/,"",$1); sub(/-[0-9]+$/,"",$1); print $1, $3, $5, $7}') >> "$2"
}
for i in $(seq 1 "$RUNS"); do
  gobench /tmp/warden-perf.test /tmp/perf-go.txt
  [ -n "$BASE" ] && gobench /tmp/warden-perf-base.test /tmp/perf-base.txt
  (cd port/oracle && node --no-warnings --experimental-transform-types --import ../bench/ts-register.mjs ../bench/perf-original.ts time) >> /tmp/perf-node.txt
done
for i in 1 2 3; do
  (cd port/oracle && node --no-warnings --experimental-transform-types --expose-gc --min-semi-space-size=128 --max-semi-space-size=128 --import ../bench/ts-register.mjs ../bench/perf-original.ts alloc) >> /tmp/perf-node-alloc.txt
done
python3 - <<'PY'
import statistics, collections, os
def load(p):
    d = collections.defaultdict(list)
    if os.path.exists(p):
        for line in open(p):
            f = line.split()
            if len(f) >= 2: d[f[0]].append([float(x) for x in f[1:]])
    return d
go, base, node, alloc = load('/tmp/perf-go.txt'), load('/tmp/perf-base.txt'), load('/tmp/perf-node.txt'), load('/tmp/perf-node-alloc.txt')
med = lambda rows, i: statistics.median(r[i] for r in rows)
def fmt(v): return f"{v:,.0f}"
print("case | Go ns/op | B/op | allocs/op | base ns/op | B/op | allocs/op | Node ns/op | Node B/op | Go/Node | Go/base")
for name in go:
    g = go[name]
    gn, gb, ga = med(g, 0), med(g, 1), med(g, 2)
    bs = base.get(name)
    b = (fmt(med(bs, 0)), fmt(med(bs, 1)), fmt(med(bs, 2)), f"{gn/med(bs,0):.2f}x") if bs else ("-", "-", "-", "-")
    nd = node.get(name)
    nb = fmt(med(alloc[name], 0)) if name in alloc else "-"
    n = (fmt(med(nd, 0)), nb, f"{gn/med(nd,0):.2f}x") if nd else ("-", "-", "-")
    print(f"{name} | {fmt(gn)} | {fmt(gb)} | {fmt(ga)} | {b[0]} | {b[1]} | {b[2]} | {n[0]} | {n[1]} | {n[2]} | {b[3]}")
PY
