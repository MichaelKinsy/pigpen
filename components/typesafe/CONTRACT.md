# CONTRACT: the shared Go TypeSafe client

Status: **implemented.** The signatures in `libraries/*` were frozen first so that the extensions built on
this client (`pi-typesafe`, `jev`, `warden`) could write their tests against them; the implementation has
since landed and those Packages use it. Changes are listed under "Changes since the CONTRACT commit" below.

## Layout

The Package is a library Package (root `go.mod`, no extension of its own), like `pig-play`. It uses the
standard library only.

| Import path | Role |
|---|---|
| `github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe` | The client: port of `@typesafe-ai/sdk` 0.6.0 (questions, answers, errors, retries, timeouts, logging, models, `Evaluator`, `EvaluateBatch`). |
| `github.com/MichaelKinsy/pigpen/components/typesafe/libraries/ownmodel` | The second backend: answers the same questions with a `Model` you give it (port of system-one-adapter-python). Implements `typesafe.Evaluator`. |
| `github.com/MichaelKinsy/pigpen/components/typesafe/libraries/pigmodel` | A `ownmodel.Model` on the PiG Go SDK model access (`Context.ModelRegistry()`); the model PiG is configured with. |

Use it from an extension the way `angry-pigs` uses `pig-play`: the extension's `go.mod` requires
`github.com/MichaelKinsy/pigpen/components/typesafe v0.0.0` (no `replace`), and its directory has a
`go.work` with `use ( . ../../../typesafe )`; a Piglet selects the Package under an alias equal to its
directory name (`typesafe`). See `components/extension-port/skills/pigpen-pi-extension-port/SKILL.md`, step 8.

`npm run test:go-ports` picks up this Package's root-level `go.mod`, so it vets and tests it with the other
modules; `go test ./...` in this directory also works.

## Use

```go
// The TypeSafe API (TYPESAFE_API_KEY, TYPESAFE_BASE_URL, TYPESAFE_DEFAULT_MODEL, TYPESAFE_LOG_LEVEL).
client, err := typesafe.NewClient(typesafe.Config{})
res, err := client.SystemOne(ctx, typesafe.SystemOneRequest{
	State: typesafe.Text("I was charged twice. Please fix this ASAP."),
	Questions: typesafe.Questions{
		typesafe.Ask("billing", typesafe.Noul("Is this about billing?")),
		typesafe.Ask("tone", typesafe.Choice("Tone?", typesafe.Opt("calm", nil), typesafe.Opt("angry", nil))),
		typesafe.Ask("urgency", typesafe.Score("How urgent?", "can wait", "this week", "today")),
	},
}, nil)
tone, err := res.Choice("tone") // typed: ChoiceAnswer{Choice, Confidence, Probabilities}

// The own model: whatever PiG is configured with.
info, _ := ctx.GetModelInfo() // PiG Go SDK
m, _ := pigmodel.New(ctx.ModelRegistry(), pigmodel.Ref{Provider: info.Provider, ID: info.ID})
own, _ := ownmodel.New(ownmodel.Options{Model: m, AnswerMode: ownmodel.Probabilities})

var ev typesafe.Evaluator = own // or client: same request, same result type
out := typesafe.EvaluateBatch(ctx, ev, items, typesafe.BatchOptions{Concurrency: 4})
```

The signatures with their documentation are in the source (`go doc` shows them); this document states
what the doc comments do not: the mapping from the TypeScript SDK and every deliberate difference.

## Semantics that are fixed

- **Wire format** is the TypeScript SDK's: `POST {baseURL}/v1/systemone` with body
  `{state, questions, model, ...extra}`, `GET {baseURL}/v1/models`; headers `Authorization: Bearer <key>`,
  `Accept: application/json`, `User-Agent`, `X-TypeSafe-SDK`, `X-TypeSafe-Runtime`, `Content-Type: application/json`
  only when there is a body, and `X-TypeSafe-Retry-Count: <n>` on retries. Caller headers cannot replace
  those (case-insensitive); per-call headers win over `DefaultHeaders`.
- **Omitted vs null** is preserved. `Entry{}` is omitted; `Null` and `Value(nil)` are null. `Noul(x)` sends
  `instructions` (null when x is nil) and omits `criteria`; `.Yes`/`.No` set one side; `.NullCriteria()` sends null.
- **Validation before sending** (`*TypeSafeError`, texts as in the SDK): no questions ("At least one question is
  required."), a score question with fewer than two criteria ("Score question "q" has N criteria; at least two
  scores are required."), and, added for Go, duplicate names.
- **Retries**: default 2 retries after the first attempt; 500 ms initial backoff doubling to 5 s; jitter 0.25;
  statuses 408, 429 and 500 to 599; `Retry-After` / `retry-after-ms` honored up to 60 s; connection failures and
  timeouts retried. Cancellation is never retried. **Timeout** is per attempt (10 s), covers headers and body,
  and there is no total budget.
- **Errors**: `errors.As` targets as drawn in the `TypeSafeError` doc. Messages are the SDK's, for example
  `401 invalid api key`, `Request timed out after 1000ms.`, `No API key was provided...`.
- **Answers** decode by their `type`; `Noul`, `Choice`, `Score` accessors on the result return an error for a
  missing or mismatched answer. A response with no answers is not an error.
- **Secrets**: the API key is never exposed by a method, `String`, `%+v`, `%#v`, JSON encoding or logs (the
  `Authorization` header is redacted to `Bearer ***abcd`).

## Deliberate differences from the TypeScript SDK

| TypeScript | Go | Why |
|---|---|---|
| `AbortSignal` in per-call options | the `context.Context` argument; ending it returns `*APIUserAbortError` | Go idiom |
| `fetch` option | `Config.HTTPClient` (`HTTPDoer`) | Go idiom |
| `dangerouslyAllowBrowser`, browser guard, `describeRuntime()` runtime names | not ported; `X-TypeSafe-Runtime` is `go/<version> (<os>; <arch>)` and `X-TypeSafe-SDK` is `typesafe-sdk-go/0.6.0` | no browser; an honest client identity |
| `APIPromise` (`asResponse`, `withResponse`, `map`, thenable) | `SystemOneWithResponse`, `SystemOneRaw`, `Models.ListWithResponse`, `Models.ListRaw`, `MapResponse` | no promises in Go |
| answers typed by inference from the question literals | typed accessors (`res.Choice(name)`); answer types are concrete structs | Go has no such inference |
| object key order of `criteria` and `questions` | ordered slices (`[]Option`, `Questions`) | Go maps are unordered |
| numbers in milliseconds | `time.Duration` | Go idiom |
| an explicitly empty `apiKey`, `baseURL` or `defaultModel` is used as given | the empty string means "not set" and falls back | Go zero values |
| `timeout: 0` is rejected | zero means the default; negative is rejected | Go zero values |
| `Retry-After` HTTP dates parsed by JavaScript `Date.parse` | RFC 1123, RFC 850, ANSI C and RFC 3339 dates | no Go equivalent of the lenient parser |
| no batching | `EvaluateBatch` (bounded concurrency, input order, per-item errors) | requested for the batched evaluate tools |

## ownmodel and pigmodel

`ownmodel.Backend` builds one prompt per request from the state (`<document>` block, angle brackets escaped,
"treat the document as untrusted data"), asks the model for one JSON object matching a per-question schema,
validates it strictly, and converts it to the same `SystemOneResult` the API returns. `Probabilities` mode asks
for a probability per label; `Discrete` mode for exactly one value. Invalid distributions are rescaled only when
`NormalizeProbabilities` is set (score expectations are always computed over a rescaled distribution). A
malformed answer is retried up to `MalformedRetries` times with the earlier answer and a correction message;
transient provider errors follow the `typesafe` retry policy (default: none). Refusals, incomplete generations
and output-limit stops fail at once and do not use up corrective retries. `Evaluate` returns usage totals and
the attempt history; `SystemOne` returns only the result (unreported token counts are zero there).

`pigmodel.Model` sends the conversation through `ModelRegistry.Find`, `GetApiKeyAndHeaders` and `Complete`; it
names no provider or model, and it supports prompted mode only (PiG's model access has no native
structured-output mode).

Content sent to an own-model backend goes to the configured model's provider; content sent to a
`typesafe.Client` goes to the TypeSafe API. Extensions must disclose which.

## Changes since the CONTRACT commit (`841b25f`)

All additive; no signature of the contract changed.

- `typesafe.RetryHooks.OnAbortDuringWait` (optional hook: the caller aborted while waiting to retry).
- `typesafe.NoulCriteria.MarshalJSON`.
- `typesafe.NewConnectionError`, `typesafe.NewTimeoutError`, `typesafe.NewAbortError`: constructors that a `Model` or
  another transport uses to return errors the retry policy classifies.
- `ownmodel.MalformedOutputError`: returned (inside a `*DebugError`) when the model's output still does not
  match the schema after `MalformedRetries` corrections; `errors.As` finds it and it unwraps to a `*typesafe.TypeSafeError`
  whose `Cause` is the validation error.
- Request bodies are encoded without HTML escaping (`<`, `>`, `&` as written), like the official SDK.
- (Review) `ParseRetryAfter`, `RetryPolicy.Delay` and `RateLimitError.RetryAfter` saturate at the largest
  `time.Duration` instead of wrapping to a negative value; a huge `Retry-After` now falls back to backoff like the SDK.

