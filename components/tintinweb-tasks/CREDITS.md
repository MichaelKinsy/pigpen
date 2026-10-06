# Credits

`extensions/tintinweb-tasks` is a Go port of **@tintinweb/pi-tasks** 0.9.0,
https://github.com/tintinweb/pi-tasks, by **tintinweb** (MIT, Copyright (c) 2026 tintinweb).

- Commit: `00ecbd8110f1a4e267791f9cecb2612144c78f6e` (release v0.9.0)
- The unmodified original (its tests included, images omitted) is kept at [`port/oracle/`](port/oracle) as the
  equivalence oracle, with the license at [`port/oracle/LICENSE`](port/oracle/LICENSE).
- The seven task tools mirror Claude Code's tool specifications; their descriptions are copied from the original
  byte for byte (`extensions/tintinweb-tasks/descriptions.go`). That wording is tintinweb's.

The Go code, the scenarios and the layer-1 tests were written for this Package by Michael Kinsy. The 386 test
cases of the original are ported as twins (`port/upstream-tests.json` lists their titles). Wording, behavior and
the event-bus protocol with `@tintinweb/pi-subagents` follow the original. Modified paths: none of the original
is modified; the port is a separate implementation. What differs is listed in [README.md](README.md) and
[port/PORT.md](port/PORT.md).

`@tintinweb/pi-subagents` (MIT, tintinweb), the other half of the bus protocol, is ported separately as the
[`tintinweb-subagents`](../tintinweb-subagents/README.md) Package.
