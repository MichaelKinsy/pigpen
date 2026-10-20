#!/usr/bin/env python3
"""Generates authority-cases.json: the inputs the authority probe (authority-probe.test.ts) runs through the original's own
src/authority modules, recorded to extensions/pi-permission-system/testdata/authority_oracle.json for auth_oracle_test.go.
Run from this directory; the output is deterministic."""
import itertools
import json, os
import random

rnd = random.Random(1402973)
cases = {}

ACTIONS = ["approve", "approveSession", "approveSessionBoth", "deny", "denyWithReason"]
DIGITS = {"approve": "1", "approveSession": "2", "approveSessionBoth": "3", "deny": "4", "denyWithReason": "5"}
DEFAULT_KEYS = {"approve": "y", "approveSession": "s", "approveSessionBoth": "b", "deny": "n", "denyWithReason": "r"}
SWAPPED = dict(DEFAULT_KEYS, approve="n", deny="y")
KEYSETS = {"default": DEFAULT_KEYS, "digits": DIGITS, "swapped": SWAPPED}
SCOPE = {"subagentLabel": "This subagent only", "servingSessionLabel": "The whole session"}
WIDTH = 'Yes, allow reads and writes to "/tmp/*" for this session'

# ── the pure decision model ─────────────────────────────────────────────────
events = [{"type": "nav", "direction": "up"}, {"type": "nav", "direction": "down"}, {"type": "confirm"}, {"type": "cancel"}]
events += [{"type": "hotkey", "action": a} for a in ACTIONS]
events += [{"type": "submitReason", "draft": d} for d in ["", "   ", "x", "  why not  ", "\n"]]
configs = []
for double in (True, False):
    for keyset in KEYSETS:
        for width in (None, WIDTH):
            for scope in (None, SCOPE):
                configs.append({"double": double, "keys": keyset, "widthLabel": width, "scope": scope})
model = []
for config in configs:
    seqs = [[e] for e in events] + [list(p) for p in itertools.product(events, repeat=2)]
    for _ in range(90):
        seqs.append([rnd.choice(events) for _ in range(rnd.randint(3, 10))])
    for seq in seqs:
        model.append({"config": config, "events": seq})
cases["model"] = model

# ── the select/input fallback ───────────────────────────────────────────────
dialog = []
labels = [None, "Yes, allow \"git *\" for this session", "Yes", "No", ""]
for session_label in labels:
    for width in (None, WIDTH, ""):
        for scope in (None, SCOPE):
            opts = {}
            if session_label is not None:
                opts["sessionLabel"] = session_label
            if width is not None:
                opts["sessionWidth"] = {"label": width}
            if scope:
                opts["sessionScope"] = scope
            first = ["Yes", "No", "No, provide reason", "Yes, for this session", WIDTH, SCOPE["servingSessionLabel"], session_label or "x", None, "bogus"]
            for a in first:
                for b in ([None, SCOPE["subagentLabel"], SCOPE["servingSessionLabel"], "bogus"] if scope else [None]):
                    for inp in ([None, "", "  ", "why", "  padded  "] if a == "No, provide reason" else [None]):
                        dialog.append({"options": opts or None, "selects": [a, b], "input": inp})
cases["dialog"] = dialog

# ── the chain ───────────────────────────────────────────────────────────────
verdicts = [{"kind": "allow"}, {"kind": "deny"}, {"kind": "deny", "reason": "no"}, {"kind": "deny", "reason": "  "}, {"kind": "deny", "reason": "  padded reason  "},
            {"kind": "defer"}, {"kind": "maybe"}]
terminals = [
    {"approved": True, "state": "approved", "decidedBy": {"kind": "user", "via": "dialog"}},
    {"approved": False, "state": "denied", "confirmationUnavailable": True, "decidedBy": {"kind": "unavailable", "reason": "r"}},
]
chain = []
for n in (0, 1, 2, 3):
    for combo in itertools.product(verdicts, repeat=n):
        for term in terminals:
            chain.append({"links": [{"name": f"link{i}", "verdict": v} for i, v in enumerate(combo)], "terminal": term})
# a link that rejects, or answers undefined: the original's promise rejects (the Go port returns an error), nothing later runs
rejecting = [{"kind": "allow"}, {"kind": "defer"}, {"throw": "boom"}, {"undefined": True}]
for n in (1, 2, 3):
    for combo in itertools.product(rejecting, repeat=n):
        if not any("throw" in v or "undefined" in v for v in combo):
            continue
        for term in terminals:
            chain.append({"links": [{"name": f"link{i}", "verdict": v} for i, v in enumerate(combo)], "terminal": term})
cases["chain"] = chain

# the registry's disposer: the original guards by function identity, the port by registration
cases["registry"] = [
    {"name": "distinct functions: a stale disposer does not evict a later registration", "first": "a", "second": "b"},
    {"name": "the same function registered again: a stale disposer evicts it in the original", "first": "a", "second": "a"},
]

# ── bracketed paste ─────────────────────────────────────────────────────────
S, E = "\x1b[200~", "\x1b[201~"
paste = []
bodies = ["", "a", "one\ntwo", "one\r\ntwo", "a\n\n\nb", "\r\n\r\n", "\n", "x\ry", "tab\there", "uni\u00e9\n\u4e16\u754c", "e\u0301\nz", "a b  c"]
for b in bodies:
    paste += [S + b + E, S + b, b + E, S + b + E + "tail\nmore", "pre\n" + S + b + E, S + b + E + S + "z\nz" + E]
paste += ["", "\r", "a", "\x1b", S, E, S + E, E + S, S + S + "a\nb" + E + E, "\x1b[200~\x1b[201~\n"]
cases["paste"] = paste

# ── ui_prompt, the request options and the notice, through LocalUserAuthorizer ─
def payload(kind="tool", surface="read", tool="read", value="read", matched=None, requester=None, evidence=None, annotations=None, invoked=None, ctx=None, unit=None):
    return {"kind": kind, "request": {"requester": requester or {"agentName": None, "forwarded": False, "sessionId": None}, "surface": surface, "toolName": tool,
            "invokedToolName": invoked, "value": value, "matchedPattern": matched, "commandContext": ctx, "executedUnit": unit}, "evidence": evidence or [], "annotations": annotations or []}

grant_sets = {
    "none": None,
    "empty": [],
    "bash": [{"surface": "bash", "pattern": "git *"}],
    "path": [{"surface": "path", "pattern": "/r/*"}],
    "ext_dir_pair": [{"surface": "external_directory", "pattern": "/r/a"}, {"surface": "external_directory", "pattern": "/r/a/*"}],
    "read": [{"surface": "external_directory_read", "pattern": "/outside/*"}],
    "write": [{"surface": "external_directory_write", "pattern": "/tmp/*"}],
    "read2": [{"surface": "external_directory_read", "pattern": "/a/*"}, {"surface": "external_directory_read", "pattern": "/b/*"}],
    "mixed": [{"surface": "external_directory_read", "pattern": "/a/*"}, {"surface": "external_directory_write", "pattern": "/b/*"}],
    "mixed_family": [{"surface": "bash", "pattern": "ls"}, {"surface": "path", "pattern": "/p"}],
    "path_read": [{"surface": "path_read", "pattern": "/p/*"}],
    "edit": [{"surface": "path_write", "pattern": "/p/*"}],
}
local = []
for gname, grants in grant_sets.items():
    for forwarding in (None, {"requesterAgentName": "Explore", "requesterSessionId": "child"}, {"requesterAgentName": None, "requesterSessionId": None}, {"requesterAgentName": "", "requesterSessionId": "c"}):
        for session_label in (None, "Yes, for 'read' tool", ""):
            for source, agent, tool, skill in (("tool_call", "test-agent", "read", None), ("skill_input", None, None, "deploy-helper"), ("tool_call", "Explore", "bash", None)):
                d = {"requestId": "req-123", "source": source, "agentName": agent, "payload": payload(),
                     "toolName": tool, "skillName": skill, "command": "git push" if tool == "bash" else None, "path": None}
                if grants is not None:
                    d["sessionApproval"] = {"grants": grants}
                if forwarding is not None:
                    d["forwarding"] = forwarding
                if session_label is not None:
                    d["sessionLabel"] = session_label
                local.append({"details": d, "session": rnd.choice([{"name": "refactor-auth", "cwd": "/w/repo"}, {"name": None, "cwd": "/w/repo"}, {"name": "", "cwd": "/w/other/dir"}])})
for tool, command, path, target, skill, preview in [("bash", "c", "p", "t", "s", "v"), ("bash", None, "p", "t", "s", None), ("x", None, None, "t", "s", None),
                                                    ("x", None, None, None, "s", None), ("x", None, None, None, None, None), (None, None, None, None, None, None), ("", "", "", "", "", "")]:
    for source in ("tool_call", "skill_input", "skill_read"):
        local.append({"details": {"requestId": "r", "source": source, "agentName": "a", "payload": payload(surface="bash", tool="bash", value="v", matched="git *"),
                                  "toolName": tool, "command": command, "path": path, "target": target, "skillName": skill, "toolInputPreview": preview},
                      "session": {"name": "n", "cwd": "/c"}})
for surface, value in [("bash", "git push"), ("external_directory", "/etc/hosts"), ("", ""), ("only", None), (None, "only")]:
    d = {"requestId": "r", "source": "tool_call", "agentName": "a", "payload": payload(), "toolName": None}
    if surface is not None:
        d["surface"] = surface
    if value is not None:
        d["value"] = value
    d["forwarding"] = {"requesterAgentName": "a", "requesterSessionId": "s"}
    local.append({"details": d, "session": {"name": "n", "cwd": "/c"}})
cases["local"] = local

# ── the prompter's review entries ───────────────────────────────────────────
decisions = []
for state, approved in [("approved", True), ("approved_for_session", True), ("approved_for_serving_session", True), ("denied", False), ("denied_with_reason", False)]:
    for width in (None, "proven", "family"):
        for reason in (None, "too sensitive"):
            for unavailable in (False, True):
                for by in ({"kind": "user", "via": "dialog"}, {"kind": "unavailable", "reason": "nobody"}, {"kind": "forwarded", "responderSessionId": "p", "decision": None}):
                    d = {"approved": approved, "state": state, "decidedBy": by}
                    if width:
                        d["sessionGrantWidth"] = width
                    if reason:
                        d["denialReason"] = reason
                    if unavailable:
                        d["confirmationUnavailable"] = True
                    decisions.append(d)
prompter = []
payloads = [payload(), payload(kind="bash", surface="bash", tool="bash", value="rm -rf build", matched="rm *", unit="rm", ctx="subshell", invoked="exec"),
            payload(kind="forwarded", requester={"agentName": "scout", "forwarded": True, "sessionId": "abc"})]
for i, d in enumerate(decisions):
    details = {"requestId": "req-123", "source": ["tool_call", "skill_input", "skill_read"][i % 3], "agentName": [None, "test-agent"][i % 2], "payload": payloads[i % 3]}
    if i % 4 == 0:
        details.update({"toolCallId": "tc-1", "toolName": "read", "skillName": "librarian", "path": "/src/foo.ts", "command": "git status", "target": "server:tool", "toolInputPreview": "{ path: '...' }"})
    prompter.append({"decision": d, "details": details})
cases["prompter"] = prompter

# ── selection: terminal kind, chain role and relay target ───────────────────
selection = []
for has_ui, subagent, env, registry, serving in itertools.product((True, False), (True, False), (None, "lead", "unknown"), (None, "reg-parent"), (True, False, None)):
    selection.append({"hasUI": has_ui, "subagent": subagent, "env": env, "registry": registry, "serving": serving})
cases["selection"] = selection

# ── buildUiPrompt ───────────────────────────────────────────────────────────
ui = []
for source in ("tool_call", "skill_input", "skill_read"):
    for tool, command, path, target, skill in itertools.product((None, "bash"), (None, "c"), (None, "p"), (None, "t"), (None, "s")):
        ui.append({"requestId": "r", "source": source, "agentName": None, "payload": payload(), "toolName": tool, "command": command, "path": path, "target": target, "skillName": skill})
cases["uiPrompt"] = ui

# ── the inline component: key sequences, rendered frames and decisions ───────
ESC, ENTER, DOWN, UP = "\x1b", "\r", "\x1b[B", "\x1b[A"
def pasted(c): return S + c + E
payloads_c = {
    "short": payload(kind="path", surface="path", tool="read", value="/repo/secret.txt"),
    "long": payload(kind="path", surface="path", tool="read", value="~/.pi/agent/sessions/" + "a" * 120),
    "bash": payload(kind="bash", surface="bash", tool="bash", value="git push origin main", matched="git *", evidence=[{"label": "command", "text": "git push origin main", "detail": None}]),
    "forwarded": payload(kind="forwarded", surface="bash", tool=None, value="@'\n" + "\n".join(["- a finding line about some module in the codebase"] * 40) + "\n'@ | Out-File -FilePath report.md",
                         requester={"agentName": "scout", "forwarded": True, "sessionId": "abc123"}, evidence=[{"label": "requested", "text": "@'\n" + "\n".join(["- a finding line"] * 40) + "\n'@", "detail": None}],
                         annotations=[{"source": "judge", "text": "advisory text"}]),
    "unicode": payload(kind="path", surface="path", tool="read", value="/repo/\u65e5\u672c\u8a9e/\U0001F600/e\u0301.txt"),
}
option_sets = {
    "none": None,
    "label": {"sessionLabel": 'Yes, allow "git *" for this session'},
    "width": {"sessionLabel": 'Yes, allow writes to "/tmp/*" for this session', "sessionWidth": {"label": WIDTH}},
    "scope": {"sessionScope": SCOPE},
    "both": {"sessionWidth": {"label": WIDTH}, "sessionScope": SCOPE},
}
pref_sets = {
    "double": {"double": True, "keys": "default", "budget": None},
    "single": {"double": False, "keys": "default", "budget": None},
    "digits": {"double": True, "keys": "digits", "budget": None},
    "swapped": {"double": False, "keys": "swapped", "budget": None},
    "tight": {"double": True, "keys": "default", "budget": {"maxRows": 6, "fieldMaxWidth": 12}},
}
CTRL = lambda c: chr(ord(c) - 96)
decision_keys = ["y", "s", "b", "n", "r", "j", "k", "Y", "N", "R", "1", "4", " ", "\t", ENTER, "\n", ESC, UP, DOWN, "\x1bOA", "\x1bOB", "\x1b[1;5B", "\x1b[1;2A",
                 "\x1b[121u", "\x1b[121;2u", "\x1b[121;5u", "\x1b[121;3u", "\x1b[121;1:3u", "\x1b[110u", "\x1b[13u", "\x1b[27u", "\x1by", "\x1bn", CTRL("y"), CTRL("n"), CTRL("c"), CTRL("o"), CTRL("g"),
                 pasted("y"), pasted("n"), pasted("some copied text"), pasted("r"), "\x7f", "\x00", "\x1b[3~", "\x1b[200~y", "y\x1b[201~", "yy", "ny", "yn", "\u00e9", "\U0001F600"]
reason_keys = ["a", "b", "c", " ", "x", "\u00e9", "\u65e5", "\U0001F600", "e\u0301", "\x7f", "\x08", "\x1b[3~", "\x1b[D", "\x1b[C", "\x1b[H", "\x1b[F", "\x1b[1;5D", "\x1b[1;5C",
               CTRL("a"), CTRL("e"), CTRL("w"), CTRL("u"), CTRL("k"), CTRL("y"), CTRL("b"), CTRL("f"), CTRL("d"), "\x1b\x7f", "\x1bb", "\x1bf", "\x1bd", "\x1by", "\x1b[45;5u",
               "\x1f", CTRL("z"), CTRL("o"), CTRL("c"), ENTER, "\n", ESC, "\t", "\x1b[97u", "\x1b[13u", "\x1b[27u",
               pasted("pasted text"), pasted("multi\nline\r\npaste"), pasted("q" * 120), pasted("tab\there"), pasted(""), pasted("e\u0301\U0001F600"), S + "partial", "rest" + E, "j", "k", "y", "n", "r"]

def seqs_for(options):
    seqs = []
    for k in decision_keys:
        seqs.append([k])
        seqs.append([k, k])
    for a, b in itertools.product(["y", "s", "b", "n", "r", DOWN, UP, "j", "k", ENTER, ESC, "1", "4"], repeat=2):
        seqs.append([a, b])
    for _ in range(60):
        seqs.append([rnd.choice(decision_keys) for _ in range(rnd.randint(2, 8))])
    # the reason step
    for _ in range(80):
        n = rnd.randint(1, 14)
        seqs.append(["r", "r"] + [rnd.choice(reason_keys) for _ in range(n)] + rnd.choice([[], [ENTER], [ESC], [ENTER, "n"], [ESC, "n"], [ENTER, ENTER]]))
        seqs.append(["r"] + [rnd.choice(reason_keys) for _ in range(n)] + rnd.choice([[], [ENTER], [ESC, "y"]]))
    # the scope step and expansion
    for _ in range(25):
        seqs.append([rnd.choice(["s", "b", "s", "s"]), rnd.choice(["s", "b"]), rnd.choice([DOWN, UP, "j", "k", CTRL("o"), ESC, "x", ENTER])] + [rnd.choice([DOWN, UP, ENTER, ESC, CTRL("o"), "y"]) for _ in range(rnd.randint(0, 4))])
    return seqs

component = []
widths = [30, 80]
for pname, p in payloads_c.items():
    for oname, o in option_sets.items():
        for prefname, pref in pref_sets.items():
            if rnd.random() > 0.3 and not (pname == "short" and prefname in ("double", "single")):
                continue
            pool = seqs_for(o)
            rnd.shuffle(pool)
            for seq in pool[:8]:
                component.append({"payload": pname, "payloadJson": p, "options": o, "pref": pref, "expandKey": rnd.choice([CTRL("o"), CTRL("o"), "e"]), "keys": seq, "widths": widths,
                                  "notifications": rnd.choice([[], [], ["bell"], ["osc9", "osc777"]]), "notice": {"title": "pi \u2014 s;n", "body": "Permission Required: read"}})
cases["component"] = component

# the fallback flow through requestPermissionDecision, with the original's rendered message
fallback = []
for pname, p in payloads_c.items():
    for opts in (None, {"sessionLabel": "L"}, {"sessionWidth": {"label": WIDTH}, "sessionScope": SCOPE}):
        for budget in (None, {"maxRows": 6, "fieldMaxWidth": 12}):
            for first, second in [("Yes", None), ("No", None), ("No, provide reason", None), ("Yes, for this session", None), (None, None), ("L", None),
                                  (WIDTH, SCOPE["servingSessionLabel"]), ("Yes, for this session", SCOPE["subagentLabel"])]:
                fallback.append({"payloadJson": p, "options": opts, "budget": budget, "selects": [first, second], "input": "why"})
cases["fallback"] = fallback

json.dump(cases, open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "extensions", "pi-permission-system", "testdata", "authority-cases.json"), "w"), separators=(",", ":"), ensure_ascii=False)
print({k: len(v) for k, v in cases.items()})
