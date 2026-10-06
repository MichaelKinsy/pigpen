# Credits

The `hardening` Package is original work by Michael Kinsy (MIT, see [LICENSE](LICENSE)). It contains no copied
source. It links these libraries, unmodified; each licence text travels with the Package in
[`licenses/`](licenses/):

- **cedar-go**, the Go implementation of the Cedar policy language, `github.com/cedar-policy/cedar-go`
  **v1.8.0** (commit `cda92d0d9345fce26b36366288afd3c909fc7bd8`, tag `v1.8.0`), © Cedar Contributors, licensed
  under the **Apache License 2.0** ([licenses/cedar-go-LICENSE](licenses/cedar-go-LICENSE),
  [licenses/cedar-go-NOTICE](licenses/cedar-go-NOTICE)). <https://github.com/cedar-policy/cedar-go>.
  Used only by the nested module `policy/cedar`. Its module sum was checked against the Go checksum database
  (`h1:9gcU7EHXwHC2RMdpph68yTAkdB3behTTssC+kt4GoS8=`).
- **go-jose**, JSON Web Signature and Key support, `github.com/go-jose/go-jose/v4` **v4.1.5**
  (commit `b10771e937ddcce419e30fc0c2b9e38464a333d0`, tag `v4.1.5`), ©
  Square Inc. and go-jose contributors, licensed under the **Apache License 2.0** ([licenses/go-jose-LICENSE](licenses/go-jose-LICENSE)); its
  `json` package is a fork of the Go standard library's, under the BSD 3-Clause licence
  ([licenses/go-jose-json-LICENSE](licenses/go-jose-json-LICENSE)). <https://github.com/go-jose/go-jose>.
  Used by `resourceserver`.
- **golang.org/x/net**, the Go supplementary network libraries, **v0.59.0**
  (commit `540d04cfe5028e2655754591a4d3e08c586809f2`, tag `v0.59.0`), © The Go Authors, licensed under the
  **BSD 3-Clause licence** ([licenses/x-net-LICENSE](licenses/x-net-LICENSE), [licenses/x-net-PATENTS](licenses/x-net-PATENTS)).
  <https://go.googlesource.com/net>. Used for IDNA conversion (`idna`) of host names, and in tests for a DNS
  message codec (`dns/dnsmessage`).
- **golang.org/x/text**, the Go supplementary text libraries, **v0.42.0**
  (commit `fafe4a06967e06550e69ee42787d9902845d2a3f`, tag `v0.42.0`), © The Go Authors, **BSD 3-Clause**
  ([licenses/x-text-LICENSE](licenses/x-text-LICENSE), [licenses/x-text-PATENTS](licenses/x-text-PATENTS)).
  <https://go.googlesource.com/text>. Required by `x/net/idna`.
- **golang.org/x/exp**, experimental Go packages, version `v0.0.0-20220921023135-46d9e7742f1e`
  (commit `46d9e7742f1ec8239841d61597d0c6ca93b0e0a4`), © The Go Authors, **BSD 3-Clause**
  ([licenses/x-exp-LICENSE](licenses/x-exp-LICENSE), [licenses/x-exp-PATENTS](licenses/x-exp-PATENTS)).
  <https://go.googlesource.com/exp>. Required by cedar-go (`policy/cedar` only).

A Piglet Binary that fuses an extension that uses this Package redistributes the libraries it links, so their licence
texts must travel with it.

## Design inputs

The Package implements the library section of the enterprise-profile plan (`docs/plan/enterprise-profile.md` at commit
`335e637c`, merged as #12, of this repository). No code was taken from any project the plan names as prior
art: Amazon Bedrock AgentCore Policy (the default-deny, forbid-overrides-permit use of Cedar for agent tool calls) and the
Microsoft Agent Governance Toolkit (the split between a policy contract and the engine behind it).
