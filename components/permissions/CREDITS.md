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
