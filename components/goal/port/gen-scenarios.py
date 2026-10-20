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
