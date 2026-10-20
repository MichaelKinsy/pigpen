#!/usr/bin/env python3
"""Generates forwarding-cases.json: the inputs the forwarding probe (forwarding-probe.test.ts) runs through the original's own
src/authority functions, recorded to extensions/pi-permission-system/testdata/forwarding_oracle.json for fwd_oracle_test.go.
Run from this directory; the output is deterministic."""
import itertools
import json

cases = {}

# ── requests ──────────────────────────────────────────────────────────────
def req(**over):
    d = {"id": "req-1", "createdAt": 1000, "requesterSessionId": "child", "targetSessionId": "parent", "requesterAgentName": "agent"}
    d.update(over)
    return d

def drop(d, k):
    d = dict(d)
    d.pop(k, None)
    return d

payload = {
    "kind": "bash",
    "request": {
        "requester": {"agentName": "Explore", "forwarded": False, "sessionId": None},
        "surface": "bash", "toolName": "bash", "invokedToolName": None, "value": "git push",
        "matchedPattern": "git *", "commandContext": None, "executedUnit": None,
    },
    "evidence": [{"label": "command", "text": "git push", "detail": None}],
    "annotations": [{"source": "judge", "text": "looks fine"}],
}
intent = {"surface": "path", "matchValues": ["/a", "a"], "boundaryValue": "/a", "requesterCwd": "/w", "principal": {"sessionId": "c", "agentName": "x"}}
reqs = [json.dumps(req())]
for k in ["id", "createdAt", "requesterSessionId", "targetSessionId", "requesterAgentName"]:
    base = req()
    reqs.append(json.dumps(drop(base, k)))
    for bad in [None, 5, True, [], {}, "", "x"]:
        b = dict(base)
        b[k] = bad
        reqs.append(json.dumps(b))
for key, vals in {
    "source": ["tool_call", "skill_input", "skill_read", "other", 5, None, ""],
    "surface": [None, "bash", 5, "", True],
    "value": [None, "git status", 5, "", []],
}.items():
    for v in vals:
        reqs.append(json.dumps(req(**{key: v})))
for kind in ["bash", "mcp", "tool", "path", "external_directory", "bash_external_directory", "skill", "skill_read", "forwarded", "telepathy", "", None, 3]:
    reqs.append(json.dumps(req(payload=dict(payload, kind=kind))))
for field, vals in {
    "surface": [None, 5], "toolName": ["x", None, 5], "invokedToolName": ["exec_command", 5], "value": [None, 5],
    "matchedPattern": [5, None], "commandContext": ["subshell", "command_substitution", "process_substitution", "weird", None, 5], "executedUnit": ["ls", 5],
}.items():
    for v in vals:
        p = json.loads(json.dumps(payload))
        p["request"][field] = v
        reqs.append(json.dumps(req(payload=p)))
    p = json.loads(json.dumps(payload))
    del p["request"][field]
    reqs.append(json.dumps(req(payload=p)))
for requester in [None, {}, {"agentName": None, "forwarded": True, "sessionId": "s"}, {"agentName": 5, "forwarded": True, "sessionId": None},
                  {"agentName": "a", "forwarded": "yes", "sessionId": None}, {"agentName": "a", "forwarded": False}, []]:
    p = json.loads(json.dumps(payload))
    p["request"]["requester"] = requester
    reqs.append(json.dumps(req(payload=p)))
for ev in [[], [{"label": "l", "text": "t", "detail": "d"}], [{"label": "l", "text": "t"}], [{"label": 1, "text": "t", "detail": None}],
           [{"label": "l", "text": None, "detail": None}], ["x"], [None], {}, None, "e", [{"label": "l", "text": "t", "detail": 5}]]:
    reqs.append(json.dumps(req(payload=dict(payload, evidence=ev))))
for an in [[], [{"source": "s", "text": "t"}], [{"source": "s"}], [{"text": "t"}], [5], None, {}, [{"source": 1, "text": "t"}]]:
    reqs.append(json.dumps(req(payload=dict(payload, annotations=an))))
for key in ["request", "evidence", "annotations", "kind"]:
    reqs.append(json.dumps(req(payload=drop(payload, key))))
for p in [None, 5, "x", [], {}, True]:
    reqs.append(json.dumps(req(payload=p)))
for sa in [None, "bash", {}, {"grants": []}, {"grants": "bash"}, {"grants": [None]}, {"grants": [{"surface": "bash", "pattern": "git *"}]},
           {"grants": [{"surface": "", "pattern": "x"}]}, {"grants": [{"surface": "bash"}]}, {"grants": [{"pattern": "x"}]},
           {"grants": [{"surface": "bash", "pattern": 5}]}, {"grants": [{"surface": "bash", "pattern": ""}]},
           {"grants": [{"surface": "bash", "pattern": "a", "extra": 1}, {"surface": "path_read", "pattern": "/x/*"}]},
           {"grants": [{"surface": "bash", "pattern": "a"}, None]}, {"surface": "bash", "patterns": ["git *"]}, [], [{"surface": "bash", "pattern": "x"}], 7, True]:
    reqs.append(json.dumps(req(sessionApproval=sa)))
for ai in [None, 5, "x", [], {}, intent,
           dict(intent, boundaryValue=None), drop(intent, "boundaryValue"), dict(intent, boundaryValue=5), dict(intent, matchValues=[]),
           dict(intent, matchValues=["/a", 5]), dict(intent, matchValues="x"), drop(intent, "matchValues"), dict(intent, surface=5), drop(intent, "surface"),
           dict(intent, requesterCwd=5), drop(intent, "requesterCwd"), drop(intent, "principal"), dict(intent, principal=None), dict(intent, principal=[]),
           dict(intent, principal={"sessionId": "c"}), dict(intent, principal={"agentName": "x"}), dict(intent, principal={"sessionId": 1, "agentName": "x"}),
           dict(intent, principal={"sessionId": "c", "agentName": "x", "extra": 1}), dict(intent, extra="x")]:
    reqs.append(json.dumps(req(accessIntent=ai)))
reqs.append(json.dumps(req(message="Allow this?", surface="read", value="/tmp/x")))
reqs.append(json.dumps(req(payload=payload, source="skill_read", surface="skill", value="deep-research", sessionApproval={"grants": [{"surface": "skill", "pattern": "*"}]}, accessIntent=intent)))
reqs.append(json.dumps(req(id="é😀", requesterAgentName="é\u2028\u0000")))
reqs += ["", "{", "{}", "null", "[]", "5", '"x"', "true", "false", "0", '{"id":"x"}', " \n", "\ufeff" + json.dumps(req()), json.dumps(req()) + " trailing", "[" + json.dumps(req()) + "]"]
cases["requests"] = reqs

# ── responses ─────────────────────────────────────────────────────────────
def resp(**over):
    d = {"approved": True, "state": "approved", "responderSessionId": "parent", "respondedAt": 1000}
    d.update(over)
    return d

def nest(n):
    d = {"kind": "rule", "surface": "bash", "pattern": "*", "origin": "global"}
    for _ in range(n):
        d = {"kind": "forwarded", "responderSessionId": "r", "decision": d}
    return d

deciders = [
    {"kind": "user", "via": "dialog"}, {"kind": "user", "via": "select"}, {"kind": "user", "via": "smoke"}, {"kind": "user"},
    {"kind": "authorizer", "name": "m", "verdict": "deny", "reason": "r"}, {"kind": "authorizer", "name": "m", "verdict": "allow", "reason": None},
    {"kind": "authorizer", "name": "m", "verdict": "defer", "reason": None}, {"kind": "authorizer", "name": "m", "verdict": "deny"},
    {"kind": "authorizer", "verdict": "deny", "reason": None}, {"kind": "authorizer", "name": 1, "verdict": "deny", "reason": None},
    {"kind": "rule", "surface": "bash", "pattern": "*", "origin": "global"}, {"kind": "rule", "surface": "bash", "pattern": None, "origin": None},
    {"kind": "rule", "surface": "bash", "pattern": 5, "origin": None}, {"kind": "rule", "surface": "bash", "pattern": None}, {"kind": "rule", "pattern": None, "origin": None},
    {"kind": "session_approval", "surface": "bash", "pattern": "git *"}, {"kind": "session_approval", "surface": "bash", "pattern": None},
    {"kind": "session_approval", "surface": "bash"}, {"kind": "yolo", "pattern": None}, {"kind": "yolo", "pattern": "<opaque-bash-wrapper>"}, {"kind": "yolo"},
    {"kind": "infrastructure_read"}, {"kind": "infrastructure_read", "extra": 1}, {"kind": "unavailable", "reason": "r"}, {"kind": "unavailable"},
    {"kind": "unavailable", "reason": None}, {"kind": "gate_error", "reason": "boom"}, {"kind": "gate_error", "reason": 5},
    {"kind": "forwarded", "responderSessionId": None, "decision": None}, {"kind": "forwarded", "responderSessionId": "r", "decision": None},
    {"kind": "forwarded", "responderSessionId": "r"}, {"kind": "forwarded", "decision": None}, {"kind": "forwarded", "responderSessionId": 5, "decision": None},
    {"kind": "forwarded", "responderSessionId": "r", "decision": {"kind": "user", "via": "dialog"}}, {"kind": "forwarded", "responderSessionId": "r", "decision": {"kind": "nope"}},
    nest(1), nest(2), nest(3), nest(4), nest(5), {"kind": "teleport"}, {"kind": 5}, {}, [], None, "user", 5, True,
]
resps = [json.dumps(resp())]
for st in ["approved", "approved_for_session", "approved_for_serving_session", "denied", "denied_with_reason", "other", "", None, 5]:
    resps.append(json.dumps(resp(state=st)))
for k in ["approved", "state", "responderSessionId"]:
    resps.append(json.dumps(drop(resp(), k)))
    for bad in [None, 5, "true", [], {}, "x"]:
        d = resp()
        d[k] = bad
        resps.append(json.dumps(d))
for ap in [True, False, "yes", 1, 0, None]:
    resps.append(json.dumps(resp(approved=ap)))
for dr in [None, "no", "", " r ", 5, [], "é"]:
    resps.append(json.dumps(resp(denialReason=dr)))
for ra in [1000, 0, -1, 1.5, "1000", None, True, 1e15]:
    resps.append(json.dumps(resp(respondedAt=ra)))
resps.append(json.dumps(drop(resp(), "respondedAt")))
for w in ["proven", "family", "everything", "", None, 5, "Family"]:
    resps.append(json.dumps(resp(sessionGrantWidth=w)))
for d in deciders:
    resps.append(json.dumps(resp(decidedBy=d)))
resps += ["", "{", "{}", "null", "[]", "5", '"x"', "true", "0", json.dumps(resp()) + " x", "\ufeff" + json.dumps(resp())]
cases["responses"] = resps

# ── heartbeats ────────────────────────────────────────────────────────────
NOW = 1_700_000_000_000
def hb(**over):
    d = {"sessionId": "s", "pid": 4242, "updatedAt": NOW}
    d.update(over)
    return d
hbs = []
for upd in [NOW, NOW - 1, NOW - 4999, NOW - 5000, NOW - 5001, NOW - 100000, NOW + 5000, NOW + 1]:
    hbs.append(json.dumps(hb(updatedAt=upd)))
for pid in [4242, 9999, 0, -1, 1.5, "4242", None, True, 2**31, 2**53, 1e21]:
    hbs.append(json.dumps(hb(pid=pid)))
for sid in ["s", "", None, 5, "a/b", "é"]:
    hbs.append(json.dumps(hb(sessionId=sid)))
for upd in [None, "x", True, "1700000000000", 0, -5, 1e300]:
    hbs.append(json.dumps(hb(updatedAt=upd)))
for k in ["sessionId", "pid", "updatedAt"]:
    hbs.append(json.dumps(drop(hb(), k)))
hbs += ["", "{", "null", "[]", "5", '"x"', json.dumps(hb()) + "x", json.dumps(dict(hb(), extra=1))]
cases["heartbeats"] = {"now": NOW, "alivePid": 4242, "raws": hbs}

# ── forwarding targets ────────────────────────────────────────────────────
targets = []
sids = [None, "", "  ", "unknown", "UNKNOWN", "child", " child ", "other", "é"]
envs = [{}, {"PI_AGENT_ROUTER_PARENT_SESSION_ID": "p1"}, {"PI_SUBAGENT_PARENT_SESSION": "p2"},
        {"PI_AGENT_ROUTER_PARENT_SESSION_ID": "p1", "PI_SUBAGENT_PARENT_SESSION": "p2"}, {"PI_AGENT_ROUTER_PARENT_SESSION_ID": "child", "PI_SUBAGENT_PARENT_SESSION": "p2"},
        {"PI_AGENT_ROUTER_PARENT_SESSION_ID": "  child  ", "PI_SUBAGENT_PARENT_SESSION": "child"}, {"PI_AGENT_ROUTER_PARENT_SESSION_ID": "unknown"},
        {"PI_AGENT_ROUTER_PARENT_SESSION_ID": ""}, {"PI_AGENT_ROUTER_PARENT_SESSION_ID": " p1 "}]
registries = [None, {}, {"child": {}}, {"child": {"parentSessionId": "reg-parent"}}, {"child": {"parentSessionId": "child"}}, {"child": {"parentSessionId": "  "}},
              {"child": {"parentSessionId": "unknown"}}, {"other": {"parentSessionId": "reg-parent"}}, {"child": {"parentSessionId": " reg-parent "}}]
for is_sub, cur, env, reg in itertools.product([True, False], [None, "child", " child ", "unknown"], envs, registries):
    targets.append({"isSubagent": is_sub, "currentSessionId": cur, "env": env, "sessionId": "child" if reg is not None else None, "registry": reg})
cases["targets"] = targets

# ── session ids ───────────────────────────────────────────────────────────
cases["sessionIds"] = ["", " ", "unknown", "UNKNOWN", " Unknown ", "unknown2", "a", "a b", "é", "😀", "a/b", "a\\b", "..", ".", "%", "%2F", "x y&z=1", "\u00a0x\u00a0", "\ufeffx",
                       "\u2003x\u2003", "\u0085x", "\u200bx", "x\u2028", "\ta\n", "A-_.!~*'()", "a#b?c", "日本語", "\u212aNOWN", "ǅ", "a\u0000b"]

# ── resolutions (decision-resolution.ts) ──────────────────────────────────
cases["resolutions"] = [{"decidedBy": d, "approved": a, "forSession": f} for d in deciders if isinstance(d, dict) and "kind" in d for a in (True, False) for f in (True, False)]

# ── delegation envelope ───────────────────────────────────────────────────
surfaces = [None, "path", "path_read", "path_write", "external_directory", "external_directory_read", "external_directory_write", "bash", "read", "write", "skill", "mcp",
            "", "path_x", "pathread", "external_directory_readwrite", "path_read_write"]
cases["envelope"] = [{"accessIntentSurface": a, "displaySurface": d, "verdict": v} for a in surfaces for d in [None, "bash", "path"] for v in ("allow", "deny", "defer")]

# ── server scenarios ──────────────────────────────────────────────────────
decisions = {
    "approved": {"approved": True, "state": "approved", "decidedBy": {"kind": "user", "via": "dialog"}},
    "for_session": {"approved": True, "state": "approved_for_session", "decidedBy": {"kind": "user", "via": "dialog"}},
    "for_session_family": {"approved": True, "state": "approved_for_session", "sessionGrantWidth": "family", "decidedBy": {"kind": "user", "via": "select"}},
    "serving": {"approved": True, "state": "approved_for_serving_session", "decidedBy": {"kind": "user", "via": "dialog"}},
    "serving_family": {"approved": True, "state": "approved_for_serving_session", "sessionGrantWidth": "family", "decidedBy": {"kind": "user", "via": "dialog"}},
    "serving_proven": {"approved": True, "state": "approved_for_serving_session", "sessionGrantWidth": "proven", "denialReason": "ignored", "decidedBy": {"kind": "user", "via": "dialog"}},
    "denied": {"approved": False, "state": "denied", "decidedBy": {"kind": "user", "via": "dialog"}},
    "denied_reason": {"approved": False, "state": "denied_with_reason", "denialReason": "no way", "decidedBy": {"kind": "authorizer", "name": "m", "verdict": "deny", "reason": "no way"}},
    "unavailable": {"approved": False, "state": "denied", "confirmationUnavailable": True, "denialReason": "nobody", "decidedBy": {"kind": "unavailable", "reason": "nobody"}},
    "yolo": {"approved": True, "state": "approved", "decidedBy": {"kind": "yolo", "pattern": None}},
}
policies = {
    "none": None,
    "allow": {"state": "allow", "matchedPattern": "git *", "origin": "global"},
    "allow_nopattern": {"state": "allow", "origin": "builtin"},
    "deny": {"state": "deny", "matchedPattern": "rm *", "origin": "project"},
    "deny_reason": {"state": "deny", "matchedPattern": "rm *", "origin": "project", "reason": "  blocked by policy  "},
    "deny_blank_reason": {"state": "deny", "origin": "project", "reason": "   "},
    "ask": {"state": "ask", "origin": "builtin"},
}
sa = {"grants": [{"surface": "bash", "pattern": "git *"}, {"surface": "external_directory_write", "pattern": "/out/*"}]}
shapes = {
    "legacy": req(id="r1"),
    "skew_display": req(id="r1", source="tool_call", surface="bash", value="git push"),
    "intent_bash": req(id="r1", source="tool_call", surface="bash", value="git push", payload=payload, accessIntent=dict(intent, surface="bash", matchValues=["git push"], boundaryValue=None)),
    "intent_path_session": req(id="r1", source="skill_input", surface="read", value="/a", payload=payload, accessIntent=intent, sessionApproval=sa),
    "no_payload_intent": req(id="r1", surface="read", value="/tmp/x", accessIntent=intent),
    "empty_agent": req(id="r1", requesterAgentName="", requesterSessionId="", payload=payload),
    "null_display": req(id="r1", surface=None, value=None, payload=payload, sessionApproval=sa),
    "wrong_target": req(id="r1", targetSessionId="someone-else", accessIntent=intent),
    "padded_target": req(id="r1", targetSessionId=" parent-session ", accessIntent=intent),
    "bad_session_approval": req(id="r1", surface="bash", value="x", sessionApproval={"grants": []}),
}
cases["server"] = []
for sk, pk, dk in itertools.product(shapes, policies, list(decisions) + ["throw", "throw_empty"]):
    shape = dict(shapes[sk])
    if sk not in ("wrong_target", "padded_target"):
        shape["targetSessionId"] = "parent-session"
    cases["server"].append({"name": f"{sk}/{pk}/{dk}", "files": [{"name": "r1.json", "raw": json.dumps(shape)}], "policy": policies[pk],
                            "escalator": ({"throw": "prompt exploded"} if dk == "throw" else {"throw": ""} if dk == "throw_empty" else {"decision": decisions[dk]})})
# several files in one inbox, in name order, with unreadable and invalid ones
ok = lambda i: json.dumps(dict(shapes["intent_bash"], id=i, targetSessionId="parent-session"))
cases["server"].append({"name": "mixed-inbox", "files": [
    {"name": "a.json", "raw": ok("a")}, {"name": "b.json", "raw": "{"}, {"name": "c.json", "raw": json.dumps(req(id="c", targetSessionId="other"))},
    {"name": "d.json", "raw": json.dumps({"id": "d"})}, {"name": "e.txt", "raw": "ignored"}, {"name": "f.json", "raw": ok("f")}, {"name": "Z.json", "raw": ok("Z")}],
    "policy": policies["ask"], "escalator": {"decision": decisions["approved"]}})
cases["server"].append({"name": "empty-inbox", "files": [], "policy": None, "escalator": {"decision": decisions["approved"]}})
cases["server"].append({"name": "id-with-path-chars", "files": [{"name": "x.json", "raw": json.dumps(dict(shapes["intent_bash"], id="é y", targetSessionId="parent-session"))}], "policy": policies["allow"], "escalator": {"decision": decisions["approved"]}})

with open("forwarding-cases.json", "w") as f:
    json.dump(cases, f, ensure_ascii=False, indent=0)
print({k: (len(v) if isinstance(v, list) else len(v["raws"])) for k, v in cases.items()})
