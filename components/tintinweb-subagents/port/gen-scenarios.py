#!/usr/bin/env python3
"""Generates port/scenarios/*.json. Every scenario starts with the original's workflows, scheduling and worktree
isolation switched off (.pi/subagents.json), the features this port does not offer, and declares git as missing
(the original's environment probe starts it)."""
import json, os
out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
SETTINGS = {"workflowsEnabled": False, "schedulingEnabled": False, "worktreeIsolation": False}

def setup(settings=SETTINGS, agents=None):
    body = json.dumps(settings).replace("'", "'\\''")
    out = [["sh", "-c", "mkdir -p .pi && printf '%s' '" + body + "' > .pi/subagents.json"]]
    for name, text in (agents or {}).items():
        out.append(["sh", "-c", "mkdir -p .pi/agents && printf '%s' '" + text.replace("'", "'\\''") + "' > .pi/agents/" + name + ".md"])
    return out

def call(name, **args): return {"name": name, "arguments": args}

def scenario(name, description, turns, settings=SETTINGS, steps=None, agents=None):
    s = {"name": name, "description": description,
         "llm": [{"toolCalls": t} for t in turns] + [{"text": "done"}],
         "setup": setup(settings, agents), "commands": {"git": {"mode": "missing"}, "gh": {"mode": "missing"}},
         "steps": steps or [{"name": "prompt", "rpc": {"type": "prompt", "message": "go"}}]}
    json.dump(s, open(os.path.join(out, name + ".json"), "w"), indent=2, ensure_ascii=False)
    open(os.path.join(out, name + ".json"), "a").write("\n")

scenario("tool-tools", "The request carries the subagent tools with their schemas and descriptions.", [], steps=[{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}])
scenario("agent-not-found", "get_subagent_result and steer_subagent name an agent that does not exist.",
         [[call("get_subagent_result", agent_id="nope")], [call("steer_subagent", agent_id="nope", message="x")]])
scenario("agent-unknown-type-strict", "With fallbackSubagent none an unknown type is refused, naming the available ones.",
         [[call("Agent", prompt="x", description="d", subagent_type="typoo")]], settings={**SETTINGS, "fallbackSubagent": "none"})

scenario("nested-agent-file", "An agent file with allowed_subagents and a maxSubagentDepth setting are accepted: the agent is listed, nothing is refused.",
         [], settings={**SETTINGS, "maxSubagentDepth": 3}, steps=[{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}],
         agents={"orchestrator": "---\ndescription: Delegates to the worker\ntools: read\nallowed_subagents: worker\n---\nYou delegate.\n",
                 "worker": "---\ndescription: Does the work\ntools: read\n---\nYou work.\n"})

# Not generated: a foreground agent. The child's model request carries "inserted" text equal to the original's (the
# agent's header, environment block and prompt) and the same user message; the harness's "removed" part is the host's
# own default system prompt, which says "pi" under Pi and "pig" under PiG, so that comparison cannot pass. PORT.md.
