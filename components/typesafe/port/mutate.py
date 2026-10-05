#!/usr/bin/env python3
"""Mutation check for the typesafe module (Skill step 6, adapted to a library).

Applies each defect of mutations.json to a copy of the module and runs the tests of the mutated
package. KILLED = a test failed; INVALID = the mutant does not build (fix the mutation);
SURVIVED = a missing test (exit status 1); NOMATCH = `find` is not found exactly once.
Usage (go on PATH, run in components/typesafe): python3 port/mutate.py [name ...]
"""
import json, os, shutil, subprocess, sys, tempfile
from concurrent.futures import ThreadPoolExecutor

root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
mutations = json.load(open(os.path.join(root, "port", "mutations.json")))
only = set(sys.argv[1:])
if only:
    mutations = [m for m in mutations if m["name"] in only]

def run(m):
    with tempfile.TemporaryDirectory(prefix="mut-") as tmp:
        dst = os.path.join(tmp, "typesafe")
        shutil.copytree(root, dst, ignore=shutil.ignore_patterns(".git", "oracle"))
        path = os.path.join(dst, "libraries", m["pkg"], m["file"])
        src = open(path).read()
        if src.count(m["find"]) != 1:
            return m["name"], "NOMATCH", f"{src.count(m['find'])} matches"
        open(path, "w").write(src.replace(m["find"], m["replace"]))
        p = subprocess.run(["go", "test", "-count=1", "./libraries/" + m["pkg"]], cwd=dst, capture_output=True, text=True, timeout=600)
        out = p.stdout + p.stderr
        if p.returncode == 0:
            return m["name"], "SURVIVED", ""
        if "[build failed]" in out or "vet:" in out.split("\n")[0:3].__str__() and "--- FAIL" not in out:
            return m["name"], "INVALID", out.strip().split("\n")[-1][:150]
        failed = [l.strip() for l in out.split("\n") if l.strip().startswith("--- FAIL")]
        return m["name"], "KILLED", (failed[0] if failed else out.strip().split("\n")[-1])[:110]

with ThreadPoolExecutor(4) as pool:
    results = list(pool.map(run, mutations))
bad = 0
for name, status, detail in results:
    print(f"{status:9} {name:40} {detail}")
    bad += status != "KILLED"
print(f"{len(results) - bad}/{len(results)} killed")
sys.exit(1 if bad else 0)
