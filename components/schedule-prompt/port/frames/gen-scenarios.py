#!/usr/bin/env python3
"""Generates port/frames/scenarios/*.json (jobs overlay and add flow scenarios). Run from this directory."""
import json, os

UP, DOWN, ESC = "\u001b[A", "\u001b[B", "\u001b"
out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
os.makedirs(out, exist_ok=True)


def job(id, name, **kw):
    j = {"id": id, "name": name, "schedule": kw.pop("schedule", "0 0 9 * * *"), "prompt": kw.pop("prompt", "say hello"),
         "enabled": kw.pop("enabled", True), "type": kw.pop("type", "cron")}
    if "intervalMs" in kw:
        j["intervalMs"] = kw.pop("intervalMs")
    j["createdAt"] = "2026-01-01T00:00:00.000Z"
    j["runCount"] = kw.pop("runCount", 0)
    j.update(kw)
    return j


mine = [
    job("a1", "morning", session="s1", runCount=3, lastRun="2026-02-01T09:00:00.000Z", lastStatus="success"),
    job("a2", "disabled-one", session="s1", enabled=False, type="interval", schedule="5m", intervalMs=300000),
    job("a3", "shared-job-with-a-very-long-name", schedule="2099-01-01T09:30:00.000Z", type="once", prompt="p" * 250),
    job("a4", "failing", session="s1", lastStatus="error", schedule="0 */5 * * * *", prompt="héllo 😀 wörld " * 4),
    job("a5", "running", session="s1", lastStatus="running", schedule="0 0 0 * * *"),
    job("a6", "off-shared", enabled=False, schedule="0 0 9 * * 1-5"),
]
foreign = [
    job("f1", "other-live", session="s2", schedule="30 * * * * *"),
    job("f2", "other-off", session="s2", enabled=False),
    job("f3", "other-err", session="s2", lastStatus="error", type="interval", schedule="1h", intervalMs=3600000),
]
nexts = {"a1": "2099-03-04T09:00:00.000Z", "a3": "2099-01-01T09:30:00.000Z", "a4": "2099-03-04T09:05:00.000Z", "f1": "2099-03-04T09:01:30.000Z"}
widths = [40, 90, 120]


def jobs(name, jobsl, steps, **kw):
    sc = {"name": name, "kind": "jobs", "session": "s1", "jobs": jobsl, "nextRuns": nexts, "widths": widths, "runAdd": "ok", "steps": steps}
    sc.update(kw)
    json.dump(sc, open(os.path.join(out, name + ".json"), "w"), indent=1, ensure_ascii=False)


jobs("jobs-empty", [], ["a", "t", "x", "c", "s", "y", DOWN, UP])
jobs("jobs-empty-quit", [], ["q"])
jobs("jobs-navigate", mine + foreign, [DOWN] * 4 + [DOWN, DOWN, DOWN, DOWN, DOWN, DOWN, DOWN, UP, UP, UP, UP, UP, UP, UP, UP, UP, UP, UP])
jobs("jobs-only-mine", mine, [DOWN, DOWN, DOWN])
jobs("jobs-only-foreign", foreign, [DOWN, "t", "s", "x", "c"], session="s1")
jobs("jobs-toggle", mine, ["t", "t", DOWN, "t", DOWN, "t"])
jobs("jobs-scope", mine, ["s", "s", DOWN, "s", DOWN, DOWN, "s"])
jobs("jobs-foreign-readonly", mine[:2] + foreign, [DOWN, DOWN, "t", "s", "x", "n", "c", "y"])
jobs("jobs-remove-confirm", mine + foreign, ["x", "n", "x", ESC, "x", "q", "t", "y", "x", "y", DOWN, "x", "y"])
jobs("jobs-cleanup-confirm", mine + foreign, ["c", "n", "c", ESC, "c", "y", "c"])
jobs("jobs-cleanup-none", [mine[0], mine[2]] + foreign, ["c", "y"])
jobs("jobs-remove-last-clamps", mine[:3], [DOWN, DOWN, "x", "y", "x", "y"])
jobs("jobs-quit", mine, ["q"])
jobs("jobs-escape", mine, [ESC])
jobs("jobs-add-ok", mine[:2], ["a", "t"], runAdd="ok")
jobs("jobs-add-adds", mine[:2], ["a", DOWN, DOWN], runAdd="adds", addJob=job("n1", "added-by-flow", session="s1", prompt="new"))
jobs("jobs-add-fail", mine[:2], ["a"], runAdd="fail")
jobs("jobs-unknown-keys", mine[:2], ["z", "A", "\r", " ", "Q", "\u001b[C"])
jobs("jobs-undefined-fields", [{"id": "u1", "name": "bare", "schedule": "0 0 9 * * *", "prompt": "x", "type": "cron", "createdAt": "2026-01-01T00:00:00.000Z", "enabled": True}], [])

# ---- add flow ----
SESSION = "Bind to this session — only this pi fires it"
SHARED = "Shared — every pi in this cwd fires it (accepts duplicate fires)"


def flow(name, answers, **kw):
    sc = {"name": name, "kind": "addflow", "session": "s1", "settings": {}, "jobs": [], "answers": answers}
    sc.update(kw)
    json.dump(sc, open(os.path.join(out, name + ".json"), "w"), indent=1, ensure_ascii=False)


flow("flow-cron-session", ["daily report", "Cron (recurring)", "0 0 9 * * *", "write the report", SESSION, True])
flow("flow-cron-shared-default-workdir", ["w", "Cron (recurring)", " 0 30 8 * * 1-5 ", "p", SHARED, True], settings={"defaultJobScope": "workdir"})
flow("flow-interval", ["poll", "Interval (periodic)", "30s", "check", SESSION, True])
flow("flow-once-iso", ["later", "Once (one-shot)", "2099-01-01T09:30:00Z", "do it", SHARED, True])
flow("flow-reprompt-cron", ["x", "Cron (recurring)", "* * *", "0 0 * * * *", "p", SESSION, True])
flow("flow-reprompt-interval", ["x", "Interval (periodic)", "5x", "0s", "10m", "p", SESSION, True])
flow("flow-reprompt-once", ["x", "Once (one-shot)", "garbage", "2020-01-01T00:00:00Z", "2099-01-01T00:00:00Z", "p", SESSION, True])
flow("flow-duplicate-name", ["taken"], jobs=[job("d1", "taken", session="s1")])
flow("flow-cancel-name", [None])
flow("flow-empty-name", [""])
flow("flow-cancel-type", ["n", None])
flow("flow-cancel-schedule", ["n", "Cron (recurring)", None])
flow("flow-empty-schedule", ["n", "Cron (recurring)", ""])
flow("flow-cancel-prompt", ["n", "Interval (periodic)", "5m", None])
flow("flow-empty-prompt", ["n", "Interval (periodic)", "5m", ""])
flow("flow-cancel-scope", ["n", "Interval (periodic)", "5m", "p", None])
flow("flow-declined", ["n", "Interval (periodic)", "5m", "p", SESSION, False])
flow("flow-whitespace-schedule", ["n", "Interval (periodic)", "   ", "5m", "p", SESSION, True])
flow("flow-answers-run-out", ["n", "Cron (recurring)"])
