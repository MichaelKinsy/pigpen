#!/usr/bin/env python3
"""Generates presentation-cases.json: the inputs the presentation probe (presentation-probe.test.ts) runs through the original's
presentation, tool-input and exposure modules (src/presentation, src/tool-input, src/exposure). Seeded: the same file every run.

Value specs for tool inputs use the markers of gen-logging-cases.py ({"$undefined": true}, {"$bigint": "1"}, {"$fn": true}, ...).
Strings never carry a lone surrogate (a JSON file would lose it); the field caps create them at an astral boundary on purpose."""
import json, os, random

rnd = random.Random(20261021)
here = os.path.dirname(os.path.abspath(__file__))

# ---- the strings every renderer must keep honest: width, wrapping, ANSI, control characters, escaping, length
UNI = [
    "", " ", "a", "plain text", "two  spaces", " leading and trailing ", "tab\there", "cr\rlf", "crlf\r\nline", "nl\nnl\n\nnl",
    "e\u0301 combining", "\u4e2d\u6587\u6f22\u5b57\u304b\u306a\u30ab\u30ca", "\uff21\uff22\uff23 fullwidth", "\U0001f600 emoji", "\U0001f468\u200d\U0001f469\u200d\U0001f467\u200d\U0001f466 family",
    "\U0001f1e9\U0001f1ea flag", "\u2764\ufe0f heart", "1\ufe0f\u20e3 keycap", "\U0001f44d\U0001f3fd thumbs", "\u05e9\u05dc\u05d5\u05dd rtl", "\u0e2a\u0e27\u0e31\u0e2a\u0e14\u0e35 thai",
    "\u0928\u092e\u0938\u094d\u0924\u0947 devanagari", "\x1b[31mred\x1b[0m ansi", "\x1b[1;4;38;5;196mdeep\x1b[0m", "\x1b]8;;http://x.test\x07link\x1b]8;;\x07 osc8",
    "\x1b[2J clear", "nbsp\u00a0here", "zero\u200bwidth", "bom\ufeffhere", "line\u2028sep", "bell\x07", "nul\x00char", "del\x7f", "\ud7ff\ue000\uffff",
    "-a-b-c-", "hyphen-ated-long-word-that-must-wrap-somewhere-sensible", "x" * 30, "y" * 61, "z" * 250, "w" * 450, "a b " * 120,
    "\U0001f600" * 25, "\u4e2d" * 40, "mix \u4e2d\U0001f600e\u0301\x1b[31m!\x1b[0m end " * 6, "it's \"quoted\" `back` $dollar \\slash",
    "<script>&amp;</script>", "path/with spaces/and\u00e9.txt", "/etc/hosts", "~/.ssh/config", "C:\\Users\\x\\file.txt", "../../../etc/passwd",
]

KINDS = ["bash", "mcp", "tool", "path", "external_directory", "bash_external_directory", "skill", "skill_read", "forwarded"]
CONTEXTS = [None, "command_substitution", "process_substitution", "subshell"]
LABELS = ["working directory", "external path", "resolves to", "read path", "full command", "input", "requested", "other label", "x", "\u4e2d\u6587", "very long label name here"]
SURFACES = ["bash", "read", "write", "edit", "path", "path_read", "path_write", "external_directory", "external_directory_read", "external_directory_write",
            "mcp", "skill", "fetch", "mcp__srv__tool", "", "\u4e2d", "my_tool"]
TOOLS = [None, "bash", "read", "write", "edit", "mcp", "fetch", "mcp__srv__tool", "skill", "", "task", "\u4e2d\u6587tool"]
PATTERNS = [None, "*", "rm *", "git push*", "<indirection-bash-wrapper>", "<opaque-bash-wrapper>", "<unparseable-bash-command>", "/etc/*", "", "a b c", "\u4e2d*"]
AGENTS = [None, "", "scout", "my-agent", "\u4e2d\u6587", "a" * 40]


def pick(xs):
    return xs[rnd.randrange(len(xs))]


def payload(kind=None):
    kind = kind or pick(KINDS)
    value = pick(UNI) if rnd.random() < 0.8 else pick(["", "ls", "git status", "/etc/hosts", "github:create_issue", "deploy"])
    tool = pick(TOOLS)
    if rnd.random() < 0.3:
        tool = {"bash": "bash", "mcp": "mcp", "skill": None, "forwarded": None}.get(kind, tool)
    forwarded = rnd.random() < 0.3
    evidence = []
    for _ in range(rnd.choice([0, 0, 1, 2, 3, 5, 9])):
        evidence.append({"label": pick(LABELS), "text": pick(UNI), "detail": pick(UNI) if rnd.random() < 0.3 else None})
    return {
        "kind": kind,
        "request": {
            "requester": {"agentName": pick(AGENTS), "forwarded": forwarded, "sessionId": pick([None, "", "abc123", "child-7", "\u4e2d"]) if forwarded else None},
            "surface": pick(SURFACES),
            "toolName": tool,
            "invokedToolName": pick([None, None, None, "exec_command", "bash", ""]),
            "value": value,
            "matchedPattern": pick(PATTERNS),
            "commandContext": pick(CONTEXTS),
            "executedUnit": pick([None, None, None, "grep foo", "aws s3 rm s3://bucket", pick(UNI)]),
        },
        "evidence": evidence,
        "annotations": [],
    }


def inf(sign=1):
    return {"$inf": sign}


# ---- fit: FitLinesToWidth
fit = []
for width in [-1, 0, 1, 2, 3, 4, 5, 8, 10, 17, 20, 40, 80, 200]:
    for i in range(0, len(UNI), 5):
        fit.append({"lines": [UNI[i], UNI[(i + 1) % len(UNI)]], "width": width})
    fit.append({"lines": [], "width": width})
    fit.append({"lines": UNI[:8], "width": width})

# ---- dialog: renderPromptDialog
WIDTHS = [-1, 0, 1, 2, 3, 5, 10, 20, 40, 80, 120, 200]
ROWS = [-1, 0, 1, 2, 3, 5, 8, 24, 100, inf()]
FIELDS = [-1, 0, 1, 3, 5, 12, 50, 400, 2.5, inf()]
PAINTS = ["none", "brackets", "ansi"]
dialog = []
for _ in range(1100):
    dialog.append({"payload": payload(), "budget": {"maxRows": pick(ROWS), "fieldMaxWidth": pick(FIELDS), "width": pick(WIDTHS)}, "paint": pick(PAINTS)})
# the complete view at several widths, and the default budget
for k in KINDS:
    for width in [20, 80, 200]:
        for _ in range(3):
            p = payload(k)
            dialog.append({"payload": p, "budget": {"maxRows": inf(), "fieldMaxWidth": inf(), "width": width}, "paint": pick(PAINTS)})
            dialog.append({"payload": p, "budget": {"maxRows": 24, "fieldMaxWidth": 400, "width": width}, "paint": "none"})
# a flagged element inside longer words and ANSI
for needle, line in [("ls", "lsof | ls && /usr/bin/lsblk"), ("ls", "ls ls ls"), ("a.b", "a.b a.bc xa.b a.b/ a.b-"), ("/etc/hosts", "cat /etc/hosts /etc/hostsbackup /etc/hosts"),
                     ("x", "x\x1b[31mx\x1b[0mx x_x x.x"), ("\u4e2d", "\u4e2d\u4e2d \u4e2d x\u4e2d"), ("ls", "ls\nls\n ls ")]:
    for paint in PAINTS:
        for kind in ["bash", "bash_external_directory", "tool"]:
            p = payload(kind)
            p["request"]["value"] = needle
            p["request"]["toolName"] = "bash"
            p["evidence"] = [{"label": "full command", "text": line, "detail": None}, {"label": "external path", "text": needle, "detail": needle + "x"}]
            dialog.append({"payload": p, "budget": {"maxRows": inf(), "fieldMaxWidth": inf(), "width": 120}, "paint": paint})
# a field cap that splits an astral character
for cut in range(1, 8):
    p = payload("bash")
    p["request"]["value"] = "ab\U0001f600\U0001f600cd\U0001f468\u200d\U0001f469"
    p["evidence"] = [{"label": "input", "text": "\U0001f600" * 6, "detail": "\U0001f600x"}]
    dialog.append({"payload": p, "budget": {"maxRows": inf(), "fieldMaxWidth": cut, "width": 80}, "paint": "none"})

# ---- notice: describePromptNotice + renderPromptNotification
CHANNELS = [[], ["bell"], ["osc9"], ["osc777"], ["bell", "osc9", "osc777"], ["osc777", "bell"], ["osc9", "osc9"]]
TITLES = ["Permission Required", "Permission Required (Subagent)", "", "a;b", "bell\x07 esc\x1b]0;x\x07", "\u4e2d\u6587 \U0001f600", "x\ny\rz\t", "\x7fdel\x00nul", "semi;colon;"]
CWDS = ["/w/repo", "/w/pi-packages", "/", "", "/w/repo/", "//", "/a/b/c//", "rel/dir", "name", "\u4e2d/\u6587", "/w/with space", "/w/semi;colon", "C:\\w\\x", ".", ".."]
NAMES = [None, "", "refactor-auth", "a;b", "x\x07y", "\u4e2d\u6587", "n" * 50]
notice = []
for _ in range(450):
    notice.append({
        "title": pick(TITLES), "channels": pick(CHANNELS),
        "request": {"toolName": pick(TOOLS), "surface": pick(SURFACES + [";", "a;b"]),
                    "requester": {"agentName": pick(AGENTS + ["a;b", "x\x07"]), "forwarded": rnd.random() < 0.3, "sessionId": None}},
        "session": {"name": pick(NAMES), "cwd": pick(CWDS)},
    })

# ---- agent: the agent-facing denials
FIELD_BUDGETS = [-1, 0, 1, 5, 20, 400, 2.5, inf()]
DECISIONS = [
    {"kind": "user", "via": "dialog"},
    {"kind": "authorizer", "name": "model-judge", "verdict": "deny", "reason": "r"},
    {"kind": "authorizer", "name": "a b'c", "verdict": "deny", "reason": None},
    {"kind": "unavailable", "reason": "no serving session"},
    {"kind": "rule", "surface": "bash", "pattern": "git push", "origin": "global"},
    {"kind": "rule", "surface": "bash", "pattern": None, "origin": None},
    {"kind": "gate_error", "reason": "boom"},
    {"kind": "gate_error", "reason": ""},
    {"kind": "session_approval", "surface": "bash", "pattern": "x"},
    {"kind": "yolo", "pattern": None},
    {"kind": "infrastructure_read"},
    {"kind": "forwarded", "responderSessionId": "parent-1", "decision": None},
    {"kind": "forwarded", "responderSessionId": "parent-1", "decision": {"kind": "authorizer", "name": "j", "verdict": "deny", "reason": None}},
    {"kind": "forwarded", "responderSessionId": "parent-1", "decision": {"kind": "rule", "surface": "bash", "pattern": "p *", "origin": "project"}},
    {"kind": "forwarded", "responderSessionId": "parent-1", "decision": {"kind": "gate_error", "reason": "forwarded boom"}},
    {"kind": "forwarded", "responderSessionId": "p", "decision": {"kind": "forwarded", "responderSessionId": "q", "decision": {"kind": "unavailable", "reason": "deep"}}},
]
REASONS = [None, "", "because", "not with sudo", "reason; with 'quotes' and \u4e2d\U0001f600", "x" * 300]
agent = []
for _ in range(800):
    agent.append({"fn": pick(["policy", "user", "authorizer", "escalated", "gateError", "unavailable", "refusal"]), "payload": payload(),
                  "budget": pick(FIELD_BUDGETS), "reason": pick(REASONS), "decision": pick(DECISIONS), "linkName": pick(["model-judge", "a'b", "\u4e2d", ""]),
                  "pattern": pick(PATTERNS), "decidedElsewhere": rnd.random() < 0.5})

# ---- suggest: pattern-suggest
bash_cmds = ["", " ", "ls", "  git log  ", "git status --short", "npm run build", "npm run build --silent", "mytool --verbose run", "rm -rf node_modules", "docker compose up --build",
             "# Check debug logs\nfind /home -path '*debug*' -type f", "# Step 1\n# Step 2\ngit status --short", "# just a comment", "# description\nrm -rf ./build; echo done",
             "git", "git checkout main", "kubectl get pods -n x", "aws s3 rm s3://b", "gh pr list", "sudo rm x", "env A=1 ls", "a\tb\tc", "a\u00a0b c", "a\u2003b\u3000c",
             "echo 'a b' c", "cat <<EOF\nx\nEOF", "\n\nls -la\n\n", "# c\n\n# d\nls", "  # indented comment\nls a", "x\ry", "ls\u2028-la", "\ufeffgit status",
             "npm", "npm run", "npm install foo", "bun run dev", "pnpm exec x", "yarn add y", "cargo build --release", "go test ./...", "python -m pytest", "terraform apply -auto-approve"]
bash_cmds += [pick(UNI) for _ in range(40)]
mcp_targets = ["", " ", "exa:search", "exa_search", "mcp", "search", "my-server:some_tool", ":x", "_x", "a:", "a_", ":", "_", " a:b ", "\u4e2d:\u6587", "a:b:c", "a_b:c", "a_b_c",
               "mcp__srv__tool", "x\ny:z"]
SURF = ["bash", "mcp", "skill", "read", "write", "edit", "find", "grep", "ls", "path", "external_directory", "my_ext_tool", "", "path_read", "external_directory_write", "webfetch"]
GRANT_SURF = ["external_directory", "external_directory_read", "external_directory_write", "path", "path_read", "path_write", "bash", "mcp", "skill", "read", "edit", "tool", ""]
GRANT_PAT = ["/tmp/*", "/tmp", "/tmp/a", "/outside/a/*", "/outside/a", "/outside/b/*", "c:\\p\\*", "c:\\p", "*", "git *", "", "/a\\*", "/a", "\u4e2d/*", "\u4e2d"]
grants = []
for _ in range(250):
    grants.append([{"surface": pick(GRANT_SURF), "pattern": pick(GRANT_PAT)} for _ in range(rnd.choice([0, 1, 1, 2, 2, 3, 4]))])
suggest = {
    "bash": [{"command": c} for c in bash_cmds],
    "mcp": [{"target": t} for t in mcp_targets],
    "session": [{"surface": s, "value": v} for s in SURF for v in (bash_cmds[:12] + mcp_targets[:6] + ["*", "librarian", "/x"])],
    "path": [{"surface": pick(SURF), "patterns": [pick(GRANT_PAT) for _ in range(rnd.choice([0, 1, 2, 3]))]} for _ in range(150)],
    "exact": [{"surface": s, "pattern": p} for s in SURF for p in ["*", "mcp__a__b", "x y"]],
    "grantTarget": [g for g in grants if g],
    "directional": [{"direction": d, "target": t} for d in ["read", "write"] for t in ['"/tmp/*"', "3 paths", "", "\u4e2d"]],
    "forwarded": [{"agentName": pick(AGENTS), "grants": g} for g in grants],
    "pathAccess": [{"grants": g} for g in grants],
}

# ---- toolinput: ToolPreviewFormatter over inputs
def jsonish(depth=0):
    k = rnd.random()
    if depth > 2 or k < 0.35:
        return pick([pick(UNI), pick(UNI), 0, 1, -1, 1.5, 1e21, 1e-7, 123456789012345680000, 10, 50, None, True, False, {"$undefined": True}, {"$bigint": "123"}, {"$fn": True}, {"$negzero": True}])
    if k < 0.6:
        return [jsonish(depth + 1) for _ in range(rnd.randint(0, 4))]
    o = {}
    for _ in range(rnd.randint(0, 5)):
        o[pick(["path", "file_path", "pattern", "glob", "command", "content", "oldText", "newText", "edits", "offset", "limit", "arguments", "tool", "query", "k", "authorization",
                "token", "0", "2", "a", "\u4e2d", "apiKey", "name", "url"])] = jsonish(depth + 1)
    return o


def edits_spec():
    return [{"oldText": pick(UNI), "newText": pick(UNI)} for _ in range(rnd.randint(0, 4))]


inputs = []
for tool in ["edit", "write", "read", "find", "grep", "ls", "mcp", "bash", "task", "http", "skill", "other", "", "constructor", "mcp__srv__tool"]:
    for _ in range(35):
        spec = jsonish()
        if rnd.random() < 0.6:
            spec = {}
            if rnd.random() < 0.7: spec["path"] = pick(UNI)
            if rnd.random() < 0.2: spec["file_path"] = pick(UNI)
            if rnd.random() < 0.4: spec["pattern"] = pick(UNI)
            if rnd.random() < 0.3: spec["glob"] = pick(UNI)
            if rnd.random() < 0.4: spec["content"] = pick(UNI)
            if rnd.random() < 0.5: spec["edits"] = edits_spec() if rnd.random() < 0.8 else pick(["x", 1, None, [1, "a", None, {"oldText": 1, "newText": "b"}]])
            if rnd.random() < 0.2: spec["oldText"], spec["newText"] = pick(UNI), pick(UNI)
            if rnd.random() < 0.3: spec["offset"] = pick([0, 5, 1.5, -1, "5", None, 1e21])
            if rnd.random() < 0.3: spec["limit"] = pick([0, 50, 2.5, "50", None])
            if rnd.random() < 0.5: spec["command"] = pick(UNI)
            if rnd.random() < 0.4: spec["arguments"] = jsonish(1)
            if rnd.random() < 0.3: spec["authorization"] = "Bearer " + pick(UNI)
            if rnd.random() < 0.2: spec["token"] = pick(UNI)
        inputs.append({"tool": tool, "input": spec, "limits": pick([None, None, {"toolInputPreviewMaxLength": 10, "toolTextSummaryMaxLength": 5}, {"toolInputPreviewMaxLength": 200, "toolTextSummaryMaxLength": 80},
                                                                      {"toolInputPreviewMaxLength": 0, "toolTextSummaryMaxLength": 0}, {"toolInputPreviewMaxLength": 3000, "toolTextSummaryMaxLength": 3000}])})
for tool in ["read", "bash", "mcp", "http"]:
    for v in [None, "str", 5, True, [], [1, 2], {"$undefined": True}, {}]:
        inputs.append({"tool": tool, "input": v, "limits": None})
# the very long and deeply nested
inputs.append({"tool": "x", "input": {"a": "z" * 5000}, "limits": None})
inputs.append({"tool": "write", "input": {"path": "/p", "content": "line\n" * 3000}, "limits": None})
inputs.append({"tool": "edit", "input": {"path": "/p", "edits": [{"oldText": "o\n" * 50, "newText": "n\r\n" * 70} for _ in range(30)]}, "limits": None})
nest = {"v": 1}
for _ in range(40):
    nest = {"n": nest, "t": "\u4e2d"}
inputs.append({"tool": "x", "input": nest, "limits": None})
mcpinput = [{"input": jsonish()} for _ in range(150)] + [{"input": {"arguments": a}} for a in [
    {"query": "x" * 100}, {"a": "x" * 80, "b": "y" * 80, "c": "z" * 80}, {"n": 1, "b": True, "z": None, "u": {"$undefined": True}, "arr": [1, 2, 3], "o": {"k": 1}, "e": []},
    {"2": "b", "1": "a", "x": "c"}, {"s": "\U0001f600" * 70}, {"k\u4e2d": "v"}, [1, 2], "str", 5, None, {}, {"f": {"$fn": True}}, {"big": {"$bigint": "5"}}, {"nan": {"$nan": True}}, {"neg": {"$negzero": True}}]]
sanitize = [{"value": v, "max": m} for v in UNI for m in [None, 0, 1, 5, 80, 2.5, -1]]
preview = {
    "truncate": [{"value": v, "max": m} for v in UNI[:40] for m in [0, 1, 3, 10, 80, 200, 2.5, -1, 1e21]],
    "count": [{"value": v} for v in UNI + ["a\r\n\r\nb", "\r\r\n\n", "x\n", "\n", "\r", "a\u2028b"]],
    "format": [{"n": n} for n in [0, 1, 2, 10, 1000000]],
    "serialize": [{"input": jsonish()} for _ in range(120)],
}

# ---- log: getToolInputPreviewForLog / getPermissionLogContext
PATH_BEARING = ["read", "write", "edit", "find", "grep", "ls"]
log = []
for _ in range(350):
    tool = pick(["bash", "mcp", "read", "write", "edit", "find", "grep", "ls", "task", "http", "some-server:some-tool", "mcp__srv__tool", "skill", "constructor"])
    spec = jsonish()
    if rnd.random() < 0.5:
        spec = {"path": pick(UNI), "command": pick(UNI), "authorization": "Bearer x", "arguments": jsonish(1)}
    log.append({"result": {"toolName": tool, "state": pick(["allow", "ask", "deny"]), "source": pick(["tool", "mcp", "default", "bash", "skill", "special", "session"]),
                           "origin": pick(["builtin", "project", "global", "agent"]), "command": pick([None, "ls -la", pick(UNI)]), "target": pick([None, "srv:tool", pick(UNI)])},
                "input": spec, "pathBearing": pick([PATH_BEARING, ["read", "write", "edit"], []])})

# ---- askpayload: the payload builders (through the dialog renderer at one budget too)
def check(tool, **kw):
    r = {"toolName": tool, "state": "ask", "source": pick(["tool", "mcp", "default"]), "origin": "builtin"}
    r.update(kw)
    return r


askpayload = []
for _ in range(250):
    tool = pick(["bash", "read", "mcp", "task", "mcp__srv__tool", "write", "skill", "fetch"])
    askpayload.append({"fn": "tool", "check": check(tool, command=pick([None, "rm -rf .", pick(UNI)]), target=pick([None, None, "srv:q", pick(UNI)]), matchedPattern=pick(PATTERNS),
                                                       commandContext=pick(CONTEXTS), executedUnit=pick([None, "grep foo"])),
                       "agentName": pick(AGENTS), "surface": pick(SURFACES), "invokedToolName": pick([None, "exec_command", tool, ""]), "input": jsonish() if rnd.random() < 0.5 else {"command": pick(UNI), "path": pick(UNI), "arguments": jsonish(1)},
                       "formatter": pick([None, "builtin", "decline", "fixed"])})
for _ in range(200):
    askpayload.append({"fn": pick(["path", "extdir"]), "facts": {"toolName": pick(["read", "write", "edit"]), "pathValue": pick(UNI), "resolvedPath": pick([None, "/x/y", pick(UNI)]),
                                                                 "agentName": pick(AGENTS), "matchedPattern": pick(PATTERNS), "surface": pick(SURFACES), "cwd": pick(UNI)}})
for _ in range(150):
    askpayload.append({"fn": "bashext", "facts": {"command": pick(UNI), "externalPaths": [{"path": pick(UNI), "resolvedPath": pick([None, "/r/p", pick(UNI)])} for _ in range(rnd.randint(0, 5))],
                                                   "cwd": pick(UNI), "agentName": pick(AGENTS), "toolName": pick(["bash", "exec_command"]), "matchedPattern": pick(PATTERNS), "surface": pick(SURFACES)}})
for _ in range(80):
    askpayload.append({"fn": pick(["skill", "skillpath"]), "name": pick(UNI), "readPath": pick(UNI), "agentName": pick(AGENTS)})
for _ in range(150):
    p = payload()
    askpayload.append({"fn": "forwarded", "request": {"requesterAgentName": pick(["scout", "", "\u4e2d"]), "requesterSessionId": pick(["abc", "", "s-1"]),
                                                       "payload": p if rnd.random() < 0.6 else None, "surface": pick(["$none", None, "bash", "read", "\u4e2d"]), "value": pick(["$none", None, "x", pick(UNI)])}})

# ---- registry: tool-registry
tool_values = ["read", "  bash  ", "", "   ", None, 42, True, {"name": "edit"}, {"toolName": "write", "name": "x"}, {"tool": "grep"}, {"toolName": "", "name": " find "}, {"unknown": "x"}, {},
               [], ["a"], {"name": 5, "tool": "ls"}, {"name": {"x": 1}}, {"name": "x", "promptGuidelines": ["a", " b ", "", "  ", 1, None, "c"]}, {"name": "y", "promptGuidelines": "no"},
               {"name": "z", "promptGuidelines": []}, {"name": "constructor"}, {"name": "toString"}, {"name": "__proto__"}, {"name": "Read"}, {"name": "read"}, {"name": "\u4e2d\u6587"},
               {"name": "a-b"}, {"name": "a_b"}, {"name": "A"}, {"name": "a"}, {"name": "B"}, {"name": "b"}, {"name": "1"}, {"name": "10"}, {"name": "2"}, {"name": "mcp"}, {"name": "mcp__srv__tool"},
               {"name": "\u00e9"}, {"name": "e"}, {"name": "f"}, {"name": "z"}, {"name": "Z"}, {"name": "_x"}, {"name": "-x"}, {"name": "x:y"}, {"name": "x y"}]
ALIASES = [None, {}, {"Execute": "bash"}, {"legacy_read": "read"}, {"a": "b", "c": "b", "d": "e"}, {"constructor": "x"}, {"bash": "bash"}, {"x": ""}, {"Read": "read"}, {"a-b": "a_b"}]
registry = {
    "name": [{"value": v} for v in tool_values],
    "guidelines": [{"value": v} for v in tool_values],
    "read": [{"tools": [pick(tool_values) for _ in range(rnd.randint(0, 8))]} for _ in range(150)],
    "check": [],
    "reasons": [],
}
reg_requested = [None, "", "   ", "read", "bash", "ghost", "Execute", "legacy_read", " read ", "mcp", "mcp__srv__tool", "constructor", "toString", "__proto__", "valueOf", "a", "c", "e", "x", "\u4e2d\u6587", "Read"]
for _ in range(700):
    registry["check"].append({"requested": pick(reg_requested), "tools": [pick(tool_values) for _ in range(rnd.randint(0, 9))], "aliases": pick(ALIASES)})
name_pool = ["read", "write", "edit", "bash", "mcp", "tool0", "tool10", "x", "my-server:tool", "mcp__a__b", "constructor", "\u4e2d", "", "a b"]
for _ in range(120):
    n = rnd.choice([0, 1, 3, 10, 11, 15])
    registry["reasons"].append({"toolName": pick(name_pool + ["mcp", "mcp__srv__gone", "my-server:tool", "'quoted'"]), "available": [("tool%d" % i if rnd.random() < 0.7 else pick(name_pool)) for i in range(n)]})

# ---- surface: renderToolSurfaceSections
SNIPPET_POOL = {"read": "Read file contents", "bash": "Execute bash commands (ls, grep, find, etc.)", "edit": "Make precise file edits", "grep": "Search file contents", "find": "Find files by name",
                "ls": "List directory contents", "powershell": "Execute PowerShell commands", "write": "Create or overwrite files", "x": "", "y": "  ", "constructor": "not own"}
TOOLSET = ["read", "bash", "edit", "write", "grep", "find", "ls", "powershell", "x", "y", "colgrep", "ask_parent"]
GL = ["Read before editing", "  Use bash carefully", "Use bash carefully ", "", "  ", "Do not use emojis", "Be concise in your responses", "Show file paths clearly when working with files", "\u4e2d guideline",
      "Use read to examine files instead of cat or sed."]
surface = []
for _ in range(500):
    allowed = [pick(TOOLSET) for _ in range(rnd.randint(0, 6))]
    surface.append({"allowedTools": allowed,
                    "toolSnippets": {k: v for k, v in SNIPPET_POOL.items() if k != "constructor" and rnd.random() < 0.8},
                    "guidelinesByTool": {t: [pick(GL) for _ in range(rnd.randint(0, 3))] for t in TOOLSET if rnd.random() < 0.4},
                    "promptGuidelines": [pick(GL) for _ in range(rnd.randint(0, 4))]})

# ---- skills: sanitizer
SKILL_NAMES = ["librarian", "ask-user", "alpha", "beta", "a&amp;b", "a&lt;b&gt;", "&amp;lt;", "x\u4e2d", "", "  padded  ", "dup", "dup"]
LOCS = ["/skills/{n}/SKILL.md", "@./skills/{n}/entry.ts", "~/skills/{n}/SKILL.md", "/skills/parent/child/SKILL.md", "/skills/parent/SKILL.md", "  /skills/{n}/x  ", "'/skills/{n}/q.md'", "rel/{n}/f", "", "/a&amp;b/f"]


def skill_block(n, loc, desc="d", missing=None):
    parts = ["  <skill>"]
    if missing != "name": parts.append("    <name>%s</name>" % n)
    if missing != "description": parts.append("    <description>%s</description>" % desc)
    if missing != "location": parts.append("    <location>%s</location>" % loc.replace("{n}", n.strip() or "x"))
    parts.append("  </skill>")
    return "\n".join(parts)


def prompt_spec():
    sections = []
    for _ in range(rnd.choice([0, 1, 1, 2, 3])):
        blocks = [skill_block(pick(SKILL_NAMES), pick(LOCS), pick(["d", "", "a &amp; b", "x\ny"]), pick([None, None, None, None, "name", "description", "location"])) for _ in range(rnd.randint(0, 4))]
        sections.append("<available_skills>\n" + "\n".join(blocks) + "\n</available_skills>")
    glue = [pick(["", "Some preamble", "text <available_skills> unclosed", "</available_skills> stray", "<skill>stray</skill>"]) for _ in range(len(sections) + 1)]
    out = glue[0]
    for i, s in enumerate(sections):
        out += "\n" + s + "\n" + glue[i + 1]
    if rnd.random() < 0.1:
        out += "\n<available_skills>\n" + skill_block("tail", "/t/f") + "\n"
    return out


skills = {"parse": [], "visible": [], "match": [], "without": []}
for _ in range(300):
    skills["parse"].append({"prompt": prompt_spec()})
for _ in range(300):
    states = {n: pick(["allow", "ask", "deny"]) for n in SKILL_NAMES}
    skills["visible"].append({"prompt": prompt_spec(), "states": states, "defaultState": pick(["allow", "ask", "deny"]), "agentName": pick(AGENTS), "cwd": pick(["/projects/my-app", "/cwd", "/"]),
                              "probes": ["/skills/librarian/SKILL.md", "/skills/alpha/x.md", "/skills/parent/child/h.md", "/cwd/skills/visible/file.ts", "/projects/my-app/rel/alpha/f", "/skills/librarian-extra/x", "", "/a&b/f", "/skills/parent/zz"]})
for _ in range(120):
    names = [pick(SKILL_NAMES) for _ in range(rnd.randint(0, 6))]
    skills["without"].append({"names": names, "states": {n: pick(["allow", "ask", "deny"]) for n in SKILL_NAMES}, "defaultState": "allow", "agentName": pick(AGENTS)})

cases = {"fit": fit, "dialog": dialog, "notice": notice, "agent": agent, "suggest": suggest, "inputs": inputs, "mcpinput": mcpinput, "sanitize": sanitize, "preview": preview,
         "log": log, "askpayload": askpayload, "registry": registry, "surface": surface, "skills": skills}
with open(os.path.join(here, "presentation-cases.json"), "w") as f:
    json.dump(cases, f, indent=None, separators=(",", ":"), ensure_ascii=True)
    f.write("\n")
print({k: (len(v) if isinstance(v, list) else {kk: len(vv) for kk, vv in v.items()}) for k, v in cases.items()})
