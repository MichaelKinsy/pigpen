#!/usr/bin/env python3
"""Writes extensions/powerline-footer/testdata/welcome-states.json: the inputs of the welcome golden (see drive/welcome-drive.mjs,
which renders them with the ORIGINAL welcome.ts, and welcome_golden_test.go, which renders them with the Go port).

"render" states: a WelcomeComponent or WelcomeHeader with given data, drawn at several widths. "sessions" states: a tree of session
files under a temporary HOME, read by getRecentSessions; every age carries a 500 ms margin so a minute or hour boundary cannot be
crossed while the state runs.
"""
import json, os

here = os.path.dirname(os.path.abspath(__file__))
out = os.path.join(here, "..", "extensions", "powerline-footer", "testdata", "welcome-states.json")

WIDTHS = [0, 20, 43, 44, 45, 60, 76, 78, 79, 96, 98, 100, 200]
states = []

def render(name, kind, model="Claude Sonnet 4.5", provider="anthropic", sessions=None, counts=None, tokens=None, countdown=30, widths=WIDTHS):
    s = {"name": name, "kind": kind, "model": model, "provider": provider,
         "sessions": sessions if sessions is not None else [],
         "counts": counts or {"contextFiles": 0, "skills": 0, "promptTemplates": 0},
         "tokens": tokens, "widths": widths}
    if kind == "overlay" and countdown is not None:
        s["countdown"] = countdown
    states.append(s)

three = [{"name": "pigpen", "timeAgo": "just now"}, {"name": "my-dashed-project", "timeAgo": "5m ago"}, {"name": "third", "timeAgo": "2d ago"}]
for kind in ("overlay", "header"):
    render(kind + "-empty", kind)
    render(kind + "-full", kind, sessions=three, counts={"contextFiles": 2, "skills": 3, "promptTemplates": 1}, tokens=1900)
    render(kind + "-singular", kind, sessions=three[:1], counts={"contextFiles": 1, "skills": 1, "promptTemplates": 1}, tokens=1)
    render(kind + "-plural-only-skills", kind, counts={"contextFiles": 0, "skills": 12, "promptTemplates": 0})
    render(kind + "-five-sessions", kind, sessions=three + [{"name": "fourth", "timeAgo": "3d ago"}, {"name": "fifth", "timeAgo": "4d ago"}])
    render(kind + "-long-model", kind, model="A very long model name that does not fit the left column at all", provider="a-provider-with-a-long-name-too", sessions=three)
    render(kind + "-wide-chars", kind, model="模型名称很长很长很长很长很长", provider="提供者😀", sessions=[{"name": "项目名称项目名称", "timeAgo": "1h ago"}])
    render(kind + "-empty-names", kind, model="", provider="")
    render(kind + "-ansi-in-names", kind, model="\x1b[31mred\x1b[0m model", provider="p\tq", sessions=[{"name": "\x1b[1mbold\x1b[0m", "timeAgo": "3h ago"}])
    for tokens in (None, 0, "NaN", "Infinity", 999, 1000, 9999, 10000, 123456, 999999, 1000000, 9999999, 12500000, 123456789):
        render("%s-tokens-%s" % (kind, tokens), kind, counts={"contextFiles": 1, "skills": 0, "promptTemplates": 0}, tokens=tokens, widths=[96])
render("overlay-default-countdown", "overlay", sessions=three, widths=[44, 78, 96], countdown=None)
for cd in (30, 29, 10, 9, 1, 0):
    render("overlay-countdown-%d" % cd, "overlay", sessions=three, countdown=cd, widths=[44, 60, 78, 96])

AGE = 500
MIN, HOUR, DAY = 60000, 3600000, 86400000
def f(dir, name, header, age, body=0):
    return {"dir": dir, "name": name, "header": header, "age": age + AGE, "body": body}

def sess(name, files, count=3, legacy=None, agentDirEnv=False):
    states.append({"name": name, "kind": "sessions", "files": files, "maxCount": count, "legacy": legacy or [], "customAgentDir": agentDirEnv})

H = lambda cwd: json.dumps({"cwd": cwd})
sess("sessions-empty", [])
sess("sessions-header-cwd", [f("--Users-nico-dev-encoded-name--", "session.jsonl", H("/Users/nico/dev/my-dashed-project"), 0)])
sess("sessions-unusable-headers", [
    f("--Users-nico-dev-invalid-json--", "session.jsonl", "not-json", 1 * MIN),
    f("--Users-nico-dev-missing-cwd--", "session.jsonl", json.dumps({"type": "session"}), 2 * MIN),
    f("--Users-nico-dev-non-string-cwd--", "session.jsonl", json.dumps({"cwd": 123}), 3 * MIN),
    f("--Users-nico-dev-blank-cwd--", "session.jsonl", json.dumps({"cwd": "   "}), 4 * MIN),
    f("plain-dir", "session.jsonl", json.dumps([1, 2]), 5 * MIN),
    f("--", "session.jsonl", "", 6 * MIN),
    f("--a-b--", "empty.jsonl", "", 7 * MIN),
], count=10)
sess("sessions-trailing-slash-and-root", [
    f("a", "1.jsonl", H("/projects/trailing/"), 1 * MIN),
    f("b", "1.jsonl", H("/"), 2 * MIN),
    f("c", "1.jsonl", H("relative"), 3 * MIN),
    f("d", "1.jsonl", H("/x/y//"), 4 * MIN),
], count=10)
sess("sessions-name-truncation", [
    f("a", "1.jsonl", H("/p/" + "x" * 20), 1 * MIN),
    f("b", "1.jsonl", H("/p/" + "y" * 21), 2 * MIN),
    f("c", "1.jsonl", H("/p/" + "界" * 25), 3 * MIN),
], count=10)
sess("sessions-newest-distinct", [f("--encoded--/artifacts/nested", "%d.jsonl" % i, H("/projects/older"), DAY) for i in range(30)] + [
    f("--encoded--/artifacts/nested", "a.jsonl", H("/projects/my-dashed-project"), 1 * MIN, 5000),
    f("--encoded--/artifacts/nested", "b.jsonl", H("/other/my-dashed-project"), 2 * MIN, 5000),
    f("--encoded--/artifacts/nested", "c.jsonl", "not-json", 3 * MIN, 5000),
    f("--encoded--/artifacts/nested", "d.jsonl", H("/projects/third"), 4 * MIN, 5000),
])
sess("sessions-time-ago", [
    f("a", "1.jsonl", H("/p/just"), 0),
    f("b", "1.jsonl", H("/p/m59"), 59 * MIN),
    f("c", "1.jsonl", H("/p/h1"), 60 * MIN),
    f("d", "1.jsonl", H("/p/h23"), 23 * HOUR),
    f("e", "1.jsonl", H("/p/d1"), 24 * HOUR),
    f("f", "1.jsonl", H("/p/d400"), 400 * DAY),
], count=10)
sess("sessions-max-count", [f("d%d" % i, "1.jsonl", H("/p/n%d" % i), i * MIN) for i in range(6)], count=2)
sess("sessions-max-zero", [f("d", "1.jsonl", H("/p/n"), 0)], count=0)
sess("sessions-non-jsonl-ignored", [f("d", "1.json", H("/p/no"), 0), f("d", "x.jsonl.bak", H("/p/no2"), 0), f("d", "page.html", H("/p/no3"), 0), f("d", "ends-in-l", H("/p/no4"), 0), f("e", "ok.jsonl", H("/p/yes"), MIN)], count=10)
sess("sessions-legacy-and-agent", [f("--custom--", "session.jsonl", H("/tmp/custom-project"), MIN)],
     count=10, legacy=[f("--legacy--", "session.jsonl", H("/tmp/legacy-project"), 2 * MIN)], agentDirEnv=True)
sess("sessions-single-dash-dir", [f("-one-dash-dir", "1.jsonl", "not-json", 1 * MIN), f("--two-dash-dir--", "1.jsonl", "not-json", 2 * MIN)], count=10)
sess("sessions-tie-agent-first", [f("agent-first", "1.jsonl", H("/p/from-agent"), MIN)], count=1, legacy=[f("legacy-second", "1.jsonl", H("/p/from-legacy"), MIN)])
sess("sessions-round-boundary", [f("a", "1.jsonl", H("/p/almost-an-hour"), 59 * MIN + 59200), f("b", "1.jsonl", H("/p/almost-a-day"), 23 * HOUR + 59 * MIN + 59200)], count=10)
sess("sessions-header-over-8192", [
    f("--Users-x-in-limit--", "ok.jsonl", json.dumps({"cwd": "/p/inside", "pad": "x" * 8000}), 1 * MIN),
    f("--Users-x-over-limit--", "cut.jsonl", json.dumps({"cwd": "/p/outside", "pad": "x" * 9000}), 2 * MIN),
], count=10)
sess("sessions-crlf-and-padding", [f("d", "1.jsonl", "  " + H("/p/padded") + "\r", 0)])

json.dump(states, open(out, "w"), indent=1, ensure_ascii=False)
open(out, "a").write("\n")
print(len(states), "states")
