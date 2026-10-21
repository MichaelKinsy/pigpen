#!/usr/bin/env python3
"""Builds skills/ from the unmodified original in port/oracle/skills, the two files the bootstrap extension embeds,
and writes port/ADAPTATIONS.md.

The only changes PiG's Skill format and Pigpen's Skill gate require:
  1. a Skill's `name` takes the `pigpen-superpowers-` prefix and its directory the same name (the gate wants
     `pigpen-<lowercase-hyphenated>` with the directory equal to the name);
  2. a reference to a Skill by its plugin name (`superpowers:<name>`) becomes the new Skill name, so the
     instruction still names a Skill that exists.
Nothing else is touched: every other file, and every other byte of a SKILL.md, is copied as it is.

The extension in extensions/superpowers embeds two files this script writes, so neither is edited by hand:
  - bootstrap_skill.md: the shipped pigpen-superpowers-using-superpowers/SKILL.md (the original reads
    skills/using-superpowers/SKILL.md at run time);
  - pi_tool_mapping.md: the text of the template literal returned by piToolMapping() in the unmodified
    .pi/extensions/superpowers.ts, with the escaped backticks of the literal unescaped."""
import os, re, shutil, sys, hashlib

here = os.path.dirname(os.path.abspath(__file__))
VERSION = "v7.0.0"
COMMIT = "bb92a77741419a4ab5f06e711a283343f1ada0c3"
src = os.path.join(here, "oracle", "skills")
dst = os.path.join(here, "..", "skills")
shutil.rmtree(dst, ignore_errors=True)
names = sorted(d for d in os.listdir(src) if os.path.isdir(os.path.join(src, d)))
new = {n: "pigpen-superpowers-" + n for n in names}
ref = re.compile(r"superpowers:(" + "|".join(sorted(names, key=len, reverse=True)) + r")(?![a-z0-9-])")
report = []
for n in names:
    for root, _, files in os.walk(os.path.join(src, n)):
        for f in sorted(files):
            p = os.path.join(root, f)
            rel = os.path.relpath(p, os.path.join(src, n))
            out = os.path.join(dst, new[n], rel)
            os.makedirs(os.path.dirname(out), exist_ok=True)
            data = open(p, "rb").read()
            refs = 0
            rename = 0
            if f.endswith(".md") or f.endswith(".txt"):
                text = data.decode("utf-8")
                if rel == "SKILL.md":
                    m = re.match(r"---\nname: " + re.escape(n) + r"\n", text)
                    assert m, "frontmatter name of " + n
                    text = "---\nname: " + new[n] + "\n" + text[m.end():]
                    rename = 1
                text, refs = ref.subn(lambda m: new[m.group(1)], text)
                data = text.encode("utf-8")
            open(out, "wb").write(data)
            shutil.copymode(p, out)
            if os.path.exists(p) and (refs or rename):
                report.append((n, rel, rename, refs))
ext = os.path.join(here, "..", "extensions", "superpowers")
os.makedirs(ext, exist_ok=True)
shutil.copyfile(os.path.join(dst, new["using-superpowers"], "SKILL.md"), os.path.join(ext, "bootstrap_skill.md"))
ts = open(os.path.join(here, "oracle", ".pi", "extensions", "superpowers.ts"), encoding="utf-8").read()
literal = re.search(r"function piToolMapping\(\): string \{\n\treturn `(.*?)`;\n\}", ts, re.S).group(1)
assert "${" not in literal and not re.search(r"\\[^`]", literal), "piToolMapping holds an escape this script does not unescape"
with open(os.path.join(ext, "pi_tool_mapping.md"), "w", encoding="utf-8", newline="") as f:
    f.write(literal.replace("\\`", "`"))
tools = os.path.join(here, "..", "extensions", "superpowers-tools")
os.makedirs(tools, exist_ok=True)
# the browser-side assets of the visual companion are served by extensions/superpowers-tools as they are
shutil.copyfile(os.path.join(dst, new["brainstorming"], "scripts", "helper.js"), os.path.join(tools, "companion_helper.js"))
shutil.copyfile(os.path.join(dst, new["brainstorming"], "scripts", "frame-template.html"), os.path.join(tools, "companion_frame.html"))
lines = ["# Adaptations", "",
         "Every change between the unmodified original (`port/oracle/skills`, obra/superpowers %s, commit" % VERSION,
         "`%s`) and the Skills shipped here (`skills/`), written by `port/adapt.py`." % COMMIT,
         "Two kinds of change, and no others: the Skill `name` and its directory take the `pigpen-superpowers-` prefix",
         "(Pigpen's Skill gate requires `pigpen-<name>` and a directory equal to the name), and a reference of the form",
         "`superpowers:<skill>` becomes `pigpen-superpowers-<skill>`. All other files and bytes are as upstream.", "",
         "| Upstream Skill | Skill here | `name` line | references rewritten (file: count) |", "|---|---|---|---|"]
by = {}
for n, rel, rename, refs in report:
    d = by.setdefault(n, {"rename": 0, "files": []})
    d["rename"] += rename
    if refs:
        d["files"].append("%s: %d" % (rel, refs))
total = 0
for n in names:
    d = by.get(n, {"rename": 0, "files": []})
    total += sum(int(x.split(": ")[1]) for x in d["files"])
    lines.append("| `%s` | `%s` | %s | %s |" % (n, new[n], "changed" if d["rename"] else "-", "; ".join(d["files"]) or "-"))
lines += ["", "%d references rewritten in total." % total, "",
          "## The extension", "",
          "`.pi/extensions/superpowers.ts`, the original's Pi extension, is ported to Go in `extensions/superpowers` (see",
          "[PORT.md](PORT.md)). It reads `skills/using-superpowers/SKILL.md` at run time; the port embeds the shipped Skill",
          "(`extensions/superpowers/bootstrap_skill.md`, a copy of `skills/pigpen-superpowers-using-superpowers/SKILL.md`) and the text",
          "of the original's `piToolMapping()` (`extensions/superpowers/pi_tool_mapping.md`), both written by this script, so the",
          "injected bootstrap names the renamed Skills.", "",
          "`extensions/superpowers-tools` is a Go port of the two Node helpers the Skills tell the model to run: the visual companion server of",
          "`brainstorming` (`scripts/server.cjs`, `start-server.sh`, `stop-server.sh`) and `writing-skills/render-graphs.js`. They are the tools",
          "`brainstorm_server` and `render_skill_graphs`; the tools' prompt guidelines tell the model to use them in place of the scripts. The",
          "scripts stay in the Skills as the original has them (this tree is byte for byte the original's apart from the changes above) and are",
          "inert without Node. The extension embeds the browser-side `helper.js` and `frame-template.html`, copied by this script.", "",
          "## Not included", "",
          "- `hooks/`, `scripts/`, `tests/`, `docs/`, `assets/` and the other harness manifests (Claude Code, Codex, Gemini,",
          "  OpenCode, Cursor): they are not Skills.", "",
          "The helper scripts inside Skills are kept as they are (`pigpen-superpowers-brainstorming/scripts/`: a visual",
          "companion server in Node plus shell wrappers; `executing-plans` and `subagent-driven-development` shell helpers).",
          "They run only when the model starts them, and the Node server needs Node on that machine; a machine without Node",
          "simply cannot use the visual companion. `pigpen-superpowers-writing-skills/render-graphs.js` is a Node script of the same kind",
          "(it renders the flowcharts of a Skill); it needs Node too and is otherwise inert.", ""]
open(os.path.join(here, "ADAPTATIONS.md"), "w").write("\n".join(lines))
print(len(names), "skills,", total, "references rewritten")
