# Port: pi-opencode-free

**Kind:** 1, a Pi TypeScript extension. **Oracle:** the original under Pi. **Target:**
PiG 0.5.3, Go on the public extension SDK.

## Pin

| | |
| --- | --- |
| Upstream | [pi-opencode-free](https://github.com/pedrostarkealexis/pi-opencode-free) |
| Commit | `0f4c16bade33106847ae4abd8d2be2d1b0058e36` (tag `v0.2.0`) |
| License | GPL-3.0-or-later, Copyright (c) 2026 pi-opencode-free contributors |
| Author | Pedro Alexis |
| Vendored original | [`port/oracle/`](oracle) (source, tests, README, license) |
| Target Package | `components/pi-opencode-free` |
| Go module | `components/pi-opencode-free/extensions/pi-opencode-free` |

## What the original does

The original registers one native Pi provider, `opencode-free`, with an empty
catalog. Its `refreshModels` restores a persisted snapshot when the host does not
allow network, otherwise discovers free Zen ids (enriched by models.dev) and
publishes a stamped snapshot. Requests go through Pi's native
`openai-completions`/`openai-responses` engines; a thin `streamSimple` shim adds
the OpenCode client headers and omits `Authorization` for the keyless free tier.

## Mapping

| ID | Upstream file and symbol | Behavior | Go operation | Test | Disposition |
| --- | --- | --- | --- | --- | --- |
| D1 | `src/discovery.ts` `FREE_REGEX` | Keep ids ending `-free` or named `big-pickle` | `freeModelID` | `discovery_test.go` | mapped |
| D2 | `src/discovery.ts` `filterFreeModels` | Defaults: no reasoning, 128k context, 16,384 output | `filterFreeModels` | `TestFilterFreeModelsUsesConservativeDefaults` | mapped |
| D3 | `src/discovery.ts` `buildThinkingLevelMap` | Effort levels become a Pi thinking map; off is hidden unless offered or toggled | `buildThinkingLevelMap` | `TestDiscoverModelsEnrichesFromModelsDev` | mapped |
| D4 | `src/discovery.ts` `discoverModels` | Zen + models.dev fetched in parallel with a boot budget; any failure is an empty list | `discoverModels` | discovery tests | mapped |
| D5 | `src/discovery.ts` models.dev routing | `@ai-sdk/openai` means the Responses API | `modelInfo.API` | `TestDiscoverModelsEnrichesFromModelsDev` | mapped |
| R1 | `src/index.ts` provider registration | provider id, name, base URL, empty catalog, keyless auth | `newProvider` | `TestProviderStartsWithEmptyCatalog`, `TestProviderAuthIsKeylessAndCarriesHeaders` | mapped |
| R2 | `src/index.ts` `refreshModels` cache path | Restore stored models, strip provider/baseUrl, ignore foreign APIs | `restore` | `TestCacheOnlyRefreshRestoresSnapshot`, `TestRestoreDropsForeignAndBrokenModels` | mapped |
| R3 | `src/index.ts` `refreshModels` network path | Discover, convert, persist `{models, checkedAt}`, publish update | `RefreshModels` | `TestNetworkRefreshDiscoversAndPersistsSnapshot` | mapped |
| R4 | `src/index.ts` empty discovery | Never wipe a good snapshot | `RefreshModels` early return | `TestNetworkRefreshNeverWipesGoodSnapshot` | mapped |
| R5 | `src/index.ts` aborted refresh | Return no models without fetching | `RefreshModels` signal check | `TestNetworkRefreshRespectsAbortBeforeFetching` | mapped |
| R6 | `src/index.ts` headers | `x-opencode-client: cli`, `x-opencode-project: global`, `User-Agent: opencode/0.0.0-dev`, no Authorization | `providerHeaders`, `do` | chat/response request tests | mapped |
| S1 | Pi's native `openai-completions` engine | Messages, tools, `max_tokens`, reasoning effort, SSE text/reasoning/tool calls/usage | `chat.go` | `chat_test.go` | mapped (own stream client) |
| S2 | Pi's native `openai-responses` engine | Input items, tools, reasoning, function calls, usage | `responses.go` | `responses_test.go` | mapped (own stream client) |
| S3 | Pi's `onPayload` hook | Replace the request body | `invokePayload` | `TestChatPayloadHookCanReplaceBody` | mapped |
| S4 | Pi's `onResponse` hook | Observe status and headers before the body | `observeResponse` | request tests (implicitly) | mapped |
| S5 | Cancellation | Cancel the HTTP request and report `aborted` | request context | `TestChatAbort`, `TestResponsesAbort` | mapped |
| S6 | Errors | Zen HTTP errors and SSE error objects become the error event | `httpError`, parsers | `TestChatHTTPErrorBecomesErrorEvent`, `TestResponsesErrorEventBecomesError` | mapped |

## Deliberate differences and limits

- The original delegates the transport to Pi's native engines. This port writes
  the two OpenAI stream clients in Go, because PiG's public SDK does not expose
  its internal `ai` package to extension modules. The request/stream behavior is
  narrowed to what these free models need: no prompt-cache controls, deferred
  generation, grammar tools, or provider-specific thinking formats.
- Discovery remains intentionally **without an offline catalog**: every failure
  yields an empty list, and a previous snapshot is preserved by the provider's
  refresh path.
- Provider-level headers are also returned in the auth result, because the PiG
  host's native-provider carrier does not otherwise forward the declaration's
  headers to requests.
- The original's compatibility shims for `pi-cache-optimizer` are not relevant
  here; the request is built in this extension, so it never contains
  `prompt_cache_retention` and it never renames `max_tokens`.

## Evidence

- `go test ./...` in the Go module: discovery, refresh, Chat Completions and
  Responses, HTTP errors, aborts and request shapes; `-race -count=5` passes.
- `GOOS=windows`, `GOOS=darwin` and `GOOS=linux` `go vet` pass.
- A mutation set is in `port/mutations.json` (16 deliberate defects across
  discovery, refresh, headers, Chat Completions and Responses). It has not been
  run yet, because the `pigeq` harness and the Pi oracle were not available in
  this lane.
- Not yet done: a golden differential run of the original under Pi against this
  port and the mutation run. Those are required before this row moves from
  `review` to `done`; it is currently `porting`.
