#!/usr/bin/env python3
"""Print the one-screen memory table for the demo from analysis.json (written by analyze.py)."""
import json, sys

d = json.load(open(sys.argv[1]))
agg, images = d["aggregate"], {i["name"]: i for i in (d.get("images") or [])}
rows = [("PiG", "pig-basket", "pig"), ("Claude Code", "claude-basket", "claude"), ("Codex", "codex-basket", "codex"), ("pi-go", "pigo-basket", "pi-go")]
B, R, G = "\033[1m", "\033[0m", "\033[32m"
print(f"{B}{'harness':<12}{'actor memory':>14}{'  peak':>9}{'  cpu ms':>9}{'  restore s':>12}{'  image MiB':>12}{'  sessions':>11}{R}")
print(f"{'':<12}{'median (MiB)':>14}{'  (MiB)':>9}{'  /turn':>9}{'  median':>12}{'  compressed':>12}{'':>11}")
for label, agent, img in rows:
    a = agg.get(agent)
    if not a or "act1" not in a["memory"]:
        continue
    m = a["memory"]["act1"]
    srv = a["srv"].get("restore_first_total_s") or {}
    col = G if label == "PiG" else ""
    print(f"{col}{label:<12}{m['ws_max_mib']['median']:>14.0f}{m['peak_mib']['median']:>9.0f}{m['cpu_ms']['median']:>9.0f}"
          f"{srv.get('median', float('nan')):>12.2f}{(images.get(img) or {}).get('compressed_mib', float('nan')):>12.0f}{a['runs']:>11}{R if col else ''}")
print("\nactor memory = working set of the actor's gVisor sandbox cgroup (sentry + app),")
print("max sample in one turn, median over sessions. Same mock model for every harness.")
