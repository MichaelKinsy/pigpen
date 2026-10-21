#!/usr/bin/env python3
"""Generates port/scenarios/*.json. Unless a scenario is about scheduled runs, it starts with the original's workflows,
scheduling and worktree isolation switched off (.pi/subagents.json), so its recorded requests do not depend on them, and
declares git as missing (the original's environment probe starts it). The scheduling scenarios leave scheduling on, as
it is by default."""
import json, os
out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
SETTINGS = {"workflowsEnabled": False, "schedulingEnabled": False, "worktreeIsolation": False}
SCHEDULING_ON = {"workflowsEnabled": False, "worktreeIsolation": False}

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

# Scheduled runs. A call that registers a job answers with the job's random id (nanoid), which the trace cannot
# normalize, so only what is deterministic is recorded: the tool text and every refusal of a `schedule` call.
scenario("schedule-tool-text", "With scheduling on (the default) the Agent tool carries the schedule parameter and its guideline.", [],
         settings=SCHEDULING_ON, steps=[{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}])
AGENT = dict(prompt="check the repo", description="nightly check", subagent_type="general-purpose")
scenario("schedule-refusals", "A schedule call is refused with the original's text: with resume, with inherit_context, in the foreground, "
         "with a schedule that is not one, with a time in the past, and for an agent type that does not exist (fallbackSubagent none).",
         [[call("Agent", **AGENT, schedule="1h", resume="some-agent")],
          [call("Agent", **AGENT, schedule="1h", inherit_context=True)],
          [call("Agent", **AGENT, schedule="1h", run_in_background=False)],
          [call("Agent", **AGENT, schedule="every now and then")],
          [call("Agent", **AGENT, schedule="* * * * *")],
          [call("Agent", **AGENT, schedule="2020-01-01T00:00:00Z")],
          [call("Agent", **{**AGENT, "subagent_type": "typoo"}, schedule="1h")]],
         settings={**SCHEDULING_ON, "fallbackSubagent": "none"})
scenario("schedule-disabled", "With schedulingEnabled false a schedule call is answered with the disabled text.",
         [[call("Agent", **AGENT, schedule="1h")]])

# Not generated: a foreground agent. The child's model request carries "inserted" text equal to the original's (the
# agent's header, environment block and prompt) and the same user message; the harness's "removed" part is the host's
# own default system prompt, which says "pi" under Pi and "pig" under PiG, so that comparison cannot pass. PORT.md.
