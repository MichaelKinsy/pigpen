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
wf_scenario("workflow-run-inline", "An inline script runs in the background: the call returns a task id, and the finished run reaches the model as a task notification.",
            [{"script": META % ("probe", "a probe") + 'phase("Scan");\nlog("scanned 3 files");\nconsole.log("a", 1);\nconst r = await parallel([async () => 1, async () => { throw new Error("x"); }, async () => 3]);\nreturn { r, spent: budget.spent(), total: budget.total === null, args };'}], tail=2500)
wf_scenario("workflow-run-throws", "A script that throws is a failed run: the notification says Error and carries the reason.",
            [{"script": META % ("boom", "throws") + 'throw new Error("script blew up");'}], tail=2500)
wf_scenario("workflow-determinism", "Date.now, new Date() and Math.random throw inside a script, with the guidance text.",
            [{"script": META % ("clock", "reads the clock") + 'try { Date.now(); } catch (e) { return e.message; }'}], tail=2500)
wf_scenario("workflow-unknown-option", "agent() rejects an option it does not know, by name, before anything runs.",
            [{"script": META % ("opts", "bad option") + 'return await agent("x", { agenttype: "Explore" });'}], tail=2500)
wf_scenario("workflow-bad-meta", "A script whose meta is not a literal is refused up front with the author-facing text.",
            [{"script": 'export const meta = { name: computeName(), description: "x" };\n'}])
wf_scenario("workflow-no-meta", "A script with no meta block is refused up front.", [{"script": "return 1;\n"}])
wf_scenario("workflow-no-source", "A call with none of script, scriptPath and name is refused, naming the three.", [{}])
wf_scenario("workflow-unknown-name", "An unknown saved name lists the saved workflows that do exist, and leaves out a non-workflow file.",
            [{"name": "nightlyy"}],
            files={".pi/workflows/nightly.js": META % ("nightly", "saved"), ".pi/workflows/utils.js": "const x = 1;\n"})
wf_scenario("workflow-saved-name", "A saved workflow runs by name from .pi/workflows and reports its own file as the script.",
            [{"name": "nightly"}], files={".pi/workflows/nightly.js": META % ("nightly", "saved") + 'return "from the saved file";'}, tail=2500)
wf_scenario("workflow-bad-name", "A name that tries to leave its directories is refused.", [{"name": "../escaped"}])
wf_scenario("workflow-resume-unknown", "A run id this session never issued is an error, not a cold start.",
            [{"script": META % ("p", "d"), "resumeFromRunId": "wf_deadbeef1234"}])
wf_scenario("workflow-missing-script-path", "A missing scriptPath is reported rather than falling back to script.",
            [{"script": META % ("p", "d"), "scriptPath": "nope.js"}])
wf_scenario("workflow-title-ignored", "title and description are accepted and ignored.",
            [{"script": META % ("titled", "d") + "return 1;", "title": "Nightly", "description": "x"}], tail=2500)
wf_scenario("workflow-nested", "workflow() runs a saved workflow inline and passes args.",
            [{"script": META % ("outer", "calls inner") + 'return await workflow("inner", { n: 21 });'}],
            files={".pi/workflows/inner.js": META % ("inner", "doubles") + "return args.n * 2;"}, tail=2500)
wf_scenario("workflow-flag", "--subagents-workflow-file runs a script at session start, appends the card entry and hands the result over as next-turn context.",
            [], files={"flow.js": META % ("flagged", "from the flag") + 'log("scanned");\nreturn "all clear";'}, args=["--subagents-workflow-file=flow.js"], tail=2500)
wf_scenario("workflow-flag-bare", "A bare --subagents-workflow-file is explained, not read as a file called true.", [], args=["--subagents-workflow-file"])
wf_scenario("workflow-off", "With workflowsEnabled false the tool is not offered and the flag is refused.", [],
            settings={**WF_SETTINGS, "workflowsEnabled": False}, files={"flow.js": META % ("flagged", "x")}, args=["--subagents-workflow-file=flow.js"])

# Not generated: a foreground agent. The child's model request carries "inserted" text equal to the original's (the
# agent's header, environment block and prompt) and the same user message; the harness's "removed" part is the host's
# own default system prompt, which says "pi" under Pi and "pig" under PiG, so that comparison cannot pass. PORT.md.
