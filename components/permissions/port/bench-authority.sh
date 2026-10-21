#!/bin/sh
# Interleaved benchmark of the approval dialog: the port (go test -bench) and the original (vitest, authority-bench.test.ts), ROUNDS
# times each, alternating, on this host. Run from components/permissions with an installed copy of port/oracle as the argument; prints
# the median of each operation. The probe is copied into the oracle copy for the run and removed after.
set -e
pkg="$1/packages/pi-permission-system"
rounds="${ROUNDS:-10}"
here="$(pwd)"
test -d "$pkg/node_modules" || { echo "usage: $0 <installed copy of port/oracle>" >&2; exit 2; }
cp port/probe/authority-bench.test.ts "$pkg/test/zz-authority-bench.test.ts"
trap 'rm -f "$pkg/test/zz-authority-bench.test.ts"' EXIT
mkdir -p "$here/.bench"
i=1
while [ "$i" -le "$rounds" ]; do
  (cd extensions/pi-permission-system && go test -run '^$' -bench 'Component|ReasonTyping|ReducePrompt|CollapsePasted|ComposeChain|PrompterPrompt|LocalAuthorizerAsk' -benchtime 300x -count=1 . | grep '^Benchmark' > "$here/.bench/go-$i.txt")
  (cd "$pkg" && BENCH_OUT="$here/.bench/node-$i.json" node_modules/.bin/vitest run test/zz-authority-bench.test.ts > /dev/null)
  i=$((i + 1))
done
python3 - "$here/.bench" "$rounds" <<'PY'
import json, re, statistics, sys
d, n = sys.argv[1], int(sys.argv[2])
go, node = {}, {}
for i in range(1, n + 1):
    for line in open(f"{d}/go-{i}.txt"):
        m = re.match(r"Benchmark(\w+?)-\d+\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op", line)
        if m: go.setdefault(m[1], []).append((float(m[2]), float(m[3]), float(m[4])))
    for k, v in json.load(open(f"{d}/node-{i}.json")).items():
        node.setdefault(k, []).append((v["nsPerOp"], v["bytesPerOp"]))
print("%-22s %14s %14s %10s | %14s %14s" % ("operation", "go ns/op", "node ns/op", "go/node", "go B/op", "node B/op"))
for k in go:
    g = statistics.median(x[0] for x in go[k]); gb = statistics.median(x[1] for x in go[k])
    nd = statistics.median(x[0] for x in node[k]); nb = statistics.median(x[1] for x in node[k])
    print("%-22s %14.0f %14.0f %9.2fx | %14.0f %14.0f" % (k, g, nd, g / nd, gb, nb))
PY
rm -rf "$here/.bench"
