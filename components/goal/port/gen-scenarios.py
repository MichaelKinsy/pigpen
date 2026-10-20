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
# HOSTDOCS: PiG's system prompt names PiG's own documentation bundle ("PiG documentation ... <home>/docs/bundle-...") where Pi names
# its source tree. That is PiG's documented additive difference D22, not an extension difference, and it falls inside the
# system-prompt delta of every request that adds tool snippets. The scenario-level rule masks only that block (cf. D22).
HOSTDOCS = {"pattern": "(?s)<docs>\\n(?:PiG|Pi) documentation.*", "replacement": "<docs>"}
IDS = [HOSTDOCS, {"pattern": "(^|[^a-z0-9])[mn][a-z0-9]{7}-[a-z0-9]{6}\\b", "replacement": "${1}<goal-id>"},
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

# ---- remaining commands: /goal-status, -focus, -settings, -resume, -cancel, -recovery, -refresh, -tweak and the guided /goal and /sisyphus flow ----
# Seeded goals have fixed ids (seed-*), so their selector labels and notifications are exact. Output that carries an id the
# extension generates (drafting creates a goal) goes through IDS. Dialog answers come from the scenario; RPC mode has no terminal,
# so the questionnaire and the proposal dialog run as the original's per-question select/input dialogs (the host's ui.custom
# resolves to undefined there). Esc pausing a running goal (ctx.ui.onTerminalInput) and the terminal-only dashboard/overlays do
# not exist in RPC mode: they stay covered by the layer-1 twins (see PORT.md).

AGE = {"pattern": "\\(pid (\\d+), \\d+s old\\)", "replacement": "(pid ${1}, <age>s old)"}
BACKUP = {"pattern": "\\d{4}-\\d\\d-\\d\\dT\\d\\d-\\d\\d-\\d\\d-\\d{3}Z", "replacement": "<backup-stamp>"}


# Wall-clock time the extension stamps on a runtime ledger event (seeded events use 2020, so they stay readable).
NOW = {"pattern": "\\b20(?:2[1-9]|[3-9]\\d)-\\d\\d-\\d\\dT\\d\\d:\\d\\d:\\d\\d\\b", "replacement": "<now>"}
# The goal snapshot a run starts with quotes ctx.getContextUsage(), an estimate over the whole prompt. PiG's prompt names its documentation
# bundle where Pi's names its source tree (D22, masked by HOSTDOCS), so the estimate differs by a constant (12 tokens in every scenario
# that shows it); the number and its percentage are masked, the line itself is compared.
CTX = {"pattern": "Context snapshot: \\d+/(\\d+) tokens \\(\\d+%\\)", "replacement": "Context snapshot: <tokens>/${1} tokens (<pct>)"}
IDS.append(NOW)
IDS.append(CTX)


def val(v):
    return {"value": v}


def sel(g, status, mode="goal", focused=False):
    """The /goal-focus (and chooseOpenGoal) selector label of a seeded goal."""
    gid, objective = g[0], g[1]
    path, _ = seed(*g[:2], **(g[2] if len(g) == 3 else {}))
    title = objective.split("\n")[0]
    return "%s %s | %s | %s | %s %s" % ("*" if focused else " ", gid, status, mode, title, path)


def ledger(*events):
    return "".join(json.dumps(e) + "\n" for e in events)


LEDGER = ".pi/goals/goal_events.jsonl"
T0 = "2020-01-02T03:04:05.000Z"
TASKS = [{"id": "task-1", "title": "Read the code", "status": "complete", "evidence": "read src/main.go", "completedAt": "2026-01-02T04:00:00.000Z"},
         {"id": "task-2", "title": "Write the fix", "status": "pending", "verificationContract": "the unit test passes"},
         {"id": "task-3", "title": "Update the docs", "status": "skipped", "skipReason": "no docs exist", "skippedAt": "2026-01-02T04:30:00.000Z"}]

scenario("cancel-none", "/goal-cancel without an active draft.", [step("/goal-cancel")])
scenario("focus-none", "/goal-focus with no open goal.", [step("/goal-focus")])
GA = ("seed-a", "First goal", {"usage": {"tokensUsed": 12500, "activeSeconds": 3725}})
GB = ("seed-b", "Second goal\nwith detail", {"created": "2026-01-03T00:00:00.000Z", "sisyphus": True})
scenario("focus-single", "One open goal: /goal-focus focuses it, a second /goal-focus focuses it again; /goal-pause shows the focus.",
         [step("/goal-focus"), step("/goal-focus"), step("/goal-pause")], files(GA))
scenario("focus-select", "Two open goals: pick one in the selector, then dismiss it, then answer with a label that is not offered.",
         [step("/goal-focus", [val(sel(GB, "sisyphus running", "sisyphus"))]), step("/goal-focus", [{"cancelled": True}]),
          step("/goal-focus", [val("not a goal")]), step("/goal-pause")], files(GA, GB))
scenario("status-none", "/goal-status, verbose and health with no goal at all.",
         [step("/goal-status"), step("/goal-status verbose"), step("/goal-status health")])
scenario("status-unfocused-open", "Open goals exist but none is focused: dashboard, health.",
         [step("/goal-status"), step("/goal-status verbose"), step("/goal-status health")], files(GA, GB))
scenario("status-focused", "A focused goal with tasks, a budget, usage, a contract and ledger history: dashboard, verbose, health.",
         [step("/goal-focus"), step("/goal-status"), step("/goal-status verbose"), step("/goal-status health")],
         dict(files(("seed-a", "Ship the parser\nSuccess criteria: tests pass", {"usage": {"tokensUsed": 12500, "activeSeconds": 3725},
                                                                                "extra": {"tokenBudget": 50000, "currentTaskId": "task-2", "verificationContract": "run the full test suite"},
                                                                                "tasks": TASKS})),
              **{LEDGER: ledger({"type": "goal_created", "goalId": "seed-a", "objective": "Ship the parser", "sisyphus": False, "autoContinue": True, "at": T0},
                                {"type": "task_complete", "goalId": "seed-a", "taskId": "task-1", "evidence": "read src/main.go", "at": "2020-01-02T04:00:00.000Z"},
                                {"type": "audit_result", "goalId": "seed-a", "verdict": "disapproved", "report": "The tests were not run.", "at": "2020-01-02T05:00:00.000Z"})}),
         normalize=[NOW])
GP = ("seed-p", "Paused by agent", {"status": "paused", "auto": False, "extra": {"stopReason": "agent", "pauseReason": "needs a key", "pauseSuggestedAction": "ask the owner"}})
GS = ("seed-s", "Over budget", {"status": "budget_limited", "created": "2026-01-02T03:04:08.000Z", "usage": {"tokensUsed": 1200, "activeSeconds": 90}, "extra": {"tokenBudget": 1000}})
scenario("status-paused", "A goal the agent paused with a reason, and a budget-limited goal: standard and verbose status after focusing each.",
         [step("/goal-focus", [val(sel(GP, "paused (agent)"))]), step("/goal-status"), step("/goal-status verbose"),
          step("/goal-unfocus"), step("/goal-focus", [val(sel(GS, "budget limited"))]), step("/goal-status"), step("/goal-status health")],
         files(GP, GS), normalize=[NOW])
scenario("resume-none", "/goal-resume with nothing to resume.", [step("/goal-resume")])
scenario("resume-disabled", "/goal-resume while maxAutonomousRuns=0 refuses with the allowance reason.",
         [step("/goal-resume"), step("/goal-status")],
         files(("seed-p", "Paused goal", {"status": "paused", "auto": False, "extra": {"stopReason": "user"}})))
scenario("resume-select", "Two open goals and no focus: /goal-resume offers the selector, a dismissed selector changes nothing.",
         [step("/goal-resume", [{"cancelled": True}]), step("/goal-resume", [val(sel(("seed-q", "Second paused", {"created": "2026-01-02T03:04:06.000Z"}), "paused"))])],
         files(("seed-p", "First paused", {"status": "paused", "auto": False, "extra": {"stopReason": "user"}}),
               ("seed-q", "Second paused", {"status": "paused", "auto": False, "created": "2026-01-02T03:04:06.000Z", "extra": {"stopReason": "user"}})))
scenario("refresh-unchanged", "/goal-refresh when nothing changed on disk.", [step("/goal-refresh")], files(GA))


# -- guided drafting (/goal, /sisyphus, /goal-tweak) with scripted model turns and dialog answers --
ENABLED, DISABLED = "Enabled \u2014 require independent approval", "Disabled \u2014 skip the completion audit"
CONFIRM, CONTINUE, CANCEL = "1. Confirm \u2014 create this goal now (Recommended)", "2. Continue chatting \u2014 keep refining", "3. Cancel \u2014 discard this draft"


def draft(msg, ui=None, name="draft"):
    return step(msg, ui, name) | {"wait": "agent_end"}


def propose(objective, **kw):
    return turn(call("propose_goal_draft", objective=objective, **kw))


PARSER = "=== Goal ===\nObjective: Refactor the parser\nSuccess criteria: the parser tests pass\nVerification contract: run go test ./..."
scenario("draft-goal-confirm", "/goal: the model asks one question (select), proposes a goal with tasks, the user keeps the auditor and confirms; the goal is created and shown by /goal-status.",
         [draft("/goal Refactor the parser module", [val("2. The AST builder"), val(ENABLED), val(CONFIRM)]), step("/goal-status"), step("/goal-status verbose")],
         llm=[turn(call("goal_question", question="Which part of the parser?", options=["The tokenizer", "The AST builder"], recommended=0)),
              propose(PARSER, tasks=[{"id": "task-1", "title": "Extract the tokenizer"}, {"id": "task-2", "title": "Rewrite the AST builder", "verification_contract": "the builder tests pass"},
                                     {"id": "task-3", "title": "Update the callers", "parent_id": "task-2"}], block_completion=True),
              turn(text="unused")],
         normalize=IDS)
scenario("draft-questionnaire-custom", "A questionnaire with a custom answer (input dialog), a free-text question and a dismissed selector.",
         [draft("/goal Plan the release", [val("3. Write your own answer..."), val("  weekly  "), val("Ship notes"), {"cancelled": True}])],
         llm=[turn(call("goal_questionnaire", questions=[{"id": "cadence", "question": "Release cadence?", "context": "Pick the rhythm.", "options": ["daily", "monthly"]},
                                                         {"id": "notes", "question": "What goes in the notes?", "options": []}])),
              turn(call("goal_question", question="Which channel?", options=["web", "mail"], allow_custom=False)),
              turn(text="Drafting continues.")],
         normalize=IDS)
scenario("draft-sisyphus", "/sisyphus: a proposal whose mode does not match is refused, the right one is confirmed with the auditor disabled.",
         [draft("/sisyphus Port the module", [val(DISABLED), val(CONFIRM)]), step("/goal-status verbose")],
         llm=[propose("1) read the code, done when the module is understood. 2) write the port, done when the tests pass.", sisyphus=False),
              propose("1) read the code, done when the module is understood. 2) write the port, done when the tests pass.", sisyphus=True),
              turn(text="unused")],
         normalize=IDS)
scenario("draft-bad-proposals", "Proposal checks: empty objective, a proposal whose mode does not match /goal, then a valid goal with a token budget and no auto-continue.",
         [draft("/goal Tidy the repo", [val(ENABLED), val(CONFIRM)]), step("/goal-status")],
         llm=[propose("   "), propose("Tidy the repo", sisyphus=True), propose("Tidy the repo", token_budget=2000, auto_continue=False), turn(text="unused")],
         normalize=IDS)
scenario("draft-sisyphus-steps-needed", "A Sisyphus draft whose proposal has no ordered steps is refused with the guidance text.",
         [draft("/sisyphus Tidy", [val(ENABLED), val(CONFIRM)])],
         llm=[propose("tidy up", sisyphus=True), propose("1) read it, done when read. 2) fix it, done when fixed.", sisyphus=True), turn(text="unused")],
         normalize=IDS)
scenario("draft-refine-then-cancel", "Continue chatting keeps the draft active and keeps the auditor choice; a later Cancel discards it; /goal-cancel then finds nothing.",
         [draft("/goal Improve the docs", [val(DISABLED), val(CONTINUE), val(DISABLED), val(CANCEL)]), step("/goal-cancel"), step("/goal-status")],
         llm=[propose("Improve the docs"), propose("Improve the docs for the CLI"), turn(text="Cancelled.")],
         normalize=IDS)
scenario("draft-active-choices", "A second /goal while a draft is active: replace it, resume it, or cancel the new start; /goal-cancel discards the draft.",
         [draft("/goal Build a thing"), draft("/goal Another thing", [val("Replace it with a new draft")]), step("/goal Third thing", [val("Resume the existing draft")]),
          step("/goal Fourth thing", [val("Cancel")]), step("/goal-cancel"), step("/goal-cancel")],
         llm=[turn(text="What do you want to build?"), turn(text="Tell me more about it.")],
         normalize=IDS)
scenario("draft-tweak", "/goal-tweak on a focused goal with a budget: the proposal removes the budget and is confirmed.",
         [step("/goal-focus"), draft("/goal-tweak remove the token budget", [val(ENABLED), val(CONFIRM)]), step("/goal-status verbose")],
         llm=[propose("First goal", token_budget=None), turn(text="unused")],
         files_=files(("seed-a", "First goal", {"usage": {"tokensUsed": 1200, "activeSeconds": 60}, "extra": {"tokenBudget": 5000}, "tasks": TASKS})),
         normalize=IDS)
scenario("draft-tweak-paused", "Tweaking a paused goal while maxAutonomousRuns=0: the goal is revised, stays paused, and keeps its task progress.",
         [step("/goal-focus"), draft("/goal-tweak make the objective concrete", [val(ENABLED), val(CONFIRM)]), step("/goal-status")],
         llm=[propose("Ship the parser with full tests", tasks=[{"id": "task-1", "title": "Read the code"}, {"id": "task-4", "title": "Add tests"}]), turn(text="unused")],
         files_=files(("seed-p", "Ship the parser", {"status": "paused", "auto": False, "extra": {"stopReason": "user", "tokenBudget": 9000}, "tasks": TASKS})),
         normalize=IDS)
scenario("tweak-guards", "/goal-tweak with no goal at all (with and without a change).",
         [step("/goal-tweak"), step("/goal-tweak remove the budget")],
         files_=files(settings=True))
scenario("tweak-empty-and-long", "One open goal: /goal-tweak focuses it and asks for the change; a long change is refused when objectiveMaxChars is set.",
         [step("/goal-tweak"), step("/goal-tweak a change that is longer than ten characters")],
         files_=dict(files(GA, settings=False), **{".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0, "objectiveMaxChars": 10})}))
scenario("tweak-select", "Two open goals: /goal-tweak offers the selector; dismissing it changes nothing.",
         [step("/goal-tweak adjust it", [{"cancelled": True}]), step("/goal-tweak adjust it", [val("not offered")]),
          step("/goal-tweak ", [val(sel(GB, "sisyphus running", "sisyphus"))])], files_=files(GA, GB))

# -- refresh and recovery --
SPARE_GOAL, SPARE_BODY = seed("seed-new", "Added from outside", created="2026-01-02T03:04:09.000Z")
spare_name = SPARE_GOAL.split("/")[-1]
scenario("refresh-external", "Files edited behind the extension's back (a goal added, a goal removed, ledger lines, settings): /goal-refresh reports each change.",
         [step("/goal-status"), prompt("Change the files."), step("/goal-refresh"), step("/goal-refresh")],
         llm=[turn(call("bash", command="cp spare/%s .pi/goals/ && cat spare/ledger.jsonl >> .pi/goals/goal_events.jsonl && cp spare/settings.json .pi/pi-goal-x-settings.json && rm .pi/goals/%s" % (spare_name, seed("seed-a", "First goal")[0].split("/")[-1]))),
              turn(text="Done.")],
         files_=dict(files(GA), **{"spare/" + spare_name: SPARE_BODY,
                                   "spare/ledger.jsonl": ledger({"type": "goal_created", "goalId": "seed-new", "objective": "Added", "sisyphus": False, "autoContinue": True, "at": T0}) + "not json\n",
                                   "spare/settings.json": json.dumps({"maxAutonomousRuns": 0, "disableContracts": True})}),
         normalize=IDS)
scenario("recovery-healthy", "/goal-recovery on clean storage, and repair with nothing to repair.",
         [step("/goal-recovery"), step("/goal-recovery repair")], files(GA))
scenario("recovery-issues", "A malformed goal file, a malformed ledger line, a dead-pid lock and an unreadable lock: the report, a declined repair, a confirmed repair, then a clean report.",
         [step("/goal-recovery"), step("/goal-recovery repair", no), step("/goal-recovery repair", yes), step("/goal-recovery")],
         dict(files(GA), **{".pi/goals/active_goal_broken.md": "not a goal file", LEDGER: ledger({"type": "goal_created", "goalId": "seed-a", "objective": "First goal", "sisyphus": False, "autoContinue": True, "at": T0}) + "{broken\n",
                            ".pi/goals/.locks/seed-a.lock": json.dumps({"pid": 2147483000, "startedAt": "2026-01-01T00:00:00.000Z"}),
                            ".pi/goals/.locks/junk.lock": "not json", ".pi/goals/.locks/notes.txt": "ignored"}),
         normalize=[AGE, BACKUP])
GB3 = ("seed-b", "Second goal", {"created": "2026-01-03T00:00:00.000Z"})
scenario("recovery-orphan", "A goal file removed behind the extension's back leaves an orphaned snapshot entry: the report, a confirmed repair.",
         [prompt("Remove a goal."), step("/goal-recovery"), step("/goal-recovery repair", yes), step("/goal-recovery")],
         llm=[turn(call("bash", command="rm .pi/goals/%s" % seed(*GB3[:2], **GB3[2])[0].split("/")[-1])), turn(text="Done.")],
         files_=files(GA, GB3), normalize=IDS + [BACKUP])
# Not generated: resume-runs (/goal-resume with maxAutonomousRuns=1 and a scripted continuation turn). Under PiG the Go lane's first model request of the
# resumed run reached the scripted server with the whole base system prompt removed (system delta inserted ""), where Pi's carries the extension's
# tool list; it is reproducible on 3 of 3 runs and not understood, so it is listed in PORT.md instead of being recorded.

print(len(os.listdir(out)), "scenarios")
