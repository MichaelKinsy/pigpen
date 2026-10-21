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

# SubagentWorkflow. The workflow runtime is on by default (workflowsEnabled unset); scheduling and worktree isolation
# stay switched off. The scripts spawn no agents: a workflow child's model requests would be served from the same
# scripted turns as the parent's, in an order that depends on how fast each host starts a child (PORT.md F2).
WF_SETTINGS = {"schedulingEnabled": False, "worktreeIsolation": False}
META = 'export const meta = { name: "%s", description: "%s" };\n'

def wf_scenario(name, description, script_args, settings=WF_SETTINGS, files=None, args=None, turns=None, tail=None):
    turns = turns or [[{"name": "SubagentWorkflow", "arguments": a}] for a in script_args]
    s = {"name": name, "description": description,
         "llm": [{"toolCalls": t} for t in turns] + [{"text": "done"}],
         "setup": setup(settings), "commands": {"git": {"mode": "missing"}, "gh": {"mode": "missing"}},
         "steps": [{"name": "prompt", "rpc": {"type": "prompt", "message": "go"}}]}
    if files: s["files"] = files
    if args: s["args"] = args
    if tail: s["tailMs"] = tail
    json.dump(s, open(os.path.join(out, name + ".json"), "w"), indent=2, ensure_ascii=False)
    open(os.path.join(out, name + ".json"), "a").write("\n")

wf_scenario("workflow-tools", "With workflows on (the default) the request carries the SubagentWorkflow tool, its description, schema and guidelines.", [])
wf_scenario("workflow-bad-meta", "A script whose meta is not a literal is refused up front with the author-facing text.",
            [{"script": 'export const meta = { name: computeName(), description: "x" };\n'}])
wf_scenario("workflow-no-meta", "A script with no meta block is refused up front.", [{"script": "return 1;\n"}])
wf_scenario("workflow-no-source", "A call with none of script, scriptPath and name is refused, naming the three.", [{}])
wf_scenario("workflow-unknown-name", "An unknown saved name lists the saved workflows that do exist, and leaves out a non-workflow file.",
            [{"name": "nightlyy"}],
            files={".pi/workflows/nightly.js": META % ("nightly", "saved"), ".pi/workflows/utils.js": "const x = 1;\n"})
wf_scenario("workflow-bad-name", "A name that tries to leave its directories is refused.", [{"name": "../escaped"}])
wf_scenario("workflow-resume-unknown", "A run id this session never issued is an error, not a cold start.",
            [{"script": META % ("p", "d"), "resumeFromRunId": "wf_deadbeef1234"}])
wf_scenario("workflow-missing-script-path", "A missing scriptPath is reported rather than falling back to script.",
            [{"script": META % ("p", "d"), "scriptPath": "nope.js"}])
# No scenario starts a run (workflow-run-inline, -run-throws, -determinism, -unknown-option, -saved-name, -title-ignored,
# -nested, -flag were written and recorded, then dropped): a run's task id (wf_ + random hex) and its duration_ms reach the
# model in the task notification and the tool result, and pigeq's normalizer (N1-N6) has no rule for either, so the same
# scenario never compares equal to its own golden. Those behaviors are covered by the twins (workflow_tool_twin_test.go).
# workflow-flag-bare (a bare --subagents-workflow-file) has no scenario: Pi refuses to start with `Extension flag requires a value`, so there is nothing to record; the twin covers it.
wf_scenario("workflow-off", "With workflowsEnabled false the tool is not offered and the flag is refused.", [],
            settings={**WF_SETTINGS, "workflowsEnabled": False}, files={"flow.js": META % ("flagged", "x")}, args=["--subagents-workflow-file=flow.js"])
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

# Not generated: a background Agent call. Pi's reply carries an `Output file:` line (output transcripts are not
# ported), a nanoid-shaped id and a trailing notification turn with `<output-file>`, and Pi's rpc mode draws no widget
# where this port sends the widget rows, so the trace can only differ (PORT.md, "Display surfaces"). The renderers
# the call feeds are covered by the layer-1 RenderTool checks and the twins instead.

# showCost and showModel are accepted settings (they only add to what is displayed and to the texts the model reads),
# not refused: with both on, a session that never starts an agent must produce the same trace as the original, with
# no refusal notice.
scenario("agent-not-found-showcost", "showCost and showModel on: accepted without a notice; an unknown agent id is reported.",
         [[call("get_subagent_result", agent_id="nope")]], settings={**SETTINGS, "showCost": True, "showModel": True})
