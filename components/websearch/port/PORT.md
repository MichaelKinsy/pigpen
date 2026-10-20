# Port record: websearch (pi-web-access 0.33.0, first slice)

Original: **pi-web-access 0.33.0** by Nico Bailon (MIT), commit `9a734ed195da2f4cccc2fb5e7128f6774a380f47`, kept
unmodified in [`oracle/`](oracle). Built against **PiG 0.3.0** (Pi 0.87.1 API), on the public Go SDK only.
Credits: [`../CREDITS.md`](../CREDITS.md).

## Status: the first slice, not the whole port

Search, fetch and source retention are ported and twinned. Everything else is a **named skipped test**:
`pigeq twins check --ledger port/upstream-tests.json --go extensions/websearch` accounts for all
875 upstream test titles (531 exact twins, 344 named skips, each with a reason). Do not call this port complete.

| Slice 1 (done) | Deferred (named skips) |
|---|---|
| Tools `web_search` (workflow `none`), `fetch_content` (readable/raw, images), `get_search_content`, `source_check`, `web_enable` and the `/search` command; dynamic activation with transcript-based selection; tool renaming and registration gates; schemas hash-identical to the original | Curator UI and every command but `/search`; `summary-review` / `auto-summary` workflows; fetch `answer` mode and query rewrite (need a model-completion call); activity widget and shortcut; TUI render hooks |
| Providers Exa (keyed and keyless MCP), Brave, Tavily (key pool), Perplexity, DuckDuckGo, SERPdive, Kagi (search and extract), TinyFish, Search1API, Searchinfinity, Querit, Parallel, Parallel MCP, Ollama, Firecrawl, Crawl4AI, Bright Data (SERP and Unlocker), Gemini (API, ADC, Cloudflare gateway; also the `gemini` fetch provider, URL context and PDF conversion), Serper, SerpApi, SerpBase, Serply, Valyu, Bocha, Baizhi, You.com, AnySearch, XCrawl, SearXNG, Jina Search; routing (`auto`, `all`, arrays, `searchRouting`, `allowedProviders`), error classification, credential sources | The other 4 providers (OpenAI/Codex hosted search, Kimi, xAI, Mistral): in the provider table; selecting or reaching one is refused (see "Refusals") |
| SSRF guard, redirect limits, domain policy, proxy scoping, private 0600 fetch cache with TTL, retained sources restored from the session, inline data-URI sanitiser, config loading | PDF text extraction (config is read), YouTube/video/frames, GitHub clone, RSC/Defuddle fallbacks, authenticated fetch, Gemini Web and browser cookies |

## Refusals (until the slices above are ported)

Every unported option, mode or URL kind is refused with one message and returns a tool error (`refusal.go`,
`refusal_test.go`; the message is checked against `package.json` and `provenance.json`):
`@pi-in-go/pigpen-websearch (Go port) does not support <feature> yet. Install the original extension, npm:pi-web-access@0.33.0, which runs unchanged in PiG.`

| Feature | When | Where |
|---|---|---|
| `<Label> search provider` | a provider this port lacks is selected, listed, configured, reached in `auto`, in `searchRouting`, or configured (credential set) for `"all"`; never a fallback, and a list or `"all"` is refused before any provider is contacted | `search.go` |
| `web_search workflow "summary-review" / "auto-summary"` | parameter or `workflow` in config | `tool_search.go` |
| `fetch_content mode answer` | `mode: "answer"` | `tool_fetch.go` |
| `authenticated fetch` | any `auth` value | `tool_fetch.go` |
| `YouTube video extraction`, `local video file analysis`, `video frame and timestamp extraction`, `GitHub repository extraction (clone)`, `GitHub issue and pull request extraction` | the URL (or `frames`/`timestamp`) the original sends to that extractor and the matching config section (`youtube`, `video`, `githubClone`, `githubPrIssue`) not disabled; raw mode bypasses them as in the original | `fetch_unported.go`, `extract.go` |
| `PDF text extraction` | a PDF response | `extract.go` |
| `the RSC payload and Defuddle fallback extractors` | readable extraction failed, where the original tries these before giving up (the message follows the original's verdict) | `extract.go` |

A batch of URLs is refused whole when one URL is. Not registered, so not advertised: the commands `/websearch`, `/curator`,
`/google-account`, the shortcuts and the activity widget. Known approximation: whether an unported provider is available
uses its credential names (OAuth and login availability of OpenAI, xAI and Kimi is not known here). Gemini is exact: see "Slice: Gemini".

## Slice 2: the REST search providers

Serper, SerpApi, SerpBase, Serply, Valyu, Bocha, Baizhi, You.com, AnySearch, XCrawl, SearXNG and Jina Search
(`serper.go`, `serpapi.go`, `serpbase.go`, `serply.go`, `valyu.go`, `bocha.go`, `baizhi.go`, `you.go`, `anysearch.go`,
`xcrawl.go`, `searxng.go`, `jinasearch.go`, shared mechanics in `restcommon.go`). Their `<Label> search provider` refusal is
gone; the refusal stayed for the 14 providers other slices owned. The provider table keeps the original's availability checks:
SerpApi reads `SERPAPI_KEY`, You.com `YDC_API_KEY`, AnySearch is always available (a key is optional), XCrawl reads
`xcrawlApiKey`/`XCRAWL_API_KEY`, SearXNG needs a valid base URL and a readable `ssrf` section (a malformed one fails the
search instead of letting a hosted provider run). Jina and XCrawl have no hosted fetch in their provider files; the Jina
Reader fetch fallback is `extract.go`'s.

**Proof, and its limit.** Eleven of the twelve providers call a fixed https endpoint with no base-URL setting, so a pigeq
scenario cannot point the original at a fake server. Their wire proof is oracle-derived instead:
`port/record-provider-requests.mjs` imports the unmodified original modules (`port/oracle/*.ts`) under Node with a
recording `fetch`, replays the inputs of the upstream tests and of the error paths (status codes, envelopes, redirects,
timeouts, cancellation) and writes the original's actual requests (method, URL, headers, body) and outcomes (result or
error text) to `extensions/websearch/testdata/provider-requests.json` (142 cases). `provider_fixtures_test.go` replays every case against the port
with the same canned responses and requires identical requests and outcomes; the twins call into it. The comparison
ignores two things on purpose: a request's `redirect` mode (a Node option; the Go transport never follows redirects by
itself) and the parser text after `invalid JSON:` (Node and Go word it differently). SearXNG has a configurable base URL and also has
pigeq scenarios against a fake server (`web-search-searxng*`, 8 scenarios), plus three offline scenarios
(`web-search-routing-rest-unavailable`, `web-search-baizhi-bad-filter`, `web-search-explicit-only-rest-auto`); all 11 were recorded
under Pi 1402973a and pass `pigeq check` on PiG.

**Differences recorded with fixtures**

- The JSON parser text after `... returned invalid JSON: ` is Go's, not V8's (`serper-invalid-json`, `xcrawl-invalid-json`).
- A provider's own deadline: SerpApi, Serply, You.com and XCrawl say `<name> request timed out after 60s` (as the original);
  Jina says `Jina Search API error 408: request timed out` (as the original). Serper, SerpBase, Valyu, Bocha, AnySearch and
  SearXNG have no such branch upstream, where a deadline is an abort error; here it is the shared network-class timeout
  (see "Provider timeouts" above), so routing can fall through. Caller cancellation is an abort everywhere.
- Serper, Valyu, Bocha, AnySearch, XCrawl and Jina use a plain `fetch` upstream, which forwards a custom credential
  header (`X-API-KEY`, `x-api-key`) to another origin on a redirect (undici drops only `Authorization`). The Go transport
  follows the redirect the same way but drops the provider's credential header when the origin changes (hardening beyond the
  original). Baizhi and SearXNG never send the credential to another origin (Baizhi's fetch is `redirect: "error"`,
  SearXNG's `onRedirect` drops the configured headers).
- A failed connection reads `fetch failed` and a body cut short `terminated`, as in Node, so the routing classifier treats it as
  a network failure (`TestRestProviderTransportFailure`, scenario `web-search-searxng-drop`).
- Baizhi's MCP client is written against the wire sequence of the SDK's streamable-HTTP client (initialize,
  `notifications/initialized`, the optional GET event stream, `tools/call`, DELETE with the session id), with the same
  headers and bodies (`baizhi-*` fixture cases); JSON and server-sent-event responses are read; the SDK's schema
  validation of results is reduced to the fields the provider reads.
- `structuredContent` is re-serialised like `JSON.stringify(x, null, 2)` (key order and string escapes of JavaScript).
- The activity monitor is the widget slice's: the cases that read it (Bocha, Jina "recorded as activity errors") run the error
  path and its text; the Curator page halves of the Serply, SerpApi, You.com and Baizhi cases belong to the curator slice.
  The real Pi session case of Baizhi runs the registered `web_search` tool instead of a Pi agent session.

## Slice: hosted search and fetch providers

Ports `tinyfish.ts`, `search1api.ts`, `searchinfinity.ts`, `querit.ts`, `parallel.ts`, `parallel-mcp.ts`, `ollama.ts`, the
Extract half of `kagi.ts`, `firecrawl.ts`, `crawl4ai.ts`, `brightdata.ts` (SERP) and `brightdata-unlocker.ts`, and the hosted
part of `extract.ts` (`fetchRouting` order, the per-provider `<Label> fallback failed` lines and the "Fallback options" list,
`extract.ts:830-1065`). Files: one per original (`tinyfish.go`, `search1api.go`, `searchinfinity.go`, `querit.go`, `parallel.go`,
`parallelmcp.go`, `ollama.go`, `kagi.go`, `firecrawl.go`, `crawl4ai.go`, `brightdata.go`, `brightdata_unlocker.go`), the shared
request plumbing in `hosted.go` and the routing in `fetch_hosted.go`. The `<Label> search provider` and `<Label> fetch provider`
refusals of these providers are gone; a configured provider that fails reports its own error exactly as the original does.

Proof:
- twins for `tinyfish`, `search1api`, `searchinfinity`, `querit-provider`, `parallel`, `parallel-mcp-provider`,
  `firecrawl-extraction`, `crawl4ai-extraction`, `brightdata-serp`, `brightdata-unlocker` and the Ollama and Kagi cases of
  `new-search-providers` (`*_test.go` named after the files). Their remaining named skips are the curator pages, the Gemini
  fallthrough of `parallel`, the activity log of `brightdata-serp`, and the Bocha and SerpBase cases that belong to other slices.
- the providers with a configurable base URL (Firecrawl, Crawl4AI) are compared by pigeq scenarios with fake servers
  (`fetch-firecrawl-fallback`, `fetch-crawl4ai-fallback`, `fetch-hosted-failure-guidance`, `web-search-firecrawl`).
- every other provider calls a fixed endpoint that no scenario server can receive under the unmodified original, so
  `port/record-hosted-requests.mjs` runs the vendored original with a recording `fetch` and checks in the requests and results
  (`extensions/websearch/testdata/provider-wire.json`, 20 cases); `provider_wire_test.go` replays the same inputs against the port and compares every
  request (method, URL, headers, body) and the result. `web-search-hosted-missing-credentials` compares the pre-network
  guidance of the fixed-endpoint providers.

Deliberate differences (each pinned by a twin):
- **No activity log.** The original opens and closes an activity entry around every provider call; this port has none (the
  activity widget belongs to the curator slice), so the twins assert the request and error behaviour and skip the entry itself
  (`the activity entry is opened once ...`).
- **Own timeouts** are "timed out" errors, like the other Go providers (a `fetch` aborted by `AbortSignal.timeout` reads
  "aborted" upstream); Querit keeps its own wording, `Querit <Search|Contents> API request timed out after N seconds`.
- **JSON parse messages** are Go's, not V8's, where a provider quotes them (`... returned invalid JSON: <detail>`).
  Bright Data's config parse error keeps only a position, as upstream; V8 omits the position for a bad token at a value
  position and Go cannot tell all V8 cases apart, so the port omits it for Go's "looking for beginning of value" and "in literal"
  messages and prints `at position N (line L column C)` for the structural ones.
- **Error names** (`TypeError`, `AbortError`) do not exist for Go errors; only messages are compared.
- **Search options** `TimeoutMs` and `Lookup` exist on `SearchOptions` for the self-hosted Firecrawl search (the original's
  `FirecrawlSearchOptions`), and `FetchRemoteOptions.OnRedirectErr` carries the Crawl4AI refusal of a cross-origin redirect.

## Mapping (original file -> Go)

`storage.ts` storage.go - `utils.ts`/`ssrf-protection.ts`/`credential-source.ts` config.go, credential.go, ssrf.go, proxy.go,
transport.go - `gemini-search.ts` (routing) search.go, searchcommon.go - `brave/tavily/perplexity/exa/duckduckgo/serpdive/kagi/serper/serpapi/serpbase/serply/valyu/bocha/baizhi/you/anysearch/xcrawl/searxng.ts`
same names (`jina-search.ts` jinasearch.go; the shared mechanics of the keyed REST providers are in restcommon.go; the hosted providers are listed in "Slice: hosted search and fetch providers") - `extract.ts` extract.go, htmlmd.go, imageproc.go, declared_links.go, featureconfig.go - `find`/`source-check`/`fetch-params`
find.go, sourcecheck.go, fetchparams.go - `data-uri-sanitize.ts` datauri.go - `index.ts` tools.go, tool_*.go, toolconfig.go,
schema.go, render.go, command.go, extension.go - `tool-activation.ts` tool_enable.go.

## Deliberate differences

- **HTML to Markdown** uses an `x/net/html` readability-lite and a Turndown-style converter, not Mozilla Readability + Turndown:
  not byte-identical (named gap in the fetch twins). RSC and Defuddle fallbacks return nothing.
- **Provider timeouts** are classified as network errors, so `searchRouting` can fall through; the original treats
  them as aborts. Caller cancellation is still an abort and never falls through.
- **SSRF hardening beyond the original**: the address is checked again when the connection is made (a name that changes
  between validation and connect cannot reach an internal address); IPv6 multicast, NAT64 and 6to4-embedded IPv4 are blocked.
- **Config directory** is PiG-first (`PIG_CODING_AGENT_DIR`, then `PI_CODING_AGENT_DIR`, XDG `pig`, `$PIG_HOME/agent`), with Pi's
  locations as read fallbacks; a new config is written under `$PIG_HOME/agent`.
- **Config reads are cached**: the original reads `web-search.json` with `readFileSync` on every call; `ReadConfigRoot` keeps the
  parsed file and serves it while a stat (size, mtime in nanoseconds, file identity) still matches. A file modified within the
  last 2 seconds is never cached (the racy-write guard), an edit is picked up on the next call, and `ResetCaches` drops the entry.
  The returned map is shared, so callers treat it as read-only. Twins: `config_cache_test.go`; mutants `config-cache-*`.
- **Tool errors**: where the original returns `isError: true` inside the result object (Pi ignores it), PiG marks the result as an
  error, so the model sees `web_enable` failures as failures.
- **Invalid `web-search.json`** makes the original throw at registration; an extension cannot fail its load here, so nothing is
  registered and the problem is shown at session start.
- **A session that cannot be read** (restore or transcript selection) still starts; the user gets a warning.
- **SOCKS4/4a proxies** validate like the original but fail closed at connect (Go's `net/http` has no SOCKS4 dialer).
- **Credential commands** run through the bare `sh` (Go) instead of `/bin/sh`, so the harness can record them and NixOS-style systems work.
- **Guidance names only what this port can do** (review fix): the "No search provider available" error of the original also
  suggests OpenAI/Codex `/login`, the Gemini API and signing into Gemini Web in Chrome, which do not work here, so they are
  left out. The fetch "Fallback options" list is the original's, hosted providers and Gemini lines included (the Gemini lines
  point at providers that arrive with the Gemini slice).
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

These upstream cases use a provider that is not ported (OpenAI), observe curl, or have a half (curator page, activity monitor, the real Pi session) with no Go counterpart; the
behaviour under test is provider-independent, so the case runs with a ported provider (or the proxy the request carries).
Each logs `ADAPTED:` when run.

| Upstream title | Adaptation | Go file |
|---|---|---|
| source_check fetchContent uses the explicit proxy for result pages | provider tavily instead of openai (not ported); recorded proxy of each request instead of curl arguments | `proxy_test.go` |
| fetch_content passes the explicit proxy through queued extraction | the page body is padded past the 500-character usefulness floor: this port's HTML pipeline (like the original's, which also rejects the short fixture body) reports shorter pages as incomplete | `proxy_test.go` |
| configured socks5h proxy is accepted and routed to curl | asserts the accepted proxy the request carries instead of curl arguments | `proxy_test.go` |
| web_search and Curator reject disabled providers before availability or requests | only workflow none runs; summary-review is the curator (deferred) | `tools_provider_test.go` |
| an absent config file preserves registration and search behavior | commands are the ported ones and web_enable is registered (dynamic activation is the default here) | `tools_provider_test.go` |
| source_check executes a successful OpenAI provider response with runtime context | provider tavily instead of openai (not ported) | `tools_provider_test.go` |
| source_check stops on cancellation instead of continuing queries | provider tavily instead of openai (not ported) | `tools_provider_test.go` |
| source_check retains a rejected page fetch in the artifact | provider tavily instead of openai (not ported); the page is an IP literal so no DNS is needed | `tools_provider_test.go` |
| web_search output tells the model the responseId that get_search_content accepts | provider tavily instead of openai (not ported); the identifier flow is provider-independent | `tools_search_test.go` |
| web_search output honours a renamed get_search_content tool | provider tavily instead of openai (not ported); the identifier flow is provider-independent | `tools_search_test.go` |
| web_search output omits the retrieval hint when get_search_content is disabled | provider tavily instead of openai (not ported); the identifier flow is provider-independent | `tools_search_test.go` |
| web_search bounds batch concurrency and preserves query order | the summary-review half runs the curator, a named gap | `tools_search_test.go` |
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
| Twins | `pigeq twins check`: 531 exact + 344 named skips = 875 titles; exit 0 |
| Go tests | `go test -race -count=24` with `GOMAXPROCS=4` passes; `go vet` clean on linux, windows, darwin |
| Equivalence | 9 scenarios, replayed on PiG with the Go port: identical traces (`pigeq check --go`). The first 5 were recorded from the original under **Pi 1.0.1** (registration, schemas, system-prompt snippets, dynamic activation, renaming and the tool gates). Four were recorded under **Pi 1402973a** and `pigeq run` shows the original under Pi, the original under PiG's Node runtime and the port identical: `web-search-tavily` (the request to a fake Tavily server and the rendered answer), `fetch-text-page` and `fetch-raw-404` (the model's tool call names a fake page server through `{{server:web}}`), `fetch-private-address-refused`. They use the harness's `normalize` rule N7 for the `responseId` that `generateId` (storage.ts) draws on every run: `Date.now().toString(36)` plus 1 to 6 random base-36 characters (so 9 to 14 characters, and a leading `m` to `o` until 2029). One rule covers the id in text (`(responseId[": ]+)[0-9a-z]{9,14}\b`), one the id as a whole value (`^[m-o][0-9a-z]{8,13}$`); a bare 14-character rule was tried first and also hid `searchinfinity` and `simultaneously` in the recorded system text |
| Mutation | `mutations.json`: 124 mutants (slice 1, the REST providers, the config cache, the hosted providers and Gemini), all killed (`pigeq mutate --unit`) |
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

## Slice: Gemini (gemini-api, gemini-adc, gemini-search, gemini-url-context, gemini-pdf-extract)

Provider `gemini` is ported (`gemini_api.go`, `gemini_adc.go`, `gemini_search.go`, `gemini_url_context.go`,
`gemini_pdf_extract.go`, `gemini_web_client.go`), its refusal is removed, and the `gemini` fetch provider and the Gemini PDF
transcription are real. What each file carries:

- **Transport** (`gemini_api.go`): the three auth modes of `fetchGeminiApi` (key header, Cloudflare AI Gateway header, ADC bearer on
  `aiplatform.googleapis.com`), the origin allow-list, the credential-in-query rejection, redaction of the key, the gateway key
  and the ADC token from every error, and the video-capable availability (`IsGeminiAPIAvailableWithVideo`: ADC does not count).
- **ADC** (`gemini_adc.go`): `authorized_user` refresh-token and `service_account` JWT (RS256) exchanges with the original's
  error messages, token caching with the 60 s skew, and the credential-rejection classification (400, 401, 403 are a
  `CredentialResolutionError`, everything else a plain error so fallbacks keep working).
- **Search and routing** (`gemini_search.go`, `search.go`): explicit, `all` (API only), arrays, `searchRouting` and `auto`.
  Gemini has no availability gate in `auto`: it answers or reports nothing, and only a credential failure or an abort is shown
  (`searchWithGemini` with `strictErrors` false). The availability of the other providers now receives the call context and
  options (`providerDef.available`) so the model-backed providers can read the session.
- **Fetch** (`extract.go`): the `gemini` fetch provider tries URL context, then Gemini Web, as in `extract.ts:1011`.
- **PDF** (`gemini_pdf_extract.go`): `ExtractPDFViaGemini` for the PDF slice (prompt, fence stripping, page-marker validation).
- **Gemini Web** (`gemini_web_client.go`): the cookie client is another slice. This port defines the interface the original's
  `gemini-web.ts` calls (`GeminiWebClient`: `Available`, `Diagnostic`, `Query`) and a default client that reports Gemini Web as
  unavailable while browser cookie access is off (the original's default) and refuses with the port's refusal message when
  it is switched on. The cookie slice installs its client with `SetGeminiWebClient`.

Deliberate differences:

- Provider timeouts follow the port-wide classification (network, so routing can fall through); the original treats them as aborts.
- The activity monitor (curator slice) is not ported, so no activity is logged around Gemini calls.
- The fetch "Fallback options" checklist names the Gemini API (`GEMINI_API_KEY`) now; the Gemini Web bullet comes with the cookie slice.
- `gemini-api-transport` drives video-extract.ts upload, status and delete; those two twins run the generate call and the
  upload base of the same transport (ADAPTED) until the video slice ships the rest.
- Config is read at each call (the original caches it for the process), so an edit to `web-search.json` is seen without a restart.
- The grounding-redirect lookup (`HEAD`, no redirect following, 5 s) goes through the provider transport, so a configured proxy applies.
