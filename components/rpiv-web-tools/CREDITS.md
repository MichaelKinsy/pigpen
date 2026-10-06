# Credits

`extensions/rpiv-web-tools` is a Go port of **@juicesharp/rpiv-web-tools** 2.12.0 from **rpiv-mono**,
https://github.com/juicesharp/rpiv-mono, by **juicesharp** (MIT, Copyright (c) 2026 juicesharp).

- Original: `packages/rpiv-web-tools`
- Commit: `7c9bc924c5bfd148f36d7ebc9f7bd0a9469d633f` (release v2.12.0, 2026-09-30)
- The unmodified original (its tests included, images omitted) is kept at
  [`port/oracle/`](port/oracle) as the equivalence oracle, with the license at
  [`port/oracle/LICENSE`](port/oracle/LICENSE). `@juicesharp/rpiv-config` 2.12.0 (MIT, juicesharp), the
  original's configuration library, is installed from npm into `port/oracle/node_modules` and is not copied;
  its `config.ts` at the pinned commit is identical to the published file.

The Go code, the scenarios and the tests were written for this Package by Michael Kinsy. Wording, tool
schema, provider precedence and behavior follow the original. Modified paths: none of the original is
modified; the port is a separate implementation. Not ported and not yet ported: see
[port/PORT.md](port/PORT.md) for the per-slice scope, the named gaps and the deferred work.
