#!/usr/bin/env python3
"""Runs chosen mutants of a mutations file against the module's complete test suite, each in its own copy and with a long timeout.
`pigeq mutate` gives a mutant 3 minutes, which the full BashProgram differential (83,300 inputs, 7 configurations) does not fit on a
loaded host, so a pass with PP_DIFF_STRIDE=8 judges the bulk and this script re-judges the survivors with the whole recording.
  python3 port/mutate-full.py <mutations.json> <go.work> <jobs> <timeout seconds> [mutant name ...]   (no names: every mutant)
Prints KILLED / SURVIVED / INVALID per mutant with the first failing test."""
import json, os, re, shutil, subprocess, sys, tempfile
from concurrent.futures import ThreadPoolExecutor

mutations, gowork, jobs, timeout = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
names = set(sys.argv[5:])
here = os.path.dirname(os.path.abspath(__file__))
module = os.path.join(here, "..", "extensions", "pi-permission-system")
todo = [m for m in json.load(open(mutations)) if not names or m["name"] in names]
scratch = tempfile.mkdtemp(prefix="mutfull-")


def run(m):
    work = os.path.join(scratch, m["name"])
    shutil.copytree(module, work)
    path = os.path.join(work, m["file"])
    src = open(path).read()
    if src.count(m["find"]) != 1:
        return m["name"], "INVALID", "find does not occur exactly once"
    open(path, "w").write(src.replace(m["find"], m["replace"], 1))
    # the copy is the workspace's module: point the workspace file at it
    wf = os.path.join(scratch, m["name"] + ".work")
    open(wf, "w").write(open(gowork).read().replace(os.path.abspath(module), work))
    env = dict(os.environ, GOWORK=wf)
    build = subprocess.run(["go", "build", "."], cwd=work, env=env, capture_output=True, text=True)
    if build.returncode != 0:
        return m["name"], "INVALID", (build.stdout + build.stderr)[:200]
    r = subprocess.run(["go", "test", "-count=1", "-timeout", "%ds" % timeout, "."], cwd=work, env=env, capture_output=True, text=True)
    shutil.rmtree(work, ignore_errors=True)
    out = r.stdout + r.stderr
    if r.returncode == 0:
        return m["name"], "SURVIVED", "the whole suite passed on the mutant"
    if "panic: test timed out" in out:
        return m["name"], "TIMEOUT", "the suite did not finish in %ds" % timeout
    fails = re.findall(r"--- FAIL: (\S+)", out)
    return m["name"], "KILLED", fails[0] if fails else out[-200:]


with ThreadPoolExecutor(jobs) as pool:
    results = list(pool.map(run, todo))
for name, status, why in results:
    print("%-9s %s\n          %s" % (status, name, why))
print("%d mutation(s), %d not killed" % (len(results), sum(1 for _, s, _ in results if s != "KILLED")))
shutil.rmtree(scratch, ignore_errors=True)
