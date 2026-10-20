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

def scenario(name, description, turns, settings=SETTINGS, steps=None, files=None):
    s = {"name": name, "description": description,
         "llm": [{"toolCalls": t} for t in turns] + [{"text": "done"}],
         "setup": setup(settings), "commands": {"git": {"mode": "missing"}, "gh": {"mode": "missing"}},
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
