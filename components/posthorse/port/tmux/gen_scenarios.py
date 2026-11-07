#!/usr/bin/env python3
"""Writes scenarios/*.json for the card probe: each tool's card collapsed and expanded, pending/partial, error, malformed
arguments, the two message cards, narrow widths, click expansion and theme changes.

usage: gen_scenarios.py   (rewrites scenarios/)
"""
import json, os

here = os.path.dirname(os.path.abspath(__file__))
out = os.path.join(here, "scenarios")
out_real = os.path.join(here, "scenarios-real")
for d in (out, out_real):
    os.makedirs(d, exist_ok=True)
    for old in os.listdir(d):
        os.remove(os.path.join(d, old))

READY = [{"wait": 2.5}]
HANDOFF = "\n".join(f"Handoff line {i + 1}" for i in range(60))


def call(tool, fixture, **args):
    return {"name": tool, "arguments": {**args, "fixture": fixture}}


def cards(calls, *, width=100, height=64, theme="dark", expand=True, extra=None):
    """One turn calls every tool at once; the screen is snapped collapsed, then expanded with ctrl+o."""
    steps = READY + [{"send": "go"}, {"key": "Enter"}, {"until": "all done", "timeout": 40}, {"snap": "collapsed", "settle": 1.0}]
    if expand:
        steps += [{"key": "C-o"}, {"snap": "expanded", "settle": 1.0}, {"key": "C-o"}, {"snap": "collapsed-again", "settle": 1.0}]
    steps += extra or []
    return {"replies": [{"tool_calls": calls, "chunk": 4000}, {"text": "all done", "chunk": 4000}], "width": width, "height": height, "theme": theme, "steps": steps}


def write(name, scenario):
    scenario["name"] = name
    json.dump(scenario, open(os.path.join(out, name + ".json"), "w"), indent=1)


groups = {
    "notes-pages": [call("notes", "note-short", op="read", path="plan.md"), call("notes", "note-long", op="read", path="ledger.md"),
                    call("notes", "note-empty", op="read", path="empty.md"), call("notes", "notes-list", op="list"),
                    call("notes", "notes-search-more", op="search", query="needle"), call("notes", "notes-none", op="list", path="sub")],
    "notes-writes": [call("notes", "note-write", op="write", path="draft.md", content="Hello\nworld"), call("notes", "note-write", op="write", path="clear.md", content=""),
                     call("notes", "note-append", op="append", path="draft.md", content="more text"), call("notes", "plain-long", op="read", path="legacy.md")],
    "errors": [call("notes", "error-missing", op="read", path="missing.md"), call("history", "error-missing", op="read", id="gone")],
    "history": [call("history", "history-search", op="search", query="Relay station", all=True), call("history", "history-search", op="search", query="Relay station"),
                call("history", "history-empty", op="search", query="absent", all=True), call("history", "history-read-images", op="read", id="pic"),
                call("history", "history-legacy", op="search", query="Relay"), call("history", "malformed-details", op="search", query="x")],
    "context": [call("get_context_remaining", "context-enabled"), call("get_context_remaining", "context-unsupported"),
                call("get_context_remaining", "context-disabled"), call("get_context_remaining", "context-unknown")],
    "new-context": [call("new_context", "new-context", handoff=HANDOFF), call("new_context", "new-context")],
    "controls": [call("notes", "controls", op="write", path="draft\x1b[2J\x1b]52;c;c2VjcmV0\x07.md", content="saved\x1b[2J")],
    "malformed-args": [{"name": "notes", "arguments": {"op": 3, "path": {}, "fixture": "note-short"}},
                       {"name": "history", "arguments": {"op": "search", "query": ["bad"], "id": False, "fixture": "history-empty"}},
                       {"name": "notes", "arguments": {"op": "read", "offset": "x", "limit": None, "fixture": "note-short"}},
                       {"name": "history", "arguments": {"op": "read", "id": "e1", "offset": 40000, "imageOffset": 2, "limit": 5, "fixture": "history-read-images"}}],
}
for name, calls in groups.items():
    write(name, cards(calls))
    write(name + "-w40", cards(calls, width=40, height=120))
    write(name + "-w24", cards(calls, width=24, height=160))
write("notes-pages-light", cards(groups["notes-pages"], theme="light"))
write("history-w60", cards(groups["history"], width=60, height=100))

# Pending: arguments stream slowly (the call card says "preparing"), then the tool runs (partial result, "running").
write("pending", {"replies": [{"tool_calls": [call("notes", "running", op="write", path="late.md", content="hello")], "chunk": 14, "delay": 0.5}, {"text": "all done", "chunk": 4000}],
                  "width": 100, "height": 30,
                  "steps": READY + [{"send": "go"}, {"key": "Enter"}, {"until": "preparing", "timeout": 30}, {"snap": "preparing", "settle": 0.2},
                                    {"until": "running", "timeout": 30}, {"snap": "running", "settle": 0.5}, {"key": "C-o"}, {"snap": "running-expanded", "settle": 0.5},
                                    {"until": "all done", "timeout": 40}, {"snap": "finished-expanded", "settle": 1.0}, {"key": "C-o"}, {"snap": "finished", "settle": 1.0}]})
write("pending-w24", {"replies": [{"tool_calls": [call("notes", "running", op="write", path="late.md", content="hello")], "chunk": 14, "delay": 0.5}, {"text": "all done", "chunk": 4000}],
                      "width": 24, "height": 40,
                      "steps": READY + [{"send": "go"}, {"key": "Enter"}, {"until": "Notes ·", "timeout": 30}, {"snap": "preparing", "settle": 0.2},
                                        {"until": "still", "timeout": 30}, {"snap": "running", "settle": 0.5}, {"until": "all done", "timeout": 40}, {"snap": "finished", "settle": 1.0}]})

# Messages: /pt message <name> injects a custom message of the type the extension owns.
def messages(names, *, width=100, height=64, theme="dark", expand=True):
    steps = list(READY)
    for n in names:
        steps += [{"send": f"/pt message {n}"}, {"key": "Enter"}, {"wait": 1.5}]
    steps += [{"snap": "collapsed", "settle": 1.0}]
    if expand:
        steps += [{"key": "C-o"}, {"snap": "expanded", "settle": 1.0}, {"key": "C-o"}, {"snap": "collapsed-again", "settle": 1.0}]
    return {"replies": ["unused"], "width": width, "height": height, "theme": theme, "steps": steps}


for width, suffix, height in ((100, "", 64), (40, "-w40", 110), (24, "-w24", 160)):
    write("messages" + suffix, messages(["reminder", "reminder-short", "window", "window-wide", "controls"], width=width, height=height))
write("messages-light", messages(["reminder", "window"], theme="light"))

# Click expansion: a card and the message cards expand on a left click on their text.
write("click-card", {"replies": [{"tool_calls": [call("notes", "note-long", op="read", path="ledger.md")], "chunk": 4000}, {"text": "all done", "chunk": 4000}],
                     "width": 100, "height": 40,
                     "steps": READY + [{"send": "go"}, {"key": "Enter"}, {"until": "all done", "timeout": 40}, {"snap": "closed", "settle": 1.0},
                                       {"click": "START", "nth": 0}, {"wait": 1.0}, {"snap": "clicked", "settle": 0.5},
                                       {"click": "START", "nth": 0}, {"wait": 1.0}, {"snap": "clicked-again", "settle": 0.5}]})
write("click-message", {"replies": ["unused"], "width": 100, "height": 50,
                        "steps": READY + [{"send": "/pt message window"}, {"key": "Enter"}, {"wait": 1.5}, {"send": "/pt message reminder"}, {"key": "Enter"}, {"wait": 1.5},
                                          {"snap": "closed", "settle": 1.0}, {"click": "Checkpoint reminder", "nth": 0}, {"wait": 1.0}, {"snap": "clicked-reminder", "settle": 0.5},
                                          {"click": "Checkpoint reminder", "nth": 0}, {"wait": 1.0}, {"snap": "clicked-reminder-again", "settle": 0.5},
                                          {"click": "Context window started", "nth": 0}, {"wait": 1.0}, {"snap": "clicked-window", "settle": 0.5},
                                          {"key": "C-o"}, {"snap": "global-expanded", "settle": 0.5}, {"key": "C-o"}, {"snap": "global-collapsed", "settle": 0.5}]})

# Theme: the cards and messages drawn under the dark theme, then under light, switched live by the probe command.
write("theme-live", {"replies": [{"tool_calls": [call("notes", "note-short", op="read", path="plan.md"), call("get_context_remaining", "context-unsupported"),
                                                call("history", "history-search", op="search", query="Relay station", all=True)], "chunk": 4000}, {"text": "all done", "chunk": 4000}],
                     "width": 100, "height": 64,
                     "steps": READY + [{"send": "go"}, {"key": "Enter"}, {"until": "all done", "timeout": 40}, {"send": "/pt message reminder"}, {"key": "Enter"}, {"wait": 1.5},
                                       {"snap": "dark", "settle": 1.0}, {"send": "/pt theme light"}, {"key": "Enter"}, {"wait": 2.0}, {"snap": "light", "settle": 1.0},
                                       {"send": "/pt theme dark"}, {"key": "Enter"}, {"wait": 2.0}, {"snap": "dark-again", "settle": 1.0}]})

# Resize: the same cards re-laid out at three widths in one session.
write("resize", {"replies": [{"tool_calls": groups["notes-pages"][:2] + groups["history"][:1], "chunk": 4000}, {"text": "all done", "chunk": 4000}],
                 "width": 100, "height": 64,
                 "steps": READY + [{"send": "go"}, {"key": "Enter"}, {"until": "all done", "timeout": 40}, {"snap": "w100", "settle": 1.0},
                                   {"resize": [60, 64]}, {"wait": 1.0}, {"snap": "w60", "settle": 1.0}, {"resize": [30, 64]}, {"wait": 1.0}, {"snap": "w30", "settle": 1.0},
                                   {"resize": [100, 64]}, {"wait": 1.0}, {"snap": "w100-again", "settle": 1.0}]})

# Real mode (tmux.sh ... real): the original under Pi and the whole port under PiG, the actual tools and hooks. The mock
# model drives them turn by turn; `prompt_tokens` is the usage a reply reports, which the budget reads.
def real(name, replies, *, width=100, height=64, settings=None, expand=True, final="all done", pre=None):
    steps = READY + (pre or []) + [{"send": "go"}, {"key": "Enter"}, {"until": final, "timeout": 60}, {"snap": "collapsed", "settle": 1.5}]
    if expand:
        steps += [{"key": "C-o"}, {"snap": "expanded", "settle": 1.0}, {"key": "C-o"}, {"snap": "collapsed-again", "settle": 1.0}]
    scenario = {"name": name, "replies": replies, "width": width, "height": height, "steps": steps}
    if settings:
        scenario["settings"] = settings
    json.dump(scenario, open(os.path.join(out_real, name + ".json"), "w"), indent=1)


def tc(tool, **args):
    return {"tool_calls": [{"name": tool, "arguments": args}], "chunk": 4000}


DONE = {"text": "all done", "chunk": 4000}
ENABLED = {"compaction": {"enabled": True, "reserveTokens": 16384, "keepRecentTokens": 20000}}
big_note = "\n".join(f"line {i}: " + "界 👩🏽‍💻 é route " * 6 for i in range(40))
for width, suffix in ((100, ""), (40, "-w40")):
    real("real-notes" + suffix, [tc("notes", op="write", path="draft.md", content="Hello\nworld"), tc("notes", op="read", path="draft.md"),
                                 tc("notes", op="append", path="draft.md", content="more text"), tc("notes", op="write", path="big.md", content=big_note),
                                 tc("notes", op="read", path="big.md"), tc("notes", op="list"), tc("notes", op="search", query="route"),
                                 tc("notes", op="read", path="missing.md"), tc("notes", op="write", path="clear.md", content=""), DONE],
         width=width, height=100 if width == 100 else 200)
    real("real-context" + suffix, [{**tc("get_context_remaining"), "prompt_tokens": 1000}, DONE], width=width, settings=ENABLED)
    real("real-context-disabled" + suffix, [{**tc("get_context_remaining"), "prompt_tokens": 1000}, DONE], width=width)
    real("real-history" + suffix, [tc("history", op="search", query="go"), tc("history", op="search", query="nothing-matches"), DONE], width=width)
real("real-reminder", [{**tc("notes", op="list"), "prompt_tokens": 78000}, {**DONE, "prompt_tokens": 78500}], settings=ENABLED)
real("real-rollover", [tc("notes", op="write", path="state.md", content="progress"),
                       tc("new_context", handoff="Goal: finish the port.\n" + "\n".join(f"Decision {i}: keep route {i}" for i in range(30))), DONE], settings=ENABLED, height=80)
# Saved legacy message entries (the context-window type only exists in older sessions) rendered by the real extension.
handoff_text = "Context window window-2 starts here. Earlier conversation is not available in this window.\n\nHandoff from the previous window:\n" + "\n".join(f"Handoff line {i + 1}" for i in range(60))
for width, suffix, height in ((100, "", 80), (40, "-w40", 200)):
    scenario = {"name": "real-saved-messages" + suffix, "replies": ["unused"], "width": width, "height": height,
                "session": {"entries": [{"type": "custom_message", "customType": "context-window", "content": handoff_text, "display": True, "details": {"windowId": "window-2", "tokensBefore": 1000}},
                                        {"type": "custom_message", "customType": "posthorse-reminder", "content": "Checkpoint now: save notes before the window fills.\n" + "remember this\n" * 6 + "LAST LINE", "display": True}]},
                "steps": READY + [{"snap": "collapsed", "settle": 1.5}, {"key": "C-o"}, {"snap": "expanded", "settle": 1.0}, {"key": "C-o"}, {"snap": "collapsed-again", "settle": 1.0}]}
    json.dump(scenario, open(os.path.join(out_real, scenario["name"] + ".json"), "w"), indent=1)
print(len(os.listdir(out)), "scenarios,", len(os.listdir(out_real)), "real scenarios")
