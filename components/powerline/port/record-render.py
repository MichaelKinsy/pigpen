#!/usr/bin/env python3
"""Records extensions/powerline-footer/testdata/render-golden.json by running the ORIGINAL through port/drive/drive.mjs.

The original runs git with a 200 ms timeout (git-status.ts runGit), so on a loaded machine a state that reads a repository can come
out with a missing branch or counts. A state is accepted only when two runs agree; a state that never settles fails the recording.
"""
import json, os, subprocess, sys, tempfile
here = os.path.dirname(os.path.abspath(__file__))
data = os.path.join(here, "..", "extensions", "powerline-footer", "testdata")
states = os.path.join(data, "render-states.json")
env = dict(os.environ, TZ="UTC")
runs = []
agreed = {}
for attempt in range(12):
    out = tempfile.mktemp(suffix=".json")
    subprocess.run(["node", "--experimental-strip-types", os.path.join(here, "drive", "drive.mjs"), states, out], env=env, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    runs.append(json.load(open(out)))
    os.unlink(out)
    for name in runs[-1]:
        if name in agreed:
            continue
        for earlier in runs[:-1]:
            if earlier.get(name) == runs[-1][name]:
                agreed[name] = runs[-1][name]
                break
    if len(agreed) == len(runs[-1]):
        break
missing = [n for n in runs[-1] if n not in agreed]
if missing:
    sys.exit("states that never settled: %s" % missing)
order = list(runs[-1])
json.dump({n: agreed[n] for n in order}, open(os.path.join(data, "render-golden.json"), "w"), indent=1, ensure_ascii=False)
open(os.path.join(data, "render-golden.json"), "a").write("\n")
print(len(order), "states recorded in", len(runs), "runs")
