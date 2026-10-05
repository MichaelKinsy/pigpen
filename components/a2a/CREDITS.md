# Credits

The `a2a` Package is original work by Michael Kinsy (MIT, see [LICENSE](LICENSE)). It is an adapter
between PiG and the Agent2Agent (A2A) protocol and contains no copied source. It **depends on** and
links the upstream Go SDK:

- **a2a-go**, the A2A Go SDK, `github.com/a2aproject/a2a-go/v2` **v2.6.0**
  (commit `ebf17c56ef7e63c72883a45454a538bbc0df66b8`, tag `v2.6.0`), © The A2A Authors, Linux Foundation,
  licensed under the **Apache License 2.0** ([port/a2a-go-LICENSE](port/a2a-go-LICENSE), unmodified).
  <https://github.com/a2aproject/a2a-go>. The SDK implements the A2A protocol version 1.0
  (<https://a2a-protocol.org>). It is used unmodified: `a2asrv` (server), `a2aclient` and
  `a2aclient/agentcard` (client). Its transitive requirements (`github.com/google/uuid`,
  `golang.org/x/mod`, `golang.org/x/sync`) are recorded in `extensions/a2a/go.mod` and `go.sum`.
  A Piglet Binary that fuses this extension redistributes a2a-go, so the Apache-2.0 text must travel with it.

## Interoperability test target

The interop test drives **kagent**'s A2A server package (`go/adk/pkg/a2a/server`, Apache-2.0,
<https://github.com/kagent-dev/kagent>) from a kagent checkout you provide. `scripts/interop-kagent.mjs`
copies that package into a temporary directory at test time and changes one line (its readiness port);
nothing of kagent is committed here. `port/interop/kagent/main.go` is original glue code (an echo
executor around kagent's server).

## What is not here

No Kubernetes orchestration is ported. kagent is an A2A peer, not a dependency.
