#!/usr/bin/env python3
"""Medians of the files ctxcons-run.sh wrote: python3 ctxcons-median.py <out-dir>"""
import collections, os, re, statistics, sys

out = sys.argv[1]


def load_go(name):
    d = collections.defaultdict(list)
    path = f"{out}/{name}"
    if os.path.exists(path):
        for line in open(path):
            f = line.split()
            d[f[0]].append([float(x) for x in f[1:]])
    return d


go, base = load_go("go.txt"), load_go("go-base.txt")

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


def med(rows):
    return tuple(statistics.median(x[i] for x in rows) for i in range(3))


print("case | before ns/op, B/op, allocs/op | after ns/op, B/op, allocs/op | Node ns/op (3+25 runs) | Node ns/op (300 warm runs) | before/Node | after/Node | rounds")
for g, n, src in pairs:
    after = med(go[g])
    before = med(base[g]) if base.get(g) else None
    nn = statistics.median(node[src][n])
    ns2 = statistics.median(node["node-ctx-steady"][n]) if node["node-ctx-steady"][n] else float("nan")
    ref = min(nn, ns2) if src == "node-ctx" else nn
    b = f"{before[0]:.0f}, {before[1]:.0f}, {before[2]:.0f}" if before else "-"
    r = f"{before[0] / ref:.2f}x" if before else "-"
    print(f"{g} | {b} | {after[0]:.0f}, {after[1]:.0f}, {after[2]:.0f} | {nn:.0f} | {ns2:.0f} | {r} | {after[0] / ref:.2f}x | {len(go[g])}")
