#!/usr/bin/env python3
"""Exact find/replace mutation run over the port's own tests (layer 1: the Go tests of extensions/stretchtext).

usage: mutate.py [--jobs N] [--sdk DIR] [--only NAME ...] [--mutations port/mutations.json]

`pigeq mutate` copies the extension into a scratch directory, which loses the go.work that reaches the shared
components/pitui module, so each mutant is built here in a sibling copy of the extension (same depth, same
relative go.work) instead. --sdk points the copy's go.work at a staged PiG SDK, as the host does when it builds the
extension. A mutant is KILLED when `go test` fails (a failed build counts as a build failure, which is not a kill and
is reported on its own: repair the find text).
"""
import argparse, json, os, shutil, subprocess, sys, tempfile
from concurrent.futures import ThreadPoolExecutor

here = os.path.dirname(os.path.abspath(__file__))
root = os.path.dirname(here)
src = os.path.join(root, "extensions", "stretchtext")
ap = argparse.ArgumentParser()
ap.add_argument("--jobs", type=int, default=2)
ap.add_argument("--sdk")
ap.add_argument("--only", nargs="*")
ap.add_argument("--mutations", default=os.path.join(here, "mutations.json"))
args = ap.parse_args()
mutations = json.load(open(args.mutations))
if args.only:
    mutations = [m for m in mutations if m["name"] in args.only]
base = os.path.join(root, "extensions")


def go(directory, *argv):
    env = dict(os.environ)
    env.pop("GOWORK", None)
    return subprocess.run(["go", *argv], cwd=directory, env=env, capture_output=True, text=True)


def prepare(directory):
    pitui = os.path.abspath(os.path.join(root, "..", "pitui"))
    go(directory, "work", "edit", "-replace", f"github.com/MichaelKinsy/pigpen/components/pitui@v0.0.0={pitui}")
    if args.sdk:
        go(directory, "work", "edit", "-replace", f"github.com/MichaelKinsy/PiG/extensions/sdk={args.sdk}")


def run(index_mutation):
    index, m = index_mutation
    directory = os.path.join(base, f".mutant-{os.getpid()}-{index}")
    shutil.copytree(src, directory)
    try:
        prepare(directory)
        path = os.path.join(directory, m["file"])
        text = open(path, encoding="utf-8").read()
        if text.count(m["find"]) != 1:
            return m["name"], "BAD-FIND", f"find text occurs {text.count(m['find'])} times"
        open(path, "w", encoding="utf-8").write(text.replace(m["find"], m["replace"]))
        result = go(directory, "test", "-count=1", "-timeout", "600s", ".")
        out = result.stdout + result.stderr
        if result.returncode == 0:
            return m["name"], "SURVIVED", ""
        if "[build failed]" in out or "setup failed" in out:
            return m["name"], "BUILD-FAILED", out[-400:]
        return m["name"], "KILLED", ""
    finally:
        shutil.rmtree(directory, ignore_errors=True)


# The unmutated extension must pass, or every mutant would count as killed.
baseline_dir = os.path.join(base, f".mutant-{os.getpid()}-base")
shutil.copytree(src, baseline_dir)
try:
    prepare(baseline_dir)
    r = go(baseline_dir, "test", "-count=1", "-timeout", "600s", ".")
    if r.returncode != 0:
        sys.exit("the unmutated extension's tests fail:\n" + (r.stdout + r.stderr)[-2000:])
finally:
    shutil.rmtree(baseline_dir, ignore_errors=True)

counts = {}
with ThreadPoolExecutor(args.jobs) as pool:
    for name, verdict, note in pool.map(run, enumerate(mutations)):
        counts[verdict] = counts.get(verdict, 0) + 1
        print(f"{verdict:13} {name} {note}", flush=True)
print(json.dumps(counts))
sys.exit(1 if set(counts) - {"KILLED"} else 0)
