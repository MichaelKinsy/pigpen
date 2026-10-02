# Credits

`extensions/warden` is a Go port of **pi-warden**, https://github.com/DevMortimer/pi-warden, by **Ryan Gapac**
(GitHub: DevMortimer), MIT, Copyright (c) 2026 Ryan Gapac.

- Original: package `pi-warden` 0.80.0, commit `a12b2703b2c7ac32401fe13f4ce037d673a5f113`.
- The unmodified original (`src/`, `tests/`, `extensions/`, `package.json`, lockfile) is kept at
  [`port/oracle`](port/oracle) as the equivalence oracle, with its license at
  [`port/oracle/LICENSE`](port/oracle/LICENSE). [`port/oracle/UPSTREAM.md`](port/oracle/UPSTREAM.md) records the pin.
- The judge seam (`extensions/warden/judge.go`) follows **pi-typesafe** (`ask`, question builders, safe error
  messages), https://github.com/DevMortimer/pi-typesafe, by Ryan Gapac, MIT, commit
  `ed439f834665ad6fc652787f5ba7c23852566d49`.
- The question texts sent to the judge are the original's, copied verbatim into `extensions/warden/questions.json`
  by `port/`'s tooling from the pinned source, so a paraphrase cannot change the judge's answers.

The Go code, the tests, the scenarios and the consent and disclosure flow were written for this Package by Michael
Kinsy. The behavior, thresholds and the wording of the messages the agent reads follow the original (they still say
`pi-warden:`); [`port/PORT.md`](port/PORT.md) maps every file, lists what was not ported and what was changed on
purpose. Modified paths of the original: none; the port is a separate implementation.
