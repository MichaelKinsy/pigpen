#!/usr/bin/env python3
"""Generates port/scenarios/*.json: the model enables the tool, then calls its management actions. The model is scripted
(one tool call per turn, G4); project files give the agents and chains to find."""
import json, os

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
os.makedirs(out, exist_ok=True)

HELPER = "---\nname: helper\ndescription: Helps with things\nmodel: claude-x\ntools: read, grep\n---\n\nYou help.\n"
REVIEWER = "---\nname: reviewer-x\ndescription: Reviews code\naliases: rx, critic\nthinking: high\nskills: lint, style\n---\n\nYou review.\n"
BROKEN = "---\nname: broken\ndescription: Broken one\nrunner:\n  type: unknown\n---\nBody\n"
PACKAGED = "---\nname: scout\npackage: Code Analysis\ndescription: Packaged scout\n---\n\nScout.\n"
REVIEW_CHAIN = "---\nname: review\ndescription: Review chain\n---\n\n## helper\n\nLook at it\n\n## reviewer-x\n\nReview {previous}\n"


# the original starts these programs somewhere (install, memory, npm root, process trees, gh); each one fails with exit 1 (a `missing` git would hide /usr/bin from the host's own launcher)
COMMANDS = ["git", "npm", "ps", "gh", "powershell.exe"]


def call(name, **args):
    return {"name": name, "arguments": args}


def scenario(name, description, calls, files=None, agent_files=None, to="scenarios"):

    llm = [{"toolCalls": [c]} for c in calls] + [{"text": "done"}]
    s = {"name": name, "description": description, "llm": llm,
         "steps": [{"name": "prompt", "rpc": {"type": "prompt", "message": "delegate the work"}}],
         "files": files or {}, "commands": {c: {"mode": "canned", "stdout": "", "stderr": "", "exit": 1} for c in COMMANDS}}
    if agent_files:
        s["agentFiles"] = agent_files
    dest = os.path.join(os.path.dirname(out), to)
    os.makedirs(dest, exist_ok=True)
    with open(os.path.join(dest, name + ".json"), "w") as f:
        json.dump(s, f, indent=2)
        f.write("\n")


ENABLE = call("subagents_enable")
PROJECT = {".pi/agents/helper.md": HELPER, ".pi/agents/reviewer-x.md": REVIEWER, ".pi/chains/review.chain.md": REVIEW_CHAIN}

scenario("list", "Enable the tool, then list the agents of the project and the builtin ones.",
         [ENABLE, call("subagent", action="list")], PROJECT)
scenario("list-scopes", "List with each agentScope; an invalid scope is refused.",
         [ENABLE, call("subagent", action="list", agentScope="project"), call("subagent", action="list", agentScope="user"),
          call("subagent", action="list", agentScope="nowhere")], PROJECT,
         {"agents/mine.md": "---\nname: mine\ndescription: A user agent\n---\n\nMine.\n"})
scenario("list-diagnostics", "A malformed definition is reported, a packaged one gets its runtime name.",
         [ENABLE, call("subagent", action="list")], {".pi/agents/broken.md": BROKEN, ".pi/agents/scout.md": PACKAGED, ".pi/agents/helper.md": HELPER})
scenario("get", "Detail of one agent by name, by alias and by an unknown name.",
         [ENABLE, call("subagent", action="get", agent="helper"), call("subagent", action="get", agent="rx"), call("subagent", action="get", agent="nobody"),
          call("subagent", action="get")], PROJECT)
# host-bound: the Path line of a builtin agent is the original's install directory, which the port cannot share;
# builtin_test.go compares this golden with that one line replaced
scenario("get-builtin", "Detail of a builtin agent with a runner and one with a default context.",
         [ENABLE, call("subagent", action="get", agent="worker"), call("subagent", action="get", agent="claude-code")], {}, to="host-bound/scenarios")
scenario("enable-only", "Enable twice: the second call finds the tool already active.",
         [ENABLE, ENABLE], {})

# agent management and the advertisement (AgentMgmt lane)
ADVERTISED = "---\nname: specialist\ndescription: Narrow specialist\nadvertise: true\n---\n\nAct narrowly.\n"
scenario("manage-create-update", "Create an agent, read it back, update its description and read it again.",
         [ENABLE, call("subagent", action="create", config={"name": "scribe", "description": "Writes notes", "systemPrompt": "Write notes.", "scope": "project"}),
          call("subagent", action="get", agent="scribe"),
          call("subagent", action="update", agent="scribe", config={"description": "Writes better notes"}),
          call("subagent", action="get", agent="scribe"),
          call("subagent", action="update", agent="scribe", config={"advertise": "yes"})], {".pi/keep": ""})
scenario("manage-delete", "Delete a project agent, then list: it is gone; deleting it again fails.",
         [ENABLE, call("subagent", action="delete", agent="helper", agentScope="project"), call("subagent", action="list", agentScope="project"),
          call("subagent", action="delete", agent="helper", agentScope="project")], PROJECT)
scenario("manage-disable-enable", "Disable a builtin agent, see it hidden from the list, enable it again.",
         [ENABLE, call("subagent", action="disable", agent="scout", agentScope="project"), call("subagent", action="list", agentScope="both"),
          call("subagent", action="enable", agent="scout", agentScope="project"), call("subagent", action="list", agentScope="both")], {".pi/keep": ""})
scenario("manage-reset", "Change a builtin agent's model through an override, then reset it.",
         [ENABLE, call("subagent", action="update", agent="scout", config={"model": "claude-x"}, agentScope="project"), call("subagent", action="get", agent="scout"),
          call("subagent", action="reset", agent="scout", agentScope="project"), call("subagent", action="get", agent="scout")], {".pi/keep": ""})
scenario("advertise-catalog", "An agent that opts in with advertise: true is listed in the system prompt once the subagent tool is active.",
         [ENABLE, call("subagent", action="list", capabilities=True)], {".pi/agents/specialist.md": ADVERTISED, ".pi/agents/helper.md": HELPER})
# the validate action: a script checked without a child (the original runs these without a child model)
OK_SCRIPT = 'const review = await runs.run("review", { agent: "helper", task: "Review the change" });\nreturn { output: review.output };'
scenario("validate-ok", "Validate a script that launches one known agent: ok, no errors; then the same script by workflowScriptPath.",
         [ENABLE, call("subagent", action="validate", workflowScript=OK_SCRIPT),
          call("subagent", action="validate", workflowScriptPath="flow.js")], {**PROJECT, "flow.js": OK_SCRIPT})
scenario("validate-errors", "Validate invalid scripts: a nested async function, a syntax error, an empty script, an unknown agent with a suggestion, a bad run key.",
         [ENABLE, call("subagent", action="validate", workflowScript="async function helper() { return 1; }\nreturn await helper();"),
          call("subagent", action="validate", workflowScript='await runs.run("a", {'),
          call("subagent", action="validate", workflowScript="   "),
          call("subagent", action="validate", workflowScript='return await runs.run("a", { agent: "helpr", task: "x" });'),
          call("subagent", action="validate", workflowScript='return await runs.run("bad key", { agent: "helper", task: "x" });')], PROJECT)
scenario("validate-budget", "Validate against a spawn budget: two static launches with a budget of one, then a dynamic fan-out that only warns.",
         [ENABLE, call("subagent", action="validate", maxSubagentSpawnsPerRun=1, workflowScript='await runs.run("a", { agent: "helper", task: "x" });\nawait runs.run("b", { agent: "helper", task: "y" });\nreturn 1;'),
          call("subagent", action="validate", maxSubagentSpawnsPerRun=4, workflowScript='const items = args.items;\nawait runs.all(items.map((i) => ({ key: "k" + i, agent: "helper", task: String(i) })));\nreturn 1;')], PROJECT)
scenario("validate-refusals", "Validate with direct execution fields, both script inputs, no script, an unreadable script path and a capacity override.",
         [ENABLE, call("subagent", action="validate", agent="helper", workflowScript="return 1"),
          call("subagent", action="validate", workflowScript="return 1", workflowScriptPath="flow.js"),
          call("subagent", action="validate"),
          call("subagent", action="validate", workflowScriptPath="missing.js"),
          call("subagent", action="validate", workflowScript="return 1", globalConcurrencyLimit=2)], PROJECT)
# The mission and schedule actions refuse bad input with the original's own text; no child model is needed (nothing launches).
scenario("mission-validation", "Every mission action refuses malformed input with the original's text.",
         [ENABLE, call("subagent", action="mission.create"),
          call("subagent", action="mission.create", mission={"title": "a", "summary": "b"}),
          call("subagent", action="mission.create", mission={"title": "Goal", "goal": True}),
          call("subagent", action="mission.create", mission={"title": "Bad", "extra": 1}),
          call("subagent", action="mission.create", mission={"title": "Bad", "budget": {"tokens": 0}}),
          call("subagent", action="mission.create", mission={"title": "Bad", "labels": [""]}),
          call("subagent", action="mission.show"),
          call("subagent", action="mission.show", missionId="../escape"),
          call("subagent", action="mission.list", missionScope="everywhere"),
          call("subagent", action="mission.update", missionId="m1", missionUpdate={}),
          call("subagent", action="mission.update", missionId="m1", missionUpdate={"unknown": 1}),
          call("subagent", action="mission.update", missionId="m1", missionUpdate={"goal": {"paused": "yes"}}),
          call("subagent", action="mission.update", missionId="m1", missionUpdate={"receipts": [{"kind": "ci", "status": "ready", "title": "t", "url": "relative"}]}),
          call("subagent", action="mission.close", missionId="m1", missionStatus="active"),
          call("subagent", action="mission.attach-run", missionId="m1"),
          call("subagent", action="mission.resolve-decision", missionId="m1", id="d1")], {})
scenario("mission-launch-validation", "A launch with a malformed mission or missionId is refused before any child starts.",
         [ENABLE, call("subagent", agent="helper", task="Look", mission=True),
          call("subagent", agent="helper", task="Look", mission={"title": " "}),
          call("subagent", agent="helper", task="Look", mission={"title": "Goal", "goal": False}),
          call("subagent", agent="helper", task="Look", mission={"title": "Goal", "goal": True}),
          call("subagent", agent="helper", task="Look", missionId="../escape"),
          call("subagent", agent="helper", task="Look", missionId="m1", mission=False)], PROJECT)
scenario("schedule-validation", "The schedule actions refuse malformed triggers and ids with the original's text.",
         [ENABLE, call("subagent", action="schedule.create", at="soon", workflowScript="return 1;"),
          call("subagent", action="schedule.create", at="+0m", workflowScript="return 1;"),
          call("subagent", action="schedule.create", every="5x", workflowScript="return 1;"),
          call("subagent", action="schedule.pause", id="bad id!"),
          call("subagent", action="schedule.run"),
          call("subagent", action="schedule.delete")], {})
# the background (async) runs: what the model reads when no child is involved. Each text is the original's, none needs a child
# session, so the original under Pi and the port under PiG can be compared on them.
scenario("async-status", "Enable the tool, then ask for the status of the session's background runs (none), and of a run id that does not exist.",
         [ENABLE, call("subagent", action="status"), call("subagent", action="status", id="no-such-run-0000")], PROJECT)
scenario("bg-wait-empty", "Wait for background work when nothing runs: blocking, non-blocking, and for a run id that is not active.",
         [call("bg_wait"), call("bg_wait", nonBlocking=True), call("bg_wait", id="no-such-run-0000")], PROJECT)
scenario("unknown-action", "A misspelled and an unknown management action get the original's recovery text.",
         [ENABLE, call("subagent", action="statsu"), call("subagent", action="launch")], PROJECT)
# Model-visible validation errors of the run options, which both the original under Pi and the port answer before any child starts
# (so no child model is needed). Each call names an existing agent and runs in the foreground (`async: false`).
def fg(**args):
    c = call("subagent", agent="helper", task="do the work", **args)
    c["arguments"]["async"] = False
    return c


scenario("run-public-validation", "The public execution boundary: an empty call, a blank agent, a non-string task and a blank action.",
         [ENABLE, call("subagent"), call("subagent", agent="  ", task="x"), call("subagent", agent="helper", task=5), call("subagent", action="  ")], PROJECT)
scenario("run-acceptance-validation", "Invalid acceptance policies of a single run: an unknown level, a bare true, a malformed JSON string.",
         [ENABLE, fg(acceptance={"level": "bogus"}), fg(acceptance=True), fg(acceptance="{bad")], PROJECT)
scenario("run-gate-validation", "Invalid gates of a single run: an empty gate, a gate that conflicts with acceptance, a gate of the wrong type.",
         [ENABLE, fg(gate={}), fg(gate="npm test", acceptance={"level": "checked"}), fg(gate=5)], PROJECT)
scenario("run-limit-validation", "Invalid run limits and output options: timeouts, an output mode that needs a file, an agent contract version.",
         [ENABLE, fg(timeoutMs=0), fg(timeoutMs=-1), fg(maxRuntimeMs="soon"), fg(outputMode="file-only"), fg(agentContract={"version": 2})], PROJECT)
