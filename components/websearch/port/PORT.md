# Port record: websearch (pi-web-access 0.33.0, first slice)

Original: **pi-web-access 0.33.0** by Nico Bailon (MIT), commit `9a734ed195da2f4cccc2fb5e7128f6774a380f47`, kept
unmodified in [`oracle/`](oracle). Built against **PiG 0.3.0** (Pi 0.87.1 API), on the public Go SDK only.
Credits: [`../CREDITS.md`](../CREDITS.md).

## Status: the first slice, not the whole port

Search, fetch and source retention are ported and twinned. Everything else is a **named skipped test**:
`pigeq twins check --ledger port/upstream-tests.json --go extensions/websearch` accounts for all
875 upstream test titles (305 exact twins, 570 named skips, each with a reason). Do not call this port complete.

| Slice 1 (done) | Deferred (named skips) |
|---|---|
| Tools `web_search` (workflow `none`), `fetch_content` (readable/raw, images), `get_search_content`, `source_check`, `web_enable` and the `/search` command; dynamic activation with transcript-based selection; tool renaming and registration gates; schemas hash-identical to the original | Curator UI and every command but `/search`; `summary-review` / `auto-summary` workflows; activity widget and shortcut; TUI render hooks |
| Providers Exa (keyed and keyless MCP), Brave, Tavily (key pool), Perplexity, DuckDuckGo, SERPdive, Kagi (search); routing (`auto`, `all`, arrays, `searchRouting`, `allowedProviders`), error classification, credential sources | The other 26 providers (OpenAI/Codex hosted search, Gemini, xAI, Jina Search, Firecrawl, ...): in the provider table; selecting or reaching one is refused (see "Refusals") |
| SSRF guard, redirect limits, domain policy, proxy scoping, private 0600 fetch cache with TTL, retained sources restored from the session, inline data-URI sanitiser, config loading | PDF text extraction (config is read), YouTube/video/frames, GitHub clone, RSC/Defuddle fallbacks, hosted fetch providers other than Jina, authenticated fetch, Gemini Web and browser cookies |

## Refusals (until the slices above are ported)

Every unported option, mode or URL kind is refused with one message and returns a tool error (`refusal.go`,
`refusal_test.go`; the message is checked against `package.json` and `provenance.json`):
`@pi-in-go/pigpen-websearch (Go port) does not support <feature> yet. Install the original extension, npm:pi-web-access@0.33.0, which runs unchanged in PiG.`

| Feature | When | Where |
|---|---|---|
| `<Label> search provider` | a provider this port lacks is selected, listed, configured, reached in `auto`, in `searchRouting`, or configured (credential set) for `"all"`; never a fallback, and a list or `"all"` is refused before any provider is contacted | `search.go` |
| `web_search workflow "summary-review" / "auto-summary"` | parameter or `workflow` in config | `tool_search.go` |
| `authenticated fetch` | any `auth` value | `tool_fetch.go` |
| `YouTube video extraction`, `local video file analysis`, `video frame and timestamp extraction`, `GitHub repository extraction (clone)`, `GitHub issue and pull request extraction` | the URL (or `frames`/`timestamp`) the original sends to that extractor and the matching config section (`youtube`, `video`, `githubClone`, `githubPrIssue`) not disabled; raw mode bypasses them as in the original | `fetch_unported.go`, `extract.go` |
| `PDF text extraction` | a PDF response | `extract.go` |
| `<Label> fetch provider` | the original would call a hosted fetch provider at this point of `fetchRouting` (base URL or key present) | `extract.go` |
| `the RSC payload and Defuddle fallback extractors` | readable extraction failed, where the original tries these before giving up (the message follows the original's verdict) | `extract.go` |

A batch of URLs is refused whole when one URL is. Not registered, so not advertised: the commands `/websearch`, `/curator`,
`/google-account`, the shortcuts and the activity widget. Known approximation: whether an unported provider is available
uses its credential names (OAuth, ADC and browser-cookie availability of OpenAI, Gemini, xAI and Kimi is not known here).

## Slice: answer mode, page query, query rewrite, summary generation

Ported: `fetch_content` `mode: "answer"` with `prompt` and `answerModel`, `page-query.ts` (`page_query.go`),
`query-rewrite.ts` (`query_rewrite.go`), `summary-model-scope.ts` (`summary_model_scope.go`), the OpenCode attribution
headers (`opencode_headers.go`, shared with the model-provider slice) and the model-call part of `summary-review.ts`
(`summary_review.go`: `generateSummaryDraft`, the prompt, the deterministic fallback, the configured deadline). The curator
workflows that call the last two (`summary-review`, `auto-summary`, the curator page and server, and the model picker of
`index.ts`) stay refused until the curator slice; that slice passes `GetSummaryGenerationDeadlineMs()` to both workflows
and calls `RewriteSearchQuery` from the curator's rewrite hook. `fetch-answer-storage`, `lazy-extract-load`,
`page-query`, `query-cold-abort`, `query-rewrite`, `summary-cold-deadline`, `summary-model-scope`,
`summary-opencode-headers` and `summary-provider-routing` have no named skip left in the slice (one twin of
`lazy-extract-load` omits the module-load marker, see below).

Wiring: the session's model state reaches the tools through `SideCallContext` (`sidecall.go`), attached to the request's
context in `bridgeContext` and read by `answerPages` (`fetch_answer.go`). It carries what the original reads off Pi's
`ExtensionContext`: `ctx.model`, `ctx.modelRegistry` (`find`, `getAvailable`, `getApiKeyAndHeaders`, `complete`),
`ctx.scopedModels` and the session id. The SDK implementation uses `Context.ModelRegistry()` (`Find`, `GetAvailable`,
`GetApiKeyAndHeaders`, `Stream` with `maxTokens`, `reasoning`, `reasoningEffort` and a `transformHeaders` callback) and
`Context.ScopedModels()`.

SDK gap (reported to the SDK owners, no workaround in the port): `ModelRegistry.Stream` and `Complete` take no abort signal,
where pi-ai's `stream`/`complete` take `options.signal`. The host serves a `cancelModelStream` call (the SDK sends it for
provider streams only) but the SDK exposes no way to send it for a model stream. The port ends its own wait when the
request's context is cancelled (the result is a message with `stopReason: "aborted"`, as a cancelled provider request gives)
and calls `ModelEventStream.End`; the host's request keeps running until it finishes, so cancelling an answer wastes the
tokens the original would have saved. A `ModelEventStream.Cancel()` (or a `signal` option carried as a context) would close
the gap.

Deliberate differences:

- **No second completion path.** The original completes through `ctx.modelRegistry.complete` when the registry has it and
  otherwise through pi-ai's `complete`/`completeSimple` with the resolved key and headers. The port has the registry path
  only (the host resolves the credential). `GenerateSummaryDraft` still accepts a `CompleteFunc` that receives the key and
  merged headers, which is how the "direct" upstream cases run; the `completeSimple` case
  (`thinking summary completion sends OpenCode attribution headers`) runs through the registry path and checks the same
  headers and level there.
- **Cold first use.** The original gates `import("@earendil-works/pi-ai/compat")` and cancels or expires while it is pending
  (`query-cold-abort`, `summary-cold-deadline`). Nothing loads lazily in Go; the host call that precedes every completion
  (the credential lookup, which cannot be cancelled either) is gated the same way in the twins, and `awaitWithAbort`
  (`abortable.ts`) ends the wait with "Aborted" while it is pending. The page answer also races its credential lookup
  against cancellation, which the original does not (it awaits it and checks the signal afterwards): the result is the same
  "Aborted", reported earlier.
- **`lazy-extract-load`.** The twin checks the registration order of the five tools, the command and the first fetch. The
  module-load marker (extraction not loaded at registration) has no Go counterpart; the `websearch` command and the shortcuts
  belong to the curator; the session handlers are checked by `TestExtensionRegistersEventsAndCommand`.
- **Source-text assertions.** Four `summary-model-scope` cases read the TypeScript source (`Promise.race` and the deadline
  abort, three call sites of the deadline, the absence of `getModel`, the model order in `index.ts`). The twins check the
  behaviour they stand for (a never-settling completion is cut at the deadline and its context aborted, a caller
  cancellation wins, no first-available fallback, the order of the summary and rewrite lists, the config values and the
  README text). The call sites in the curator and the order of its model picker are the curator slice's.
- **Equivalent mutants dropped**: `answer-extractor-gets-prompt` (the prompt only reaches the YouTube extractor, which is refused until the video slice, so no test can see it) and `summary-deadline-not-aborting-completion` (the deadline's cancel and the deferred cancel leave the completion context in the same state once the call returns).
- **`ctx.model`** is the registry's model for the session's provider and id (the SDK gives the pair, not the object).
- **JSON parse errors** in `web-search.json` carry Go's decoder text where the original shows V8's, as for the rest of the config.
- **Provider `null`** in a stored query result reads as "unknown" only when it is empty (a Go string has no `undefined`).

## Mapping (original file -> Go)

`storage.ts` storage.go - `utils.ts`/`ssrf-protection.ts`/`credential-source.ts` config.go, credential.go, ssrf.go, proxy.go,
transport.go - `gemini-search.ts` (routing) search.go, searchcommon.go - `brave/tavily/perplexity/exa/duckduckgo/serpdive/kagi.ts`
same names - `extract.ts` extract.go, htmlmd.go, imageproc.go, declared_links.go, featureconfig.go - `find`/`source-check`/`fetch-params`
find.go, sourcecheck.go, fetchparams.go - `data-uri-sanitize.ts` datauri.go - `index.ts` tools.go, tool_*.go, toolconfig.go,
schema.go, render.go, command.go, extension.go - `tool-activation.ts` tool_enable.go - `page-query.ts` page_query.go, fetch_answer.go -
`query-rewrite.ts` query_rewrite.go - `summary-model-scope.ts` summary_model_scope.go - `summary-review.ts` (generation) summary_review.go -
`opencode-session-headers.ts` opencode_headers.go - `abortable.ts` abortable.go.

## Deliberate differences

- **HTML to Markdown** uses an `x/net/html` readability-lite and a Turndown-style converter, not Mozilla Readability + Turndown:
  not byte-identical (named gap in the fetch twins). RSC and Defuddle fallbacks return nothing.
- **Provider timeouts** are classified as network errors, so `searchRouting` can fall through; the original treats
  them as aborts. Caller cancellation is still an abort and never falls through.
- **SSRF hardening beyond the original**: the address is checked again when the connection is made (a name that changes
  between validation and connect cannot reach an internal address); IPv6 multicast, NAT64 and 6to4-embedded IPv4 are blocked.
- **Config directory** is PiG-first (`PIG_CODING_AGENT_DIR`, then `PI_CODING_AGENT_DIR`, XDG `pig`, `$PIG_HOME/agent`), with Pi's
  locations as read fallbacks; a new config is written under `$PIG_HOME/agent`.
- **Tool errors**: where the original returns `isError: true` inside the result object (Pi ignores it), PiG marks the result as an
  error, so the model sees `web_enable` failures as failures.
- **Invalid `web-search.json`** makes the original throw at registration; an extension cannot fail its load here, so nothing is
  registered and the problem is shown at session start.
- **A session that cannot be read** (restore or transcript selection) still starts; the user gets a warning.
- **SOCKS4/4a proxies** validate like the original but fail closed at connect (Go's `net/http` has no SOCKS4 dialer).
- **Credential commands** run through the bare `sh` (Go) instead of `/bin/sh`, so the harness can record them and NixOS-style systems work.
- **Guidance names only what this port can do** (review fix): the fetch "Fallback options" checklist and the
  "No search provider available" error of the original also suggest Firecrawl, Crawl4AI, TinyFish, Search1API, Querit,
  Ollama, Parallel, Bright Data, OpenAI/Codex `/login`, the Gemini API and signing into Gemini Web in Chrome. None of
  them works here, so they are left out; the upstream tests only match the headings and the Jina and search bullets.
- **Image pixel budget** (review fix): images larger than 2000 px are decoded for resizing only up to 64 megapixels
  (8192x8192). Go's decoders allocate the full pixel buffer from the declared size, so a few kilobytes of PNG could
  otherwise demand gigabytes and kill the extension process; larger images fail with "Image too large to process".
  Full decodes run one at a time: under the cap a progressive JPEG can still cost about 1 GiB, and fetches run three at once.
- **Panic boundaries** (hardening): the fetch, multi-query and all-provider fan-outs and the background prefetch recover a
  panic into that URL's, query's, provider's or prefetch's error. The SDK recovers only the request goroutine, and in a
  Piglet Binary this extension runs inside the agent process.
- Tool schemas are hash-identical to the original's (`tools-default.json` in `golden/`); on the wire the SDK carries them as JSON
  objects, so key order is the SDK's, not the original's.
- The tool descriptions are kept identical to the original even where they mention unported capabilities (YouTube, GitHub,
  PDF, video, Node fetch): the original's own compatibility test pins their hashes.

## Security posture

- SSRF guard on every remote fetch (private, loopback, link-local, multicast, NAT64, 6to4), with redirect limit 5 and per-hop
  re-validation; `ssrf.allowRanges` exempts explicit CIDRs, and requests through a proxy (`proxy`, or the environment
  proxy with `ssrf.trustEnvProxy`) skip the dial-time check (see below).
- **No browser-cookie access**: Chrome/Gemini Web cookie extraction and authenticated fetch are not ported; `auth` profiles
  return an explicit "not available" error.
- **No paid fallback without configuration**: providers with keys are only used when configured; unported providers never run.
- Two defaults send data to third parties (both as in the original): the keyless Exa MCP (`mcp.exa.ai`), which `auto` uses
  when nothing is configured and `exa` uses without a key, and the Jina Reader fetch fallback (off for remote URLs unless `fetchRouting.allowRemoteHostedProviders`).
- Credentials are redacted from every error; the fetch cache is private (0600), symlink-free and size/TTL bounded.
- Size and time limits: page bodies are capped at 5 MB (declared or streamed), provider API bodies at 16 MB, images at
  64 megapixels before decoding (one full decode at a time), and every direct fetch runs under `fetch.timeout`.
- The `proxy` tool parameter is chosen by the model, as in the original. A request through a proxy is validated by name
  before it is sent, but the dial-time address check does not apply (the proxy connects, not this process), and the proxy
  host itself is not restricted, so a model can route fetches through a proxy on a local address (a call parameter wins
  over `proxy` in `web-search.json`). Open follow-up: an option to ignore the parameter.

## Adapted twins

These upstream cases use a provider that is not ported (OpenAI, XCrawl, AnySearch, SerpBase, Serply) or observe curl; the
behaviour under test is provider-independent, so the case runs with a ported provider (or the proxy the request carries).
Each logs `ADAPTED:` when run.

| Upstream title | Adaptation | Go file |
|---|---|---|
| source_check fetchContent uses the explicit proxy for result pages | provider tavily instead of openai (not ported); recorded proxy of each request instead of curl arguments | `proxy_test.go` |
| fetch_content passes the explicit proxy through queued extraction | the page body is padded past the 500-character usefulness floor: this port's HTML pipeline (like the original's, which also rejects the short fixture body) reports shorter pages as incomplete | `proxy_test.go` |
| configured socks5h proxy is accepted and routed to curl | asserts the accepted proxy the request carries instead of curl arguments | `proxy_test.go` |
| non-curated search stops after caller cancellation | provider tavily instead of anysearch (not ported) | `tools_provider_test.go` |
| web_search and Curator reject disabled providers before availability or requests | only workflow none runs; summary-review is the curator (deferred) | `tools_provider_test.go` |
| allowlisting an explicit-only provider does not opt it into auto or all | duckduckgo instead of serply (not ported); both are explicit-only | `tools_provider_test.go` |
| an allowlisted explicit-only provider remains directly selectable with its credential | duckduckgo (keyless) instead of serply (not ported); credential header check dropped | `tools_provider_test.go` |
| an absent config file preserves registration and search behavior | commands are the ported ones and web_enable is registered (dynamic activation is the default here) | `tools_provider_test.go` |
| source_check executes a successful OpenAI provider response with runtime context | provider tavily instead of openai (not ported) | `tools_provider_test.go` |
| source_check stops on cancellation instead of continuing queries | provider tavily instead of openai (not ported) | `tools_provider_test.go` |
| source_check retains a rejected page fetch in the artifact | provider tavily instead of openai (not ported); the page is an IP literal so no DNS is needed | `tools_provider_test.go` |
| web_search output tells the model the responseId that get_search_content accepts | provider tavily instead of openai (not ported); the identifier flow is provider-independent | `tools_search_test.go` |
| web_search output honours a renamed get_search_content tool | provider tavily instead of openai (not ported); the identifier flow is provider-independent | `tools_search_test.go` |
| web_search output omits the retrieval hint when get_search_content is disabled | provider tavily instead of openai (not ported); the identifier flow is provider-independent | `tools_search_test.go` |
| web_search expands a JSON-array string in query into separate searches | provider brave instead of xcrawl (not ported); query expansion is provider-independent | `tools_search_test.go` |
| web_search keeps mixed JSON arrays in query as a single literal query | provider brave instead of xcrawl (not ported); query expansion is provider-independent | `tools_search_test.go` |
| web_search does not reinterpret already-structured queries entries | provider brave instead of xcrawl (not ported); query expansion is provider-independent | `tools_search_test.go` |
| web_search reports the existing no-query error for empty JSON-array strings | provider brave instead of xcrawl (not ported); query expansion is provider-independent | `tools_search_test.go` |
| web_search bounds batch concurrency and preserves query order | provider brave instead of xcrawl (not ported); the summary-review half runs the curator, a named gap | `tools_search_test.go` |
| web_search preserves OpenAI answers even when no sources are returned | provider tavily instead of openai (not ported) | `tools_search_test.go` |
| default raw multi-query output is bounded, attributed, and stored without mutation | provider tavily (answer text passes through) instead of openai/anysearch (not ported) | `tools_search_test.go` |
| stored search answers support bounded continuation and findText | provider tavily (answer text passes through) instead of openai/anysearch (not ported) | `tools_search_test.go` |
| truncated output discloses disabled retrieval while remaining within the cap | provider tavily (answer text passes through) instead of openai/anysearch (not ported) | `tools_search_test.go` |
| configured inline limit bounds the complete raw presentation | provider tavily (answer text passes through) instead of openai/anysearch (not ported) | `tools_search_test.go` |
| below-minimum inline limit falls back to the default and exact minimum remains complete | provider tavily (answer text passes through) instead of openai/anysearch (not ported) | `tools_search_test.go` |
| truncated inline-ready search retains fetch and search retrieval guidance | provider tavily (answer text passes through) instead of openai/anysearch (not ported) | `tools_search_test.go` |
| truncated background-fetch search retains state, fetchId, and search retrieval guidance | provider tavily (answer text passes through) instead of openai/anysearch (not ported) | `tools_search_test.go` |

## Proof

| Layer | Result |
|---|---|
| Twins | `pigeq twins check`: 305 exact + 570 named skips = 875 titles; exit 0 |
| Go tests | `go test -race -count=24` with `GOMAXPROCS=4` passes; `go vet` clean on linux, windows, darwin |
| Equivalence | 9 scenarios, replayed on PiG with the Go port: identical traces (`pigeq check --go`). The first 5 were recorded from the original under **Pi 1.0.1** (registration, schemas, system-prompt snippets, dynamic activation, renaming and the tool gates). Four were recorded under **Pi 1402973a** and `pigeq run` shows the original under Pi, the original under PiG's Node runtime and the port identical: `web-search-tavily` (the request to a fake Tavily server and the rendered answer), `fetch-text-page` and `fetch-raw-404` (the model's tool call names a fake page server through `{{server:web}}`), `fetch-private-address-refused`. They use the harness's `normalize` rule N7 for the `responseId` that `generateId` (storage.ts) draws on every run: `Date.now().toString(36)` plus 1 to 6 random base-36 characters (so 9 to 14 characters, and a leading `m` to `o` until 2029). One rule covers the id in text (`(responseId[": ]+)[0-9a-z]{9,14}\b`), one the id as a whole value (`^[m-o][0-9a-z]{8,13}$`); a bare 14-character rule was tried first and also hid `searchinfinity` and `simultaneously` in the recorded system text |
| Mutation | `mutations.json`: 15 mutants, all killed (`pigeq mutate --unit`) |
| Binary | a Piglet Binary with only this extension fused (a temporary Piglet, deleted after the run) builds with PiG 0.3.0 and passes the process-global safety check; replaying the 5 scenarios on it (`pigeq record --self --builtin`) gives tool results and events identical to the Pi golden traces. `pig-with-batteries` itself cannot build a Binary yet because of the Node herdr extension (documented in its README) |
| Go tests | 477 passing subtests, 572 skipped (570 ledger skips + 2 environment skips), coverage 86% |

## Notes for PiG 0.4.0 (Pi 0.99.1)

- Re-check `toolsAdded`/`toolsRemoved` system messages in `BuildSessionContext` (transcript-based activation depends on them) and the
  tool activation API.
- `sdk.ToolResult.Content` is one string: several text blocks are joined with a blank line; a structured content array would
  remove that.
- The SDK `Schema` is a `map`, so schema key order is not preserved.
- `Context` stays valid after its request completes (used for background-fetch messages); keep that guarantee.
- The template fake host's `Command` helper sends the command name in `args` where the SDK reads `tool`; arrays cannot be
  answered by `OnCall`. Both are known issues of the template (`components/extension-port`).

## Re-verified on PiG 0.4.1 (porter-verify)

The golden traces were recorded again from the original under Pi 1.0.1 (host check: identical under PiG's Node runtime), with PiG 0.4.1 content (`5f948f86a`, `pig --version`
`0.3.1+1.0.1`) and normalizer v3, because Pi 1.0.x changed the trace format: a `prompt` response now carries
`data.disposition` (Pi 0.99.0, #9098). An event-by-event diff against the previous traces shows no other
difference, and `pigeq check` passes on the Go port. Details: `docs/plan/progress/porter-verify.md`.
