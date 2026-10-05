# typesafe

Shared Go libraries for the TypeSafe AI evaluation API, for Pigpen's Jev ports:

- `libraries/typesafe`: a Go port of the official TypeScript SDK `@typesafe-ai/sdk` 0.6.0 (typed
  Noul, Choice and Score questions, retries, timeouts, errors, logging, batching helper).
- `libraries/ownmodel`: a second backend that answers the same typed questions with the model PiG is
  configured with (port of `system-one-adapter-python`), in probability and discrete modes, with
  corrective retries on malformed output.
- `libraries/pigmodel`: the `ownmodel.Model` on the PiG Go SDK's model access.

Read [`CONTRACT.md`](CONTRACT.md) for the API contract and every difference from the TypeScript SDK, and
[`port/PORT.md`](port/PORT.md) for the file-by-file upstream mapping, test twins and proof.

This Package has no extension or command of its own; an extension uses it through a `go.work` `use`
entry (see the contract). It uses the standard library only. The TypeSafe API needs `TYPESAFE_API_KEY`;
tests never call it (a fake server stands in).

Credits and licenses: [`CREDITS.md`](CREDITS.md). MIT.
