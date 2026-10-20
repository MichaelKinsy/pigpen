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
    core_bug = extra.pop("core_bug", False)
    s.update(extra)
    # core_bug: scenarios that expose a PiG core bug (reported in PORT.md) live in port/core-bugs, outside the checked set, until PiG is fixed.
    d = os.path.join(os.path.dirname(out), "core-bugs") if core_bug else out
    os.makedirs(d, exist_ok=True)
    with open(os.path.join(d, name + ".json"), "w") as f:
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

# ---- BEGIN GoalScenTasksSched: set_goal_tasks / update_goal_task and the autonomous-run scheduler ----
# create_goal, set_goal_tasks and the scheduler declaration (update_goal continuation) end the model's turn (terminate: true), so the next scripted model
# turn is consumed by the NEXT run (the next prompt step, or the scheduler's continuation), never by the same prompt. update_goal_task, a rejected call and a kept
# task list do not terminate: the same prompt continues with the next scripted turn. (An earlier tools-tasks attempt scripted "create_goal, then a text turn, then
# set_goal_tasks" in two prompts: the text turn answered the second prompt, so set_goal_tasks never ran and the dialog "was never requested".)
# Dialog: under Pi's RPC mode ctx.hasUI is true and ctx.mode is "rpc" (rpc-mode.ts:319-321, runner.ts:572-574, :628-630), so showTaskConfirmation skips ui.custom
# and asks ui.select with the titled proposal; PiG binds the same (rpc_mode_runtime.go:196, inproc/runner.go:736-741) and its Node host reports the same
# ctx.mode and ctx.hasUI, so the original asks the same select under PiG (identical traces). The scenarios answer it: confirmed, "Keep current tasks", dismissed.
# tools-tasks* need a focused ACTIVE goal with the task tools offered; maxAutonomousRuns=0 would pause a created goal, so the goal is seeded with autoContinue off.
# Epoch milliseconds are different in every run by construction, and no normalize rule can reach a number (the rules rewrite strings): the
# original's checkpoint message carries details.timestamp = Date.now(), and the details of a goal tool result carry the scheduler's claimedAt and
# nextCheckAt. The scheduler scenarios therefore do not capture the host events that carry them. The model request (llm channel) still shows every
# continuation prompt, the notifications (ui channel) every scheduler message, and tool_execution_start every tool call.
CAPTURE = ["response", "extension_ui_request", "extension_error", "agent_start", "agent_end", "turn_start", "turn_end", "tool_execution_start", "tool_execution_end", "message_end"]
NO_MESSAGES = [c for c in CAPTURE if c != "message_end"]
NO_RESULTS = [c for c in NO_MESSAGES if c != "tool_execution_end"]


# The model request's "Context snapshot: N/100000 tokens" is the host's estimate of the context, which before the first reply includes the host's own system prompt.
# PiG's system prompt wording differs from Pi's (D22: "inside pig", the PiG documentation block; 2715 against 2666 characters in the same probe), so the estimate
# differs by the tokens of that wording (2539 against 2527). Only the estimates of four digits or more (the ones made before the model has reported a usage)
# are masked; the figures that follow a model reply come from the scripted model's usage and are compared exactly.
CTXEST = {"pattern": "Context snapshot: \\d{4,}/100000 tokens \\(\\d+%\\)", "replacement": "Context snapshot: <estimate>/100000 tokens"}

# The archive file of a completed goal is named from the clock.
ARCHIVED = {"pattern": "archived/goal_\\d{16}_", "replacement": "archived/goal_<stamp>_"}


def cfg(**kw):
    return {".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0, **kw})}


def task(id_, title, parent=None, contract=None, light=None):
    t = {"id": id_, "title": title}
    if parent:
        t["parent_id"] = parent
    if contract:
        t["verification_contract"] = contract
    if light is not None:
        t["lightweight_subtasks"] = light
    return t


keep_tasks = [{"value": "Keep current tasks"}]
dismissed = [{"cancelled": True}]
TREE = [task("task-1", "Write the parser"),
        task("task-2", "Wire the command line", contract="the CLI test passes", light=True),
        task("task-2a", "Add the flags", parent="task-2"), task("task-2b", "Add the help text", parent="task-2"),
        task("task-3", "Update the docs")]


def focused(objective, settings, auto=False, **seed_kw):
    """One open goal on disk, focused at startup by autoSelectSingleGoal. With autoContinue off nothing starts a run and the task tools are active
    (update_goal_task needs an active goal; create_goal would leave a paused one under maxAutonomousRuns=0 and a scheduled run otherwise)."""
    f = dict(settings)
    p, b = seed("seed-t", objective, auto=auto, **seed_kw)
    f[p] = b
    return f


scenario("tools-tasks", "set_goal_tasks proposes a task tree (select dialog, confirmed) and update_goal_task drives it: start, contract gate, batch, skip, reopen, completed tasks stay closed.",
         [prompt("Plan it.", ui=confirm_tasks, name="plan"),
          prompt("Work on it.", name="work")],
         files_=focused("Ship two files", cfg(autoSelectSingleGoal=True)),
         llm=[turn(call("set_goal_tasks", tasks=TREE, block_completion=True, change_summary="Initial plan")),
              turn(call("update_goal_task", task_id="task-1", status="start")),
              turn(call("update_goal_task", task_id="task-1", status="complete", evidence="parser tests pass")),
              turn(call("update_goal_task", task_id="task-2", status="complete")),
              turn(call("update_goal_task", task_id="task-2", status="complete", evidence="CLI test green")),
              turn(call("update_goal_task", updates=[{"task_id": "task-2a", "status": "start"}, {"task_id": "task-2a", "status": "complete", "evidence": "flags parse"}])),
              turn(call("update_goal_task", task_id="task-3", status="skipped")),
              turn(call("update_goal_task", task_id="task-3", status="skipped", reason="docs ship later")),
              turn(call("update_goal_task", task_id="task-3", status="pending")),
              turn(call("update_goal_task", task_id="task-1", status="pending")),
              turn(call("get_goal", section="tasks")),
              turn(text="Progress recorded.")],
         normalize=IDS + [CTXEST])
scenario("tools-tasks-edge", "set_goal_tasks: keeping the current list, a dismissed dialog, invalid trees (duplicate, missing parent, cycle, misplaced lightweight, depth, blank title).",
         [prompt("Plan it.", ui=confirm_tasks, name="plan"),
          prompt("Restructure.", ui=keep_tasks, name="keep"),
          prompt("Restructure again.", ui=dismissed, name="dismissed"),
          prompt("Bad trees.", name="invalid")],
         files_=focused("Migrate the data", cfg(autoSelectSingleGoal=True)),
         llm=[turn(call("set_goal_tasks", tasks=[task("t1", "Dump"), task("t2", "Load")])),
              turn(call("set_goal_tasks", tasks=[task("t1", "Dump"), task("t2", "Load"), task("t3", "Verify")], block_completion=True)),
              turn(text="Kept."),
              turn(call("set_goal_tasks", tasks=[task("t9", "Other")])),
              turn(text="Dismissed."),
              turn(call("set_goal_tasks", tasks=[task("t1", "Dump"), task("t1", "Again")])),
              turn(call("set_goal_tasks", tasks=[task("t1", "Dump", parent="nope")])),
              turn(call("set_goal_tasks", tasks=[task("t1", "Dump", parent="t2"), task("t2", "Load", parent="t1")])),
              turn(call("set_goal_tasks", tasks=[task("t1", "Dump", light=True)])),
              turn(call("set_goal_tasks", tasks=[task("t1", "Dump"), task("t2", "Load", parent="t1"), task("t3", "Verify", parent="t2")])),
              turn(call("set_goal_tasks", tasks=[task("t1", "  ")])),
              turn(text="Rejected.")],
         normalize=IDS + [CTXEST])
scenario("tools-tasks-disabled", "disableTasks: the task tools are not offered (the system prompt lists neither) and a call to one is the host's tool-not-found error.",
         [prompt("Plan it.", name="plan")],
         files_=focused("Ship the release", cfg(disableTasks=True, autoSelectSingleGoal=True)),
         llm=[turn(call("set_goal_tasks", tasks=[task("a", "A")])),
              turn(text="Disabled.")],
         normalize=IDS + [CTXEST])
# Under Pi the dialog above is requested (rpc: hasUI true, mode "rpc", so ctx.ui.custom is skipped and ctx.ui.select asked): see PORT.md.

# Scheduler (maxAutonomousRuns > 0). After every run of a focused active auto-continue goal the scheduler settles: it claims the next run (the continuation
# prompt is the original's checkpoint turn), or pauses with the reason. The runs follow each other without a prompt step, so the step that starts the first
# run waits long enough (settleMs) for every scripted run to land inside it.
#
# PiG DIFFERENCE (core bug, reported to the lead; not normalized): under PiG a run that an extension starts with pi.sendMessage(..., {triggerTurn: true}) does not
# use the system prompt its before_agent_start built: it sends the system prompt of the previous run (stale tool snippets) or, as the first run of a session, none.
# Pi sends the right one. The scheduler's continuation runs are such runs, so the scenarios keep them away from the two shapes that trigger it: the goal is
# focused before the first run (seeded, autoSelectSingleGoal) and the first run is a user prompt, so the tool set and the system prompt are the same
# for every run. scheduler-direct-first-run (first run started by /goal-direct) and scheduler-budget (tools activated by create_goal in the previous run)
# are the shapes that trigger it and therefore differ from the Pi golden in the llm channel's system field until PiG is fixed.
def sched(n, **kw):
    return cfg(**{**kw, "maxAutonomousRuns": n, "autoSelectSingleGoal": True})


def focused_active(objective, settings, **seed_kw):
    return focused(objective, settings, auto=True, **seed_kw)


def runs(msg, ms=5000, name="cmd", ui=None):
    s = step(msg, ui, name)
    s["settleMs"] = ms
    return s


def kick(msg="Get going.", ms=5000, name="prompt"):
    return prompt(msg, name=name, settleMs=ms)


scenario("scheduler-run-limit", "maxAutonomousRuns=2: after the user's run two autonomous runs follow, then the allowance is exhausted and the goal is paused; /goal-resume renews it for two more.",
         [kick(name="user"), runs("/goal-resume", name="resume")],
         files_=focused_active("Ship the report", sched(2)),
         llm=[turn(text="User run."), turn(text="Run one."), turn(text="Run two."), turn(text="Run three."), turn(text="Run four.")],
         normalize=IDS + [CTXEST], capture=NO_MESSAGES)
scenario("scheduler-blocked-ends", "A continuation run reports a blocker: the goal is blocked, the scheduler stops although the allowance is not spent.",
         [kick(name="user")],
         files_=focused_active("Deploy to production", sched(5)),
         llm=[turn(text="Looking."),
              turn(call("update_goal", status="blocked", reason="The deploy key is missing", attempted_actions=["searched the vault"], suggested_action="Add DEPLOY_KEY"))],
         normalize=IDS + [CTXEST], capture=NO_RESULTS)
scenario("scheduler-model-pauses", "A continuation run pauses the goal with a reason: no further run.",
         [kick(name="user")],
         files_=focused_active("Tidy the backlog", sched(5)),
         llm=[turn(text="First pass."),
              turn(call("update_goal", status="paused", reason="Waiting for the maintenance window", suggested_action="Resume after 22:00"))],
         normalize=IDS + [CTXEST], capture=NO_RESULTS)
scenario("scheduler-complete-ends", "With the auditor disabled a completion in a continuation run completes the goal and archives it; nothing further is scheduled.",
         [kick(name="user")],
         files_=focused_active("Rename the module", sched(5, disabled=True)),
         llm=[turn(text="Renaming."), turn(call("update_goal", status="complete", completion_summary="Renamed and tests pass"))],
         normalize=IDS + [ARCHIVED, CTXEST], capture=NO_RESULTS)
scenario("scheduler-sisyphus", "A Sisyphus goal with ordered steps and a task list and a run allowance of 3: the continuation prompts carry the step and task state.",
         [kick(name="user")],
         files_=focused_active("1) read the code. 2) write the fix. 3) run the tests.", sched(3), sisyphus=True,
                               tasks=[{"id": "step-1", "title": "read the code.", "status": "complete", "evidence": "read it"},
                                      {"id": "step-2", "title": "write the fix.", "status": "pending"},
                                      {"id": "step-3", "title": "run the tests.", "status": "pending", "verificationContract": "all tests pass"}],
                               extra={"currentTaskId": "step-2"}),
         llm=[turn(text="Reading."), turn(text="Writing."), turn(text="Testing."), turn(text="Done reading.")],
         normalize=IDS + [CTXEST], capture=NO_MESSAGES)
scenario("scheduler-strict-repair", "strictExecutionContract: a run without a disposition gets one repair run; saving ready lets the next run start; a second missing disposition after the repair pauses the goal.",
         [kick(name="user")],
         files_=focused_active("Backfill the table", sched(6, strictExecutionContract=True)),
         llm=[turn(text="No disposition."), turn(call("update_goal", continuation={"kind": "ready"})), turn(text="Ready run, no disposition."), turn(text="Repair run, still none.")],
         normalize=IDS + [CTXEST], capture=NO_RESULTS)
scenario("scheduler-wait-poll", "A strict goal declares a wait with polling; the poll fires a check run, then the repair run ends the chain with a pause for lack of a disposition.",
         [kick(name="user", ms=9000)],
         files_=focused_active("Wait for the build", sched(8, strictExecutionContract=True)),
         llm=[turn(text="Started."),
              turn(call("update_goal", continuation={"kind": "wait", "reason": "The build is queued", "deadline": "2099-01-01T00:00:00Z", "polling": {"interval_seconds": 1, "max_checks": 2}})),
              turn(text="Checked: still queued."), turn(text="Repair: nothing to report.")],
         normalize=IDS + [CTXEST], capture=NO_RESULTS)
scenario("scheduler-direct-first-run", "/goal-direct in a fresh session: the kickoff is the session's first run (PiG sends it without a system prompt: see above); the allowance of 2 is spent, /goal-resume renews it.",
         [runs("/goal-direct Ship the report", name="direct"), runs("/goal-resume", name="resume")],
         files_=sched(2),
         llm=[turn(text="Run one."), turn(text="Run two."), turn(text="Run three."), turn(text="Run four.")],
         normalize=IDS + [CTXEST], capture=NO_MESSAGES, core_bug=True)
scenario("scheduler-budget", "A token budget spent inside an autonomous run ends the scheduling: the goal is budget-limited and no further run starts although the allowance remains. (create_goal activates the goal tools: PiG's next run sends stale tool snippets, see above.)",
         [prompt("Create it.", name="create", settleMs=5000)],
         files_=cfg(maxAutonomousRuns=5),
         llm=[turn(call("create_goal", objective="Summarize the log", token_budget=3)), turn(text="Working on it."), turn(text="Wrapping up.")],
         normalize=IDS + [CTXEST], capture=NO_RESULTS, core_bug=True)
# ---- END GoalScenTasksSched ----
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

# ==== GoalScenAuditNet: the completion auditor (child pig) and recovery from provider outages =====================================
# Completion auditor. The original runs the auditor on an in-process nested session (createAgentSession); the port runs a child
# `pig --mode json` that inherits the lane's agent directory, so it reaches the harness's scripted model server exactly like the
# parent does and the nested session's requests enter the same trace (messages, tool definitions, system delta). The scripted turns
# are consumed in request order across parent and auditor: the parent's update_goal(complete) call waits for the audit, so the order
# is fixed (parent turn, auditor turns, parent turn). update_goal(complete) is a terminating tool: after an approval the run ends
# without another parent request; after a rejection the parent gets the rejection text as the tool result and one more request.
# The goal is seeded and auto-selected: create_goal also terminates the run (the model's next turn would never be requested).
OBJECTIVE = "Write notes.txt with the word ready\nSuccess criteria: notes.txt exists and says ready"
AUDIT_FILES = files(("seed-aud", OBJECTIVE, {}), settings=False)
AUDIT_FILES.update({".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0, "autoSelectSingleGoal": True}), "notes.txt": "ready\n"})
# The auditor's own system prompt replaces the host's whole prompt, so the harness records the host's baseline text as the part the
# auditor's prompt "removed" (systemDelta), and that wording differs between Pi and PiG. The scenarios run under a scenario-chosen base
# prompt (--system-prompt, identical in both hosts, plus the host's <cwd> section, which the delta strips as a common suffix).
BASE_PROMPT = ["--system-prompt", "You are the executor of a goal. Work on it with the goal tools and keep every answer short."]
# The archive's file name carries the time of archival (archived/goal_<16 digits>_<id>.md).
AUDIT_NORMALIZE = IDS + [{"pattern": "archived/goal_\\d{16}_", "replacement": "archived/goal_<stamp>_"}]
# tool_execution_end is not captured: the original's update_goal(complete) returns {ok, content, details, terminate} (the completion commit
# result) and Pi forwards the extra `ok` property in the RPC event's result, while pig's sdk.ToolResult has no field for properties
# beyond content, details, terminate and the like (see PORT.md). The same result reaches the model as the toolResult message, which
# message_end still captures (content and details), and tool_execution_start still captures every call.
AUDIT_CAPTURE = ["response", "extension_ui_request", "extension_error", "agent_start", "agent_end", "turn_start", "turn_end",
                 "tool_execution_start", "message_end"]


def claim(summary):
    return call("update_goal", status="complete", completion_summary=summary)


scenario("audit-approved", "The auditor (a child pig on the scripted model) approves: the goal completes and archives without another parent request.",
         [prompt("Finish the notes.")], AUDIT_FILES,
         llm=[turn(claim("notes.txt holds the word ready")),
              turn(text="notes.txt exists and holds the word ready.\n<approved/>")],
         normalize=AUDIT_NORMALIZE, args=BASE_PROMPT, capture=AUDIT_CAPTURE)
scenario("audit-rejected-then-approved",
         "The auditor rejects with feedback (the parent sees it as the tool result; /goal-status shows the audit result), the next turn's prompt carries the "
         "rejection, the second audit is warm with the previous rejection and approves.",
         [prompt("Finish the notes.", name="first"), step("/goal-status", name="status"), prompt("Fix what the auditor found.", name="second")],
         AUDIT_FILES,
         llm=[turn(claim("done")),
              turn(text="notes.txt does not say ready yet.\nRequired: the file must contain the word ready.\n<disapproved/>"),
              turn(text="I will fix notes.txt."),
              turn(call("write", path="notes.txt", content="ready\n")), turn(claim("notes.txt now says ready")),
              turn(text="The previous rejection is resolved: notes.txt says ready.\n<approved/>")],
         normalize=AUDIT_NORMALIZE, args=BASE_PROMPT, capture=AUDIT_CAPTURE)
scenario("audit-tool-calls", "The auditor inspects the workspace with its read-only tools (read, grep, ls) before approving: each tool result reaches the next auditor request.",
         [prompt("Finish the notes.")], AUDIT_FILES,
         llm=[turn(claim("notes.txt holds the word ready")),
              turn(call("read", path="notes.txt")), turn(call("grep", pattern="ready", path="notes.txt")), turn(call("ls", path=".")),
              turn(text="Read notes.txt, searched it and listed the directory: the objective is met.\n<approved/>")],
         normalize=AUDIT_NORMALIZE, args=BASE_PROMPT, capture=AUDIT_CAPTURE)
scenario("audit-no-marker", "An audit report without a verdict marker on its last line is a rejection; the parent continues with the report as the tool result.",
         [prompt("Finish the notes.")], AUDIT_FILES,
         llm=[turn(claim("done")),
              turn(text="I looked at notes.txt and it seems fine, but I will not commit to a verdict."),
              turn(text="The audit gave no verdict.")],
         normalize=AUDIT_NORMALIZE, args=BASE_PROMPT)

# Provider outages. A fake provider (a scenario server) answers 503 or 429; the model is not scripted. Pi retries a failed request three
# times (settings.json retry, 10 ms base delay), then the goal's own recovery waits on its ladder (5, 10, 20 ... s; networkRecovery.maxDelayMs
# shortens it) and sends a checkpoint again. The scenario ends while a recovery timer is pending: shutdown cancels it.
BODY_503 = {"error": {"type": "server_error", "message": "Error from provider (Console): Upstream request failed: Endpoint is unavailable."}}
BODY_429 = {"message": "Provider returned error", "code": 429,
            "metadata": {"raw": "stealth/ox-alpha is temporarily rate-limited upstream. Please retry shortly.", "provider_name": "Stealth", "is_byok": False,
                         "limit_source": "upstream_provider_shared_pool"}}
BODY_401 = {"error": {"type": "authentication_error", "message": "Invalid API key."}}


# The recovery checkpoint is a custom message whose details carry Date.now() (a JSON number, which the normalizer's string rules do not
# reach), so a message_end event of this scenario cannot be compared between runs. The checkpoint stays visible: it starts the agent run
# whose model request (the http channel) carries the marker message, and the notifications and run lifecycle are captured.
CAPTURE = [c for c in ["response", "extension_ui_request", "extension_error", "agent_start", "agent_end", "turn_start", "turn_end",
                       "tool_execution_start", "tool_execution_end"]]


def sse_answer(text):
    chunks = [{"choices": [{"index": 0, "delta": {"role": "assistant", "content": ""}, "finish_reason": None}]},
              {"choices": [{"index": 0, "delta": {"content": text}, "finish_reason": None}]},
              {"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]},
              {"choices": [], "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}}]
    return "".join("data: %s\n\n" % json.dumps(dict(c, id="outage-ok", object="chat.completion.chunk", created=1, model="broken")) for c in chunks) + "data: [DONE]\n\n"


def failing(status, body, times=0):
    return {"path": "/v1/chat/completions", "status": status, "json": body, **({"times": times} if times else {})}


def outage(name, description, routes, goal_settings, steps, tail_ms=None):
    agent = {"settings.json": json.dumps({"defaultProvider": "outage", "defaultModel": "broken", "retry": {"maxRetries": 3, "baseDelayMs": 10}}),
             "models.json": json.dumps({"providers": {"outage": {"baseUrl": "{{server:provider}}/v1", "api": "openai-completions", "apiKey": "dummy",
                                                                 "compat": {"supportsDeveloperRole": False, "supportsReasoningEffort": False},
                                                                 "models": [{"id": "broken", "name": "Broken"}]}}}),
             "pi-goal-x-settings.json": json.dumps(goal_settings)}
    s = {"name": name, "description": description, "steps": steps,
         "files": files(("seed-net", "keep making progress despite provider outages", {}), settings=False),
         "agentFiles": agent, "servers": {"provider": {"routes": routes}},
         "args": BASE_PROMPT + ["--provider", "outage", "--model", "broken"], "normalize": IDS, "capture": CAPTURE}
    if tail_ms is not None:
        s["tailMs"] = tail_ms
    with open(os.path.join(out, name + ".json"), "w") as f:
        json.dump(s, f, indent=2)
        f.write("\n")


def after(msg, ms, name):
    return dict(step(msg, name=name), settleMs=ms)


LADDER = {"autoSelectSingleGoal": True, "maxAutonomousRuns": 100}
WORK = prompt("continue working on the task", name="prompt", settleMs=1500)
outage("net-503-ladder", "Sustained 503: after Pi's own retries the goal announces recovery 1 in 5 s, retries, and announces recovery 2 in 10 s (unbounded, escalating); shutdown cancels the pending retry.",
       [failing(503, BODY_503)], LADDER, [WORK, after("/goal-list", 5500, "ladder")], tail_ms=-1)
outage("net-429-ladder", "Sustained 429 rate limit (the OpenRouter shape): the same escalating, unbounded recovery.",
       [failing(429, BODY_429)], LADDER, [WORK, after("/goal-list", 5500, "ladder")], tail_ms=-1)
outage("net-503-recovers", "The provider fails the first run (503 on every Pi retry), then answers: recovery 1 in 1 s succeeds, the backoff is forgotten and the goal carries on under its run limit.",
       [failing(503, BODY_503, 4), {"path": "/v1/chat/completions", "headers": {"Content-Type": "text/event-stream"}, "body": sse_answer("Recovered.")}],
       {"autoSelectSingleGoal": True, "maxAutonomousRuns": 2, "networkRecovery": {"maxDelayMs": 1000}},
       [WORK, after("/goal-list", 6000, "recovery"), step("/goal-status", name="status")])
outage("net-429-allowance", "The provider keeps failing: each recovery run spends the autonomous-run allowance (maxAutonomousRuns 2) until the goal pauses; the notices carry the configured cap (networkRecovery.maxAttempts 5); /goal-recovery and /goal-status report afterwards.",
       [failing(429, BODY_429)], {"autoSelectSingleGoal": True, "maxAutonomousRuns": 2, "networkRecovery": {"maxAttempts": 5, "maxDelayMs": 1000}},
       [WORK, after("/goal-list", 4500, "exhausted"), step("/goal-recovery", name="recovery"), step("/goal-status", name="status")])
outage("net-401-not-recovered", "A 401 is not a transient outage: Pi does not retry it and the goal announces no recovery.",
       [failing(401, BODY_401)], {"autoSelectSingleGoal": True, "maxAutonomousRuns": 100, "networkRecovery": {"maxDelayMs": 1000}},
       [WORK, after("/goal-list", 3000, "quiet")])
# ==== end GoalScenAuditNet ========================================================================================================
