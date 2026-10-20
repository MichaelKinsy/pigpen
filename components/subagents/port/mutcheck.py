#!/usr/bin/env python3
"""Runs the mutations whose name starts with the given prefix against the tests that should kill them (a stand-in for `pigeq mutate
--unit` on a machine without pigeq). usage: mutcheck.py <prefix> <go test -run regexp> [timeout]"""
import json, os, subprocess, sys
root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "extensions", "pi-subagents")
prefix, run = sys.argv[1], sys.argv[2]
muts = [m for m in json.load(open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations.json"))) if m["name"].startswith(tuple(prefix.split(",")))]
survivors = []
for m in muts:
    path = os.path.join(root, m["file"])
    orig = open(path).read()
    assert orig.count(m["find"]) == 1, m["name"]
    open(path, "w").write(orig.replace(m["find"], m["replace"]))
    try:
        r = subprocess.run(["go", "test", "-timeout", "90s", "-count=1", "-run", run, "."], cwd=root, capture_output=True, text=True)
        out = r.stdout + r.stderr
        killed = r.returncode != 0
        why = "build" if "[build failed]" in out or "cannot use" in out else ("fail" if killed else "")
    finally:
        open(path, "w").write(orig)
    print(("KILLED   " if killed else "SURVIVED ") + m["name"] + (" (" + why + ")" if why == "build" else ""), flush=True)
    if not killed:
        survivors.append(m["name"])
print("survivors:", survivors)
