#!/usr/bin/env python3
"""Generates port/scenarios/config-*.json: what the operator sees of the config system under Pi: the warnings at session start
(schema violations, JSON errors in V8's wording, legacy files, deprecated caps, unusable dialog keys, a permissive `*`), the project
scope under the host's trust answer, and the `/permission-system` command (show, path, help, reset, unknown). The model is scripted
and does nothing but answer; the effects are the UI notifications and status in the trace."""
import json, os

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
os.makedirs(out, exist_ok=True)
CONFIG = "extensions/pi-permission-system/config.json"


def scenario(name, description, config=None, raw=None, agent_files=None, files=None, steps=None):
    agent = dict(agent_files or {})
    if raw is not None:
        agent[CONFIG] = raw
    elif config is not None:
        agent[CONFIG] = json.dumps(config, indent=2)
    s = {"name": name, "description": description, "llm": [{"text": "done"}] * (len([x for x in (steps or []) if x["rpc"]["message"][0] != "/"]) or 1),
         "steps": steps or [{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}], "files": files or {}, "agentFiles": agent}
    with open(os.path.join(out, name + ".json"), "w") as f:
        json.dump(s, f, indent=2)
        f.write("\n")


def cmd(args):
    return {"name": "permission-system " + (args or "(none)"), "rpc": {"type": "prompt", "message": ("/permission-system " + args).rstrip()}}


PROMPT = [{"name": "prompt", "rpc": {"type": "prompt", "message": "hello"}}]

scenario("config-unknown-key", "A config with unknown keys is refused whole: one warning per key at session start, once.",
         config={"yoloMode": True, "nope": 1, "alsoNope": {"a": 1}}, steps=PROMPT + [{"name": "prompt-again", "rpc": {"type": "prompt", "message": "hello again"}}])
scenario("config-invalid-value", "Schema violations with their paths: a bad knob, a bad surface key, a bad action.",
         raw=json.dumps({"debugLog": "yes", "forwardingTimeoutMs": 0, "permission": {"path_wrote": {"*": "deny"}, "bash": {"x": "maybe"}}}))
scenario("config-json-error", "A config that is not JSON warns with the host's JSON.parse message.", raw='{"permission": {"bash": }')
scenario("config-json-error-unterminated", "An unterminated string, V8's wording and position.", raw='{\n  "permission": {\n    "bash": "al')
scenario("config-deprecated-caps", "The two superseded preview caps are accepted and noted.",
         config={"permission": {"*": "ask", "bash": "deny"}, "toolInputPreviewMaxLength": 400, "toolTextSummaryMaxLength": 80})
scenario("config-dialog-keys", "Unusable dialog hotkeys keep their defaults and are named.",
         config={"permissionDialogKeys": {"approve": "A", "deny": "j", "approveSession": "b"}, "permission": {"*": "ask", "bash": "ask"}})
scenario("config-legacy-global", "A legacy global policy file is noted with the move command.",
         agent_files={"pi-permissions.jsonc": json.dumps({"defaultPolicy": {"tools": "allow"}})}, config={"permission": {"*": "ask", "bash": "ask"}})
scenario("config-permissive-fallback", "A top-level allow with no bash policy warns once.", config={"permission": {"*": "allow"}})
scenario("config-comments-trailing", "Comments are stripped; a trailing comma is not JSON.", raw='{\n  // note\n  "permission": { "*": "ask", /* x */ "bash": "ask", },\n}')
scenario("config-project-scope", "The project config's runtime knobs apply only when the host says the project is trusted (the golden records Pi's answer).",
         config={"yoloMode": False, "permission": {"*": "ask", "bash": "ask"}},
         files={".pi/extensions/pi-permission-system/config.json": json.dumps({"yoloMode": True, "permission": {"bash": "allow"}})})
scenario("config-project-invalid", "A rejected project config is reported with its path once the project is trusted.",
         config={"permission": {"*": "ask", "bash": "ask"}}, files={".pi/extensions/pi-permission-system/config.json": json.dumps({"yoloMod": True})})
scenario("command-show", "`/permission-system show` lists the knobs and the composed config rules with their origins.",
         config={"yoloMode": True, "debugLog": True, "permission": {"*": "ask", "read": "allow", "bash": {"*": "ask", "git *": "allow", "rm *": {"action": "deny", "reason": "no"}}}},
         steps=[cmd("show")])
scenario("command-path", "`/permission-system path` prints the global config path.", config={"permission": {"*": "ask", "bash": "ask"}}, steps=[cmd("path")])
scenario("command-help-unknown", "`help` and an unknown word print the usage, the latter as a warning.", config={"permission": {"*": "ask", "bash": "ask"}},
         steps=[cmd("help"), cmd("bogus"), cmd("SHOW  ")])
scenario("command-reset", "`reset` writes the three knobs back to the global file and says so; the status follows.",
         config={"yoloMode": True, "debugLog": True, "permission": {"*": "ask", "bash": "ask"}}, steps=[cmd("show"), cmd("reset"), cmd("show")])
