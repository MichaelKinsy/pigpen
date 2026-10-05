# Credits

`extensions/rpiv-ask-user-question` is a Go port of **@juicesharp/rpiv-ask-user-question** 2.11.0 from **rpiv-mono**,
https://github.com/juicesharp/rpiv-mono, by **juicesharp** (MIT, Copyright (c) 2026 juicesharp).

- Original: `packages/rpiv-ask-user-question`
- Commit: `61904e69e1a50e12585bdf15f0310e633a62ba36` (release v2.11.0)
- The unmodified original (its tests included, images omitted) is kept at
  [`port/oracle/`](port/oracle) as the equivalence oracle, with the license at
  [`port/oracle/LICENSE`](port/oracle/LICENSE). `@juicesharp/rpiv-config` 2.12.0 (MIT, juicesharp), the
  original's configuration library, is installed from npm into `port/oracle/node_modules` and is not
  copied; the port's `config.go` is the todo port's, which was written against its `config.ts` at the pinned commit.

The Go code, the scenarios and the tests were written for this Package by Michael Kinsy. Wording, tool schema,
prompt guidance and behavior follow the original. Modified paths: none of the original is modified; the port is a
separate implementation. Not ported: the locales (E1, English only: approved by the owner), the interactive questionnaire component and the overlay
collapse key (see [port/PORT.md](port/PORT.md)).
