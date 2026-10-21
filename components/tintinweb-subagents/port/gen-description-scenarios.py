#!/usr/bin/env python3
"""Generates port/description-scenarios/*.json: the Agent tool's request for every combination of the three optional
features (scheduling, worktree isolation, workflows) in each toolDescriptionMode, plus the custom-template cases.
They are recorded from the original under Pi into port/description-golden (`pigeq record --scenarios
port/description-scenarios --golden port/description-golden --ts port/oracle ...`) and read by the Go test
description_golden_test.go, which compares the text this port builds with what Pi sent. They are NOT checked against
the port by `pigeq check` (the port refuses the features it does not offer yet), so they live in their own directory."""
import itertools, json, os, shutil
here = os.path.dirname(os.path.abspath(__file__))
out = os.path.join(here, "description-scenarios")
shutil.rmtree(out, ignore_errors=True)
os.makedirs(out)

TEMPLATE = "My agents:\n{{typeList}}\nCompact:\n{{compactTypeList}}\nDir: {{agentDir}}\nRULES:{{scheduleGuideline}}{{isolationGuideline}}\nUnknown: {{nope}}\nCost: $& stays literal"

def setup(settings):
    body = json.dumps(settings).replace("'", "'\\''")
    return [["sh", "-c", "mkdir -p .pi && printf '%s' '" + body + "' > .pi/subagents.json"]]

def scenario(name, description, settings, files=None, agent_files=None):
    s = {"name": name, "description": description, "llm": [{"text": "done"}], "setup": setup(settings),
         "commands": {"git": {"mode": "missing"}, "gh": {"mode": "missing"}},
         "steps": [{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}]}
    if files: s["files"] = files
    if agent_files: s["agentFiles"] = agent_files
    with open(os.path.join(out, name + ".json"), "w") as f:
        json.dump(s, f, indent=2, ensure_ascii=False)
        f.write("\n")

def tag(sched, iso, wf): return "-".join(n for n, on in (("sched", sched), ("iso", iso), ("wf", wf)) if on) or "plain"

for mode in ("full", "compact", "custom"):
    for sched, iso, wf in itertools.product((False, True), repeat=3):
        settings = {"toolDescriptionMode": mode, "schedulingEnabled": sched, "worktreeIsolation": iso, "workflowsEnabled": wf}
        files = {".pi/agent-tool-description.md": TEMPLATE} if mode == "custom" else None
        scenario("desc-%s-%s" % (mode, tag(sched, iso, wf)),
                 "The Agent tool's request in %s mode with scheduling=%s, worktreeIsolation=%s, workflows=%s." % (mode, sched, iso, wf),
                 settings, files=files)

ALL_ON = {"toolDescriptionMode": "custom", "schedulingEnabled": True, "worktreeIsolation": True, "workflowsEnabled": False}
scenario("desc-custom-no-file", "custom mode with no template file falls back to the full description.", ALL_ON)
scenario("desc-custom-global-file", "custom mode reads the global template when the project has none.", ALL_ON,
         agent_files={"agent-tool-description.md": "GLOBAL CUSTOM\n{{compactTypeList}}"})
scenario("desc-custom-empty-project-file", "an empty project template is ignored and the global one is read.", ALL_ON,
         files={".pi/agent-tool-description.md": "  \n"}, agent_files={"agent-tool-description.md": "GLOBAL AFTER EMPTY {{agentDir}}"})
scenario("desc-custom-empty-both", "empty project and global templates fall back to the full description.", ALL_ON,
         files={".pi/agent-tool-description.md": "\n"}, agent_files={"agent-tool-description.md": "   "})
