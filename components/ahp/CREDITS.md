# Credits

`extensions/ahp` is a Go port of **pi-ahp**, https://github.com/Qusic/pi-ahp, by **Bang Lee**
(Qusic; MIT, Copyright (c) 2026 Bang Lee).

- Original: the `src/` tree of pi-ahp, a standalone host that embeds Pi and serves it over
  Microsoft's Agent Host Protocol; npm package `pi-ahp`.
- Revision: commit `4065e98309b03c1d2d916300856c3c21a9c10674`.
- The unmodified `src/`, `test/` and metadata are kept in [`proof/oracle`](proof/oracle) as the
  equivalence oracle, with the upstream license at [`proof/oracle/LICENSE`](proof/oracle/LICENSE).
  The 430 leaf test cases of the original are listed in
  [`upstream-tests.json`](extensions/ahp/testdata/upstream-tests.json); each has a Go twin or a named skipped
  twin ([`proof/PORT.md`](proof/PORT.md)).

The protocol is **Agent Host Protocol** (AHP) by **Microsoft Corporation**,
https://github.com/microsoft/agent-host-protocol (MIT, Copyright (c) Microsoft Corporation),
protocol 0.9.0, commit `296b25e7b698a4a84a0ee5a28d9573e70048a0bf`. Its Go types and reducers
(`clients/go`, `ahp` and `ahptypes`) are vendored byte for byte, tests excluded, in
[`extensions/ahp/third_party/agent-host-protocol-go`](extensions/ahp/third_party/agent-host-protocol-go)
with the Microsoft license. The five protocol JSON schemas used by the tests are copied from the
same commit into `extensions/ahp/internal/testkit/schema`, and the recorded Pi event streams in
`extensions/ahp/internal/mapper/testdata/fixtures` come from pi-ahp's test fixtures.

The Go code, the scenarios and the tests were written for this Package by Michael Kinsy. The
behavior, wording and structure of the host follow the original. Modified paths: none of the
originals is modified; the port is a separate implementation, and its deviations are listed in
[`proof/PORT.md`](proof/PORT.md).
