# Credits

`extensions/rpiv-web-tools` is a Go port of **@juicesharp/rpiv-web-tools** 2.12.0 from **rpiv-mono**,
https://github.com/juicesharp/rpiv-mono, by **juicesharp** (MIT, Copyright (c) 2026 juicesharp).

- Original: `packages/rpiv-web-tools`
- Commit: `7c9bc924c5bfd148f36d7ebc9f7bd0a9469d633f` (release v2.12.0)
- The unmodified original (its tests included, images omitted) is kept at [`port/oracle/`](port/oracle) as the
  equivalence oracle, with the license at [`port/oracle/LICENSE`](port/oracle/LICENSE). `@juicesharp/rpiv-config`
  2.12.0 (MIT, juicesharp), the original's configuration library, is installed from npm into `port/oracle/node_modules`
  and is not copied: its config path resolution, JSON loading, 0600 saving and guidance validation are re-implemented
  in `extensions/rpiv-web-tools/config.go`, and the oracle runs against the real library.
- The truncation of `web_fetch` output (`truncateHead`, `formatSize`) is Pi's, from `@earendil-works/pi-coding-agent`
  (MIT, Mario Zechner); `port/gen-differential.mjs` compares the Go version with Pi's own.

The Go modules `golang.org/x/net` (its `idna` package reads an internationalized host name as Node's URL parser does)
and `golang.org/x/text` are The Go Authors' (BSD-3-Clause), resolved as module dependencies and not copied here.

The Go code, the scenarios and the tests were written for this Package by Michael Kinsy. Wording, tool schemas,
prompt guidance, the provider request shapes and their error messages follow the original. Modified paths: none of
the original is modified; the port is a separate implementation. Not ported: the GitHub URL interceptor (see
[port/PORT.md](port/PORT.md), slice 2).
