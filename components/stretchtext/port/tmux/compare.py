#!/usr/bin/env python3
"""Runs a scenario under Pi and under PiG and compares the screens they recorded.

usage: compare.py SCENARIO.json OUTDIR     (run.py's STRETCHTEXT_TMUX_* environment)

The footer (working directory, token counts, model) and the startup warning differ between the two hosts by design and are
dropped before the comparison; the working spinner's frame is the same character in both. Exits 1 when a screen differs or a scenario failed to run.
"""
import os, re, shutil, subprocess, sys

scenario, out = sys.argv[1], sys.argv[2]
here = os.path.dirname(os.path.abspath(__file__))
shutil.rmtree(out, ignore_errors=True)
for host in ("pi", "pig"):
    subprocess.run([sys.executable, os.path.join(here, "run.py"), host, scenario, os.path.join(out, host)], check=True)


def normal(path):
    keep = []
    for line in open(path).read().split("\n"):
        if re.search(r"extended-keys|~/\.tmux|^\s+on\s*$|^/work/|^\S*/work\b|^↑\d", line):
            continue
        keep.append(re.sub("[\u2800-\u28ff]", "\u283f", line).rstrip())  # the working spinner's frame differs by timing
    while keep and not keep[-1]:
        keep.pop()
    return keep


bad = 0
for host in ("pi", "pig"):
    if os.path.exists(os.path.join(out, host, "FAILED.txt")):
        print(f"{host}: FAILED\n" + open(os.path.join(out, host, "FAILED.txt")).read())
        bad += 1
snaps = sorted(f for f in os.listdir(os.path.join(out, "pi")) if f.endswith(".txt") and f != "FAILED.txt")
for name in snaps:
    a, b = normal(os.path.join(out, "pi", name)), os.path.join(out, "pig", name)
    if not os.path.exists(b):
        print(f"{name}: missing under pig")
        bad += 1
        continue
    b = normal(b)
    if a != b:
        bad += 1
        print(f"{name}: DIFFERS")
        for i in range(max(len(a), len(b))):
            x, y = a[i] if i < len(a) else "", b[i] if i < len(b) else ""
            if x != y:
                print(f"  row {i}\n    pi : {x!r}\n    pig: {y!r}")
    else:
        print(f"{name}: same")
sys.exit(1 if bad else 0)
