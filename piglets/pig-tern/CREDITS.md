# Credits

The `pig-tern` Piglet and its frontend member are original work by Michael Kinsy (MIT, see the repository
[LICENSE](../../LICENSE)). The member contains no copied source. It **depends on** and links Stencil's official
Go SDK for the Tern Surface Protocol:

- **tern-sdk**, the Tern Go SDK, `github.com/stencil-hq/tern-sdk/go/tern` **v0.1.0**
  (commit `46bd7df153d3135d84a0b5658dfb4705d0bddb81`, tag `go/tern/v0.1.0`), © Stencil Labs, Inc.,
  licensed under the **MIT License** ([licenses/tern-sdk-LICENSE](licenses/tern-sdk-LICENSE), unmodified).
  <https://github.com/stencil-hq/tern-sdk>. It is used unmodified. Its requirements (`golang.org/x/sys`,
  `golang.org/x/term`, `golang.org/x/text`, BSD-3-Clause, The Go Authors) are recorded in `frontend/go.mod`
  and `go.sum`. A Piglet Binary that fuses this member redistributes tern-sdk, so the MIT text must travel with it.
