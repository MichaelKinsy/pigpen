#!/usr/bin/env python3
"""Writes port/probe/cases.json: the inputs the review probe runs through the original (bash decisions, config validation,
wrapper classification). A bash case's "port" names an expected difference: "unsupported" (the port blocks a command it cannot
read without a parser) or "stricter" (the port asks where the original's floor exemption allows). No "port": the port must give
the original's decision."""
import json, os, sys

PATHS = {"path": "allow", "external_directory": "allow"}
def perm(**kw):
    d = dict(PATHS); d.update(kw); return d

CONFIGS = {
    "bash-allow": (perm(bash="allow"), False),
    "universal-allow": (perm(**{"*": "allow"}), False),
    "patterns": (perm(bash={"*": "allow", "rm *": "deny", "git push *": "ask"}), False),
    "deny-default": (perm(bash={"*": "deny", "git status": "allow", "npm *": {"action": "deny", "reason": "Use pnpm instead"},
                                "echo *": "allow", "sudo *": "allow", "nice *": "allow", "ls": "allow", "wc *": "allow"}), False),
    "bash-ask": (perm(bash="ask"), False),
    "universal-deny": (perm(**{"*": "deny", "bash": {"echo *": "allow"}}), False),
    "yolo-allow": (perm(bash="allow"), True),
    "yolo-patterns": (perm(bash={"*": "ask", "rm *": "deny"}), True),
    "default": (perm(), False),
}

PLAIN = ["git status", "echo ok", "rm x", "npm install", "ls", "git push origin main", "echo  ok", " git status ", "wc -l"]
WRAPPERS = ["nice touch a", "env touch b", "/usr/bin/env touch b", "sudo rm x", "sudo ls", "eval touch c", "sh -c ls",
            "/bin/sh -c ls", "bash -ec ls", "zsh -xc ls", "flock out.lock touch a", "rush touch c", "rust-parallel touch d",
            "fd -x rm", "fd --exec-batch rm", "find . -execdir rm x", "find . -name x", "timeout 5 touch z", "xargs rm",
            "watch ls", "setsid touch x", "doas ls", "stdbuf -oL touch x", "nohup touch x", "parallel rm", "time touch a"]
EXEMPT = ["nice echo d", "env echo b", "sudo ls", "timeout 5 wc -l", "xargs grep foo"]  # the original's floor exemption: a pure reader
EXTRA_GUARDS = ["ssh host ls", "command ls", "bash script.sh", "bash -- -c", "su root", "source x.sh"]
CHAINS = ["ls | wc -l; rm x", "echo x && nice touch y", "git status && rm x", "git status; ls", "git status || echo no", "ls\nrm x",
          "ls|wc -l", "echo a&&echo b", "git status && sudo ls", "ls && ssh host ls"]
UNREADABLE = ["ls &", "ls & rm x", "ls;", "echo $(ls)", "echo 'a'", "A=1 ls", "git status\t", "cat < f", "echo hi > out.txt",
              "rm -rf /tmp/y; echo hi > out.txt", "", "   ", "echo \u001b]0;x\u0007", "echo {a,b}", "echo é", "ls ;; rm x"]

# More cases under configs of their own: which of two denies (or asks) in a chain the decision names.
EXTRA = {
    "two-denies": (perm(bash={"*": "allow", "rm *": {"action": "deny", "reason": "no rm"}, "mv *": {"action": "deny", "reason": "no mv"},
                              "git push *": "ask", "git pull *": "ask"}), False,
                   ["rm x; mv y", "mv y && rm x", "git push a; git pull b", "git push a; rm x", "ls | mv a b | rm c"]),
    # a wildcard surface key written before bash: the schema's output puts bash (a surface it names) first, so b* matches last
    "surface-order": (dict(perm(), **{"b*": "deny", "bash": "allow"}), False, ["ls", "echo hi"]),
}

cases = []
for name in CONFIGS:
    cmds = PLAIN + WRAPPERS + EXEMPT + EXTRA_GUARDS + CHAINS + UNREADABLE
    seen = set()
    for c in cmds:
        if c in seen:
            continue
        seen.add(c)
        perm_, yolo = CONFIGS[name]
        cases.append({"config": name, "permission": perm_, "yolo": yolo, "command": c})

for name, (perm_, yolo, cmds) in EXTRA.items():
    for c in cmds:
        cases.append({"config": name, "permission": perm_, "yolo": yolo, "command": c})

wrapper = ["eval rm", "bash -c rm", "sh -c rm", "dash -c rm", "zsh -c rm", "ksh -c rm", "bash -ec rm", "bash -xc rm", "/bin/bash -c rm",
           "bash script.sh", "bash -- -c", "sudo aws s3 ls", "env FOO=bar aws", "xargs grep foo", "timeout 10 grep foo", "nice -n 5 make",
           "doas ls", "flock /tmp/lock ls", "find . -exec grep foo {} ;", "find . -execdir rm {} ;", "fd -x rm", "fd --exec-batch rm",
           "find . -name x", "ls -la", "grep -c foo file", "git status", "", "./sudo ls", "sudo", "bash -c", "bash --c x", "rush x",
           "rust-parallel x", "setsid x", "stdbuf x", "watch x", "parallel x", "time x", "nohup x", "/usr/bin/xargs rm", "fd -X rm",
           "fd --exec rm", "find . -ok rm", "find . -okdir rm", "find -exec", "sh -n -c x", "sh -cx x", "sh x -c y"]

here = os.path.dirname(os.path.abspath(__file__))
config = json.load(open(os.path.join(here, "config-cases.json")))
json.dump({"bash": cases, "config": config, "wrapper": wrapper}, open(os.path.join(here, "cases.json"), "w"), indent=1, ensure_ascii=False)
print(len(cases), "bash cases,", len(config), "configs,", len(wrapper), "wrapper units")
