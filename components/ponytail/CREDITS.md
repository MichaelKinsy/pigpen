# Credits

`extensions/ponytail` is a Go port of the Pi extension of **@dietrichgebert/ponytail** 4.10.0,
https://github.com/DietrichGebert/ponytail, by **Dietrich Gebert** (MIT, Copyright (c) 2026 DietrichGebert), and the
six Skills under `skills/` are that project's Skills.

- Commit: `1d95ff7d39de12d87014ea40d4e22201bddc501b`
- The unmodified original (`pi-extension/`, `hooks/`, `skills/`, with its license and README) is kept at
  [`port/oracle/`](port/oracle) as the equivalence oracle; the license is at [`port/oracle/LICENSE`](port/oracle/LICENSE).
- The ponytail ruleset, the text of the six Skills and the notices are Dietrich Gebert's. The Skills are copied byte for
  byte except for the `name` line and the directory name (`pigpen-` prefix), recorded in
  [port/ADAPTATIONS.md](port/ADAPTATIONS.md) and written by `port/adapt.py`. The main Skill is also embedded in the extension
  (`extensions/ponytail/ponytail_skill.md`), as the original reads it at run time.

The Go code, the scenarios and the layer-1 tests were written for this Package by Michael Kinsy. The 23 test cases of the
original's Pi extension are ported as twins (`port/upstream-tests.json`). What differs is listed in
[README.md](README.md) and [port/PORT.md](port/PORT.md). The project's marketing claims (benchmarks, savings) are
the author's and are not repeated here.
