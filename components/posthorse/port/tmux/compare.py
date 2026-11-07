#!/usr/bin/env python3
"""Runs a scenario under Pi and under PiG and compares the screens they recorded.

usage: compare.py SCENARIO.json OUTDIR [pi|pig|both]     (run.py's POSTHORSE_TMUX_* environment)

The footer (working directory, token counts, model) and the startup warning differ between the two hosts by design and are
dropped before the comparison; the working spinner's frame is normalised. Plain text is the verdict; the color escapes
(*.ansi) are compared too and reported as "colors differ" when only they do. Exits 1 when a screen differs or a scenario failed to run.
"""
import os, re, shutil, subprocess, sys

scenario, out = sys.argv[1], sys.argv[2]
which = sys.argv[3] if len(sys.argv) > 3 else "both"
here = os.path.dirname(os.path.abspath(__file__))
hosts = ("pi", "pig") if which == "both" else (which,)
for host in hosts:
    shutil.rmtree(os.path.join(out, host), ignore_errors=True)
    subprocess.run([sys.executable, os.path.join(here, "run.py"), host, scenario, os.path.join(out, host)], check=True)
if which != "both":
    sys.exit(0)

NOISE = r"extended-keys|~/\.tmux|^\s+on\s*$|^/work/|^\S*/work\b|^↑\d|^[\d.]+%/\d+k|^\s*(\(pi\)|\(pig\)) "


def normal(path):
    keep = []
    for line in open(path).read().split("\n"):
        if re.search(NOISE, re.sub(r"\x1b\[[0-9;:]*[A-Za-z]", "", line)):
            continue
        line = re.sub(r"\d{4}-\d\d-\d\dT\d\d:\d\d(:\d\d(\.\d+)?)?Z", "<time>", line)  # file times and entry timestamps differ by run
        keep.append(re.sub("[\u2800-\u28ff]", "\u283f", line).rstrip())  # the working spinner's frame differs by timing
    while keep and not keep[-1]:
        keep.pop()
    return keep


bad = 0
for host in ("pi", "pig"):
    if os.path.exists(os.path.join(out, host, "FAILED.txt")):
        print(f"{host}: FAILED\n" + open(os.path.join(out, host, "FAILED.txt")).read())
        bad += 1
snaps = sorted(f for f in os.listdir(os.path.join(out, "pi")) if f.endswith(".txt") and f not in ("FAILED.txt", "stderr.txt"))
for name in snaps:
    a, b = normal(os.path.join(out, "pi", name)), os.path.join(out, "pig", name)
    if not os.path.exists(b):
        print(f"{name}: missing under pig")
        bad += 1
        continue
    b = normal(b)
    if a != b:
        bad += 1
        shown = 0
        print(f"{name}: DIFFERS")
        for i in range(max(len(a), len(b))):
            x, y = a[i] if i < len(a) else "", b[i] if i < len(b) else ""
            if x != y:
                shown += 1
                if shown <= int(os.environ.get("POSTHORSE_TMUX_ROWS", "8")):
                    print(f"  row {i}\n    pi : {x!r}\n    pig: {y!r}")
        if shown > int(os.environ.get("POSTHORSE_TMUX_ROWS", "8")):
            print(f"  ... {shown} rows differ")
    else:
        ansi = name[:-4] + ".ansi"
        ca, cb = normal(os.path.join(out, "pi", ansi)), normal(os.path.join(out, "pig", ansi))
        print(f"{name}: same" + ("" if ca == cb else " (colors differ)"))
sys.exit(1 if bad else 0)
