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
scenario("agent-isolation-worktree", "A foreground agent with isolation worktree runs in a copy; its changes land on a branch the result names.", [],
         settings=ISO_SETTINGS, commands=GIT_REAL, setup_extra=REPO,
         llm=[{"toolCalls": [isolated(isolation="worktree")]}] + WRITE_MARKER + [{"text": "done"}])
scenario("agent-isolation-unchanged", "A worktree agent that changes nothing leaves no branch and no note.", [],
         settings=ISO_SETTINGS, commands=GIT_REAL, setup_extra=REPO,
         llm=[{"toolCalls": [isolated(isolation="worktree")]}, {"text": "NOTHING-TO-CHANGE"}, {"text": "done"}])
scenario("agent-isolation-not-git", "isolation worktree outside a git repository fails the tool call (strict).", [],
         settings=ISO_SETTINGS, commands=GIT_REAL,
         llm=[{"toolCalls": [isolated(isolation="worktree")]}, {"text": "done"}])
scenario("agent-isolation-off", "isolation off runs the agent in the checkout.", [],
         settings=ISO_SETTINGS, commands=GIT_REAL, setup_extra=REPO,
         llm=[{"toolCalls": [isolated(isolation="off")]}] + WRITE_MARKER + [{"text": "done"}])
scenario("agent-isolation-setting-off", "worktreeIsolation false drops a requested worktree: the agent runs in the checkout.", [],
         settings={**ISO_SETTINGS, "worktreeIsolation": False}, commands=GIT_REAL, setup_extra=REPO,
         llm=[{"toolCalls": [isolated(isolation="worktree")]}] + WRITE_MARKER + [{"text": "done"}])
WT_AGENT = "---\ndescription: Isolated worker\nisolation: worktree\n---\nWork in your copy.\n"
NOWT_AGENT = "---\ndescription: Never isolated\nisolation: off\n---\nWork in place.\n"
scenario("agent-isolation-agent-file", "An agent file with isolation: worktree isolates a call that did not ask.", [],
         settings=ISO_SETTINGS, commands=GIT_REAL, setup_extra=REPO, files={".pi/agents/wt.md": WT_AGENT},
         llm=[{"toolCalls": [call("Agent", prompt=CHILD_PROMPT, description="worktree work", subagent_type="wt", run_in_background=False)]}] + WRITE_MARKER + [{"text": "done"}])
scenario("agent-isolation-agent-file-off", "An agent file with isolation: off vetoes the call's worktree.", [],
         settings=ISO_SETTINGS, commands=GIT_REAL, setup_extra=REPO, files={".pi/agents/nowt.md": NOWT_AGENT},
         llm=[{"toolCalls": [call("Agent", prompt=CHILD_PROMPT, description="worktree work", subagent_type="nowt", run_in_background=False, isolation="worktree")]}] + WRITE_MARKER + [{"text": "done"}])
MEM_PROJECT = "---\ndescription: Remembers\nmemory: project\n---\nRemember things.\n"
MEM_RO = "---\ndescription: Reads memory\ntools: read, grep\nmemory: local\n---\nRead memory.\n"
MEMORY_MD = "# Memory\n- the build needs RUN_ME=1\n"
scenario("agent-memory-project", "An agent with memory: project gets the memory block with its MEMORY.md in its system prompt.", [],
         settings=ISO_SETTINGS, commands=GIT_REAL,
         files={".pi/agents/mem.md": MEM_PROJECT, ".pi/agent-memory/mem/MEMORY.md": MEMORY_MD},
         llm=[{"toolCalls": [call("Agent", prompt="Recall.", description="recall", subagent_type="mem", run_in_background=False)]}, {"text": "REMEMBERED"}, {"text": "done"}])
scenario("agent-memory-readonly", "An agent that cannot write gets the read-only memory block.", [],
         settings=ISO_SETTINGS, commands=GIT_REAL,
         files={".pi/agents/ro.md": MEM_RO, ".pi/agent-memory-local/ro/MEMORY.md": MEMORY_MD},
         llm=[{"toolCalls": [call("Agent", prompt="Recall.", description="recall", subagent_type="ro", run_in_background=False)]}, {"text": "REMEMBERED"}, {"text": "done"}])
