# pi-typesafe (Go port)

[Jev](https://typesafe.ai) inside PiG: TypeSafe's judgment model answers typed questions about some state and returns
probabilities instead of prose. This Package gives PiG three things:

- **A tool for the agent**: `typesafe_evaluate` hands batched Choice, Score and Noul questions to Jev in one call.
- **A playground**: `/typesafe test` and `/typesafe playground` run requests from the terminal without touching the model's context.
- **A typed API for other extensions**: the library half is the Package [`pi-typesafe-api`](../pi-typesafe-api/README.md).

It is a Go port of [pi-typesafe](https://github.com/DevMortimer/pi-typesafe) by Ryan Gapac (MIT; see [CREDITS.md](CREDITS.md)),
built on the shared client [`typesafe`](../typesafe/README.md). Independent project: not affiliated with TypeSafe AI.

## Off by default, and where your content goes

The tool is registered but **disabled**: nothing is sent until the operator runs `/typesafe enable` (and confirms the notice) or
sets `PI_TYPESAFE_ENABLED=1` for a headless run. Every submission goes to **`api.typesafe.ai`** and may incur charges, **unless the
own-model backend is selected**, which answers with the model PiG is configured with and sends the content to *that model's
provider* instead, never to TypeSafe. Select it with `PI_TYPESAFE_BACKEND=ownmodel` before launch or `/typesafe backend ownmodel`
in a session. Consent is per destination: switching backend disables the tool again. The consent dialog and the tool description
name the destination in use. Do not submit secrets; results are model judgments, not proof or authorization.

## Use

```sh
pig install ./components/pi-typesafe        # builds from source on first use (Go toolchain), or fuses into a Piglet Binary
```

Inside PiG: `/typesafe login` (hidden input; the key is verified and stored with owner-only permissions in
`<PiG agent dir>/pi-typesafe/`), `/typesafe test`, `/typesafe enable`. For CI set `TYPESAFE_API_KEY`; it wins over the stored key.
Commands: `login logout setup status enable disable test playground` and `backend [typesafe|ownmodel]`.
Limits: 32 questions and 64 KiB of JSON per request, 20 attempts per session, 15 s per request, no automatic retries; optional
day caps `PI_TYPESAFE_MAX_REQUESTS_PER_DAY`, `PI_TYPESAFE_MAX_INPUT_TOKENS_PER_DAY`, `PI_TYPESAFE_MAX_USD_PER_DAY`.

The **Piglet** [`pig-typesafe`](../../piglets/pig-typesafe/README.md) selects this Package (still off by default).

## Differences from the original

Listed with reasons in [port/PORT.md](port/PORT.md): PiG's agent directory, the own-model backend, question order in tool
arguments (the SDK decodes them into maps), TypeBox-versus-host validation wording, and a backend switch that stops a request
admitted before it (nothing is sent to a destination nobody consented to).

## Proof

`port/` holds the evidence: the unmodified original and its 134 tests (`port/oracle`), Go twins of every test case, 12 scenarios
whose traces the original recorded under Pi (re-recorded on Pi 1.0.1) and the port reproduces under PiG (`port/golden`), and a mutation list.
