#!/bin/sh
# Interleaved benchmark of the path layer: the port (go test -bench) and the original (vitest, probe/paths-bench.test.ts), ROUNDS times
# each, alternating, on this host. Run from components/permissions with an installed copy of port/oracle as the argument; prints the
# median of each operation (go ns/op, node ns/op, ratio, bytes). The probe is copied into the oracle copy for the run and removed after.
set -e
pkg="$1/packages/pi-permission-system"
rounds="${ROUNDS:-10}"
here="$(pwd)"
test -d "$pkg/node_modules" || { echo "usage: $0 <installed copy of port/oracle>" >&2; exit 2; }
cp port/probe/paths-bench.test.ts "$pkg/test/zz-paths-bench.test.ts"
trap 'rm -f "$pkg/test/zz-paths-bench.test.ts"' EXIT
mkdir -p "$here/.bench-paths"
i=1
while [ "$i" -le "$rounds" ]; do
  (cd extensions/pi-permission-system && go test -run '^$' -bench 'AccessPathForPath|ForToolPathRead|Win32Comparable|NormalizeInputMcp|CreatePiMcpToolTargets' -benchtime 20000x -count=1 . | grep '^Benchmark' > "$here/.bench-paths/go-$i.txt")
  (cd "$pkg" && BENCH_OUT="$here/.bench-paths/node-$i.json" node_modules/.bin/vitest run test/zz-paths-bench.test.ts > "$here/.bench-paths/node-$i.log" 2>&1) || { cat "$here/.bench-paths/node-$i.log"; exit 1; }
  i=$((i + 1))
done
python3 - "$here/.bench-paths" "$rounds" <<'PY'
import json, re, statistics, sys
d, n = sys.argv[1], int(sys.argv[2])
go, node = {}, {}
for i in range(1, n + 1):
    for line in open(f"{d}/go-{i}.txt"):
        m = re.match(r"Benchmark(\w+?)-\d+\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op", line)
        if m: go.setdefault(m[1], []).append((float(m[2]), float(m[3])))
    for k, v in json.load(open(f"{d}/node-{i}.json")).items():
        node.setdefault(k, []).append(v)
print("%-24s %12s %12s %9s %10s" % ("operation", "go ns/op", "node ns/op", "go/node", "go B/op"))
for k in go:
    g = statistics.median(x[0] for x in go[k]); gb = statistics.median(x[1] for x in go[k]); nd = statistics.median(node[k])
    print("%-24s %12.0f %12.0f %8.2fx %10.0f" % (k, g, nd, g / nd, gb))
PY
rm -rf "$here/.bench-paths"
