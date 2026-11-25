#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Michael Kinsy
"""Deterministic case generator for the approval differential. Writes cases.jsonl (no verdicts).
One case per line:
  id        int, stable
  section   "base" | "paths" | "unwrap" | "restricted" | "cached" | "interceptor" | "project" | "glob-path" | "nil-setting" | "malformed"
  tool      tool name
  args      the arguments as raw JSON TEXT
  settings  {"operator": {<setting id>: value}, "project": {<setting id>: value} | null, "trusted": bool,
             "operatorRaw": "<config.yml text>" (only section malformed)}
  ui        "interactive" | "headless"
  session   "top" | "subagent"
  state     optional: {"nilSetting": true} | {"prior": {"tool", "args" (raw JSON text), "answer": "target"|"folder"|"session"|"once"}}
"""
import json, sys

def edit(*paths):
    return {"input": "".join(f"[{p}#1A2B]\n+ 1A\n~ x\n" for p in paths)}

ARGS = {
 "read": [{"path": p} for p in ["a.txt","dir/","local://x.md","out://reply","cfg://tools.approvalMode","proc://bg_1","conflict://1","history://","help://","skill://foo","ssh://host/etc/passwd","SSH://host/x","https://example.com","http://example.com","doc.pdf:image1","doc.pdf:1-20","doc.pdf:raw","doc.pdf","a.txt:10-20","agent://x","artifact://1","mcp://x","rule://r","issue://1","pr://1","notes ssh://h/x","a.txt?q=what","doc.pdf:image1?q=what"]] + [{}],
 "write": [{"path": p, "content": "x"} for p in ["a.txt","/abs/a.txt","local://x.md","LOCAL://x.md","local:/x.md","out://reply","cfg://tools.approvalMode","cfg://tools.approvalMode/save","proc://bg_1/kill","proc://bg_1","conflict://1","history://x","help://x","skill://foo","rule://r","mcp://x","attachment://1","agent://x","issue://1","pr://1","ssh://host/f","https://example.com/x","[a.txt#1A2B]","[local://x.md#1A2B]","[skill://foo#1A2B]","[proc://bg_1/kill#1A2B]","artifact://1","vault://x","memory://x","omp://x"]] + [{"content":"x"}],
 "edit": [edit("a.txt"), edit("local://x.md"), edit("skill://foo"), edit("proc://bg_1"), edit("a.txt","skill://foo"), edit("a.txt","local://x.md"), edit("cfg://x"), edit("ssh://h/f"), {"input": ""}, {}, {"input": "[a.txt#1A2B]\nMV skill://foo\n"}, {"input": "[a.txt#1A2B]\nMV b.txt\n"}, {"input": "garbage"}],
 "ast_edit": [{"ops":[{"pat":"a","out":"b"}],"paths":p} for p in [["a.ts"],["local://x.ts"],["skill://foo"],["a.ts","skill://foo"],["proc://x"],["out://x"],[],["ssh://h/x"]]] + [{"ops":[{"pat":"a","out":"b"}]}, {"ops":[{"pat":"a","out":"b"}],"paths":[1]}],
 "glob": [{"pattern":"*.go"},{"pattern":"*.go","path":"src"},{"pattern":"*.go","path":"ssh://h/x"},{"pattern":"ssh://h/x/*.go"},{"pattern":"*.go","path":"local://"},{"pattern":"a;ssh://h/b"},{"pattern":"*.go","path":"src;ssh://h/x"}],
 "grep": [{"pattern":"x"},{"pattern":"x","path":"src"},{"pattern":"x","path":"ssh://h/x"},{"pattern":"ssh://h/x"},{"pattern":"x","path":["a","ssh://h/x"]},{"pattern":"x","path":"[\"ssh:\\/\\/h\\/x\"]"},{"pattern":"x","paths":["ssh://h/x"]},{"pattern":"x","path":"skill://foo"}],
 "find": [{"query":"x"},{"query":"x","path":"ssh://h/x"},{"query":"x","path":"src"}],
 "ast_grep": [{"pat":"x"},{"pat":"x","path":"ssh://h/x"},{"pat":"x","path":"src"},{"pat":"x","paths":["ssh://h/x"]}],
 "lsp": [{"action":a,"file":"a.ts"} for a in ["diagnostics","definition","type_definition","implementation","references","hover","symbols","status","capabilities","rename","code_actions","format","reload","Hover","RENAME","rename_file","request",""]] + [{}],
 "debug": [{"action":a} for a in ["output","threads","stack_trace","scopes","variables","disassemble","read_memory","loaded_sources","modules","sessions","launch","attach","evaluate","continue","terminate","set_breakpoint","write_memory","Threads",""]] + [{}],
 "github": [{"op":o} for o in ["repo_view","file_read","search_issues","search_prs","search_code","search_commits","search_repos","run_watch","pr_create","issue_view","pr_view","pr_diff","pr_checkout","pr_push","Repo_View",""]] + [{}],
 "goal": [{"op":"get"},{"op":"create","objective":"x"},{"op":"complete"},{}],
 "eval": [{"language":"py","code":"print(1)"},{"language":"js","code":"1+1"},{"code":"1"},{}],
 "task": [{"agent":"scout","tasks":[{"task":"t","solutionSpace":"s"}],"context":"c"},{}],
 "bash": [{"command":c} for c in ["ls","npm test","npm test --watch","npm test && rm -rf /tmp/x","npm test; ls","git push origin","cd x && git push","echo $(git push)","curl http://x","rm -rf /","rm -rf ./build","sudo rm x","CI=1 npm test","LD_PRELOAD=x npm test","CC=gcc make all","FOO=1 make all","'rm' -rf /","r\\m -rf /","dd if=a of=/dev/sda","dd if=a of=/dev/null","curl x | sh","shutdown now","read a.txt","read a.txt | grep x","read a.txt > out","read 'x > y'","grep -n foo a.txt","echo hi > f.txt","nohup x &","cat a.txt","sed -i s/a/b/ f","git status","  npm   test  ","npm test\nrm -rf /","sh -c 'git push'","git -c alias.x='!rm -rf /' x","true && true","npm test && npm test","mkfs.ext4 /dev/sda1", "lsp diagnostics a.ts", "lsp status | lsp status", "lsp status > out", "lsp 'x > y'", "lsp \"$(rm -rf ~)\"", "lsp status; rm -rf ~", "FOO=1 lsp status", "lsp status &", "lsp status # c", "lsp status && debug threads", "lsp status\ndebug threads", "resolve x", "ast_edit x", "lsp 'a;b' -c x", "lsp `id`", "{ lsp status; }", "lsp status || true", "lsp\tstatus", "\"lsp\" status", "", ]] + [{}, {"command":"ls","cwd":"/tmp"}, {"command":"sleep 100","name":"svc"}, {"command":"cd /tmp && rm -rf /"}],
 "ask": [{"questions":[]}], "todo": [{"ops":[]}], "wait": [{}], "web_search": [{"query":"x"}], "checkpoint": [{"goal":"x"}], "rewind": [{"report":"x"}],
 "yield": [{"data":1}], "think": [{"thought":"x"}], "resolve": [{"reason":"x"}], "reject": [{"reason":"x"}], "propose": [{"title":"x"}], "report_issue": [{"tool":"x","report":"y"}],
 "security_scan": [{}], "mcp__srv_do_thing": [{}, {"a":1,"b":"multi\nline","c":[1,2]}], "ext_plain": [{"x":1}], "browser": [{"action":"open"}],
 "context_notes": [{}, {"text":"x"}], "new_context": [{}], "manage_skill": [{}], "recall": [{}], "reflect": [{}], "memory_edit": [{}], "learn": [{}], "retain": [{}], "nosuchtool": [{}], "computer": [{}], "image_gen": [{}], "tts": [{}],
}
PAT = [{"match":"npm test*","approval":"allow"},{"match":"git push*","approval":"deny"},{"match":"curl *","approval":"prompt"},{"match":"make *","approval":"allow","env":["CC"]},{"match":"true","approval":"allow"}]
MODES = [None,"always-ask","always-ask-cached","write","yolo"]
POLS = [None,"allow","deny","prompt"]

out = open(sys.argv[1] if len(sys.argv) > 1 else "cases.jsonl", "w")
n = 0
counts = {}
def emit(section, tool, args, operator, ui=True, sub=False, project=None, trusted=True, state=None, operatorRaw=None):
    global n
    s = {"operator": operator, "project": project, "trusted": trusted}
    if operatorRaw is not None: s["operatorRaw"] = operatorRaw
    c = {"id": n, "section": section, "tool": tool, "args": json.dumps(args), "settings": s,
         "ui": "interactive" if ui else "headless", "session": "subagent" if sub else "top"}
    if state: c["state"] = state
    out.write(json.dumps(c) + "\n"); n += 1; counts[section] = counts.get(section, 0) + 1

def op(mode=None, pol=None, tools=None, **extra):
    o = {}
    if mode: o["tools.approvalMode"] = mode
    if pol: o["tools.approval"] = {t: pol for t in (tools or ARGS)}
    o.update(extra)
    return o

# ---- base: 26,160 (every tool x 5 mode values x 4 policies x interactive/headless x top/subagent; bash twice: without and with patterns)
for tool, argl in ARGS.items():
    for args in argl:
        for mode in MODES:
            for pol in POLS:
                for ui in (True, False):
                    for sub in (False, True):
                        variants = [{}]
                        if tool == "bash": variants.append({"bash.patterns": PAT, "bash.allowedEnvVars": ["CI"]})
                        for v in variants:
                            emit("base", tool, args, op(mode, pol, **v), ui, sub)

# ---- paths: 624 (path shapes x write/read/ast_edit/grep x 3 modes)
PATHS = [" proc://bg_1/kill","proc://bg_1/kill ","PROC://bg_1/kill","proc:/bg_1/kill","Proc:/bg_1/kill","[proc://bg_1/kill]","[proc://bg_1/kill#1a2b]","[ proc://bg_1/kill#1A2B]","[proc://bg_1/kill#1A2B] ","  [local://x#1A2B]","[local://x#1A2B]junk","[a.txt#zzzz]","[local://x#1A2]","local://x/../../../etc/passwd","local://","local:x","local:///x","LOCAL:/x","\tlocal://x","./local://x","a/local://x","file://local://x","local://x\nproc://y/kill","out://x ","OUT://","out:/x","agent://x/../y","cfg:/tools.approvalMode","skill:/x"," skill://x","SKILL://x","rule://x","mcp:/x","history:/x","help:/x","attachment:/1","conflict:/1","issue:/1","pr:/1","mr://1","~/x","/etc/passwd","..\\x","C:\\x","local://x#1A2B","[[local://x#1A2B]#1A2B]","[cfg://a#1A2B]","[out://x#1A2B]","agent:/x","Agent://x", "ssh:/h/x","[ssh://h/x#1A2B]"]
for p in PATHS:
    for tool, args in (("write", {"path": p, "content": "x"}), ("read", {"path": p}), ("ast_edit", {"ops":[{"pat":"a","out":"b"}],"paths":[p]}), ("grep", {"pattern":"x","path":p})):
        for mode in ("always-ask","write","yolo"):
            emit("paths", tool, args, op(mode))

# ---- unwrap: hashline-header shapes of write.path (the gate's side; the tool's side is TestZZOmpApprovalUnwrapRoute)
UNWRAP = []
for inner in ("proc://bg_1/kill","proc://bg_1","cfg://tools.approvalMode","out://reply","local://x.md","agent://x","skill://foo","a.txt"):
    for tag in ("#x","#12345","#","","#1A2B","#1a2b","#zzzz","#1A2B#x","##1A2B"):
        UNWRAP.append("[" + inner + tag + "]")
UNWRAP += ["[proc://bg_1/kill#x] ", " [proc://bg_1/kill#x]", "[proc://bg_1/kill #x]", "[proc://bg_1/kill#x]]", "[[proc://bg_1/kill#x]"]
for p in UNWRAP:
    for mode in ("always-ask","write","yolo"):
        for ui in (True, False):
            emit("unwrap", "write", {"path": p, "content": ""}, op(mode), ui)
        emit("unwrap", "write", {"path": p, "content": ""}, op(mode), False, True)

# ---- restricted: tools.restrictedFolders (operator layer) x tools with a path / tools matched by cwd
RTOOLS = ["read","write","ast_edit","grep","glob","find","ast_grep","bash","eval","task","lsp","todo","ask","web_search","debug","github"]
RCASES = [
 ("read", {"path":"secrets/k.txt"}), ("read", {"path":"secrets"}), ("read", {"path":"secrets/k.txt:1-5"}), ("read", {"path":"other.txt"}), ("read", {"path":"secretsX/k.txt"}), ("read", {"path":"./secrets/../secrets/k.txt"}), ("read", {"path":"skill://foo"}),
 ("write", {"path":"secrets/k.txt","content":"x"}), ("write", {"path":"a.txt","content":"x"}), ("write", {"path":"[secrets/k.txt#1A2B]","content":"x"}), ("write", {"path":"local://x.md","content":"x"}),
 ("ast_edit", {"ops":[{"pat":"a","out":"b"}],"paths":["secrets/a.ts"]}), ("ast_edit", {"ops":[{"pat":"a","out":"b"}],"paths":["src/a.ts"]}),
 ("grep", {"pattern":"x","path":"secrets"}), ("grep", {"pattern":"x","path":"secrets/k.txt"}), ("grep", {"pattern":"x","path":"."}), ("grep", {"pattern":"x"}), ("grep", {"pattern":"x","path":"src"}),
 ("glob", {"path":"secrets"}), ("glob", {"path":"secrets/*.txt"}), ("glob", {"path":"**/*.txt"}), ("glob", {"path":"src"}),
 ("find", {"query":"x","path":"secrets"}), ("find", {"query":"x"}),
 ("ast_grep", {"pat":"x","path":"secrets"}), ("ast_grep", {"pat":"x","path":"src"}),
 ("bash", {"command":"ls"}), ("bash", {"command":"ls","cwd":"secrets"}), ("bash", {"command":"cat secrets/k.txt"}), ("bash", {"command":"cd secrets && ls"}),
 ("eval", {"language":"js","code":"1"}), ("task", {"agent":"scout","tasks":[{"task":"t","solutionSpace":"s"}],"context":"c"}),
 ("lsp", {"action":"hover","file":"secrets/a.ts"}), ("lsp", {"action":"rename","file":"secrets/a.ts"}), ("todo", {"ops":[]}), ("ask", {"questions":[]}), ("web_search", {"query":"x"}), ("debug", {"action":"threads"}), ("github", {"op":"repo_view"}),
]
for folders in (["secrets"], ["."], ["./secrets/", "other-dir"]):
    for mode in (None, "always-ask", "write"):
        for pol in POLS:
            for ui in (True, False):
                for sub in (False, True):
                    if sub and ui: continue
                    for tool, args in RCASES:
                        emit("restricted", tool, args, op(mode, pol, RTOOLS, **{"tools.restrictedFolders": folders}), ui, sub)

# ---- cached: always-ask-cached with a granted target / folder, and session grants (EMPTY grant store is section base)
def prior(tool, args, answer): return {"prior": {"tool": tool, "args": json.dumps(args), "answer": answer}}
R = lambda p: {"path": p}
W = lambda p: {"path": p, "content": "x"}
CACHED = [
 ("always-ask-cached", "read", R("a.txt"), prior("read", R("a.txt"), "target")),
 ("always-ask-cached", "read", R("a.txt:10-20"), prior("read", R("a.txt"), "target")),
 ("always-ask-cached", "read", R("b.txt"), prior("read", R("a.txt"), "target")),
 ("always-ask-cached", "read", R("a.txt"), prior("read", R("a.txt"), "once")),
 ("always-ask-cached", "read", R("dir/b.txt"), prior("read", R("dir/a.txt"), "folder")),
 ("always-ask-cached", "read", R("dir/sub/c.txt"), prior("read", R("dir/a.txt"), "folder")),
 ("always-ask-cached", "read", R("other/c.txt"), prior("read", R("dir/a.txt"), "folder")),
 ("always-ask-cached", "write", W("a.txt"), prior("read", R("a.txt"), "target")),
 ("always-ask-cached", "read", R("a.txt"), prior("write", W("a.txt"), "target")),
 ("always-ask-cached", "write", W("a.txt"), prior("write", W("a.txt"), "target")),
 ("always-ask-cached", "write", W("b.txt"), prior("write", W("a.txt"), "target")),
 ("always-ask-cached", "write", W("dir/b.txt"), prior("write", W("dir/a.txt"), "folder")),
 ("always-ask-cached", "write", W("dir/b.txt"), prior("read", R("dir/a.txt"), "folder")),
 ("always-ask-cached", "grep", {"pattern":"x","path":"src"}, prior("grep", {"pattern":"x","path":"src"}, "target")),
 ("always-ask-cached", "grep", {"pattern":"x","path":"src/a.go"}, prior("read", R("src/a.go"), "target")),
 ("always-ask-cached", "glob", {"path":"src/*.go"}, prior("read", R("src/a.go"), "folder")),
 ("always-ask-cached", "write", W("local://x.md"), prior("write", W("local://x.md"), "target")),
 ("always-ask-cached", "bash", {"command":"ls"}, prior("bash", {"command":"ls"}, "session")),
 ("always-ask-cached", "bash", {"command":"rm -rf /"}, prior("bash", {"command":"ls"}, "session")),
 ("always-ask-cached", "eval", {"language":"js","code":"1"}, prior("bash", {"command":"ls"}, "session")),
 ("always-ask-cached", "bash", {"command":"ls"}, prior("write", W("a.txt"), "target")),
 ("always-ask", "write", W("b.txt"), prior("write", W("a.txt"), "session")),
 ("always-ask", "write", W("b.txt"), prior("write", W("a.txt"), "once")),
 ("always-ask", "bash", {"command":"ls"}, prior("write", W("a.txt"), "session")),
 ("always-ask", "write", W("proc://bg_1/kill"), prior("write", W("a.txt"), "session")),
 ("always-ask", "write", W("cfg://tools.approvalMode"), prior("write", W("a.txt"), "session")),
 ("always-ask", "ast_edit", {"ops":[{"pat":"a","out":"b"}],"paths":["a.ts"]}, prior("write", W("a.txt"), "session")),
 ("always-ask", "bash", {"command":"git status"}, prior("bash", {"command":"ls"}, "session")),
 ("always-ask", "bash", {"command":"rm -rf /"}, prior("bash", {"command":"ls"}, "session")),
 ("always-ask", "bash", {"command":"lsp status"}, prior("bash", {"command":"ls"}, "session")),
 ("write", "bash", {"command":"git status"}, prior("bash", {"command":"ls"}, "session")),
 ("write", "eval", {"language":"js","code":"1"}, prior("bash", {"command":"ls"}, "session")),
 ("write", "write", W("proc://bg_1/kill"), prior("bash", {"command":"ls"}, "session")),
 ("write", "write", W("proc://bg_2/kill"), prior("write", W("proc://bg_1/kill"), "session")),
]
for mode, tool, args, st in CACHED:
    emit("cached", tool, args, op(mode), True, False, state=st)

# ---- interceptor: bashInterceptor.enabled
ICMDS = ["cat a.txt","head -n 5 a.txt","tail -f log","grep -rn foo .","rg foo","find . -name x","fd x","sed -i s/a/b/ f","perl -pi -e s/a/b/ f","echo hi > f.txt","printf x > f.txt","cat <<EOF > f\nx\nEOF","echo hi","ls","git status","cd x && cat a.txt","lsp status","sleep 5 &","nohup x","npm run dev","npm test -- --watch","cat a.txt | wc -l","echo \"a -> b\""]
for mode in (None, "always-ask"):
    for pol in (None, "allow"):
        for ui in (True, False):
            for sub in (False, True):
                if sub and ui: continue
                for cmd in ICMDS:
                    emit("interceptor", "bash", {"command": cmd}, op(mode, pol, ["bash"], **{"bashInterceptor.enabled": True}), ui, sub)
                emit("interceptor", "bash", {"command": "npm run dev", "name": "svc"}, op(mode, pol, ["bash"], **{"bashInterceptor.enabled": True}), ui, sub)
                emit("interceptor", "bash", {"command": "cat a.txt", "cwd": "src"}, op(mode, pol, ["bash"], **{"bashInterceptor.enabled": True}), ui, sub)

# ---- project: operator layer x trusted project layer (and the same project layer untrusted)
WA = ("write", {"path":"a.txt","content":"x"})
RA = ("read", {"path":"a.txt"})
def B(c): return ("bash", {"command": c})
ALLOW_ALL = [{"match":"*","approval":"allow"}]
PROJECT = [
 ({"tools.approvalMode":"always-ask"}, {"tools.approvalMode":"yolo"}, [WA, B("ls"), RA]),
 ({"tools.approvalMode":"write"}, {"tools.approvalMode":"yolo"}, [WA, B("ls")]),
 ({"tools.approvalMode":"always-ask"}, {"tools.approvalMode":"write"}, [WA, B("ls")]),
 ({"tools.approvalMode":"always-ask-cached"}, {"tools.approvalMode":"always-ask"}, [RA, WA, B("ls")]),
 ({"tools.approvalMode":"always-ask"}, {"tools.approvalMode":"always-ask-cached"}, [RA, WA, B("ls")]),
 ({}, {"tools.approvalMode":"always-ask"}, [WA, B("ls"), RA]),
 ({"tools.approvalMode":"write"}, {"tools.approvalMode":"always-ask"}, [WA, B("ls")]),
 ({"tools.approval":{"write":"deny"}}, {"tools.approval":{"write":"allow"}}, [WA]),
 ({"tools.approval":{"write":"prompt"}}, {"tools.approval":{"write":"allow"}}, [WA]),
 ({"tools.approval":{"write":"deny"}}, {"tools.approval":{"write":"prompt"}}, [WA]),
 ({"tools.approvalMode":"always-ask"}, {"tools.approval":{"write":"allow","bash":"allow"}}, [WA, B("ls")]),
 ({"tools.approval":{"write":"allow"}}, {"tools.approval":{"write":"deny"}}, [WA]),
 ({"tools.approval":{"write":"allow"},"tools.approvalMode":"always-ask"}, {"tools.approval":{"write":"prompt"}}, [WA]),
 ({}, {"tools.approval":{"write":"deny","bash":"prompt"}}, [WA, B("ls")]),
 ({"bash.patterns":[{"match":"npm *","approval":"prompt"}]}, {"bash.patterns":[{"match":"npm *","approval":"allow"}]}, [B("npm test")]),
 ({"bash.patterns":[{"match":"npm *","approval":"deny"}]}, {"bash.patterns":[{"match":"npm *","approval":"allow"}]}, [B("npm test")]),
 ({"bash.patterns":[{"match":"npm *","approval":"allow"}]}, {"bash.patterns":[{"match":"npm *","approval":"deny"}]}, [B("npm test")]),
 ({"bash.patterns":[{"match":"npm *","approval":"allow"}],"tools.approvalMode":"always-ask"}, {"bash.patterns":[{"match":"npm *","approval":"prompt"}]}, [B("npm test")]),
 ({"bash.patterns":[{"match":"npm test*","approval":"allow"},{"match":"*","approval":"prompt"}]}, {"bash.allowedEnvVars":["LD_PRELOAD"]}, [B("LD_PRELOAD=/tmp/x.so npm test"), B("npm test")]),
 ({"tools.approvalMode":"always-ask"}, {"bash.patterns":ALLOW_ALL}, [B("rm -r ./x"), B("ls"), B("rm -rf /"), B("ls && ls")]),
 ({"tools.approvalMode":"write"}, {"bash.patterns":ALLOW_ALL}, [B("rm -r ./x"), B("ls")]),
 ({"tools.approvalMode":"always-ask-cached"}, {"bash.patterns":ALLOW_ALL}, [B("ls")]),
 ({"tools.approval":{"bash":"prompt"}}, {"bash.patterns":ALLOW_ALL}, [B("rm -r ./x"), B("ls")]),
 ({"tools.approval":{"bash":"deny"}}, {"bash.patterns":ALLOW_ALL}, [B("ls")]),
 ({}, {"bash.patterns":ALLOW_ALL}, [B("ls"), B("rm -rf /")]),
 ({"tools.approvalMode":"always-ask","bash.patterns":[{"match":"npm test*","approval":"allow"}]}, {"bash.allowedEnvVars":["LD_PRELOAD"]}, [B("LD_PRELOAD=/tmp/x.so npm test"), B("npm test"), B("CI=1 npm test")]),
 ({"tools.approvalMode":"always-ask","bash.patterns":[{"match":"npm test*","approval":"allow"}],"bash.allowedEnvVars":["CI"]}, {"bash.allowedEnvVars":["LD_PRELOAD"]}, [B("LD_PRELOAD=/tmp/x.so npm test"), B("CI=1 npm test"), B("npm test")]),
 ({"tools.approvalMode":"always-ask","bash.patterns":[{"match":"npm test*","approval":"allow"}]}, {"bash.patterns":[{"match":"git push*","approval":"deny"}]}, [B("npm test"), B("git push origin"), B("ls")]),
 ({"tools.approvalMode":"always-ask","bash.patterns":[{"match":"make *","approval":"allow"}]}, {"bash.patterns":[{"match":"make *","approval":"allow","env":["CC"]}]}, [B("CC=gcc make all"), B("make all")]),
 ({"tools.approvalMode":"always-ask","bash.patterns":[{"match":"npm test*","approval":"allow"}]}, {"bash.patterns":[{"match":"npm test*","approval":"allow"},{"match":"npm run *","approval":"allow"}]}, [B("npm test"), B("npm run build")]),
 ({"tools.approvalMode":"always-ask"}, {"bash.patterns":[{"match":"lsp *","approval":"allow"}]}, [B("lsp status"), B("lsp rename x")]),
]
for operator, project, calls in PROJECT:
    for trusted in (True, False):
        for ui in (True, False):
            for sub in (False, True):
                if sub and ui: continue
                for tool, args in calls:
                    emit("project", tool, args, operator, ui, sub, project=project, trusted=trusted)

# ---- glob-path: glob by its schema (path only; the base glob cases carry the legacy `pattern` key)
for args in ({"path":"src"}, {"path":"ssh://h/x"}, {"path":"src;ssh://h/x"}, {"path":"**/*.go"}, {"path":"local://"}, {"path":"skill://foo/*.md"}):
    for mode in MODES:
        for ui in (True, False):
            for sub in (False, True):
                emit("glob-path", "glob", args, op(mode), ui, sub)

# ---- nil-setting: approval.Setting and approval.SessionSetting unset (omp: resolveApprovalFromContext without settings)
BASIC = [RA, WA, B("ls"), ("eval", {"language":"js","code":"1"}), ("write", {"path":"local://x.md","content":"x"}), ("write", {"path":"skill://foo","content":"x"}), ("todo", {"ops":[]}), ("mcp__srv_do_thing", {})]
for operator in ({}, {"tools.approvalMode":"always-ask","tools.approval":{"write":"deny"}}, {"tools.approvalMode":"yolo","tools.approval":{"write":"allow","bash":"allow"}}):
    for ui in (True, False):
        for tool, args in BASIC:
            emit("nil-setting", tool, args, operator, ui, False, state={"nilSetting": True})

# ---- malformed: the operator's config.yml does not parse
for raw in ("tools:\n  approvalMode: always-ask\n  approval: [unclosed\n", "tools:\n  approval:\n    write: deny\n  approvalMode: [\n", "bash:\n  patterns:\n    - match: \"*\"\n      approval: deny\n  x: {\n"):
    for ui in (True, False):
        for tool, args in BASIC:
            emit("malformed", tool, args, {}, ui, False, operatorRaw=raw)

out.close()
print("cases", n, counts)
