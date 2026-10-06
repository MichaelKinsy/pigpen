#!/usr/bin/env python3
"""Generates port/scenarios/*.json: the ruleset the model sees at each level, /ponytail and its subcommands, the
deactivation phrases, the persisted default and the settings that quiet or hide the extension. Each runs against the
original under Pi and the port under PiG. The five skill aliases (/ponytail-review ...) are not scenarios: the Skills
are renamed pigpen-ponytail-* here, so the message they send differs by design (layer-1 twin)."""
import json, os
out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
os.makedirs(out, exist_ok=True)

def prompt(msg, name=None): return {"name": name or msg.replace("/", "").replace(" ", "-")[:30] or "p", "rpc": {"type": "prompt", "message": msg}}

def config_setup(cfg):
    body = json.dumps(cfg).replace("'", "'\\''")
    return [["sh", "-c", "mkdir -p \"$HOME/.config/ponytail\" && printf '%s' '" + body + "' > \"$HOME/.config/ponytail/config.json\""]]

# Pi draws the indicator with its theme (ANSI codes chosen by the terminal's colour mode); the Go SDK has no theme, so
# the port's text is plain. Every scenario therefore keeps the indicator hidden (hideStatus), which leaves the ruleset
# and the notices to compare; the indicator is a layer-1 twin (extension_twin_test.go).
def scenario(name, description, steps, replies=1, **extra):
    extra.setdefault("setup", config_setup({"hideStatus": True}))
    s = {"name": name, "description": description, "llm": [{"text": "done"}] * replies, "steps": steps}
    s.update(extra)
    json.dump(s, open(os.path.join(out, name + ".json"), "w"), indent=2, ensure_ascii=False)
    open(os.path.join(out, name + ".json"), "a").write("\n")

scenario("ruleset-full", "A prompt at the default level carries the full ruleset in the system prompt.", [prompt("hello", "hello")])
for lvl in ("lite", "ultra"):
    scenario("mode-" + lvl, "/ponytail %s sets the session level; the next request carries that level's ruleset." % lvl,
             [prompt("/ponytail " + lvl, "set"), prompt("hello", "hello")])
# (And turning off clears the status text with an empty string, which PiG's host reports as a missing text and Pi's as "":
# a host difference visible even when the original runs on both, so these three keep the indicator hidden (the
# ruleset is unaffected) and the clearing is a layer-1 test (extra_test.go).
scenario("mode-off", "/ponytail off removes the ruleset from the next request .", [prompt("/ponytail off", "set"), prompt("hello", "hello")])
scenario("bare-command", "/ponytail with no argument sets the default level.", [prompt("/ponytail ultra", "set"), prompt("/ponytail", "bare"), prompt("hello", "hello")])
scenario("command-status", "/ponytail status reports the current and the default level.", [prompt("/ponytail status", "status")], replies=0)
scenario("command-invalid", "An unknown level, and review as a default, are refused.",
         [prompt("/ponytail bogus", "bogus"), prompt("/ponytail review", "review"), prompt("/ponytail default review", "default-review"), prompt("/ponytail default", "default-bare")], replies=0)
scenario("command-default", "/ponytail default <level> saves the default and the next status shows it.",
         [prompt("/ponytail default lite", "save"), prompt("/ponytail status", "status")], replies=0)
scenario("normal-mode", "'normal mode' as a whole message turns the ruleset off; a sentence containing it does not.",
         [prompt("add a normal mode toggle next to dark mode", "mention"), prompt("normal mode", "off"), prompt("hello", "after")], replies=3)
scenario("stop-ponytail", "'Stop ponytail.' (case and punctuation aside) turns the ruleset off.",
         [prompt("Stop ponytail.", "off"), prompt("hello", "after")], replies=2)
scenario("config-default-ultra", "A default level in the config file is the starting level.", [prompt("hello", "hello")],
         setup=config_setup({"defaultMode": "ultra", "other": [1, 2], "hideStatus": True}))
scenario("config-quiet-hide", "quietStartup and hideStatus in the config file keep the notice and the indicator away; the ruleset stays.",
         [prompt("hello", "hello")], setup=config_setup({"quietStartup": True, "hideStatus": True}))
scenario("config-default-written", "Saving a default keeps the other fields of the config file, in order.",
         [prompt("/ponytail default full", "save"), prompt("/ponytail status", "status")], replies=0,
         setup=config_setup({"zeta": {"a": [1, {"b": 2}], "c": None}, "hideStatus": True, "alpha": "x"}))
