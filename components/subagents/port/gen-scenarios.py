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
