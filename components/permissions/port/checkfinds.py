#!/usr/bin/env python3
"""checkfinds.py <components/permissions dir>: every mutation's `find` must occur exactly once in its file (pigeq refuses otherwise)."""
import json, glob, os, sys
base = sys.argv[1].rstrip('/') + '/'
ext = base + 'extensions/pi-permission-system/'
total = 0
for f in sorted(glob.glob(base + 'port/mutations*.json')):
    ms = json.load(open(f)); bad = []
    for m in ms:
        p = None
        for sub in ('', 'bashanalysis/', 'bashparse/'):
            if os.path.exists(ext + sub + m['file']): p = ext + sub + m['file']; break
        if p is None: bad.append((m['name'], 'no file ' + m['file'])); continue
        c = open(p).read().count(m['find'])
        if c != 1: bad.append((m['name'], f"{m['file']}: {c} occurrences"))
    total += len(bad)
    print(f"{os.path.basename(f):34s} {len(ms):4d} mutants, {len(bad)} unusable", bad[:40] if bad else "")
sys.exit(1 if total else 0)
