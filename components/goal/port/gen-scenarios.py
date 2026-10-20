#!/usr/bin/env python3
"""Generates port/scenarios/*.json: the goal commands as Pi's RPC mode drives them.

Every scenario sets maxAutonomousRuns=0 (the original's documented switch for "no automatic continuation"), so a created or resumed
goal never starts a model turn; the scheduler's other branches are not ported. Output that carries a generated goal id is avoided
(the ids are random in both lanes): such text is covered by the layer-1 tests with an injectable id."""
import json, os

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
os.makedirs(out, exist_ok=True)

SETTINGS = {".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0})}


def seed(gid, objective, status="active", auto=True, sisyphus=False, created="2026-01-02T03:04:05.000Z", usage=None, extra=None, tasks=None):
    meta = {"version": 3, "id": gid, "objective": objective, "status": status, "autoContinue": auto,
            "usage": usage or {"tokensUsed": 0, "activeSeconds": 0}, "sisyphus": sisyphus, "revision": 0,
            "createdAt": created, "updatedAt": created, "activePath": ".pi/goals/active_goal_%s_%s.md" % (created[:19].replace("-", "").replace(":", "").replace("T", ""), gid)}
    meta.update(extra or {})
    if tasks:
        meta["taskList"] = {"tasks": tasks, "blockCompletion": False, "proposedAt": created}
    body = "%s\n\n# Goal Prompt\n\n%s\n\n## Progress\n\n- Status: %s\n" % (json.dumps(meta, indent=2), objective, status)
    return meta["activePath"], body


def files(*goals, settings=True):
    f = dict(SETTINGS) if settings else {}
    for g in goals:
        p, b = seed(*g[:2], **g[2]) if len(g) == 3 else seed(*g)
        f[p] = b
    return f


def step(msg, ui=None, name=None):
    s = {"name": name or "cmd", "rpc": {"type": "prompt", "message": msg}}
    if ui:
        s["ui"] = ui
    return s


def scenario(name, description, steps, files_=None, **extra):
    s = {"name": name, "description": description, "llm": [{"text": "ok"}], "steps": steps}
    s["files"] = SETTINGS if files_ is None else files_
    s.update(extra)
    with open(os.path.join(out, name + ".json"), "w") as f:
        json.dump(s, f, indent=2)
        f.write("\n")


yes = [{"confirmed": True}]
no = [{"confirmed": False}]

scenario("direct-create", "/goal-direct creates and focuses a goal; with no autonomous runs allowed nothing is started.",
         [step("/goal-direct Ship the refactor")])
scenario("direct-multiline", "The notification shows the first meaningful line of a structured objective.",
         [step("/goal-direct === Goal ===\nObjective: Reduce latency\nSuccess criteria: p99 under 200ms")])
scenario("direct-empty", "No objective: a warning naming the command.", [step("/goal-direct   "), step("/sisyphus-direct")])
scenario("sisyphus-needs-steps", "A Sisyphus objective must carry ordered steps.",
         [step("/sisyphus-direct tidy up"), step("/sisyphus-direct 1) read the code. 2) write the fix.")])
scenario("direct-twice", "A second direct goal replaces the focus; no id appears in the output.",
         [step("/goal-direct First thing"), step("/goal-direct Second thing")])
scenario("contract-extracted", "A 'Verification contract:' line is split off the objective.",
         [step("/goal-direct Migrate the schema\nVerification contract: the migration test passes")])
scenario("contract-disabled", "With disableContracts the line stays in the objective.",
         [step("/goal-direct Migrate the schema\nVerification contract: the migration test passes")],
         {".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0, "disableContracts": True})})
scenario("pause-goal", "/goal-pause on a running goal, then again, then with none focused after /goal-unfocus is not asserted.",
         [step("/goal-direct Keep going"), step("/goal-pause"), step("/goal-pause")])
scenario("pause-none", "No goal and no open goals.", [step("/goal-pause"), step("/goal-clear"), step("/goal-unfocus")])
scenario("clear-confirmed", "/goal-clear asks, then archives.", [step("/goal-direct Throwaway"), step("/goal-clear", yes), step("/goal-pause")])
scenario("clear-declined", "Declining the confirmation changes nothing.", [step("/goal-direct Keep me"), step("/goal-clear", no), step("/goal-pause")])
scenario("pool-startup", "Two open goals on disk: the session starts unfocused and says so; /goal-list shows both.",
         [step("/goal-list")],
         files(("seed-b", "Second goal\nwith detail", {"created": "2026-01-03T00:00:00.000Z", "sisyphus": True}),
               ("seed-a", "First goal", {"usage": {"tokensUsed": 12500, "activeSeconds": 3725}})))
scenario("pool-skips-complete", "A complete goal and a malformed goal file do not count as open.",
         [step("/goal-list")],
         dict(files(("seed-a", "Open goal", {}), ("seed-done", "Done goal", {"status": "complete"})),
              **{".pi/goals/active_goal_broken.md": "not a goal file"}))
scenario("pool-states", "Paused, blocked and budget-limited goals are open; labels follow the status.",
         [step("/goal-list")],
         files(("seed-p", "Paused by user", {"status": "paused", "auto": False, "extra": {"stopReason": "user"}}),
               ("seed-q", "Paused by agent", {"status": "paused", "auto": False, "extra": {"stopReason": "agent", "pauseReason": "needs a key"}, "created": "2026-01-02T03:04:06.000Z"}),
               ("seed-r", "Blocked", {"status": "blocked", "created": "2026-01-02T03:04:07.000Z"}),
               ("seed-s", "Over budget", {"status": "budget_limited", "created": "2026-01-02T03:04:08.000Z", "extra": {"tokenBudget": 1000}})))
scenario("empty-list", "No goals at all.", [step("/goal-list")])
scenario("unfocus-seeded-session", "A new session after a goal exists starts unfocused (the default does not auto-select).",
         [step("/goal-direct Created here"), {"name": "new", "rpc": {"type": "new_session"}}, step("/goal-pause")])
scenario("direct-limit-unset-is-not-run", "Settings with an unknown key: the original rejects the whole file; direct goals still work.",
         [step("/goal-direct Odd settings")], {".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0, "nonsense": 1})})

# ---- scripted-model scenarios (the port runs the original's tools, scheduler and commands; the model is the harness's scripted server) ----

# Values the extension makes up on every run (goal ids from the clock and Math.random, timestamps): not text a port is expected to produce.
IDS = [{"pattern": "[a-z0-9]{8}-[a-z0-9]{6}", "replacement": "<goal-id>"},
       {"pattern": "\\d{4}-\\d\\d-\\d\\dT\\d\\d:\\d\\d:\\d\\d\\.\\d{3}Z", "replacement": "<iso>"},
       {"pattern": "active_goal_\\d{16}_", "replacement": "active_goal_<stamp>_"}]


def call(name, **arguments):
    return {"name": name, "arguments": arguments}


def turn(*calls, text=None):
    t = {}
    if text:
        t["text"] = text
    if calls:
        t["toolCalls"] = list(calls)
    return t


def prompt(msg, ui=None, name="prompt", **extra):
    s = {"name": name, "rpc": {"type": "prompt", "message": msg}}
    if ui:
        s["ui"] = ui
    s.update(extra)
    return s


confirm_tasks = [{"value": "Confirm task list"}]

scenario("tools-create-get", "The model creates a goal with create_goal and reads it with get_goal (summary, then the objective page).",
         [prompt("Start working on the report.")],
         llm=[turn(call("create_goal", objective="Write the quarterly report\nSuccess criteria: three sections")),
              turn(call("get_goal", section="objective")), turn(call("get_goal")), turn(text="The goal is set.")],
         normalize=IDS)
scenario("tools-tasks", "set_goal_tasks (confirmed through the select dialog), update_goal_task, get_goal tasks.",
         [prompt("Plan it.", confirm_tasks, name="plan"), prompt("Work the plan.", name="work")],
         llm=[turn(call("create_goal", objective="Ship two files")),
              turn(call("set_goal_tasks", tasks=[{"id": "a", "title": "First file"}, {"id": "b", "title": "Second file"}], block_completion=False)),
              turn(call("update_goal_task", task_id="a", status="complete", evidence="written")),
              turn(call("get_goal", section="tasks")), turn(text="One task left.")],
         normalize=IDS)
scenario("tools-blocked", "update_goal reports a blocker: the goal is blocked with the reason, attempted actions and suggested action.",
         [prompt("Do it.")],
         llm=[turn(call("create_goal", objective="Deploy to production")),
              turn(call("update_goal", status="blocked", reason="The deploy key is missing", attempted_actions=["searched the vault"], suggested_action="Add DEPLOY_KEY")),
              turn(text="Blocked.")],
         normalize=IDS)
scenario("tools-pause-and-bad-args", "update_goal pause, then an invalid combination and a missing reason.",
         [prompt("Go.")],
         llm=[turn(call("create_goal", objective="Migrate the data")),
              turn(call("update_goal", status="paused")),
              turn(call("update_goal", status="paused", reason="Waiting for the maintenance window", suggested_action="Resume after 22:00")),
              turn(text="Paused.")],
         normalize=IDS)

print(len(os.listdir(out)), "scenarios")
