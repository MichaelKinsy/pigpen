#!/usr/bin/env python3
"""Medians of the files ctxcons-run.sh wrote: python3 ctxcons-median.py <out-dir>"""
import collections, re, statistics, sys

out = sys.argv[1]
go = collections.defaultdict(list)
for line in open(f"{out}/go.txt"):
    f = line.split()
    go[f[0]].append([float(x) for x in f[1:]])

node = {}
for name in ("node-ctx", "node-ctx-steady", "node-cs"):
    d = collections.defaultdict(list)
    for line in open(f"{out}/{name}.txt"):
        m = re.match(r"(\S.*?)(?::| )\s*(?:median )?([\d.]+) (ms|us)", line.strip().replace("\t", " "))
        if m:
            d[m.group(1)].append(float(m.group(2)) * (1e6 if m.group(3) == "ms" else 1e3))
    node[name] = d

pairs = [
    ("CompressOutputGeneric200KB", "CompressOutputGeneric200KB", "node-ctx"),
    ("CompressOutputParserTsc100KB", "CompressOutputParserTsc100KB", "node-ctx"),
    ("RelevanceRenderSummary120Calls", "RelevanceRenderSummary120Calls", "node-ctx"),
    ("ConscienceSanitizeDescription", "sanitizeDescription 1.8 KB", "node-cs"),
    ("ConscienceWriteAndReadIndex200Entries", "writeIndex + readIndex 200 entries", "node-cs"),
]
print("case | Go ns/op | B/op | allocs/op | Node ns/op (3+25) | Node ns/op (300 warm) | Go/Node | n")
for g, n, src in pairs:
    r = go[g]
    ns = statistics.median(x[0] for x in r)
    b = statistics.median(x[1] for x in r)
    a = statistics.median(x[2] for x in r)
    nn = statistics.median(node[src][n]) if node[src][n] else float("nan")
    ns2 = statistics.median(node["node-ctx-steady"][n]) if node["node-ctx-steady"][n] else float("nan")
    ref = min(nn, ns2) if src == "node-ctx" else nn
    print(f"{g} | {ns:.0f} | {b:.0f} | {a:.0f} | {nn:.0f} | {ns2:.0f} | {ns/ref:.2f}x | {len(r)}")
