# Credits

`extensions/posthorse` is a Go port of **pi-posthorse** 0.10.11, https://github.com/fitchmultz/pi-posthorse, by
**Mitch Fultz** (MIT, Copyright (c) 2026 Mitch Fultz).

- Commit: `c8596fff2cec7123ef37486fd9701a24019c934f` (release v0.10.11)
- The original's runtime sources (`index.ts`, `ui.ts`, `src/`), its tests, documentation, `package.json` and license are
  kept unmodified at [`port/oracle/`](port/oracle) as the equivalence oracle, with the license at
  [`port/oracle/LICENSE`](port/oracle/LICENSE). Images and CI files are omitted.
- The tool descriptions, guidance and reminder text, and the card wording are the original's, copied byte for byte
  into the Go sources. That wording is Mitch Fultz's.

The Go code, the scenarios and the layer-1 tests were written for this Package by Michael Kinsy. Test cases of the
original are ported as twins (`port/upstream-tests.json` lists their titles). What differs is listed in
[README.md](README.md) and [port/PORT.md](port/PORT.md).
