#!/usr/bin/env python3
"""Generates port/scenarios/*.json: tool calls gated by the permission config. The model is scripted: each turn calls the given
tools and a final "done" turn follows. The permission map is a project config file; a scenario's `ui` list answers dialogs."""
import json, os

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
os.makedirs(out, exist_ok=True)
CONFIG = "extensions/pi-permission-system/config.json"  # under the agent directory (the project scope is withheld until the project is trusted)


def call(name, **args):
    return {"name": name, "arguments": args}


def scenario(name, description, permission, turns, ui=None, extra_files=None, config=None, commands=None, raw_config=None):
    llm = [{"toolCalls": t} for t in turns] + [{"text": "done"}]
    step = {"name": "prompt", "rpc": {"type": "prompt", "message": "do the work"}}
    if ui:
        step["ui"] = ui
    agent = {CONFIG: raw_config if raw_config is not None else json.dumps(config if config is not None else {"permission": permission}, indent=2)}
    files = dict(extra_files or {})
    s = {"name": name, "description": description, "llm": llm, "steps": [step], "files": files, "agentFiles": agent}
    if commands:
        s["commands"] = commands
    with open(os.path.join(out, name + ".json"), "w") as f:
        json.dump(s, f, indent=2)
        f.write("\n")


BASE = {"path": "allow", "external_directory": "allow"}


def perm(**kw):
    d = dict(BASE)
    d.update(kw)
    return d


REACH = {"never-matches-anything": "allow"}  # keeps a denied tool exposed: a tool whose every value is denied is withheld from the model

def seq(*calls):
    """One tool call per turn: parallel calls finish in no fixed order (G4)."""
    return [[c] for c in calls]


BASH_RULES = {"*": "deny", "git status": "allow", "npm *": {"action": "deny", "reason": "Use pnpm instead"}, "echo ok": "allow"}

scenario("bash-patterns", "Simple bash commands against a deny-by-default pattern map, with a reason.", perm(bash=BASH_RULES),
         seq(call("bash", command="git status"), call("bash", command="npm install"), call("bash", command="echo bye"), call("bash", command="echo ok"),
             call("bash", command="git status --short"), call("bash", command="npm"), call("bash", command="echo  ok")),
         commands={"npm": {"mode": "missing"}})

scenario("bash-reason-last-match", "A later allow overrides an earlier deny with a reason; a later deny wins again.",
         perm(bash={"*": "ask", "rm *": {"action": "deny", "reason": "no deletes"}, "rm -i *": "allow", "rm -i -r *": {"action": "deny", "reason": "never recursive"}, "echo *": "allow"}),
         seq(call("bash", command="rm x"), call("bash", command="rm -i x"), call("bash", command="rm -i -r x"), call("bash", command="echo a b"), call("bash", command="echo")))

scenario("tools-allowed", "Allowed built-in tools: a path-bearing tool and bash.", perm(read="allow", bash="allow"),
         seq(call("read", path="a.txt"), call("bash", command="echo hello")), extra_files={"a.txt": "hello\n"})

scenario("universal-allow", "The universal fallback decides tools without a rule of their own (and the config warns about bash).", perm(**{"*": "allow"}),
         seq(call("read", path="a.txt"), call("bash", command="echo hello")), extra_files={"a.txt": "hello\n"})

scenario("universal-allow-bash-gated", "A universal allow with an explicit bash policy raises no warning.", perm(**{"*": "allow", "bash": {"*": "allow", "rm *": "deny"}}),
         seq(call("bash", command="echo hello"), call("bash", command="rm x")))

scenario("universal-deny-bash-exception", "A universal deny with one bash exception: bash stays exposed.", perm(**{"*": "deny", "bash": {"echo hello": "allow"}}),
         seq(call("bash", command="echo hello"), call("bash", command="echo bye")))

scenario("yolo-mode", "yoloMode turns asks into allows and shows the status; denies stay.", None,
         seq(call("bash", command="echo hello"), call("bash", command="rm x"), call("read", path="a.txt")),
         config={"yoloMode": True, "permission": perm(**{"*": "ask", "bash": {"*": "ask", "rm *": {"action": "deny", "reason": "no deletes"}}})},
         extra_files={"a.txt": "hello\n"})


scenario("config-comments", "The config file may carry // and /* */ comments (config-loader.ts stripJsonComments); the rules still apply.", None,
         seq(call("bash", command="rm -f x"), call("bash", command="touch y")),
         raw_config='{\n  // comment\n  "permission": {"path": "allow", "external_directory": "allow", "*": "allow",\n'
                    '    "bash": {"*": "allow", "rm *": "deny"}} /* end */\n}')
