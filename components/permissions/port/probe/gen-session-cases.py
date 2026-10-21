#!/usr/bin/env python3
"""Generates testdata/session-cases.json: operation scripts the PermissionSession probe (session-probe.test.ts) drives through the original's
PermissionSession (real SessionRules, real active-agent lookups, recording fakes for the forwarding controller, the authorizer selection,
the permission manager and the config store), recorded to extensions/pi-permission-system/testdata/session-oracle.json for
session_oracle_test.go. Deterministic (fixed seed)."""
import json
import random

rnd = random.Random(20261021)

CWDS = ["/work/a", "/work/b", "C:\\work\\c", "/home/u/proj", "/"]
NAMES = ["reviewer", "coder", "  spaced  ", "", "   ", None, "a-b", "Ünï"]
PROMPTS = [None, "", "no tag", '<active_agent name="from-prompt">', "<active_agent name='p2'>", '<active_agent name="  ">',
           "<ACTIVE_AGENT\tname=\"tabbed\">", "x\n<active_agent name=\"nl\">\ny"]
TOOLS = ["read", "ls", "write", "bash", "grep", "edit", "find"]
SURFACES = ["bash", "mcp", "skill", "path_read", "external_directory_write", "external_directory", "path", "read", "task"]
PATTERNS = ["*", "git *", "/tmp/*", "src/*", "*.env"]
SHELLS = [None, {}, {"exec_command": {"commandArgument": "cmd"}},
          {"exec_command": {"commandArgument": "cmd", "workdirArgument": "workdir"}, "run": {"commandArgument": "c"}}]
INFRA = [None, [], ["/opt/a"], ["/opt/a", "/opt/b/c"]]


def entries():
    out = []
    for _ in range(rnd.randint(0, 4)):
        kind = rnd.random()
        if kind < 0.7:
            out.append({"type": "custom", "customType": "active_agent", "data": {"name": rnd.choice(NAMES)}})
        elif kind < 0.85:
            out.append({"type": "message", "customType": "active_agent", "data": {"name": "wrong-type"}})
        else:
            out.append({"type": "custom", "customType": "other", "data": {"name": "wrong-custom"}})
    return out


def ctx():
    c = {"cwd": rnd.choice(CWDS), "hasUI": rnd.random() < 0.6}
    c["entries"] = "throws" if rnd.random() < 0.12 else entries()
    return c


def op(nctx):
    r = rnd.random()
    if r < 0.08:
        return {"op": "activate", "ctx": rnd.randrange(nctx)}
    if r < 0.14:
        return {"op": "deactivate"}
    if r < 0.22:
        return {"op": "reset", "ctx": rnd.randrange(nctx), "trusted": rnd.random() < 0.6}
    if r < 0.28:
        return {"op": "reload", "trusted": rnd.random() < 0.6}
    if r < 0.32:
        return {"op": "shutdown"}
    if r < 0.38:
        return {"op": "notify", "msg": rnd.choice(["hello", "", "warn \u2603"])}
    if r < 0.50:
        return {"op": "resolveAgent", "ctx": rnd.randrange(nctx), "prompt": rnd.choice(PROMPTS)}
    if r < 0.54:
        return {"op": "lastKnown"}
    if r < 0.58:
        return {"op": "setSkills", "names": rnd.sample(["s1", "s2", "s3"], rnd.randint(0, 3))}
    if r < 0.61:
        return {"op": "getSkills"}
    if r < 0.72:
        active = rnd.sample(TOOLS, rnd.randint(0, len(TOOLS)))
        return {"op": "exposed", "active": active, "registered": rnd.sample(TOOLS, rnd.randint(0, len(TOOLS))),
                "denied": rnd.sample(TOOLS, rnd.randint(0, 3))}
    if r < 0.78:
        return {"op": "refresh", "ctx": rnd.choice([None] + list(range(nctx))), "trusted": rnd.random() < 0.6}
    if r < 0.81:
        return {"op": "logPaths"}
    if r < 0.84:
        return {"op": "config"}
    if r < 0.87:
        return {"op": "infra"}
    if r < 0.90:
        return {"op": "shell"}
    if r < 0.92:
        return {"op": "previewLimits"}
    if r < 0.95:
        return {"op": "normalizer"}
    if r < 0.98:
        return {"op": "approve", "surface": rnd.choice(SURFACES), "pattern": rnd.choice(PATTERNS)}
    return {"op": "rules"}


cases = []
for i in range(400):
    nctx = rnd.randint(1, 3)
    cases.append({
        "id": f"session-{i}",
        "flavor": rnd.choice(["posix", "win32"]),
        "ctxs": [ctx() for _ in range(nctx)],
        "configs": [{"yoloMode": rnd.random() < 0.4, "piInfrastructureReadPaths": rnd.choice(INFRA), "shellTools": rnd.choice(SHELLS)}
                    for _ in range(rnd.randint(1, 3))],
        "ops": [op(nctx) for _ in range(rnd.randint(8, 30))],
    })

with open("../../extensions/pi-permission-system/testdata/session-cases.json", "w") as f:
    json.dump(cases, f, ensure_ascii=False)
    f.write("\n")
print(len(cases), "scripts,", sum(len(c["ops"]) for c in cases), "ops")
