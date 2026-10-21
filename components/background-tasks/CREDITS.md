# Credits

`extensions/pi-background-tasks` is a Go port of **@vanillagreen/pi-background-tasks** 2.2.0,
https://github.com/vanillagreencom/kendex (directory `pi-extensions/pi-background-tasks`), by **vanillagreen**
(MIT, Copyright (c) 2026 vanillagreen).

- Commit: `bd611b212d2bec33aac5b62642b6aba21100d7f8`. At that commit the `extensions/` tree is byte-identical to the
  published npm tarball `@vanillagreen/pi-background-tasks@2.2.0` (integrity
  `sha512-wIpmFABX1fPKODRducUXXu+Au4mK6TJeKj/8Kl2U924ntd7BkqY31iWgvcKmSAD7GxPtQAC8mtz+Vu9+R9MAMA==`).
- The unmodified original (its tests, scripts and `instructions.md` included, images omitted) is kept at
  [`port/oracle/`](port/oracle) as the equivalence oracle, with its license at
  [`port/oracle/LICENSE`](port/oracle/LICENSE).
- The original is, in its own words, "based on ideas and portions of" **@ifi/pi-background-tasks** by **Ifiok Jr.**
  (MIT, Copyright (c) 2025 Ifiok Jr.), https://github.com/ifiokjr/oh-pi/tree/main/packages/background-tasks. Its notice is
  kept in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and in the oracle.
- The tool descriptions, the tool schemas, the prompt guidelines and `instructions.md` are copied from the original byte
  for byte (`extensions/pi-background-tasks/tooldefs.json`, `instructions.md`). That wording is vanillagreen's.

The Go code, the scenarios and the layer-1 tests were written for this Package by Michael Kinsy. The test cases of the
original are ported as twins (`port/upstream-tests.json` lists their titles). Wording and behavior follow the original.
Modified paths: none of the original is modified; the port is a separate implementation. What differs is listed in
[README.md](README.md) and [port/PORT.md](port/PORT.md).
