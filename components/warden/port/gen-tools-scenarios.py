#!/usr/bin/env python3
"""Regenerates the pigeq scenarios of the tools slice (warden_remember, warden_loops, warden_recall, the open-loops
lifecycle, the compaction evidence appendix) into port/scenarios. Run from components/warden/port."""
import json, os

here = os.path.dirname(os.path.abspath(__file__))
out = os.path.join(here, "scenarios")

TOOLS = "read,bash,edit,write,warden_remember,warden_loops,warden_recall"
ENV = {"PIGPEN_WARDEN_ENABLED": "1", "PIGPEN_WARDEN_STEER_VISIBLE": "0"}
CAPTURE = ["response", "extension_error", "agent_start", "agent_end", "turn_start", "turn_end", "tool_execution_start", "tool_execution_end", "message_end"]


def call(name, **arguments):
    return {"toolCalls": [{"name": name, "arguments": arguments}]}


def loops(**arguments):
    return call("warden_loops", **arguments)


def prompt(message, name=None, **extra):
    step = {"name": name or "prompt", "rpc": {"type": "prompt", "message": message}}
    step.update(extra)
    return step


def scenario(name, description, llm, steps, folder="scenarios", **extra):
    sc = {"name": name, "description": description, "env": ENV, "commands": {"git": {"mode": "missing"}}, "args": ["--tools", TOOLS],
          "capture": CAPTURE, "llm": llm, "steps": steps}
    sc.update(extra)
    os.makedirs(os.path.join(here, folder), exist_ok=True)
    with open(os.path.join(here, folder, name + ".json"), "w") as f:
        json.dump(sc, f, indent=2)
        f.write("\n")


scenario(
    "tools-listed-in-model-request",
    "The three tools reach the model with the original's descriptions and JSON schemas, and warden_recall answers an empty session.",
    [call("warden_recall"), {"text": "nothing yet"}],
    [prompt("what was already tried?")],
)

scenario(
    "loops-add-list-done-drop",
    "warden_loops add, list, done and drop through the tool; /warden loops shows the session's loops to the user; a closed list reminds nothing at the end of the run.",
    [
        loops(action="add", text="Bump the version", when="after CI passes"),
        loops(action="add", text="Rerun the flaky upload test"),
        loops(action="add", text="Answer the review thread"),
        loops(action="list"),
        loops(action="done", id=2),
        loops(action="drop", id=3),
        loops(action="drop", id=3, reason="the thread was resolved"),
        loops(action="done", id=2),
        loops(action="done", id=9),
        loops(action="list"),
        loops(action="add", text="  "),
        loops(action="add", text="x" * 161),
        loops(action="frobnicate"),
        loops(action="done", id=1),
        loops(action="list"),
        {"text": "noted"},
    ],
    [prompt("keep track of what I promise"), prompt("/warden loops", name="loops-command")],
)

scenario(
    "loops-reminder-once-per-list",
    "An open loop is named once at the end of the run, for the next turn, and not again until the open list changes; closing every loop silences it.",
    [
        loops(action="add", text="Bump the version", when="after CI passes"),
        {"text": "CI is running; I will bump the version after it passes."},
        {"text": "still waiting"},
        loops(action="add", text="Tag the release"),
        {"text": "waiting"},
        loops(action="done", id=1),
        loops(action="done", id=2),
        {"text": "all done"},
        {"text": "nothing left"},
    ],
    [prompt("ship the release", name="first"), prompt("status?", name="same-list"), prompt("go on", name="changed-list"), prompt("done?", name="closed"), prompt("anything else?", name="silent")],
)

LESSON = "Always use Firefox for the vendor site"
scenario(
    "remember-recorded-then-confirmed",
    "warden_remember records a lesson, refuses a weakened check, a task-bound note and a non-standing one, confirms the lesson in a second session, and the third session starts with it quoted as a standing preference.",
    [
        call("warden_remember", lesson=LESSON),
        call("warden_remember", lesson="Always skip the tests when the build is slow"),
        call("warden_remember", lesson="Don't commit or stage it"),
        call("warden_remember", lesson="The vendor site blocks Chrome"),
        call("warden_remember", lesson="Don't touch the schema yet"),
        call("warden_remember", lesson="Never " + "write long notes " * 12),
        {"text": "recorded"},
        call("warden_remember", lesson="always use firefox for the vendor site"),
        {"text": "confirmed"},
        {"text": "third session"},
    ],
    [
        prompt("add the export button", name="first-session"),
        prompt("/eq-new", name="second-session", settleMs=500),
        prompt("check the vendor invoices", name="second-prompt"),
        prompt("/eq-new", name="third-session", settleMs=500),
        prompt("open the vendor portal", name="third-prompt"),
    ],
    folder="scenarios-b4",
)

scenario(
    "recall-after-a-failure",
    "warden_recall lists the failed call with its error line and whether the last passing check still covers the code.",
    [
        call("bash", command="echo 'src/a.ts(3,7): error TS2322: Type string is not assignable to type number.' >&2; echo 'Found 1 error.' >&2; exit 1"),
        call("bash", command="echo 'all 42 tests passed'; exit 0"),
        call("write", path="src/a.ts", content="export const a = 1;\n"),
        call("warden_recall"),
        {"text": "recalled"},
    ],
    [prompt("fix the build")],
    files={"src/keep.txt": "keep\n"},
)

scenario(
    "compact-evidence-appendix",
    "After a compaction the evidence warden holds comes back as one message: the failed attempt, the last passing check, and the open loops.",
    [
        call("bash", command="echo 'error: boom' >&2; exit 1"),
        loops(action="add", text="Rerun the build", when="after the type fix"),
        {"text": "working on it"},
        {"text": "SUMMARY of the session so far."},
        {"text": "continuing"},
    ],
    [prompt("fix the build", name="work"), {"name": "compact", "rpc": {"type": "compact"}, "wait": "response", "settleMs": 800}, prompt("go on", name="after")],
)
