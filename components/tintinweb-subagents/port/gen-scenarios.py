#!/usr/bin/env python3
"""Generates port/scenarios/*.json. Every scenario starts with the original's workflows, scheduling and worktree
isolation switched off (.pi/subagents.json) unless it is about one of them, and declares git as missing (the original's
environment probe starts it) unless it needs a real repository."""
import json, os
out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
SETTINGS = {"workflowsEnabled": False, "schedulingEnabled": False, "worktreeIsolation": False}

def setup(settings=SETTINGS):
    body = json.dumps(settings).replace("'", "'\\''")
    return [["sh", "-c", "mkdir -p .pi && printf '%s' '" + body + "' > .pi/subagents.json"]]

def call(name, **args): return {"name": name, "arguments": args}

def scenario(name, description, turns, settings=SETTINGS, steps=None, files=None, agent_files=None, llm=None, commands=None, setup_extra=None):
    s = {"name": name, "description": description,
         "llm": llm if llm is not None else [{"toolCalls": t} for t in turns] + [{"text": "done"}],
         "setup": setup(settings) + (setup_extra or []), "commands": commands or {"git": {"mode": "missing"}, "gh": {"mode": "missing"}},
         "steps": steps or [{"name": "prompt", "rpc": {"type": "prompt", "message": "go"}}]}
    if files: s["files"] = files
    if agent_files: s["agentFiles"] = agent_files
    json.dump(s, open(os.path.join(out, name + ".json"), "w"), indent=2, ensure_ascii=False)
    open(os.path.join(out, name + ".json"), "a").write("\n")

scenario("tool-tools", "The request carries the subagent tools with their schemas and descriptions.", [], steps=[{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}])
scenario("agent-not-found", "get_subagent_result and steer_subagent name an agent that does not exist.",
         [[call("get_subagent_result", agent_id="nope")], [call("steer_subagent", agent_id="nope", message="x")]])
scenario("agent-unknown-type-strict", "With fallbackSubagent none an unknown type is refused, naming the available ones.",
         [[call("Agent", prompt="x", description="d", subagent_type="typoo")]], settings={**SETTINGS, "fallbackSubagent": "none"})
scenario("agent-resume-not-found", "Agent with resume naming an agent that does not exist: the tool says it was cleaned up.",
         [[call("Agent", prompt="x", description="d", subagent_type="Plan", resume="nope")]])
scenario("agent-resume-ignores-type", "resume replays a stored session and ignores subagent_type: an unusable type does not stop it, even with fallbackSubagent none.",
         [[call("Agent", prompt="x", description="d", subagent_type="typoo", resume="nope")]], settings={**SETTINGS, "fallbackSubagent": "none"})

# toolDescriptionMode: the Agent description of each mode (every combination of the optional features is proved in
# port/description-scenarios, which the port cannot run while it refuses those features).
HELLO = [{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}]
TEMPLATE = "Custom agents:\n{{typeList}}\n\nCompact:\n{{compactTypeList}}\nDir: {{agentDir}}\nUnknown: {{nope}}\nCost: $& stays literal"
scenario("tool-tools-compact", "toolDescriptionMode compact: the short Agent description with the one-line type list.", [],
         settings={**SETTINGS, "toolDescriptionMode": "compact"}, steps=HELLO)
scenario("tool-tools-custom", "toolDescriptionMode custom: the project's agent-tool-description.md with its placeholders substituted.", [],
         settings={**SETTINGS, "toolDescriptionMode": "custom"}, steps=HELLO, files={".pi/agent-tool-description.md": TEMPLATE})
scenario("tool-tools-custom-global", "toolDescriptionMode custom reads the global template when the project has none.", [],
         settings={**SETTINGS, "toolDescriptionMode": "custom"}, steps=HELLO, agent_files={"agent-tool-description.md": "GLOBAL CUSTOM\n{{compactTypeList}}"})
scenario("tool-tools-custom-no-file", "toolDescriptionMode custom without a template file falls back to the full description.", [],
         settings={**SETTINGS, "toolDescriptionMode": "custom"}, steps=HELLO)
scenario("tool-tools-invalid-mode", "An invalid toolDescriptionMode in subagents.json is dropped: the full description.", [],
         settings={**SETTINGS, "toolDescriptionMode": "tiny"}, steps=HELLO)
scenario("agent-model-not-found", "A model the call names that matches nothing is an error result listing the available models.",
         [[call("Agent", prompt="x", description="d", subagent_type="Plan", model="no-such-model-xyz")]])

# Not generated: a foreground agent. The child's model request carries "inserted" text equal to the original's (the
# agent's header, environment block and prompt) and the same user message; the harness's "removed" part is the host's
# own default system prompt, which says "pi" under Pi and "pig" under PiG, so that comparison cannot pass. PORT.md.

# Observability settings: outputTranscript, reportUsage and the output_transcript agent-file field are honored, so no
# refusal notice appears and the agent list shows the file's agent (the request carries the tool text).
scenario("obs-settings-honored", "outputTranscript and reportUsage in subagents.json are honored: the session starts with no notice.", [],
         settings={**SETTINGS, "outputTranscript": True, "reportUsage": True, "showCost": False},
         steps=[{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}])
scenario("obs-agent-file-output-transcript", "An agent file with output_transcript: true is accepted and listed in the Agent tool's text.", [],
         files={".pi/agents/audited.md": "---\ndescription: Always keeps a transcript\noutput_transcript: true\n---\n\nWrite a transcript regardless of the project default.\n"},
         steps=[{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}])
scenario("agent-result-verbose-not-found", "get_subagent_result with verbose names an agent that does not exist.",
         [[call("get_subagent_result", agent_id="nope", verbose=True, wait=False)]])
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
