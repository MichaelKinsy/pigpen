#!/usr/bin/env python3
"""Generates port/scenarios/*.json. Every scenario starts with the original's workflows, scheduling and worktree
isolation switched off (.pi/subagents.json), the features this port does not offer, and declares git as missing
(the original's environment probe starts it)."""
import json, os
out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
SETTINGS = {"workflowsEnabled": False, "schedulingEnabled": False, "worktreeIsolation": False}

def setup(settings=SETTINGS):
    body = json.dumps(settings).replace("'", "'\\''")
    return [["sh", "-c", "mkdir -p .pi && printf '%s' '" + body + "' > .pi/subagents.json"]]

def call(name, **args): return {"name": name, "arguments": args}

def scenario(name, description, turns, settings=SETTINGS, steps=None, llm=None, commands=None, setup_extra=None, files=None):
    s = {"name": name, "description": description,
         "llm": llm if llm is not None else [{"toolCalls": t} for t in turns] + [{"text": "done"}],
         "setup": setup(settings) + (setup_extra or []), "commands": commands or {"git": {"mode": "missing"}, "gh": {"mode": "missing"}},
         "steps": steps or [{"name": "prompt", "rpc": {"type": "prompt", "message": "go"}}]}
    if files:
        s["files"] = files
    json.dump(s, open(os.path.join(out, name + ".json"), "w"), indent=2, ensure_ascii=False)
    open(os.path.join(out, name + ".json"), "a").write("\n")

scenario("tool-tools", "The request carries the subagent tools with their schemas and descriptions.", [], steps=[{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}])
scenario("agent-not-found", "get_subagent_result and steer_subagent name an agent that does not exist.",
         [[call("get_subagent_result", agent_id="nope")], [call("steer_subagent", agent_id="nope", message="x")]])
scenario("agent-unknown-type-strict", "With fallbackSubagent none an unknown type is refused, naming the available ones.",
         [[call("Agent", prompt="x", description="d", subagent_type="typoo")]], settings={**SETTINGS, "fallbackSubagent": "none"})

# Not generated: a foreground agent. The child's model request carries "inserted" text equal to the original's (the
# agent's header, environment block and prompt) and the same user message; the harness's "removed" part is the host's
# own default system prompt, which says "pi" under Pi and "pig" under PiG, so that comparison cannot pass. PORT.md.

# ---- worktree isolation and persistent memory (a real git repository in the workspace) ----
GIT_REAL = {"git": {"mode": "real"}, "gh": {"mode": "missing"}}
REPO = [["sh", "-c", "git init -q && echo '# repo' > README.md && git add README.md && git commit -q -m initial"]]
ISO_SETTINGS = {"workflowsEnabled": False, "schedulingEnabled": False, "fleetView": False, "widgetMode": "off"}  # worktreeIsolation left at its default (on)
CHILD_PROMPT = "Create the marker file."
def isolated(**extra):
    return call("Agent", prompt=CHILD_PROMPT, description="worktree work", subagent_type="general-purpose", run_in_background=False, **extra)
WRITE_MARKER = [{"toolCalls": [call("bash", command="echo isolated > agent-work.txt")]}, {"text": "CHILD-EDITED-ITS-TREE"}]

scenario("tool-tools-isolation", "With worktree isolation on (the default) the Agent tool carries the isolation parameter and its guideline.", [],
         settings=ISO_SETTINGS, commands=GIT_REAL, steps=[{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}])
scenario("agent-isolation-not-git", "isolation worktree outside a git repository fails the tool call (strict).", [],
         settings=ISO_SETTINGS, commands=GIT_REAL,
         llm=[{"toolCalls": [isolated(isolation="worktree")]}, {"text": "done"}])
# Not generated: a worktree agent or a memory agent that runs a child model. The child's request cannot match under
# PiG (its host system prompt names pig where the original's names pi: F2 in PORT.md), and a worktree path carries a
# random agent id, so the worktree behavior is pinned by the unit tests (worktree_feature_test.go, worktree_twin_test.go,
# worktree_e2e_twin_test.go) instead. What is observable without a child is generated here.
WT_AGENT = "---\ndescription: Isolated worker\nisolation: worktree\n---\nWork in your copy.\n"
scenario("agent-isolation-agent-file-not-git", "An agent file with isolation: worktree outside a git repository fails the tool call (strict).", [],
         settings=ISO_SETTINGS, commands=GIT_REAL, files={".pi/agents/wt.md": WT_AGENT},
         llm=[{"toolCalls": [call("Agent", prompt=CHILD_PROMPT, description="worktree work", subagent_type="wt", run_in_background=False)]}, {"text": "done"}])
# Memory is the same: an agent with `memory:` that fails still reports a random agent id and a duration in the tool
# result's details, which this port's failure result does not carry, so the memory behavior is pinned by memory_twin_test.go
# and worktree_feature_test.go (TestMemoryThroughTheAgentTool).
