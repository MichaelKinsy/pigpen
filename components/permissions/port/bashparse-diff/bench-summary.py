#!/usr/bin/env python3
"""Median ns/op (and B/op, allocs/op) per input from a bench-rounds.sh log: Go rows against WASM rows."""
import json, re, statistics, sys
go, wasm, cold = {}, {}, []
for line in open(sys.argv[1]):
    m = re.match(r"BenchmarkParse/(\S+?)(?:-\d+)?\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op", line)
    if m:
        go.setdefault(m[1], []).append((float(m[2]), int(m[3]), int(m[4])))
    elif line.startswith("WASM {"):
        d = json.loads(line[5:])
        cold.append(d.pop("cold_ms"))
        for k, v in d.items():
            wasm.setdefault(k, []).append(v)
    elif "COLD" in line or line.startswith(("LOAD", "CPUSET")):
        print(line.strip())
print("%-24s %10s %10s %7s %9s %8s  rounds" % ("input", "go ns/op", "wasm ns/op", "go/wasm", "B/op", "allocs"))
for k in sorted(go):
    g = statistics.median(x[0] for x in go[k])
    w = statistics.median(wasm[k])
    print("%-24s %10.0f %10.0f %7.2f %9d %8d  %d/%d" % (k, g, w, g / w, statistics.median(x[1] for x in go[k]), statistics.median(x[2] for x in go[k]), len(go[k]), len(wasm[k])))
print("wasm cold ms median %.1f" % statistics.median(cold))
