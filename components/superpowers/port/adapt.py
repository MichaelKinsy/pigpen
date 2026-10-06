#!/usr/bin/env python3
"""Builds skills/ from the unmodified original in port/oracle/skills and writes port/ADAPTATIONS.md.

The only changes PiG's Skill format and Pigpen's Skill gate require:
  1. a Skill's `name` takes the `pigpen-superpowers-` prefix and its directory the same name (the gate wants
     `pigpen-<lowercase-hyphenated>` with the directory equal to the name);
  2. a reference to a Skill by its plugin name (`superpowers:<name>`) becomes the new Skill name, so the
     instruction still names a Skill that exists.
Nothing else is touched: every other file, and every other byte of a SKILL.md, is copied as it is."""
import os, re, shutil, sys, hashlib

here = os.path.dirname(os.path.abspath(__file__))
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
lines = ["# Adaptations", "",
         "Every change between the unmodified original (`port/oracle/skills`, obra/superpowers v6.4.2, commit",
         "`8ca22dba9a94f28898bbce59f2537ff4d87c747d`) and the Skills shipped here (`skills/`), written by `port/adapt.py`.",
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
          "## Not included", "",
          "- `.pi/extensions/superpowers.ts`, the original's Node extension for Pi: it injects the `using-superpowers` bootstrap",
          "  into every first model request. A Package here ships no Node code and no hook, so the Skill is available by its",
          "  description like any other (`/skill:pigpen-superpowers-using-superpowers` loads it). Nothing else of the original",
          "  runs at start-up.",
          "- `hooks/`, `scripts/`, `tests/`, `docs/`, `assets/` and the other harness manifests (Claude Code, Codex, Gemini,",
          "  OpenCode, Cursor): they are not Skills.", "",
          "The helper scripts inside Skills are kept as they are (`pigpen-superpowers-brainstorming/scripts/`: a visual",
          "companion server in Node plus shell wrappers; `executing-plans` and `subagent-driven-development` shell helpers).",
          "They run only when the model starts them, and the Node server needs Node on that machine; a machine without Node",
          "simply cannot use the visual companion. `pigpen-superpowers-writing-skills/render-graphs.js` is a Node script of the same kind",
          "(it renders the flowcharts of a Skill); it needs Node too and is otherwise inert.", ""]
open(os.path.join(here, "ADAPTATIONS.md"), "w").write("\n".join(lines))
print(len(names), "skills,", total, "references rewritten")
