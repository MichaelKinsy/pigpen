#!/usr/bin/env python3
"""Generates port/scenarios/*.json: every observable branch of the todo tool and the /todos command.

The model is scripted: each `calls` entry is one assistant turn with the given todo tool calls, followed by a final
"done" turn. `/todos` and other slash commands are separate steps."""
import json, os

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
os.makedirs(out, exist_ok=True)


def todo(**args):
    return {"name": "todo", "arguments": args}


def scenario(name, description, turns, steps=None, after=(), **extra):
    llm = [{"toolCalls": t} for t in turns] + [{"text": "done"}] + [{"text": a} for a in after]
    s = {"name": name, "description": description, "llm": llm}
    s["steps"] = steps or [{"name": "prompt", "rpc": {"type": "prompt", "message": "plan the work"}}]
    s.update(extra)
    with open(os.path.join(out, name + ".json"), "w") as f:
        json.dump(s, f, indent=2)
        f.write("\n")


def cmd(name, message="/todos"):
    return {"name": name, "rpc": {"type": "prompt", "message": message}}


PLAN = {"name": "prompt", "rpc": {"type": "prompt", "message": "plan the work"}}

scenario("create-update-list", "Create, start, complete, list and get a task.", [
    [todo(action="create", subject="Write tests", description="red first", activeForm="writing tests"),
     todo(action="create", subject="Implement", owner="porter", metadata={"area": "core", "size": 3})],
    [todo(action="update", id=1, status="in_progress", activeForm="writing tests")],
    [todo(action="update", id=1, status="completed")],
    [todo(action="list"), todo(action="list", status="pending"), todo(action="get", id=2)],
])

scenario("errors", "Every rejected call: missing fields, unknown ids, illegal transition, self block, cycle, double delete.", [
    [todo(action="create", subject="  "), todo(action="create"),
     todo(action="update"), todo(action="update", id=9, status="completed"),
     todo(action="get"), todo(action="get", id=9), todo(action="delete"), todo(action="delete", id=9)],
    [todo(action="create", subject="a"), todo(action="create", subject="b"),
     todo(action="create", subject="c", blockedBy=[7]), todo(action="update", id=1)],
    [todo(action="update", id=1, status="completed"), todo(action="update", id=1, status="in_progress"),
     todo(action="update", id=1, addBlockedBy=[1]), todo(action="update", id=1, addBlockedBy=[8])],
    [todo(action="update", id=1, addBlockedBy=[2]), todo(action="update", id=2, addBlockedBy=[1])],
    [todo(action="delete", id=2), todo(action="delete", id=2),
     todo(action="update", id=1, addBlockedBy=[2]), todo(action="create", subject="d", blockedBy=[2])],
])

scenario("blocked-by", "Dependencies: create with blockedBy, add and remove, the get blocks line and the list chain suffix.", [
    [todo(action="create", subject="root"), todo(action="create", subject="mid", blockedBy=[1]),
     todo(action="create", subject="leaf")],
    [todo(action="update", id=3, addBlockedBy=[1, 2]), todo(action="get", id=1), todo(action="get", id=3)],
    [todo(action="update", id=3, removeBlockedBy=[1, 2]), todo(action="list")],
])

scenario("no-change", "Updates that change nothing say so; a dependency-only update is a change.", [
    [todo(action="create", subject="x", description="d"), todo(action="create", subject="y")],
    [todo(action="update", id=1, status="pending"), todo(action="update", id=1, subject="x", description="d"),
     todo(action="update", id=2, addBlockedBy=[1]), todo(action="update", id=2, addBlockedBy=[1])],
    [todo(action="update", id=2, subject="y2"), todo(action="update", id=2, status="in_progress", activeForm="y-ing")],
])

scenario("delete-clear", "Tombstones, includeDeleted, the deleted filter, and clear resetting the ids.", [
    [todo(action="create", subject="one"), todo(action="create", subject="two"), todo(action="create", subject="three")],
    [todo(action="delete", id=2), todo(action="list"), todo(action="list", includeDeleted=True),
     todo(action="list", status="deleted", includeDeleted=True), todo(action="list", status="deleted")],
    [todo(action="update", id=1, status="completed"), todo(action="update", id=1, status="deleted")],
    [todo(action="clear")], [todo(action="list"), todo(action="create", subject="fresh")],
])

scenario("metadata", "Metadata merges: set, overwrite, add, null deletes a key, the last null drops the field.", [
    [todo(action="create", subject="m", metadata={"a": 1, "b": 2})],
    [todo(action="update", id=1, metadata={"a": 99, "c": 3}), todo(action="update", id=1, metadata={"a": None}),
     todo(action="update", id=1, metadata={"b": None, "c": None}), todo(action="update", id=1, metadata={"b": None})],
])

scenario("sanitize", "Terminal control characters in model-controlled text never reach the result.", [
    [todo(action="create", subject="evil\u001b[31mred\u001b[0m\u009b2J", description="line1\nline2", owner="who\u009b31mami",
          activeForm="clear\u001b[2Jing\u202e"),
     todo(action="create", subject="tab\there sep"), todo(action="create", subject="osc\u001b]0;title\u0007ok")],
    [todo(action="update", id=1, status="in_progress"), todo(action="get", id=1), todo(action="list")],
])

scenario("bad-input", "An ESC before a line break, and ids no task can have, formatted as JavaScript formats numbers.", [
    [todo(action="create", subject="a\u001b\nb\u001b\rc")],
    [todo(action="get", id=1e-7), todo(action="get", id=0.000001), todo(action="get", id=1.5),
     todo(action="update", id=1, addBlockedBy=[1.5e-7])],
])

scenario("command-empty", "/todos with nothing to show.", [], steps=[cmd("empty")])
scenario("command-only-deleted", "/todos when every task is a tombstone.", [
    [todo(action="create", subject="gone")], [todo(action="delete", id=1)]], steps=[PLAN, cmd("todos")])

scenario("command-groups", "/todos groups by status, with the counts header, ids, activeForm and dependency marks.", [
    [todo(action="create", subject="alpha"), todo(action="create", subject="beta", blockedBy=[1]),
     todo(action="create", subject="gamma", activeForm="gamma-ing"), todo(action="create", subject="delta"),
     todo(action="create", subject="epsilon")],
    [todo(action="update", id=3, status="in_progress", activeForm="gamma-ing"), todo(action="update", id=4, status="completed"),
     todo(action="delete", id=5)],
], steps=[PLAN, cmd("todos")])

scenario("command-pending-only", "/todos with only pending tasks (no completed segment in the header).", [
    [todo(action="create", subject="p1"), todo(action="create", subject="p2")]], steps=[PLAN, cmd("todos")])

scenario("command-completed-only", "/todos with only completed tasks.", [
    [todo(action="create", subject="c1"), todo(action="update", id=1, status="completed")]], steps=[PLAN, cmd("todos")])

scenario("many-tasks", "More tasks than the overlay shows; the tool output and /todos list all of them.", [
    [todo(action="create", subject=f"task {i}") for i in range(1, 16)],
    [todo(action="update", id=i, status="completed") for i in range(1, 6)],
], steps=[PLAN, cmd("todos")])

scenario("guidance-in-request", "The tool definition and its prompt guidance reach the model unchanged.", [],
         steps=[{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}])

scenario("new-session-resets", "A new session replays an empty branch: the previous list is gone (session_start replay).", [
    [todo(action="create", subject="old one")]], steps=[PLAN, {"name": "new", "rpc": {"type": "prompt", "message": "/eq-new"}}, cmd("todos")])
