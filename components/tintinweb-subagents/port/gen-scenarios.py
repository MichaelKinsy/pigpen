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

def scenario(name, description, turns, settings=SETTINGS, steps=None):
    s = {"name": name, "description": description,
         "llm": [{"toolCalls": t} for t in turns] + [{"text": "done"}],
         "setup": setup(settings), "commands": {"git": {"mode": "missing"}, "gh": {"mode": "missing"}},
         "steps": steps or [{"name": "prompt", "rpc": {"type": "prompt", "message": "go"}}]}
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

# @name mentions. The RPC prompt reaches the input hook as source "rpc" in a headless host, where only the `model` mode
# acts; everything else falls through to the model, which then sees the text as typed (or, for @main, without the prefix).
def say(text, **kw):
    return [{"name": "prompt", "rpc": {"type": "prompt", "message": text}, **kw}]

scenario("mention-main-escape", "`@main text` goes to the main model without the prefix.", [], steps=say("@main @explore is not a mention"))
scenario("mention-unknown-handle", "A handle that names no agent is left to the main model, text intact.", [], steps=say("@nosuchagent hello"))
scenario("mention-bare-handle", "A bare handle is not a send: the main model gets it as typed.", [], steps=say("@explore"))
scenario("mention-file-path", "A leading file path is pi's attachment, not a mention.", [], steps=say("@src/index.ts summarize this"))
scenario("mention-off", "With agentMentions off nothing is claimed.", [], settings={**SETTINGS, "agentMentions": "off"}, steps=say("@explore find the flaky test"))
scenario("mention-legacy-false", "The legacy boolean false reads as off.", [], settings={**SETTINGS, "agentMentions": False}, steps=say("@explore find the flaky test"))
scenario("mention-direct-headless", "In direct mode a headless prompt is left to the main model.", [], settings={**SETTINGS, "agentMentions": "direct"}, steps=say("@explore find the flaky test"))
scenario("mention-agent-prefix-unknown", "Claude Code's @agent-<type> spelling with no such type falls through.", [], steps=say("@agent-nosuchtype hello"))
scenario("mention-clone-turn", "In model mode a mention is taken by an off-screen clone: its request carries the conversation, the reminder and only the Agent tool; the clone calls Agent (here refused by fallbackSubagent none) and the main model is never asked.",
         [[call("Agent", prompt="x", description="d", subagent_type="typoo")]], settings={**SETTINGS, "fallbackSubagent": "none"},
         steps=say("@explore find the flaky test", wait="response", settleMs=3000))
# The oracle asks the model a third time after the clone's turns (the script is read in order, so a spare turn keeps the
# run going and lets the golden show what each side does with it).
_p = os.path.join(out, "mention-clone-turn.json"); _s = json.load(open(_p)); _s["llm"].append({"text": "done"}); json.dump(_s, open(_p, "w"), indent=2, ensure_ascii=False); open(_p, "a").write("\n")

# Not generated: a foreground agent. The child's model request carries "inserted" text equal to the original's (the
# agent's header, environment block and prompt) and the same user message; the harness's "removed" part is the host's
# own default system prompt, which says "pi" under Pi and "pig" under PiG, so that comparison cannot pass. PORT.md.
