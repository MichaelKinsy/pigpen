#!/usr/bin/env python3
"""Generates port/drive/cases.json: scripted host sessions for drive.mjs (the original) and the Go replay test.

A case is a workspace (files, agentFiles, env), the session entries present at the start, and steps: a command with arguments, an
event, or a new session; a step may set the clock (`at`, ms after 2026-01-02T03:04:05Z) and answer the next confirmation."""
import json, os

here = os.path.dirname(os.path.abspath(__file__))
SET0 = {".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0})}
T0 = "2026-01-02T03:04:05.000Z"


def stamp(created):
    return created[:19].replace("-", "").replace(":", "").replace("T", "")


def seed(gid, objective, status="active", auto=True, sisyphus=False, created=T0, usage=None, extra=None, tasks=None, contract=None, version=3):
    meta = {"version": version, "id": gid, "objective": objective, "status": status, "autoContinue": auto,
            "usage": usage or {"tokensUsed": 0, "activeSeconds": 0}, "sisyphus": sisyphus, "revision": 0,
            "createdAt": created, "updatedAt": created, "activePath": ".pi/goals/active_goal_%s_%s.md" % (stamp(created), gid)}
    meta.update(extra or {})
    if tasks:
        meta["taskList"] = {"tasks": tasks, "blockCompletion": False, "proposedAt": created}
    if contract:
        meta["verificationContract"] = contract
    body = "%s\n\n# Goal Prompt\n\n%s\n\n## Progress\n\n- Status: %s\n" % (json.dumps(meta, indent=2), objective, status)
    # The file always lives at its own name under .pi/goals; an `activePath` in extra changes only the metadata (an unsafe
    # value must never place the seed outside the workspace).
    return ".pi/goals/active_goal_%s_%s.md" % (stamp(created), gid), body


def files(*goals, settings=SET0):
    f = dict(settings)
    for g in goals:
        p, b = seed(*g[:2], **g[2])
        f[p] = b
    return f


def focus(gid, reason="selected"):
    return {"type": "custom", "customType": "pi-goal-focus", "data": {"version": 1, "focusedGoalId": gid, "reason": reason}}


def cmd(c, args="", **kw):
    d = {"cmd": c, "args": args}
    d.update(kw)
    return d


cases = []


def case(name, steps, files_=None, **kw):
    d = {"name": name, "steps": [{"event": "session_start", "reason": "startup"}] + steps, "files": SET0 if files_ is None else files_}
    d.update(kw)
    cases.append(d)


T = [{"id": "t1", "title": "Read the code", "status": "complete", "completedAt": T0, "evidence": "read it"},
     {"id": "t2", "title": "Write the fix", "status": "pending", "verificationContract": "tests pass", "subtasks": [{"id": "t2a", "title": "Sub step", "status": "pending"}]},
     {"id": "t3", "title": "Skip me", "status": "skipped", "skippedAt": T0, "skipReason": "not needed"}]

case("direct-create", [cmd("goal-direct", "Ship the refactor", at=0), cmd("goal-list", at=1000)])
case("direct-twice", [cmd("goal-direct", "First thing", at=0), cmd("goal-direct", "Second thing", at=2000), cmd("goal-list", at=3000)])
case("direct-sisyphus", [cmd("sisyphus-direct", "1) read the code. 2) write the fix.", at=0), cmd("sisyphus-direct", "tidy", at=100), cmd("sisyphus-direct", ""), cmd("goal-direct", "  ")])
case("direct-multiline", [cmd("goal-direct", "=== Goal ===\nObjective: Reduce latency\nSuccess criteria: p99 under 200ms\nVerification contract: dashboards green", at=0)])
case("direct-crlf-contract", [cmd("goal-direct", "Fix it\r\nverification CONTRACT:   all green  \r\nBoundaries: none", at=0)])
case("direct-disabled-contract", [cmd("goal-direct", "Fix it\nVerification contract: green", at=0)],
     {".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0, "disableContracts": True})})
case("direct-env-contract", [cmd("goal-direct", "Fix it\nVerification contract: green", at=0)], env={"PI_GOAL_DISABLE_CONTRACTS": "1"})
case("direct-unicode-id", [cmd("goal-direct", "Résumé 🚀 work: ✓ done", at=5000), cmd("goal-list")])
case("direct-long-title", [cmd("goal-direct", "x" * 150 + "\nsecond line", at=0), cmd("goal-list")])
case("direct-global-settings", [cmd("goal-direct", "Global only", at=0)], {}, agentFiles={"pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0})})
case("direct-project-wins", [cmd("goal-direct", "Project wins", at=0)], {".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0})},
     agentFiles={"pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 5})})
case("direct-settings-file-env", [cmd("goal-direct", "Env file", at=0)], {"alt.json": json.dumps({"maxAutonomousRuns": 0})}, env={"PI_GOAL_SETTINGS_FILE": "alt.json"})
case("pause-goal", [cmd("goal-direct", "Keep going", at=0), cmd("goal-pause", at=4000), cmd("goal-pause", at=5000), cmd("goal-list")])
case("pause-none", [cmd("goal-pause"), cmd("goal-clear"), cmd("goal-unfocus")])
case("clear-confirmed", [cmd("goal-direct", "Throwaway", at=0), cmd("goal-clear", at=3000, confirm=True), cmd("goal-pause"), cmd("goal-list")])
case("clear-declined", [cmd("goal-direct", "Keep me", at=0), cmd("goal-clear", at=3000, confirm=False), cmd("goal-list")])
case("clear-after-pause", [cmd("goal-direct", "Pause then clear", at=0), cmd("goal-pause", at=1000), cmd("goal-clear", at=2000, confirm=True)])
case("unfocus-created", [cmd("goal-direct", "Detach me", at=0), cmd("goal-unfocus", at=1000), cmd("goal-unfocus", at=2000), cmd("goal-list")])
case("unfocus-none-with-open", [cmd("goal-unfocus")], files(("seed-a", "Open one", {})))
case("pool-startup", [cmd("goal-list")], files(("seed-b", "Second goal\nwith detail", {"created": "2026-01-03T00:00:00.000Z", "sisyphus": True}),
                                              ("seed-a", "First goal", {"usage": {"tokensUsed": 12500, "activeSeconds": 3725}})))
case("pool-states", [cmd("goal-list")], files(("seed-p", "Paused by user", {"status": "paused", "auto": False, "extra": {"stopReason": "user"}}),
     ("seed-q", "Paused by agent", {"status": "paused", "auto": False, "extra": {"stopReason": "agent", "pauseReason": "needs a key", "pauseSuggestedAction": "ask"}, "created": "2026-01-02T03:04:06.000Z"}),
     ("seed-r", "Blocked", {"status": "blocked", "created": "2026-01-02T03:04:07.000Z"}),
     ("seed-s", "Over budget", {"status": "budget_limited", "created": "2026-01-02T03:04:08.000Z", "extra": {"tokenBudget": 1000}}),
     ("seed-t", "Same time b", {"created": "2026-01-02T03:04:08.000Z"})))
case("pool-skips", [cmd("goal-list")], dict(files(("seed-a", "Open goal", {}), ("seed-done", "Done goal", {"status": "complete"}), ("seed-v2", "Old version", {"version": 2})),
     **{".pi/goals/active_goal_broken.md": "not a goal file", ".pi/goals/active_goal_empty.md": "{}\n\n# Goal Prompt\n\n\n",
        ".pi/goals/notes.md": "x", ".pi/goals/archived/goal_20250101000000000_old.md": "{}"}))
case("pool-unsafe-paths", [cmd("goal-list")], files(("seed-a", "Odd path", {"extra": {"activePath": "../escape.md", "archivedPath": "/etc/passwd"}})))
case("pool-body-edit", [cmd("goal-list")], {**files(("seed-a", "meta objective", {})), ".pi/goals/active_goal_20260102030405_seed-a.md":
     json.dumps({"version": 3, "id": "seed-a", "objective": "meta objective", "status": "active", "autoContinue": True, "usage": {"tokensUsed": 0, "activeSeconds": 0}, "sisyphus": False, "createdAt": T0, "updatedAt": T0, "activePath": ".pi/goals/active_goal_20260102030405_seed-a.md"}, indent=2) + "\n\n# Goal Prompt\n\nEdited by hand\n\n## Progress\n\n- Status: x\n"})
case("pool-tasks-pause", [cmd("goal-pause", at=7000)], files(("seed-a", "With tasks", {"tasks": T, "contract": "all tests pass", "extra": {"tokenBudget": 5000, "currentTaskId": "t2", "skipAuditor": True, "scheduler": {"version": 1, "owner": "other", "generation": "g1", "used": 2, "phase": "idle", "repairUsed": False}}})))
case("pool-bad-scheduler", [cmd("goal-pause", at=7000)], files(("seed-a", "Odd scheduler", {"extra": {"scheduler": {"version": 2}}})))
case("pause-select-cancelled", [cmd("goal-pause"), cmd("goal-clear")], files(("seed-a", "One", {}), ("seed-b", "Two", {"created": "2026-01-03T00:00:00.000Z"})))
case("pause-single-open", [cmd("goal-pause", at=3000), cmd("goal-list")], files(("seed-a", "Only open goal", {})))
case("resume-focus-entry", [{"event": "session_start", "reason": "resume"}, cmd("goal-list"), cmd("goal-pause", at=9000)],
     files(("seed-a", "One", {}), ("seed-b", "Two", {"created": "2026-01-03T00:00:00.000Z"})), entries=[focus("seed-b")])
case("resume-focus-lost", [cmd("goal-list")], files(("seed-a", "One", {})), entries=[focus("gone-goal")])
case("resume-focus-null", [cmd("goal-list")], files(("seed-a", "One", {})), entries=[focus("seed-a"), focus(None, "unfocused")])
case("resume-legacy-state", [cmd("goal-list"), cmd("goal-pause", at=1000)], files(("seed-a", "One", {})),
     entries=[{"type": "custom", "customType": "pi-goal-state", "data": {"version": 3, "goal": {"id": "seed-a", "objective": "One", "status": "active", "autoContinue": True}}}])
case("new-session", [cmd("goal-direct", "Created here", at=0), {"event": "new_session"}, cmd("goal-pause", at=2000), cmd("goal-list")])
case("shutdown", [cmd("goal-direct", "Before shutdown", at=0), {"event": "session_shutdown"}])
case("direct-millis", [cmd("goal-direct", "Millisecond stamp", at=1230), cmd("goal-pause", at=1999)])
case("direct-quotes", [cmd("goal-direct", 'Say "hi" \\ done {brace} } and [x]\nsecond: \t tab', at=0), {"event": "session_start", "reason": "resume"}, cmd("goal-list")])
SNAP = lambda name, goals: json.dumps({"version": 1, "dirMtimeMs": 1.5, "goals": goals})
a_path, a_body = seed("seed-a", "On disk A", {}) if False else seed("seed-a", "On disk A")
case("pool-snapshot-served", [cmd("goal-list")], {**files(("seed-a", "On disk A", {})), ".pi/.goals-pool-snapshot.json": SNAP("s", [
    {"id": "seed-a", "objective": "From the snapshot", "status": "active", "autoContinue": True, "usage": {"tokensUsed": 0, "activeSeconds": 0}, "sisyphus": False,
     "createdAt": T0, "updatedAt": T0, "activePath": ".pi/goals/active_goal_20260102030405_seed-a.md", "revision": 0}])})
case("pool-snapshot-stale", [cmd("goal-list")], {**files(("seed-a", "On disk A", {}), ("seed-b", "On disk B", {"created": "2026-01-03T00:00:00.000Z"})), ".pi/.goals-pool-snapshot.json": SNAP("s", [
    {"id": "seed-a", "objective": "From the snapshot", "status": "active", "autoContinue": True, "usage": {"tokensUsed": 0, "activeSeconds": 0}, "sisyphus": False,
     "createdAt": T0, "updatedAt": T0, "activePath": ".pi/goals/active_goal_20260102030405_seed-a.md", "revision": 0}])})
case("pool-snapshot-legacy", [cmd("goal-list")], {**files(("seed-a", "On disk A", {})), ".pi/goals/.goals-pool-snapshot.json": SNAP("s", [
    {"id": "seed-a", "objective": "Legacy snapshot", "status": "active", "autoContinue": True, "usage": {"tokensUsed": 0, "activeSeconds": 0}, "sisyphus": False,
     "createdAt": T0, "updatedAt": T0, "activePath": ".pi/goals/active_goal_20260102030405_seed-a.md", "revision": 0}])})
EDIT = seed("seed-a", "Edited elsewhere", extra={"revision": 5})
case("stale-revision", [cmd("goal-list"), {"write": {EDIT[0]: EDIT[1]}}, cmd("goal-pause", at=5000), cmd("goal-list")], files(("seed-a", "One", {})), entries=[focus("seed-a")])
case("deleted-elsewhere", [cmd("goal-list"), {"remove": [seed("seed-a", "One")[0]]}, cmd("goal-pause", at=5000), cmd("goal-list")], files(("seed-a", "One", {})), entries=[focus("seed-a")])
case("clear-changed-elsewhere", [cmd("goal-clear", at=5000, confirm=True, before={"remove": [seed("seed-a", "One")[0]]})], files(("seed-a", "One", {})), entries=[focus("seed-a")])
case("pause-complete-elsewhere", [cmd("goal-list"), {"write": {seed("seed-a", "One", status="complete")[0]: seed("seed-a", "One", status="complete")[1]}}, cmd("goal-pause"), cmd("goal-list")], files(("seed-a", "One", {})), entries=[focus("seed-a")])
case("pause-already-paused", [cmd("goal-pause")], files(("seed-a", "Paused", {"status": "paused", "auto": False, "extra": {"stopReason": "user"}})), entries=[focus("seed-a")])
case("pause-complete-focus", [cmd("goal-pause")], files(("seed-a", "Complete", {"status": "complete"})), entries=[focus("seed-a")])
case("pause-with-pause-reason", [cmd("goal-pause", at=900)], files(("seed-a", "Agent pause", {"extra": {"pauseReason": "r", "pauseSuggestedAction": "a"}})), entries=[focus("seed-a")])
GPATH = ".pi/goals/active_goal_2026010203040500_mjwaid1k-0e97rx.md"
case("shutdown-with-pause-reason", [{"event": "session_shutdown"}], files(("seed-a", "Agent paused", {"status": "paused", "auto": False, "extra": {"stopReason": "agent", "pauseReason": "needs a key", "pauseSuggestedAction": "ask the user"}})), entries=[focus("seed-a")])
EDIT2 = seed("seed-a", "Edited elsewhere", extra={"revision": 5})
case("shutdown-stale-revision", [{"write": {EDIT2[0]: EDIT2[1]}}, {"event": "session_shutdown"}], files(("seed-a", "One", {})), entries=[focus("seed-a")])
SCH = {"version": 1, "owner": "drive-session", "generation": "g1", "used": 3, "phase": "ready", "decision": {"kind": "ready", "purpose": "ready"}, "repairUsed": True}
case("unfocus-takeover", [cmd("goal-unfocus", at=3000), cmd("goal-list")], files(("seed-a", "Owned here", {"extra": {"scheduler": SCH}})), entries=[focus("seed-a")])
case("unfocus-other-owner", [cmd("goal-unfocus", at=3000)], files(("seed-a", "Owned elsewhere", {"extra": {"scheduler": dict(SCH, owner="other-session")}})), entries=[focus("seed-a")])
PI = ".pi/.goals-pool-snapshot.json"
case("lost-list", [cmd("goal-direct", "Soon gone", at=0), cmd("goal-pause", at=1000), {"remove": [GPATH, PI]}, cmd("goal-list")])
case("lost-unfocus", [cmd("goal-direct", "Soon gone", at=0), cmd("goal-pause", at=1000), {"remove": [GPATH, PI]}, cmd("goal-unfocus")])
case("lost-clear", [cmd("goal-direct", "Soon gone", at=0), cmd("goal-pause", at=1000), {"remove": [GPATH, PI]}, cmd("goal-clear", confirm=True)])
SEEDEDIT = seed("seed-a", "Edited elsewhere", extra={"revision": 5, "scheduler": dict(SCH, phase="idle", decision=None)})
case("unfocus-stale-revision", [{"write": {SEEDEDIT[0]: SEEDEDIT[1]}}, cmd("goal-unfocus", at=3000)], files(("seed-a", "Owned here", {"extra": {"scheduler": dict(SCH, phase="idle", decision=None)}})), entries=[focus("seed-a")])
case("unfocus-takeover-idle", [cmd("goal-unfocus", at=3000)], files(("seed-a", "Owned here", {"extra": {"scheduler": dict(SCH, phase="idle", decision=None, used=1)}})), entries=[focus("seed-a")])
case("restore-interrupted", [cmd("goal-list")], files(("seed-a", "Was running", {"extra": {"scheduler": dict(SCH, phase="running", dispatch={"id": "d1", "kind": "ready", "claimedAt": 5}, decision=None)}})), entries=[focus("seed-a")])
case("restore-other-owner", [cmd("goal-list")], files(("seed-a", "Someone else", {"extra": {"scheduler": dict(SCH, owner="other-session")}})), entries=[focus("seed-a")])
case("restore-budget", [cmd("goal-list")], files(("seed-a", "Spent", {"usage": {"tokensUsed": 900, "activeSeconds": 4}, "extra": {"tokenBudget": 500, "scheduler": SCH}})), entries=[focus("seed-a")])
case("restore-allowance", [cmd("goal-list")], files(("seed-a", "Allowance used", {"extra": {"scheduler": dict(SCH, used=2)}}), settings={".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 2})}), entries=[focus("seed-a")])
case("restore-budget-equal", [cmd("goal-list")], files(("seed-a", "Exactly spent", {"usage": {"tokensUsed": 500, "activeSeconds": 4}, "extra": {"tokenBudget": 500, "scheduler": SCH}})), entries=[focus("seed-a")])
case("restore-wait-over", [cmd("goal-list")], files(("seed-a", "Waited too long", {"extra": {"scheduler": dict(SCH, phase="waiting", decision={"kind": "wait"}, wait={"id": "w", "token": "t", "reason": "r", "deadline": 5})}}), settings={".pi/pi-goal-x-settings.json": json.dumps({"autoSelectSingleGoal": False})}), entries=[focus("seed-a")])
case("unfocus-interrupted", [cmd("goal-unfocus", at=3000)], files(("seed-a", "Interrupted", {"extra": {"scheduler": dict(SCH, phase="interrupted", decision=None)}})), entries=[focus("seed-a")])
case("pause-blocked", [cmd("goal-pause", at=3000), cmd("goal-list")], files(("seed-a", "Blocked", {"status": "blocked"})), entries=[focus("seed-a")])
case("pool-activepath-mismatch", [cmd("goal-list")], files(("seed-a", "Odd metadata", {"extra": {"activePath": ".pi/goals/active_goal_other.md"}})))
GHOST = {"id": "ghost", "objective": "Ghost", "status": "active", "autoContinue": True, "usage": {"tokensUsed": 0, "activeSeconds": 0}, "sisyphus": False,
         "createdAt": T0, "updatedAt": T0, "activePath": ".pi/goals/active_goal_ghost.md", "revision": 0}
SEEDA = {"id": "seed-a", "objective": "From the snapshot", "status": "active", "autoContinue": True, "usage": {"tokensUsed": 0, "activeSeconds": 0}, "sisyphus": False,
         "createdAt": T0, "updatedAt": T0, "activePath": ".pi/goals/active_goal_20260102030405_seed-a.md", "revision": 0}
case("pool-snapshot-ghost", [cmd("goal-list")], {**files(("seed-a", "On disk A", {})), ".pi/.goals-pool-snapshot.json": SNAP("s", [SEEDA, GHOST])})
LEG = lambda g: [{"type": "custom", "customType": "pi-goal-state", "data": {"version": 3, "goal": g}}]
case("resume-legacy-complete", [cmd("goal-list")], files(("seed-a", "One", {})), entries=LEG({"id": "legacy-x", "objective": "Old", "status": "complete", "autoContinue": True}))
case("resume-legacy-unsafe", [cmd("goal-list")], files(("seed-a", "One", {})), entries=LEG({"id": "legacy-y", "objective": "Old but open", "status": "active", "autoContinue": True, "activePath": "../escape.md"}))
case("direct-env-beats-project", [cmd("goal-direct", "Fix it\nVerification contract: green", at=0)], {".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0, "disableContracts": False})}, env={"PI_GOAL_DISABLE_CONTRACTS": "true"})
case("direct-allowance-string", [cmd("goal-direct", "String allowance", at=0)], {".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": " 0 "})})
case("resume-autoselect", [cmd("goal-list")], files(("seed-a", "Only one", {}), settings={".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0, "autoSelectSingleGoal": True})}))

# Review additions (rev-port-popular-3). Hosts the earlier cases never had: no UI (print or json mode) and a busy agent; a goal
# file removed while the clear confirmation is open; a wait whose deadline is exactly now; a focus entry from another storage root.
case("headless-clear-and-pause", [cmd("goal-clear"), cmd("goal-pause", at=1000), cmd("goal-unfocus"), cmd("goal-list")],
     files(("seed-a", "One", {}), ("seed-b", "Two", {"created": "2026-01-03T00:00:00.000Z"})), noUI=True)
case("headless-focused", [cmd("goal-clear"), cmd("goal-direct", "Headless goal", at=2000), cmd("goal-pause", at=3000)], files(("seed-a", "One", {})), entries=[focus("seed-a")], noUI=True)
case("unfocus-busy", [cmd("goal-unfocus", at=3000), cmd("goal-unfocus", at=4000)], files(("seed-a", "Busy goal", {})), entries=[focus("seed-a")], busy=True)
case("clear-removed-during-confirm", [cmd("goal-clear", at=5000, confirm=True, duringConfirm={"remove": [seed("seed-a", "One")[0]]}), cmd("goal-list")],
     files(("seed-a", "One", {})), entries=[focus("seed-a")])
case("restore-wait-deadline-now", [cmd("goal-list")], files(("seed-a", "Deadline now", {"extra": {"scheduler": dict(SCH, phase="waiting", decision={"kind": "wait"},
     wait={"id": "w", "token": "t", "reason": "r", "deadline": 1767323045000})}}), settings={".pi/pi-goal-x-settings.json": json.dumps({"autoSelectSingleGoal": False})}),
     entries=[focus("seed-a")])
case("resume-focus-other-root", [cmd("goal-list"), cmd("goal-pause", at=1000)], files(("seed-a", "One", {})),
     entries=[{"type": "custom", "customType": "pi-goal-focus", "data": {"version": 1, "focusedGoalId": "seed-a", "reason": "selected", "storageRoot": "/elsewhere/.pi/goals"}}])
case("restore-repair-ready", [cmd("goal-list")], files(("seed-a", "Repair pending", {"extra": {"scheduler": dict(SCH, decision={"kind": "ready", "purpose": "repair"})}})),
     entries=[focus("seed-a")])
case("restore-repair-strict", [cmd("goal-list")], files(("seed-a", "Repair pending, strict", {"extra": {"scheduler": dict(SCH, decision={"kind": "ready", "purpose": "repair"})}}),
     settings={".pi/pi-goal-x-settings.json": json.dumps({"maxAutonomousRuns": 0, "strictExecutionContract": True})}), entries=[focus("seed-a")])
# Bad input: control characters and escape sequences in an objective, a large objective, case-insensitive matches that JavaScript's
# /i (no u flag) does not make (U+017F and U+212A fold onto s and k in RE2), and numbers outside the double range in the files.
CTL = "Fix\tthe \x1b]8;;http://x\x07link\x1b]8;;\x07 now\x00 \x85 end\x7f\u2028tail\x1b[31mred\x1b_apc\x1b\\ \u200b\ufeffz"
case("bad-control-objective", [cmd("goal-direct", CTL, at=0), cmd("goal-list"), cmd("goal-clear", confirm=False), {"event": "session_start", "reason": "resume"}, cmd("goal-list")])
case("bad-control-sisyphus", [cmd("sisyphus-direct", "1)\x1b[2Jstep\tone\x00\n2) two", at=0), cmd("goal-list")])
case("bad-large-objective", [cmd("goal-direct", "B" * 200000 + "\nVerification contract: " + "c" * 50000, at=0), cmd("goal-list"), cmd("goal-clear", confirm=False)])
case("bad-fold-long-s-step", [cmd("sisyphus-direct", "Do it: x\u017ftep 1 then more", at=0)])
case("bad-fold-kelvin-section", [cmd("goal-direct", "if bloc\u212aed: wait\nReal title here", at=0), cmd("goal-list")])
case("bad-fold-long-s-banner", [cmd("goal-direct", "=== \u017fi\u017fyphu\u017f goal ===\nTitle line", at=0), cmd("goal-list")])
OVER = seed("seed-a", "Overflow numbers", extra={"scheduler": dict(SCH, phase="idle", decision=None)})
case("bad-number-overflow", [cmd("goal-list"), cmd("goal-pause", at=1000), cmd("goal-list")],
     {".pi/pi-goal-x-settings.json": '{"maxAutonomousRuns": 0, "disableContracts": 1e999}', OVER[0]: OVER[1].replace('"revision": 0', '"revision": 1e400').replace('"tokensUsed": 0', '"tokensUsed": -1e400').replace('"repairUsed": true', '"repairUsed": true,\n    "note": 1e400')},
     entries=[focus("seed-a")])
case("bad-number-overflow-settings", [cmd("goal-direct", "Fix it\nVerification contract: green", at=0)],
     {".pi/pi-goal-x-settings.json": '{"maxAutonomousRuns": 1e400, "disableContracts": true, "maxAutonomousRuns": 0}'})

for c in cases:
    for s in c["steps"]:
        s.setdefault("at", None)
        if s["at"] is None:
            del s["at"]
with open(os.path.join(here, "drive", "cases.json"), "w") as f:
    json.dump(cases, f, indent=1)
print(len(cases), "cases")
