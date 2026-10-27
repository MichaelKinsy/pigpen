# pi-opencode-free Package

A PiG model provider for the **free models on OpenCode Zen**, with no API key.
It is a Go port of
[pi-opencode-free](https://github.com/pedrostarkealexis/pi-opencode-free) 0.2.0
by Pedro Alexis, under the GPL-3.0-or-later; see [CREDITS.md](CREDITS.md).
Models appear as `opencode-free/<model>`.

The provider discovers the free ids from OpenCode's Zen endpoint and describes
them from [models.dev](https://models.dev). It sends the OpenCode client headers
(`x-opencode-client`, `x-opencode-project`, `User-Agent`) on every request, so
the free tier accepts a keyless call. It never sends a bearer token.

**It does nothing until you use it.** It is not part of any Piglet and makes no
network call at start-up.

## Install

From the Pigpen checkout root:

```sh
pig package validate ./components/pi-opencode-free
pig install ./components/pi-opencode-free
```

Or load the source without installing:

```sh
pig -e ./components/pi-opencode-free/extensions/pi-opencode-free
```

## Use

Start `pig` interactively (PiG refreshes provider catalogs when a session starts)
or run a command that needs the catalog. Then pick a `(Free)` model, or name one
directly:

```sh
pig --list-models opencode-free
pig --model opencode-free/hy3-free -p "Say hello"
```

The catalog is stored after a successful refresh, so later runs, including
one-shot `pig -p`, find their models without asking Zen again. An empty or failed
refresh keeps the previous snapshot instead of wiping it.

## What it does

- **Discovery** reads `https://opencode.ai/zen/v1/models` and keeps the ids
  matching `*-free` or `big-pickle`. Display names, reasoning support, context
  window, output limit and image input come from
  `https://models.dev/api.json` under the `opencode` provider. A model with no
  models.dev entry gets conservative defaults: no reasoning, a 128,000 token
  context window and a 16,384 token output limit.
- **Thinking** follows models.dev `reasoning_options`: effort values become
  Pi's `thinkingLevelMap`; a model without them defers to the provider default.
- **Chat** uses Zen's OpenAI-compatible `/chat/completions` endpoint. Streaming
  text, reasoning (`reasoning_content`), tool calls and usage are supported.
  The request follows the original's compat flags: `max_tokens`, no `store`,
  `system` rather than `developer` messages, and an empty `reasoning_content`
  on assistant messages that have none.
- **Responses** models (the ones models.dev routes through `@ai-sdk/openai`)
  use `/responses` with reasoning, output text and function calls.
- **Headers** are the OpenCode client identity only. `Authorization` is not sent
  at all; a caller-provided `Authorization: null` is accepted and removes it.
- **Errors** include Zen's HTTP status and response body, a timeout, or the SSE
  error object. Cancelling a turn cancels the HTTP request.

## Configuration

No key or environment variable is required. Provider headers can be overridden
through the normal request `headers` option; a `null` value removes a default
header. `PI_OFFLINE=1` (or `--offline`) keeps PiG from refreshing catalogs on its
own.

## Limits

- Only chat and Responses turns are implemented; there is no image generation,
  classifier, embeddings or model management.
- Only the free tier is targeted. Paid OpenCode plans and API keys are out of
  scope, and no bearer token is ever sent.
- The model catalog is discovery-driven. A model that Zen stops advertising
  disappears at the next successful refresh; the previous snapshot remains until
  then.
- The Go stream clients implement the subset of Pi's OpenAI engines that these
  free models use. They do not carry PiG-internal provider features such as
  deferred generation, prompt-cache controls or grammar-constrained tools.

## Tests

`go test ./...` in `extensions/pi-opencode-free` runs discovery, refresh,
Chat Completions and Responses tests against local HTTP servers; it needs no
network. The tests are in the Go module and use the PiG public SDK.

## License

GPL-3.0-or-later. The original and its license are retained in
[`port/oracle/`](port/oracle); the Go implementation and tests are in this
Package. See [CREDITS.md](CREDITS.md) and [`port/PORT.md`](port/PORT.md).
