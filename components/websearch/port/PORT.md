# Port record: websearch (pi-web-access 0.33.0, first slice)

Original: **pi-web-access 0.33.0** by Nico Bailon (MIT), commit `9a734ed195da2f4cccc2fb5e7128f6774a380f47`, kept
unmodified in [`oracle/`](oracle). Built against **PiG 0.3.0** (Pi 0.87.1 API), on the public Go SDK only.
Credits: [`../CREDITS.md`](../CREDITS.md).

## Status: the first slice, not the whole port

Search, fetch and source retention are ported and twinned. Everything else is a **named skipped test**:
`pigeq twins check --ledger port/upstream-tests.json --go extensions/websearch` accounts for all
875 upstream test titles (362 exact twins, 513 named skips, each with a reason). Do not call this port complete.

| Slice 1 (done) | Deferred (named skips) |
|---|---|
| Tools `web_search` (workflow `none`), `fetch_content` (readable/raw, images), `get_search_content`, `source_check`, `web_enable` and the `/search` command; dynamic activation with transcript-based selection; tool renaming and registration gates; schemas hash-identical to the original | Curator UI and every command but `/search`; `summary-review` / `auto-summary` workflows; fetch `answer` mode and query rewrite (need a model-completion call); activity widget and shortcut; TUI render hooks |
| Providers Exa (keyed and keyless MCP), Brave, Tavily (key pool), Perplexity, DuckDuckGo, SERPdive, Kagi (search); routing (`auto`, `all`, arrays, `searchRouting`, `allowedProviders`), error classification, credential sources | The other 26 providers (OpenAI/Codex hosted search, Gemini, xAI, Jina Search, Firecrawl, ...): in the provider table; selecting or reaching one is refused (see "Refusals") |
| SSRF guard, redirect limits, domain policy, proxy scoping, private 0600 fetch cache with TTL, retained sources restored from the session, inline data-URI sanitiser, config loading | YouTube/video/frames, GitHub clone, RSC/Defuddle fallbacks, hosted fetch providers other than Jina, authenticated fetch, Gemini Web and browser cookies |

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
| `<Label> fetch provider` | the original would call a hosted fetch provider at this point of `fetchRouting` (base URL or key present) | `extract.go` |
| `the RSC payload and Defuddle fallback extractors` | readable extraction failed, where the original tries these before giving up (the message follows the original's verdict) | `extract.go` |

A batch of URLs is refused whole when one URL is. Not registered, so not advertised: the commands `/websearch`, `/curator`,
`/google-account`, the shortcuts and the activity widget. Known approximation: whether an unported provider is available
uses its credential names (OAuth and login availability of OpenAI, xAI and Kimi is not known here). Gemini is exact: see "Slice: Gemini".

## Mapping (original file -> Go)

`storage.ts` storage.go - `utils.ts`/`ssrf-protection.ts`/`credential-source.ts` config.go, credential.go, ssrf.go, proxy.go,
transport.go - `pdf-extract.ts` pdf_extract.go, featureconfig.go (`pdf.*`), pdf_text.go (the unpdf extraction) - `datalab-pdf-extract.ts` datalab_pdf_extract.go - `gemini-search.ts` (routing) search.go, searchcommon.go - `brave/tavily/perplexity/exa/duckduckgo/serpdive/kagi.ts`
same names - `extract.ts` extract.go, htmlmd.go, imageproc.go, declared_links.go, featureconfig.go - `find`/`source-check`/`fetch-params`
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
| Twins | `pigeq twins check`: 330 exact + 545 named skips = 875 titles; exit 0 |
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

## Slice: PDF text extraction (pdf-extract, datalab-pdf-extract, `pdf.*` config, fetch-pdf-answer)

A PDF response of `fetch_content` is converted to Markdown, saved as `<tmp>/pi-web-pdf/<title>.md`, and reported as the original
reports it; its refusal (`PDF text extraction`) is removed. What each file carries:

- **`pdf_extract.go`** (`pdf-extract.ts`): `ExtractPDFToMarkdown`: the `pdf.provider` chain (`auto`: Datalab, the Gemini API, local
  text; or one of them pinned), the Markdown file layout (title, source, pages, author, page markers, the truncation notice),
  the file and title names from the URL (`extractTitleFromURL`, `sanitizeFilename`, `arxiv.org` ids), and the rule of which errors
  end the chain (a cancellation, a credential failure, an unreadable config) and which fall through. The `fetch_content` branch
  (`extract.go`, `extract.ts:1298`) reports `PDF extracted and saved to: ...` with the page and character counts, the Markdown itself
  in `mode: "answer"`, the size and configuration errors as they are, and any other failure as `PDF extraction failed: ...`.
- **`datalab_pdf_extract.go`** (`datalab-pdf-extract.ts`): presigned upload, `PUT`, confirm, convert (multipart, no
  `processing_location`), polling with the shared deadline and the 1.5 s interval, the cross-origin check on `request_check_url`,
  redirects as errors, the 4 MiB response bound, redaction of the key from every error, best-effort cleanup that never delays the
  caller. `DATALAB_API_BASE`, `DATALAB_MODE` and `DATALAB_PROCESSING_LOCATION` as in the original.
- **`pdf_text.go`** (the `unpdf` call): the library `github.com/ledongthuc/pdf` (BSD-3-Clause, pure Go, no cgo, no Node) reads the
  file structure: cross-reference tables and streams, object streams, filters, RC4 and AES-128 encryption with the empty password,
  fonts and ToUnicode maps. The text assembly is PDF.js's `getTextContent` as unpdf drives it (`pdf-extract.ts:226`): the library's
  own `Content()` reader is not used, because it joins streams without a separator, ignores word and character spacing and measures
  every glyph with its first byte. `pdf_text.go` has its own content-stream interpreter that tracks the text matrix, spacing and
  scale, form XObjects and `q`/`Q`, and splits items where PDF.js does (a font or size change, a baseline change beyond the glyph
  height, a gap under -0.2 em or over 0.6 em, a gap between 0.102 and 0.6 em becomes a space); items are joined with a space and
  whitespace collapses, as in the original. Text outside the crop box is dropped, ligatures and compatibility forms fold to NFKC like
  PDF.js's `normalizeUnicode`, right-to-left runs are reordered to logical order, zero-width and bidi control characters are skipped.
  A PDF whose cross-reference table is damaged is rebuilt from its objects and trailer (what PDF.js does); a file with no trailer, a
  password, no bytes or no PDF structure fails with PDF.js's own messages (`Invalid PDF structure.`, `No password given`,
  `The PDF file is empty, i.e. its size is zero bytes.`).

### Evidence that the text matches unpdf

`port/make-pdf-fixtures.mjs` writes 24 PDFs by hand (no PDF library: text, several pages, metadata, Unicode through WinAnsi and
through a Type0/ToUnicode font, Cyrillic, CJK, Hebrew, ligatures, `/Differences`, run splitting and kerning gaps, flate content,
RC4-40, AES-128 and AES-256 encryption, a password-protected file, four corrupt files, a scanned page, 120 pages, rotated text,
a form XObject, a content array, off-page text, an inline image, an object-stream and xref-stream file, control codes, and a
30-page report). `port/record-pdf-extract.mjs` runs the original `pdf-extract.ts` (unpdf 1.8.0, PDF.js 6.1) over them under Node 24 in the
container and records the saved Markdown or the error (`testdata/pdf-unpdf.json`, also the title and file name derived from 16 URLs).
`TestPDFCorpusAgainstUnpdf` requires byte-identical Markdown, page and character counts and file names, and identical error text, on
every fixture but the one below. Seven real documents outside the corpus (not redistributed; two from system documentation, the
library's own test files) were compared the same way while building the extractor: six are identical, including a 19-page, 37 356
character specification; the seventh, a 6-page license text, differs in 2 of 33 728 characters (see the font metrics item below).

### Deliberate differences

- **AES-256 (PDF 2.0, V5/R6) encryption**: the library reads encryption up to V4 (AES-128). Fixture `encrypted-aes256-empty.pdf`:
  the port fails with `Invalid PDF structure.`; the original also fails on PiG's Node 24 runtime, because PDF.js 6.1 calls
  `Math.sumPrecise` for the AES-256 key hash (`Math.sumPrecise is not a function`); on a runtime that has it (Node 25) the original
  extracts the text and the port still fails. Pinned by `knownPDFDifferences`.
- **Font metrics**: PDF.js measures glyphs with the font program (embedded or its built-in standard-14 metrics); the port has
  only the `/Widths` array (0.5 em for a standard font without one, 0.6 em for Courier, 0.278 em for a space). A word gap decided
  by a position that lands near a threshold (0.102 em, 0.6 em) can therefore differ. Fixture `runs-and-spacing.pdf` shows the rule
  on exact positions (identical); the one observed difference is two spaces before `.` and `,` in a real license text (not
  redistributed), where PDF.js inserted a space and the port did not.
- **Right-to-left text** is reordered with the Unicode bidirectional algorithm of `golang.org/x/text`, PDF.js uses its own port of it:
  fixture `fonts-encodings-rtl.pdf` (Hebrew in visual order) is identical; mixed numbers and neutrals are not proven to be.
- **Vertical writing** (`WMode 1`, `-V` CMaps) and Type3 glyph metrics are approximated; rotation by any angle follows the text direction
  (fixture `rotated.pdf`).
- **Recovery**: a damaged cross-reference table is rebuilt from the `N G obj` headers and the last trailer that has a `/Root`
  (fixture `corrupt-xref.pdf`, identical); objects that live in object streams are not found by the rebuild, and a file that lost its
  trailer fails, as in PDF.js (`corrupt-truncated.pdf`, identical).
- **Error messages** of the library are mapped to the three PDF.js messages above; every other open failure reads `Invalid PDF structure.`
  where PDF.js has more specific ones (for example for a missing page tree).
- **The saved file is private** (directory 0700, file 0600); the original writes with the default mode. Nothing else reads it.
- **Datalab transport**: the calls go through the provider transport, so a configured proxy applies; a redirect is `fetch failed:
  unexpected redirect`; a request that outlives the shared deadline reads `request timed out (...)`, not the runtime's TimeoutError text;
  the key from `datalabApiKey` is read on every call (the original caches the config for the process).
- **No activity monitor** around the fetch (curator slice).
- **fetch-pdf-answer**: its six answer-mode twins stay named skips, each with its own reason, until the answer-mode slice ports the model
  call (`mode: "answer"` is still refused in `tool_fetch.go`); the PDF half of each is a Go test beside it
  (`TestPDFAnswerModeExtraction*`, driving `ExtractContent` with `Mode: "answer"`). The readable-PDF twin runs through the tool.
  `pdf-extract` keeps two named skips: `works on Node 22 without native Promise.try` (a Node runtime shim) and `passes PDF.js
  errors-only verbosity` (a PDF.js option); both have no Go counterpart.

### Scenarios and harness gaps

`fetch-pdf-readable`, `fetch-pdf-disabled` and `fetch-pdf-datalab-fallback` (the Datalab upload request and its rejection, then the
local text) pass under Pi 1402973a, PiG's Node runtime and the Go port, with goldens recorded under Pi (`port/make-pdf-scenarios.mjs`
writes them; the saved-file directory is normalized). Two gaps in the harness are reported to the lane lead: a scenario route cannot
carry binary bytes (the body is a JSON string), so only uncompressed ASCII PDFs can be served; and `{{server:NAME}}` is not expanded in
route bodies, so a fake Datalab server cannot return an `upload_url` that points at itself and the full Datalab flow cannot run in a
scenario. That flow is proven instead by `port/record-datalab-requests.mjs`, which runs the original with `fetch` replaced by a
recorder (`testdata/datalab-wire.json`, seven cases) and `TestDatalabWireFixture`, which requires the same requests (method, URL,
headers, JSON, multipart fields in order, the uploaded bytes) and the same result or error. The failure texts of a bad or oversized PDF
carry the fetch "Fallback options" list, which this base lists differently from the original until the hosted-provider slice lands, so
no scenario asserts them (twins do).
