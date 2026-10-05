# Credits

`extensions/pi-subagents` is a Go port of **pi-subagents** 0.73.1, https://github.com/nicobailon/pi-subagents, by **Nico Bailon**.

- License: MIT, from the original's `LICENSE` file, kept unmodified at [`port/oracle/LICENSE`](port/oracle/LICENSE) and used as
  the upstream `licenseFile` in `provenance.json`.
- Commit: `8a403efba6975988cc0488ec8bb941db5ef1a19e` of nicobailon/pi-subagents.
- The original is kept unmodified at [`port/oracle/`](port/oracle) as the equivalence oracle (one image is left out; see
  `port/oracle/UPSTREAM.md`). The agent definitions in `extensions/pi-subagents/builtin/` are the original's `agents/*.md`,
  byte for byte, and so are the tool descriptions and schemas in `tooldefs.json`, which are read from a request the original made.

The Go code, the scenarios and the tests were written for this Package by Michael Kinsy. Behavior, wording and file formats
follow the original; the original's code is not modified, and the port is a separate implementation. This is a **partial** port:
see [port/PORT.md](port/PORT.md).
