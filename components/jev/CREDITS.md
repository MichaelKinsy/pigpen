# Credits

`extensions/jev` is a Go port of **pi-jev** (`@y0usaf/pi-jev` 0.2.2), https://github.com/y0usaf/pi-jev,
by **y0usaf** (MIT, Copyright (c) 2026 y0usaf). The idea, the three surfaces (tool-call gate, output
judge, `jev_ask`), the questions and their measured phrasing, the thresholds and their calibration
tables, the wording of every notice, the `/jev` command and the fail-open policy are y0usaf's.

- Original: `src/client.ts`, `src/config.ts`, `src/gate.ts`, `src/output.ts`, `src/index.ts`
- Pinned commit: `88e5fb3888948e7065110d47cdf6ac57abb71ba4` (release 0.2.2, "failing visibly on a
  response that skips a question"). The npm `latest` is still 0.2.0; this is the reviewed Git commit.
- The unmodified sources are kept in [`port/oracle/src/`](port/oracle/src) as the equivalence oracle, with
  the upstream license at [`port/oracle/LICENSE`](port/oracle/LICENSE), `package.json` and README.

[TypeSafe](https://docs.typesafe.ai) defines Jev (its typed decision model: Noul, Choice, Score).
The HTTP protocol and the own-model backend come from the shared Go client
[`components/typesafe`](../typesafe/CREDITS.md) (ports of TypeSafe's `typesafe-sdk-js` and
`system-one-adapter-python`), which this Package uses; it does not carry a client of its own.

The Go code, the tests and the scenarios were written for this Package by Michael Kinsy. What was
modified relative to the original, and why, is the list of corrections in
[`port/PORT.md`](port/PORT.md) (C1 to C10) and the opt-in, disclosure and trust rules in the README.
Everything else follows the original's behavior, checked against the original under Pi.
