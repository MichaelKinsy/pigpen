# Credits

`extensions/pi-permission-system` is a Go port of **@gotgenes/pi-permission-system** 39.0.3,
https://github.com/gotgenes/pi-packages/tree/main/packages/pi-permission-system, by **MasuRii and Christopher D. Lasher**.

- License: MIT, from the original's `LICENSE` file, kept unmodified at
  [`port/oracle/packages/pi-permission-system/LICENSE`](port/oracle/packages/pi-permission-system/LICENSE) and used as the
  upstream `licenseFile` in `provenance.json`.
- Commit: `c5bc74712cd2fe808d70246d93d26a092f736676` of gotgenes/pi-packages.
- The original package and the repository files it builds with (no other package of the repository) are kept unmodified at
  [`port/oracle/`](port/oracle) as the equivalence oracle; `port/oracle/UPSTREAM.md` is a note added by this Package.

The Go code, the scenarios and the tests were written for this Package by Michael Kinsy. Wording, rule semantics (wildcards,
last match wins, scope merging, the denial text) and behavior follow the original; none of the original is modified, and the port
is a separate implementation. This is a **partial** port: see [port/PORT.md](port/PORT.md).

## bashparse (third-party material)

`extensions/pi-permission-system/bashparse` parses bash exactly as the original does, which needs the parser the original runs.

- **tree-sitter-bash** 0.25.1, https://github.com/tree-sitter/tree-sitter-bash, MIT, by Max Brunsfeld and Amaan Qureshi
  ([`LICENSE-tree-sitter-bash`](extensions/pi-permission-system/bashparse/internal/ts/LICENSE-tree-sitter-bash)). Its generated
  parse tables are embedded (`bash_tables.bin.gz`, dumped from its `src/parser.c` by `port/bashparse-diff/gen/dumptables.c`),
  its generated lexer is transpiled to Go (`lex_gen.go`, by `gen/genlex.py`), and its external scanner (`src/scanner.c`) is
  ported to Go (`bash_scanner.go`).
- **tree-sitter** 0.26.9 (commit `7f534862c3ec939c3a6ee147f7600ef5c1bf900f`), https://github.com/tree-sitter/tree-sitter, MIT, by
  Max Brunsfeld and contributors ([`LICENSE-tree-sitter`](extensions/pi-permission-system/bashparse/internal/ts/LICENSE-tree-sitter)).
  `bashparse/internal/ts` is a port to Go of the runtime's `lexer.c`, `subtree.c`, `stack.c`, `parser.c`, `language.c` and `node.c`
  (the web-tree-sitter 0.26.9 runtime the original uses is built from it); it cites the C function it ports.
- The wide-character classes the scanner calls (`wctype_gen.go`) are recorded from the original by probing, not copied from a libc.
- No third-party Go module is added: the package uses the standard library only.
