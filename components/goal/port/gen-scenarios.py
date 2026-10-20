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
         normalize=IDS + [CTXEST], capture=NO_MESSAGES)
scenario("scheduler-budget", "A token budget spent inside an autonomous run ends the scheduling: the goal is budget-limited and no further run starts although the allowance remains. (create_goal activates the goal tools: PiG's next run sends stale tool snippets, see above.)",
         [prompt("Create it.", name="create", settleMs=5000)],
         files_=cfg(maxAutonomousRuns=5),
         llm=[turn(call("create_goal", objective="Summarize the log", token_budget=3)), turn(text="Working on it."), turn(text="Wrapping up.")],
         normalize=IDS + [CTXEST], capture=NO_RESULTS)
# ---- END GoalScenTasksSched ----

print(len(os.listdir(out)), "scenarios")
