# Credits

`extensions/acp/cmd/pig-acp` is a Go port of **pi-acp**, https://github.com/svkozak/pi-acp, by
**Sergii Kozak** (MIT, Copyright (c) 2025 Sergii Kozak): the Agent Client Protocol adapter for the Pi
coding agent, which the port adapts to PiG.

- Original: `src/` and `test/` of pi-acp 0.0.34, commit `b0581c9c1d675e634234674484247008b03d69b4`.
- The unmodified original, with its README, `package.json` and license, is kept at
  [`port/oracle/pi-acp`](port/oracle/pi-acp) as the oracle the Go tests were ported from. Its license is at
  [`port/oracle/pi-acp/LICENSE`](port/oracle/pi-acp/LICENSE).
- What was modified: nothing of the original is modified in place. The port is a separate Go implementation with the
  same structure (`agent`, `session`, `translate`, `pi-rpc`), PiG's directories and executable in place of Pi's,
  the adapter's own JSON-RPC transport in place of the ACP SDK, and the additions listed in
  [`port/PORT.md`](port/PORT.md). The ported tests keep the original test names.

The protocol is the **Agent Client Protocol**, https://agentclientprotocol.com. The JSON schema in
[`port/schema/schema.json`](port/schema/schema.json) is `schema/schema.json` of
`@agentclientprotocol/sdk` 0.26.0 by **Zed Industries** (Apache-2.0, https://github.com/agentclientprotocol/typescript-sdk,
tag `v0.26.0`, commit `73bc30649b650de320340c782733bf69a545bd28`), unmodified, with its license at
[`port/schema/LICENSE`](port/schema/LICENSE). It is used only to check the adapter's messages in tests.

The Go code, the scenarios and the tests were written for this Package by Michael Kinsy and are MIT
licensed like the rest of Pigpen.
