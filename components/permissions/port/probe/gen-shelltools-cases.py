#!/usr/bin/env python3
"""Writes extensions/pi-permission-system/testdata/shelltools-cases.json: config files (global, project) with `shellTools` in every
shape the schema accepts and rejects, and the tool calls whose shell-invocation the original decides from the resulting aliases."""
import json, os

def sh(**kw): return json.dumps({"shellTools": kw})
G = []  # (id, global text, project text or None, trusted)
def add(i, g, p=None, trusted=True): G.append((i, g, p, trusted))

add("none", "{}")
add("empty-config-file", "")
add("not-json", "{shellTools:")
add("array-top", "[]")
add("null-shelltools", '{"shellTools":null}')
add("empty-map", '{"shellTools":{}}')
for name, val in [
    ("exec", {"exec_command": {"commandArgument": "cmd"}}),
    ("exec-workdir", {"exec_command": {"commandArgument": "cmd", "workdirArgument": "workdir"}}),
    ("two", {"exec_command": {"commandArgument": "cmd", "workdirArgument": "workdir"}, "run": {"commandArgument": "c", "workdirArgument": "w"}}),
    ("empty-command-arg", {"exec_command": {"commandArgument": ""}}),
    ("empty-workdir-arg", {"exec_command": {"commandArgument": "cmd", "workdirArgument": ""}}),
    ("extra-keys", {"exec_command": {"commandArgument": "cmd", "extra": 1, "workdirArgument": "workdir"}}),
    ("padded-name", {" exec_command ": {"commandArgument": "cmd"}}),
    ("proto-name", {"constructor": {"commandArgument": "cmd"}}),
    ("tostring-name", {"toString": {"commandArgument": "cmd"}}),
    ("bash-name", {"bash": {"commandArgument": "cmd"}}),
    ("empty-name", {"": {"commandArgument": "cmd"}}),
    ("missing-command", {"exec_command": {}}),
    ("missing-command-workdir", {"exec_command": {"workdirArgument": "workdir"}}),
    ("command-number", {"exec_command": {"commandArgument": 5}}),
    ("command-null", {"exec_command": {"commandArgument": None}}),
    ("workdir-number", {"exec_command": {"commandArgument": "cmd", "workdirArgument": 5}}),
    ("workdir-null", {"exec_command": {"commandArgument": "cmd", "workdirArgument": None}}),
    ("entry-null", {"exec_command": None}),
    ("entry-string", {"exec_command": "cmd"}),
    ("entry-array", {"exec_command": [{"commandArgument": "cmd"}]}),
    ("entry-number", {"exec_command": 1}),
    ("good-and-bad", {"exec_command": {"commandArgument": "cmd"}, "run": {}}),
    ("bad-and-good", {"run": {"workdirArgument": "w"}, "exec_command": {"commandArgument": "cmd"}}),
]:
    add("shell-" + name, json.dumps({"shellTools": val}))
for name, raw in [("string", '"x"'), ("number", "3"), ("bool", "true"), ("array", "[]"), ("array-entries", '[{"commandArgument":"cmd"}]')]:
    add("shell-top-" + name, '{"shellTools":%s}' % raw)
add("proto-key-json", '{"shellTools":{"__proto__":{"commandArgument":"cmd"}}}')
add("proto-key-json-bad", '{"shellTools":{"__proto__":{}}}')
add("dup-keys", '{"shellTools":{"exec_command":{"commandArgument":"a"},"exec_command":{"commandArgument":"b"}}}')
add("dup-inner-keys", '{"shellTools":{"exec_command":{"commandArgument":"a","commandArgument":"b"}}}')
add("unicode-name", sh(**{"ｅxec": {"commandArgument": "cmd"}}))
# a rejected field elsewhere in the same file
add("other-bad-field", '{"yoloMode":"yes","shellTools":{"exec_command":{"commandArgument":"cmd"}}}')
add("other-bad-field-bad-shell", '{"promptMaxRows":"x","shellTools":{"exec_command":{}}}')
add("unknown-key", '{"nope":1,"shellTools":{"exec_command":{"commandArgument":"cmd"}}}')
# project scope: merged shallowly by tool name over the global one
GL = sh(exec_command={"commandArgument": "cmd", "workdirArgument": "workdir"}, run={"commandArgument": "c"})
for name, proj in [
    ("override", sh(exec_command={"commandArgument": "other"})),
    ("add", sh(extra={"commandArgument": "e"})),
    ("empty", sh()),
    ("null", '{"shellTools":null}'),
    ("bad-entry", sh(exec_command={})),
    ("bad-top", '{"shellTools":"x"}'),
    ("not-json", "{"),
    ("none", "{}"),
]:
    add("project-" + name, GL, proj, True)
    add("project-untrusted-" + name, GL, proj, False)
add("project-only", "{}", sh(run={"commandArgument": "c"}), True)
add("global-bad-project-good", sh(exec_command={}), sh(run={"commandArgument": "c"}), True)

NAMES = ["exec_command", "bash", "run", "extra", "other", "x", "  exec_command ", " exec_command ", "constructor", "toString", "__proto__", "hasOwnProperty", "valueOf", "", "Bash"]
INPUTS = [
    {"cmd": "  ls -la  ", "workdir": " /w ", "command": "pwd", "c": "cc", "w": "ww", "undefined": "u", "other": "oo"},
    {"cmd": 5, "workdir": "", "command": None, "c": ["x"], "w": 3},
    {"cmd": "", "workdir": "   ", "command": "  "},
    None, "str", [1], {},
]
out = [{"id": i, "global": g, "project": p, "trusted": t,
        "tools": [{"name": n, "input": inp} for n in NAMES for inp in INPUTS]} for i, g, p, t in G]
path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "../../extensions/pi-permission-system/testdata/shelltools-cases.json")
json.dump(out, open(path, "w"), separators=(",", ":"), ensure_ascii=False)
print(len(out), "cases,", sum(len(c["tools"]) for c in out), "tool calls ->", path)
