#!/usr/bin/env python3
"""Builds skills/ from the unmodified original in port/oracle/skills, and the embedded ruleset of the extension.

The only change PiG's Skill format and Pigpen's Skill gate require: a Skill's `name` takes the `pigpen-` prefix and
its directory the same name (the gate wants `pigpen-<lowercase-hyphenated>` with the directory equal to the name).
Every other byte of every file is copied as it is. The slash commands (`/ponytail`, `/ponytail-review`) keep their
names: they are commands of the extension, not Skills."""
import os, re, shutil
here = os.path.dirname(os.path.abspath(__file__))
src = os.path.join(here, "oracle", "skills")
dst = os.path.join(here, "..", "skills")
shutil.rmtree(dst, ignore_errors=True)
rows = []
for n in sorted(os.listdir(src)):
    new = "pigpen-" + n
    for root, _, files in os.walk(os.path.join(src, n)):
        for f in sorted(files):
            p = os.path.join(root, f)
            rel = os.path.relpath(p, os.path.join(src, n))
            out = os.path.join(dst, new, rel)
            os.makedirs(os.path.dirname(out), exist_ok=True)
            data = open(p, "rb").read()
            if rel == "SKILL.md":
                text = data.decode("utf-8")
                m = re.match(r"---\nname: " + re.escape(n) + r"\n", text)
                assert m, n
                data = ("---\nname: " + new + "\n" + text[m.end():]).encode("utf-8")
                rows.append((n, new))
            open(out, "wb").write(data)
# the extension reads the main skill's body as its ruleset; embed the shipped file
shutil.copyfile(os.path.join(dst, "pigpen-ponytail", "SKILL.md"), os.path.join(here, "..", "extensions", "ponytail", "ponytail_skill.md"))
lines = ["# Adaptations", "",
         "Every change between the unmodified original (`port/oracle/skills`, @dietrichgebert/ponytail 4.10.0, commit",
         "`1d95ff7d39de12d87014ea40d4e22201bddc501b`) and the Skills shipped here (`skills/`), written by `port/adapt.py`.",
         "One kind of change: the Skill `name` and its directory take the `pigpen-` prefix (Pigpen's Skill gate requires",
         "`pigpen-<name>` and a directory equal to the name). Every other byte is as upstream. The main Skill is also",
         "copied to `extensions/ponytail/ponytail_skill.md`, which the extension embeds as its ruleset (the original reads",
         "`skills/ponytail/SKILL.md` at run time).", "",
         "| Upstream Skill | Skill here |", "|---|---|"] + ["| `%s` | `%s` |" % r for r in rows]
open(os.path.join(here, "ADAPTATIONS.md"), "w").write("\n".join(lines) + "\n")
print(len(rows), "skills")
