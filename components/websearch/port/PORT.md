# Port record: websearch (pi-web-access 0.33.0, first slice)

Original: **pi-web-access 0.33.0** by Nico Bailon (MIT), commit `9a734ed195da2f4cccc2fb5e7128f6774a380f47`, kept
unmodified in [`oracle/`](oracle). Built against **PiG 0.3.0** (Pi 0.87.1 API), on the public Go SDK only.
Credits: [`../CREDITS.md`](../CREDITS.md).

## Status: the first slice, not the whole port

Search, fetch and source retention are ported and twinned. Everything else is a **named skipped test**:
`pigeq twins check --ledger port/upstream-tests.json --go extensions/websearch` accounts for all
875 upstream test titles (777 exact twins, 98 named skips, each with a reason). Do not call this port complete.

| Slice 1 (done) | Deferred (named skips) |
|---|---|
| Tools `web_search` (workflow `none`), `fetch_content` (readable/raw, images), `get_search_content`, `source_check`, `web_enable` and the `/search` command; dynamic activation with transcript-based selection; tool renaming and registration gates; schemas hash-identical to the original | Curator UI and every command but `/search`; `summary-review` / `auto-summary` workflows; activity widget and shortcut; TUI render hooks |
| Providers Exa (keyed and keyless MCP), Brave, Tavily (key pool), Perplexity, DuckDuckGo, SERPdive, Kagi (search and extract), TinyFish, Search1API, Searchinfinity, Querit, Parallel, Parallel MCP, Ollama, Firecrawl, Crawl4AI, Bright Data (SERP and Unlocker), Gemini (API, ADC, Cloudflare gateway; also the `gemini` fetch provider, URL context and PDF conversion), OpenAI, xAI, Kimi, Mistral, Serper, SerpApi, SerpBase, Serply, Valyu, Bocha, Baizhi, You.com, AnySearch, XCrawl, SearXNG, Jina Search; routing (`auto`, `all`, arrays, `searchRouting`, `allowedProviders`), error classification, credential sources | None: every search provider of the original is ported |
| SSRF guard, redirect limits, domain policy, proxy scoping, private 0600 fetch cache with TTL, retained sources restored from the session, inline data-URI sanitiser, config loading | Defuddle fallback |

## Refusals (until the slices above are ported)

Every unported option, mode or URL kind is refused with one message and returns a tool error (`refusal.go`,
`refusal_test.go`; the message is checked against `package.json` and `provenance.json`):
`@pi-in-go/pigpen-websearch (Go port) does not support <feature> yet. Install the original extension, npm:pi-web-access@0.33.0, which runs unchanged in PiG.`

| Feature | When | Where |
|---|---|---|
| `web_search workflow "summary-review" / "auto-summary"` | parameter or `workflow` in config | `tool_search.go` |
| `the Defuddle fallback extractor` | readable extraction failed or was short and the RSC payload gave nothing, where the original tries Defuddle before giving up (the refusal follows the original's verdict) | `extract.go`, `defuddle.go` |

A batch of URLs is refused whole when one URL is. Not registered, so not advertised: the commands `/websearch`, `/curator`,
`/google-account`, the shortcuts and the activity widget. Provider availability follows the original for every provider (OpenAI, xAI and Kimi read the model registry, see slice 4a; Gemini: see "Slice: Gemini").

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
  SearXNG have no such branch upstream, where a deadline is an abort error ("The operation was aborted due to timeout"); here it
  is the same since the extract-fidelity slice (see "Provider timeouts"), so routing does not fall through it. Caller
  cancellation is an abort everywhere.
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
- **Own timeouts** are aborts, as upstream (a `fetch` cut off by `AbortSignal.timeout` reads "The operation was aborted due to
  timeout", which the routing classifier takes for an abort; see "Provider timeouts"); Querit keeps its own wording,
  `Querit <Search|Contents> API request timed out after N seconds`.
- **JSON parse messages** are Go's, not V8's, where a provider quotes them (`... returned invalid JSON: <detail>`).
  Bright Data's config parse error keeps only a position, as upstream; V8 omits the position for a bad token at a value
  position and Go cannot tell all V8 cases apart, so the port omits it for Go's "looking for beginning of value" and "in literal"
  messages and prints `at position N (line L column C)` for the structural ones.
- **Error names** (`TypeError`, `AbortError`) do not exist for Go errors; only messages are compared.
- **Search options** `TimeoutMs` and `Lookup` exist on `SearchOptions` for the self-hosted Firecrawl search (the original's
  `FirecrawlSearchOptions`), and `FetchRemoteOptions.OnRedirectErr` carries the Crawl4AI refusal of a cross-origin redirect.

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
`pigeq twins check --ledger port/upstream-tests.json --go extensions/websearch` at the head of the stack through this slice: 661 exact twins, 214 named skips.

Wiring: `bridgeContext` attaches two session views to a request's context: `ExtensionContext` (the model providers' credential reads: registry `GetAll`, `Find`, `GetApiKeyAndHeaders`, session id) and `SideCallContext` (the model calls below). They read the same Pi `ExtensionContext`; the second adds `getAvailable`, `complete` and `scopedModels`. The session's model state reaches the tools through `SideCallContext` (`sidecall.go`), attached to the request's
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
- **Page length and cut in UTF-16 units, a block at a time.** The original keeps the length of a string and slices it without copying (a lazily joined string until something reads it). The port has to scan a 2 MB page to know its length in UTF-16 units and to find the cut, so `jsLen` and `jsPrefix` (`jsstr.go`, `page_query.go`) test 64 bytes of ASCII per step (`asciiRun`, a read-only view of the string) and fall back to the decoder for any other rune, with the same result as the rune loop (`TestJSLenMatchesTheRuneLoop`, `TestJSPrefixMatchesTheRuneLoop`; three mutants). The summary prompt is written into one buffer. See the pull request for the measured effect.
- **`ctx.model`** is the registry's model for the session's provider and id (the SDK gives the pair, not the object).
- **JSON parse errors** in `web-search.json` carry Go's decoder text where the original shows V8's, as for the rest of the config.
- **Provider `null`** in a stored query result reads as "unknown" only when it is empty (a Go string has no `undefined`).

## Slice: GitHub repositories, issues and pull requests

Ports `github-extract.ts`, `github-api.ts` and `github-issue-pr.ts` and their call site in `extract.ts` (`extract.ts:719-742`).
Files: `github_extract.go` (URL parsing, settings, the clone runtime and its cleanup, the views of a clone),
`github_api.go` (the `gh api` view), `github_issue_pr.go` (issue and pull request documents: `gh` and the REST fallback),
`procrun.go`, `procgroup_unix.go`, `procgroup_windows.go` (child processes) and `nodedecode.go` (Node's lenient base64 and
UTF-8 decoding, `parseInt`). The `GitHub repository extraction (clone)` and `GitHub issue and pull request extraction` refusals are gone;
`githubClone.enabled` and `githubPrIssue.enabled` work as upstream.

Programs: `gh` (`--version`, `api`, `repo clone`, `issue view`, `pr view`) and `git clone`, with the original's arguments, timeouts
(5 s probe, 10 s and 15 s API calls, 20 s for a whole issue or pull request, `githubClone.cloneTimeoutSeconds` for a clone) and
buffers. The extractor state of the original's module variables (the clone cache, the settings, whether `gh` works) is one
`githubState`; the extension has one for the process and the tests make more to stand for other processes.

Proof:
- twins for `github-extract` (16) and `github-issue-pr` (16): every upstream title, with fake `gh` and `git` programs (`/bin/sh`
  scripts ahead of `PATH`) where the upstream tests use Node scripts. A "process" of the upstream tests that share a clone directory
  is a `githubState` in the Go twins. Red commit first (30 of 32 failed against the stubs).
- `port/record-github-wire.mjs` runs the vendored original with the same fake programs and records 44 cases (clone views of every
  kind, the cache and concurrent callers, the size threshold, forceClone, commit SHAs, clone failure and timeout, configuration
  errors, issue and pull request documents through `gh`, the REST fallback with a recording `fetch`, direct renderer cases and
  Node's decoders): the commands started (arguments, and the environment a clone sets) and the documents returned.
  `extensions/websearch/testdata/github-wire.json` holds the recording and `github_wire_test.go` replays it against the port
  (`node record-github-wire.mjs --check` fails when a fresh recording differs).
- three pigeq scenarios with `gh` and `git` declared in `commands`: `fetch-github-clone` (`git clone` through the shim, the cache),
  `fetch-github-api` (the size threshold, a commit SHA and `forceClone` through a canned `gh`) and `fetch-github-pr` (a pull request and
  an issue through a canned `gh`). The shims cannot give a clone content (the harness forbids `GIT_*` variables, so no `insteadOf`
  redirect reaches `git`, and a canned program answers every call alike), so the clone views are proven by the recording above.
  `exec:gh` is no longer an accepted gap; `exec:taskkill` (the Windows process-tree kill, which no Linux or macOS scenario reaches) is.
- `github_route_test.go`: the routing inside `ExtractContent`, the session hooks that clear the clone cache and edge cases of URL parsing.

Deliberate differences:
- **os/exec, not Context.Exec.** The original starts `gh` and `git` with Node's `child_process`, not `pi.exec`: it adds `GIT_TERMINAL_PROMPT=0`,
  `GCM_INTERACTIVE=Never` and `GH_PROMPT_DISABLED=1` to the environment (so a credential helper never reads the terminal) and kills the whole
  process group of a clone that times out (SIGTERM, then SIGKILL after 3 s; `taskkill /T /F` on Windows). `Context.Exec` takes neither an
  environment nor a process group, so the port runs them with `os/exec` like `credential.go`. SDK request for a later switch: `ExecOptions.Env`
  and termination of the process tree, as Node's `spawn` with `detached` provides.
- **Cancellation reaches the `gh api` calls.** The original starts them without the caller's signal, so a cancelled fetch lets them finish
  (up to 15 s) and returns their result; here they are cancelled with the call and the result is the usual "Aborted".
- **A child's pipes are not waited for forever.** After a killed or exited child the call waits 0.5 s for its pipes (a killed shell script
  leaves its `sleep` holding them); execFile's callback fires on abort without waiting, and otherwise waits for them.
- **Timer limit.** `githubClone.cloneTimeoutSeconds` above 2,147,483.647 (Node's `setTimeout` maximum) is that maximum; Node would fire the timer after 1 ms.
- **File views read a bounded prefix.** A file or README is read for the first 100,001 (8,193) UTF-16 units only (three bytes cover a unit),
  where the original reads the whole file before cutting it; the output is the same.
- **Messages that quote a platform error** are Go's: the REST fallback's `GitHub REST fallback failed: <message>` for a transport or JSON error
  (`fetch failed`, `Unexpected token ...` upstream), a configuration that does not parse (`Failed to parse <path>: <message>`, only the prefix
  is compared), and `Could not inspect <path>: <message>`.
- **No activity log.** The original opens an activity entry around a clone (the activity widget belongs to the curator slice).
- **The runtime directory sweep on Windows** only removes a runtime whose process id `OpenProcess` reports as nonexistent; Linux and macOS
  follow the original (boot id and start time on Linux, `kill(pid, 0)` elsewhere). The directory names (`runtime-` and six letters or
  digits), the owner file and the clone digest are the original's, so the original and the port can share a clone directory.
- **The hint** for a missing `gh` goes to stderr once, as `console.error` does.
- **Large API responses** read through `restJson` are capped at 16 MB (the provider body cap); upstream reads the body whole.

## Mapping (original file -> Go)

`storage.ts` storage.go - `utils.ts`/`ssrf-protection.ts`/`credential-source.ts` config.go, credential.go, ssrf.go, proxy.go,
transport.go, proxy_curl.go (`fetchViaCurl`), hostexec.go - `rsc-extract.ts` rsc_extract.go - the Readability and Turndown steps of `extract.ts` htmlmd.go, turndown.go - `pdf-extract.ts` pdf_extract.go, featureconfig.go (`pdf.*`), pdf_text.go (the unpdf extraction) - `datalab-pdf-extract.ts` datalab_pdf_extract.go - `gemini-search.ts` (routing) search.go, searchcommon.go - `brave/tavily/perplexity/exa/duckduckgo/serpdive/kagi/serper/serpapi/serpbase/serply/valyu/bocha/baizhi/you/anysearch/xcrawl/searxng.ts`
same names (`jina-search.ts` jinasearch.go; the shared mechanics of the keyed REST providers are in restcommon.go; the hosted providers are listed in "Slice: hosted search and fetch providers") - `github-extract/github-api/github-issue-pr.ts` github_extract.go, github_api.go, github_issue_pr.go (and procrun.go, nodedecode.go) - `extract.ts` extract.go, htmlmd.go, imageproc.go, declared_links.go, featureconfig.go - `find`/`source-check`/`fetch-params`
find.go, sourcecheck.go, fetchparams.go - `data-uri-sanitize.ts` datauri.go - `index.ts` tools.go, tool_*.go, toolconfig.go,
schema.go, render.go, command.go, extension.go - `tool-activation.ts` tool_enable.go - `page-query.ts` page_query.go, fetch_answer.go -
`query-rewrite.ts` query_rewrite.go - `summary-model-scope.ts` summary_model_scope.go - `summary-review.ts` (generation) summary_review.go -
`opencode-session-headers.ts` opencode_headers.go - `abortable.ts` abortable.go.

## Deliberate differences

- **HTML to Markdown** is `github.com/go-shiori/go-readability` (a line-by-line port of Readability.js 0.5.0; the original runs
  @mozilla/readability 0.6.0) and a hand port of turndown 7.2.4 (`turndown.go`) over `x/net/html`, where the original has linkedom
  (htmlparser2) and domino. Recorded from the original (`port/record-html-extract.mjs`, `port/record-turndown.mjs`): 25 HTML
  fixtures and 177 turndown snippets, byte for byte equal except the fixture below. Remaining differences: **malformed HTML**
  (`malformed-misnested`): linkedom and `x/net/html` build different trees for misnested inline tags and an unclosed table
  (the original keeps `italic only` unstyled and turns the cells into paragraphs; the port applies the HTML adoption agency
  and loses the table); an article whose `articleContent` is non-null but empty reads as null here (no fixture reaches it);
  JSON lone surrogates in an RSC chunk become U+FFFD; a non-string RSC `href` is not the TypeError it is upstream.
- **RSC payload fallback** (`rsc-extract.ts`) is ported (`rsc_extract.go`), proven on four recorded fixtures (main chunk,
  fallback chunks with ordering and de-duplication, short, malformed). **The Defuddle fallback is not ported**: no Go port tracks
  Defuddle 0.19.3 (dotcommander/defuddle and kaptinlin/defuddle-go build the Markdown with html-to-markdown and score
  differently, vaayne/go-defuddle runs the JavaScript in an embedded engine) and its removal, scoring, standardization and
  Markdown steps are several thousand lines. A page the original would give to Defuddle is reported with the original's verdict
  followed by the refusal (`the Defuddle fallback extractor`); four upstream `rsc-fallback` cases stay named skips.
- **Provider timeouts** are aborts, as in the original (`searchcommon.go timeoutFix`: a provider's own deadline reads "The operation
  was aborted due to timeout", so `searchRouting` does not fall through and the search stops). Twins: `timeout_test.go`. Providers
  with their own timeout text (SerpApi, Serply, You.com, XCrawl, Jina, Querit, Kimi) keep it. The provider slices of the stack
  (REST, hosted, model, Gemini) describe their timeouts in terms of this rule.
- **SSRF hardening beyond the original**: the address is checked again when the connection is made (a name that changes
  between validation and connect cannot reach an internal address); IPv6 multicast, NAT64 and 6to4-embedded IPv4 are blocked.
- **Config directory** is PiG-first (`PIG_CODING_AGENT_DIR`, then `PI_CODING_AGENT_DIR`, XDG `pig`, `$PIG_HOME/agent`), with Pi's
  locations as read fallbacks; a new config is written under `$PIG_HOME/agent`.
- **Config reads are cached**: the original reads `web-search.json` with `readFileSync` on every call; `ReadConfigRoot` keeps the
  parsed file and serves it while a stat (size, mtime in nanoseconds, file identity) still matches. A file modified within the
  last 2 seconds is never cached (the racy-write guard), an edit is picked up on the next call, and `ResetCaches` drops the entry.
  The returned map is shared, so callers treat it as read-only. Twins: `config_cache_test.go`; mutants `config-cache-*`.
- **Config reads of the Gemini files** go through the same cache (`readConfigValue` keeps the decoded document whatever its root
  type; `ReadConfigRoot` rejects a non-object root, the Gemini files read it as empty like `gemini-api.ts`). They used to open,
  read and parse the file several times per search. The directory and the file path are memoized together. Twins:
  `config_gemini_cache_test.go`; mutants `gemini-config-non-object-root-ignored`, `config-cache-stores-no-value`.
- **Provider responses are decoded by a direct parser** (`unmarshalAny`, `fastjson.go`) into the generic values the callers read
  with `jsonPath` and type assertions. `encoding/json` decodes into an interface through reflection and allocated 78 percent of the
  bytes of a Gemini search. The parser takes the well-formed common case; a syntax error, a repeated key, invalid UTF-8, a lone
  surrogate, a number out of range or nesting beyond 200 levels goes to `encoding/json`, so value and error text are the standard
  ones by construction. `fastjson_test.go` compares both on the recorded provider fixtures, an edge-case table and a seeded random
  corpus (with damaged and truncated copies) and has a fuzz target; mutants `fast-json-*`. Response bodies are read into one
  buffer sized from the announced length (`readBody`; mutants `read-body-*`), and a plain host name in a domain filter skips the
  URL parse (`NormalizeDomain`, `fastpaths_test.go`; mutants `domain-shape-*`, `numeric-label-*`).
- **Tool errors**: where the original returns `isError: true` inside the result object (Pi ignores it), PiG marks the result as an
  error, so the model sees `web_enable` failures as failures.
- **Invalid `web-search.json`** makes the original throw at registration; an extension cannot fail its load here, so nothing is
  registered and the problem is shown at session start.
- **A session that cannot be read** (restore or transcript selection) still starts; the user gets a warning.
- **Proxied requests run `curl`** through the host's command runner (`proxy_curl.go`, `hostexec.go`), with the original's arguments,
  header dump, write-out JSON and temporary files (`port/record-proxy-curl.mjs` records the original's invocations for 17 requests;
  `testdata/proxy-curl.json`; scenarios `fetch-proxy-curl` and `fetch-proxy-curl-config` run the real `curl` against a fake HTTP proxy),
  so SOCKS4 and SOCKS4a work. Differences: the host runner cannot tell a missing `curl` from a failing one, so the original's "curl
  executable not found on PATH" reads "curl exited with code 1 via <proxy>"; the body read back from curl's file is bounded at 64 MB;
  requests are single hops (every caller follows redirects by hand, as `fetchRemoteUrl` does with `redirect: "manual"`), so the
  follow-mode header stripping of the original's fetch wrapper has no counterpart; a localhost or NO_PROXY host is fetched directly,
  behind the dial-time address check.
  Stack integration: the original wraps the global `fetch`, so every request of a proxied call runs curl (Gemini API, hosted
  providers, thumbnails, redirect probes, Datalab), except Gemini Web, which uses its own undici agent and the proxy environment. All
  of those paths here end in `guardedFetch`, `doAPI` or `apiClientFor`, which choose curl; `proxy_paths_test.go` runs one entry point
  of each family under a scoped proxy and requires curl and no `net/http` request. The `exec:curl` accepted gap is gone: the two
  scenarios declare `curl`.
- **Credential commands** run through the bare `sh` (Go) instead of `/bin/sh`, so the harness can record them and NixOS-style systems work.
- **Guidance** (review fix): the "No search provider available" error of `auto` is the original's (gemini-search.ts:820-828) and
  lists every provider of the port, OpenAI/Codex `/login` included, and (since the browser cookie slice) its item 5, signing
  into gemini.google.com in a Chromium browser (`auto_guidance_test.go`). The fetch "Fallback options" list is the original's,
  hosted providers and Gemini lines included.
- **Image pixel budget** (review fix): images larger than 2000 px are decoded for resizing only up to 64 megapixels
  (8192x8192). Go's decoders allocate the full pixel buffer from the declared size, so a few kilobytes of PNG could
  otherwise demand gigabytes and kill the extension process; larger images fail with "Image too large to process".
  Full decodes run one at a time: under the cap a progressive JPEG can still cost about 1 GiB, and fetches run three at once.
- **Panic boundaries** (hardening): the fetch, multi-query and all-provider fan-outs and the background prefetch recover a
  panic into that URL's, query's, provider's or prefetch's error. The SDK recovers only the request goroutine, and in a
  Piglet Binary this extension runs inside the agent process.
- Tool schemas are hash-identical to the original's (`tools-default.json` in `golden/`); on the wire the SDK carries them as JSON
  objects, so key order is the SDK's, not the original's.
- The tool descriptions are kept identical to the original even where they mention unported capabilities (YouTube,
  PDF, video, Node fetch): the original's own compatibility test pins their hashes.

## Slice 4a: model-backed providers (OpenAI, xAI, Kimi, Mistral)

Ported from `openai-search.ts`, `opencode-session-headers.ts`, `xai-search.ts`, `kimi-search.ts` and `mistral-search.ts` into
`openai.go`, `opencode_headers.go`, `xai.go`, `kimi.go`, `mistral.go`, with `model_providers.go` (shared helpers) and
`extensioncontext.go` (the seam below). Their rows are gone from the "Refusals" table: `openai`, `xai`, `kimi` and `mistral` run
like the other ported providers. 91 upstream titles are twinned (`openai-alpha-search` 42, `openai-origin` 1,
`openai-search-opencode-headers` 5, `xai-provider` 14, `kimi-provider` 14, `mistral-provider` 11, and three of the four of
`provider-credential-source`), plus `auto still uses provider fallback when no provider is configured` of `provider-precedence`.
`pigeq twins check --ledger port/upstream-tests.json --go extensions/websearch` at the head of the stack through this slice: 620 exact twins, 255 named skips (the first slice had 305 and 570).

**Credentials, mapped to the public Go SDK.** The originals call `ctx.modelRegistry.getAll()`, `.find(provider, id)` and
`.getApiKeyAndHeaders(model)` and read `ctx.model` and `ctx.sessionManager.getSessionId()`. The port reads the same through
`Context.ModelRegistry()` (`GetAll`, `Find`, `GetApiKeyAndHeaders`, which the host answers with `ok`, `apiKey`, `headers`,
`baseUrl`), `Context.Model()` with `Context.ModelProvider()` and `ModelRegistry().Find` for `ctx.model`, and
`Context.SessionManager().GetSessionID()`. A tool call carries them in its `context.Context` (`WithExtensionContext`,
`ExtensionContextFrom`): the Go counterpart of the original's `options.extensionContext`. Nothing reads `auth.json`: the original
does not either, the OAuth refresh and the login belong to the host. Standalone keys (`openaiApiKey`, `xaiApiKey`,
`mistralApiKey`, `OPENAI_API_KEY`, ...) use the shared credential sources. No SDK capability is missing. The SDK also has
`Context.GetModelInfo` (0.5.0); the module still requires SDK 0.4.1, so `ctx.model` is read with `Model`, `ModelProvider` and
`Find`, which is the same object the registry holds.

**Fixed endpoints and how they are proven.** `api.x.ai`, `api.kimi.com`, `api.mistral.ai`, `api.openai.com` and `chatgpt.com`
cannot be redirected to a pigeq server under the unmodified original; only OpenAI has a configurable endpoint
(`openaiResponsesUrl`). So: pigeq scenarios cover OpenAI through a fake server (`web-search-openai-responses`,
`-alpha`, `-registry-key` (the key held in `auth.json` reaches the request under Pi and PiG alike), `-http-error`) and the
credential and configuration errors of all four providers (`web-search-model-providers-unavailable`, `-bad-config`).
For the fixed endpoints, `port/record-model-requests.mjs` runs the original modules with a recording `fetch` and checks in
`port/fixtures/model-provider-requests.json` (18 cases: method, URL, headers, raw body, result or error);
`TestModelProviderWireFixtures` replays every case against the port and requires the same.

**Deliberate differences**

- Parallel queries of one `web_search` call reach the endpoint in no fixed order, so the twin of `alpha executes through
  registered web_search ...` compares the two queries as a set (the upstream asserts start order).
- `configured routing does not fall back when the caller cancels Kimi` cancels while the request is in flight; the upstream
  case aborts before the call and still reaches `fetch`, while `Search` here answers a caller that is already gone before any work
  (a first-slice behaviour).
- `JSON.parse` messages are V8's; the port reports Go's decoder text after `returned invalid JSON:` (the prefix and the
  classification are the same).
- xAI, Kimi and Mistral call plain `fetch`, which follows up to 20 redirects and drops `Authorization` across origins; the port
  uses the API redirect helper (5 redirects, `Authorization`, `Cookie` and `Proxy-Authorization` dropped across origins).
- A registry's extra headers are applied in alphabetical order: the host's JSON object does not keep Pi's insertion order. It
  matters only when two header names differ in case, which the port joins with `, ` like `Headers` does.
- `pickSearchModel` sorts with `localeCompare(..., {numeric: true})`; Go has no ICU, so `compareNumericLocale` implements the
  ordering for model ids (digit runs by value, punctuation before digits before letters, case-insensitive first and lower case
  first among equals). It is exact for ASCII ids.
- A non-object `web-search.json` root fails every provider with `Invalid config in <path>: expected a JSON object`; the original
  does that for Mistral only.
- The activity monitor (`activityMonitor.logStart/logComplete`) is not driven: the activity widget is the curator slice.
- Provider timeouts are aborts (see "Provider timeouts" above), except Kimi, whose own
  `Kimi Code search API timed out` message is ported.
- The shared "No search provider available" text of `auto` lists every provider present (integration of this slice with the Gemini slice).
- Responses of these providers are decoded by the direct parser described under "Deliberate differences" (`unmarshalAny`), the request
  body is encoded into one pre-sized buffer, and OpenAI computes the domain filters once per request. Twins: `fastjson_test.go`
  (the recorded model fixtures are part of its corpus), `model_fastpaths_test.go`; mutants `openai-snippet-keeps-markdown-links`,
  `uuid-variant-bits`.

**Named skips kept** (no Go counterpart): `curator page exposes xAI as a manual provider` and `Curator page exposes Mistral as an
available manual provider` (the curator page is the curator slice). `provider-credential-source` is twinned over the original's
seven providers (parallel, tinyfish and the Cloudflare gateway rows joined once those slices were in the stack) plus Mistral
(`credentialSourceRedactionAttempts`).

## Security posture

- SSRF guard on every remote fetch (private, loopback, link-local, multicast, NAT64, 6to4), with redirect limit 5 and per-hop
  re-validation; `ssrf.allowRanges` exempts explicit CIDRs, and requests through a proxy (`proxy`, or the environment
  proxy with `ssrf.trustEnvProxy`) skip the dial-time check (see below).
- **Browser cookies are opt-in**: nothing reads a browser profile unless `allowBrowserCookies` is true in `web-search.json`
  (or `PI_ALLOW_BROWSER_COOKIES=1`); see "Slice: browser cookies".
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
| source_check fetchContent uses the explicit proxy for result pages | provider tavily instead of openai (not ported here); the curl arguments are asserted | `proxy_test.go` |
| fetch_content passes the explicit proxy through queued extraction | the page body is padded past the 500-character usefulness floor: this port's HTML pipeline (like the original's, which also rejects the short fixture body) reports shorter pages as incomplete | `proxy_test.go` |
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
| Twins | `pigeq twins check`: 777 exact + 98 named skips = 875 titles; exit 0 |
| Go tests | `go test -race -count=24` with `GOMAXPROCS=4` passes; `go vet` clean on linux, windows, darwin |
| Equivalence | 9 scenarios, replayed on PiG with the Go port: identical traces (`pigeq check --go`). The first 5 were recorded from the original under **Pi 1.0.1** (registration, schemas, system-prompt snippets, dynamic activation, renaming and the tool gates). Four were recorded under **Pi 1402973a** and `pigeq run` shows the original under Pi, the original under PiG's Node runtime and the port identical: `web-search-tavily` (the request to a fake Tavily server and the rendered answer), `fetch-text-page` and `fetch-raw-404` (the model's tool call names a fake page server through `{{server:web}}`), `fetch-private-address-refused`. They use the harness's `normalize` rule N7 for the `responseId` that `generateId` (storage.ts) draws on every run: `Date.now().toString(36)` plus 1 to 6 random base-36 characters (so 9 to 14 characters, and a leading `m` to `o` until 2029). One rule covers the id in text (`(responseId[": ]+)[0-9a-z]{9,14}\b`), one the id as a whole value (`^[m-o][0-9a-z]{8,13}$`); a bare 14-character rule was tried first and also hid `searchinfinity` and `simultaneously` in the recorded system text |
| Mutation | `mutations.json`: 404 mutants (slice 1, the REST providers, the config cache, the hosted providers, Gemini, the model providers, answer mode, PDF, GitHub, browser cookies, video, and the decoder, body read and domain fast paths, video, extract fidelity); each slice's own mutants were all killed (`pigeq mutate --unit`) at its stack position, and the whole file again at this head (cumulative run) |
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
- **Gemini Web** (`gemini_web_client.go`): the interface the original's `gemini-web.ts` callers use (`GeminiWebClient`:
  `Available`, `Diagnostic`, `Query`); the default client is the cookie client of "Slice: browser cookies".

Deliberate differences:

- Provider timeouts are aborts, as in the original (see "Provider timeouts"): configured routing does not fall through them.
- The activity monitor (curator slice) is not ported, so no activity is logged around Gemini calls.
- The fetch "Fallback options" checklist names the Gemini API (`GEMINI_API_KEY`) and Gemini Web ("Sign into gemini.google.com in Chrome").
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

`port/make-pdf-fixtures.mjs` writes 25 PDFs by hand (no PDF library: text, several pages, metadata, Unicode through WinAnsi and
through a Type0/ToUnicode font, Cyrillic, CJK, Hebrew, ligatures, `/Differences`, run splitting and kerning gaps, flate content,
RC4-40, AES-128 and AES-256 encryption, a password-protected file, four corrupt files, a scanned page, 120 pages, rotated text,
a form XObject, a content array, off-page text, an inline image, an object-stream and xref-stream file, control codes, and a
30-page report, and spacing operators with item splits). `port/record-pdf-extract.mjs` runs the original `pdf-extract.ts` (unpdf 1.8.0, PDF.js 6.1) over them under Node 24 in the
container and records the saved Markdown or the error (`testdata/pdf-unpdf.json`, also the title and file name derived from 16 URLs).
`TestPDFCorpusAgainstUnpdf` requires byte-identical Markdown, page and character counts and file names, and identical error text, on
every fixture but the one below. Seven PDFs outside the corpus (not redistributed: four of the library's own test and example files and three real documents) were
compared the same way while building the extractor: five were identical, including a 19-page, 37 356 character specification; a
library test file with NUL character codes differed until the control-code rule was added (fixture `control-codes.pdf`; not re-compared
afterwards); a 6-page license text differs in 2 of 33 728 characters (see the font metrics item below).

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
- **Cancelled PDF answer**: the original's `execute` resolves with `details.error` "Aborted" for an already aborted signal; the Go tool returns an `Aborted: context canceled` error for a cancelled context, as every `fetch_content` cancellation does (`tool_fetch.go`). The twin accepts either form and still requires that no request and no model call happened.
- **fetch-pdf-answer**: all seven upstream cases are twins. The six answer-mode ones run through `fetch_content` with a scripted
  model registry (`pdfAnswerFixture`, the upstream test's `ctx`) on top of the answer-mode slice; the PDF half of each is also a Go test
  beside them (`TestPDFAnswerModeExtraction*`, driving `ExtractContent` with `Mode: "answer"`).
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
carry the fetch "Fallback options" list; no scenario asserts them (twins do).

Mutations: 50 mutants for this slice in `port/mutations.json`, all killed by `pigeq mutate --unit`. Three candidates were removed as
equivalent: no cancellation rethrow in the chain (the local extractor checks the context itself, so the result is the same), no
wide-gap flush (a flush and an inserted space both read as one space after the join), and no baseline-change flush (a new line always
begins with a gap beyond -0.2 em, which flushes first); one was removed because it did not compile.

## Slice: browser cookies (auth-fetch, chrome-cookies, gemini-web, gemini-web-config)

Authenticated fetch (`fetch_content` with `auth`) and Gemini Web are ported, and their refusals are removed. Both are opt-in, as in
the original: nothing reads a browser profile unless `allowBrowserCookies` is true in `web-search.json` or
`PI_ALLOW_BROWSER_COOKIES=1` / `FEYNMAN_ALLOW_BROWSER_COOKIES=1`. The same programs run, through the host (`Context.Exec`, the
original's `pi.exec` equivalent, `hostexec.go`): `security` (macOS keychain), `secret-tool` and `kwallet-query` (Linux),
`powershell.exe` (Windows DPAPI), `sqlite3` and `python3` (reading a copy of the cookie database). No secret is printed, logged
or put in a diagnostic.

- `auth_fetch.go`: the `authFetch` profiles (named, `auth: true` for a single profile, host policy, same-origin redirects), the
  per-hop cookie header, the direct-only transport (no hosted provider ever receives the cookies), and the session cache
  (`cache: "off"` stores nothing and the tool text and details say so).
- `chrome_cookies.go`: macOS (Helium, Chrome, Brave, Arc), Linux (Chromium, Chrome; Secret Service, then KWallet on KDE, then the
  basic-store password) and Windows (Chrome, Edge; DPAPI key, AES-GCM v10, v20 reported as unsupported); profile discovery and
  traversal checks, the process password cache (a failed lookup is retried), the diagnostics with their attempts, and the
  expiry and partition filters of the query.
- `gemini_web_config.go`, `gemini_web.go`: the opt-in, the `browserCookies` selection, the Gemini Web protocol (access token,
  `StreamGenerate`, uploads, account email), cookies never sent across origins, generation and upload with redirects refused,
  and a dedicated HTTP/1.1 transport with a 4 MiB response header budget, a 30 s connect timeout and the proxy environment.

Deliberate differences:

- node:sqlite is not available to a Go extension, so the SQLite CLI is tried first and python3 second (the original tries
  node:sqlite, then the same two). The scenarios therefore use placeholder database files that node:sqlite rejects, so the
  original falls back to the same canned programs.
- The host command runner reports a program that cannot start as exit code 1, like Pi, so "unavailable" (no `sqlite3` or `python3`
  on PATH) is told from "query failed" by looking the program up on PATH.
- The DPAPI-protected blob is embedded in the encoded PowerShell script: `Context.Exec` has no environment option for the
  original's `PIWA_PROTECTED`. The blob is only decryptable by the same Windows user. SDK capability to report: an `env` option on
  exec options, which Pi's own `pi.exec` lacks too.
- Cookies are sent to Gemini Web in alphabetical order (the original keeps database row order; the interface hands over a map).
- Profile names sort like `localeCompare` with numeric collation, approximated by a case-insensitive natural comparison.
- Twin without a Go counterpart: `resolveGeminiFetch falls back to the ambient global fetch when undici is not resolvable`
  (the dedicated transport is the standard library, always resolvable).
- Gemini Web and the fixed Google endpoints cannot be reached by a scenario server, and authenticated fetch requires HTTPS to a
  host the profile names; the scenarios therefore cover profile errors, the opt-in, and the full cookie-extraction chain up to the
  point where no request is sent. The request side is covered by the twins.
- Stack integration: the browser cookie opt-in (`allowBrowserCookies`) and the `browserCookies` selection are read through the
  stat-validated config cache of `config.go` (`readLenientConfig`), not by a file read of their own: the opt-in is checked on every
  Gemini Web and cookie call, and the original's `readFileSync` per call is slower than a cache hit. An edit of the file is still seen
  on the next call. `ResetCaches` also resets the cookie state and the selection.
- Stack integration: the Gemini slice's `GeminiWebClient` default is the cookie client, so `gemini_search` and the `gemini` fetch
  provider reach Gemini Web with the extracted cookies when cookie access is on (`TestGeminiSearchUsesTheCookieClientByDefault`; the
  Gemini routing twins keep using a fake client). The `auto` "No search provider available" text again carries the original's item 5.
- Two process mechanisms coexist: the cookie programs run through `Context.Exec` (`hostexec.go`), while the GitHub extractors run
  `gh` and `git` through os/exec because they need a child environment and a process-group kill that `Context.Exec` lacks.
## Slice: video (youtube-extract, video-extract, frames and timestamps)

YouTube URLs, local video files and `frames`/`timestamp` are ported, and the refusals `YouTube video extraction`, `local video file
analysis` and `video frame and timestamp extraction` are removed (`youtube_extract.go`, `video_extract.go`, `video_route.go`,
`frames.go`, `media_exec.go`, tool rendering in `tool_fetch.go`).

- YouTube (`extract.ts:709`, `youtube-extract.ts`): Gemini Web, then the Gemini API (`QueryGeminiAPIWithVideo`), then Perplexity,
  each failure kept as `Gemini Web: ...` lines of the combined error, the thumbnail from `img.youtube.com` when images are enabled.
- Local video (`video-extract.ts`): the Files API upload (start, upload, state poll every 5 s for up to 120 s, generate, delete
  that is not awaited), then Gemini Web with the file; the size limit (`video.maxSizeMB`, default 50) message; the first frame as
  the thumbnail.
- Frames and timestamps (`extract.ts:560-720`): single frames, ranges, `frames` density, duration checks, the same labels and titles;
  `fetch_content` shows each frame followed by `Frame at <timestamp>`.
- Programs: `yt-dlp`, `ffmpeg` and `ffprobe` with the original's arguments, timeouts (15 s, 30 s, 10 s) and 5 MiB frame limit.
  `exec:yt-dlp`, `exec:ffmpeg` and `exec:ffprobe` are no longer accepted gaps: the scenarios declare them.
- `GeminiWebClient` gained `QueryWithOptions` (model, YouTube URL, files to attach).
- Stack integration: the cookie client of the browser cookie slice (`cookieGeminiWeb`) implements `QueryWithOptions` by passing the
  options to `QueryGeminiWebWithCookies`; the hosted slice's `jsToNumber` was dropped for the video slice's (`frames.go`), which
  follows `Number()` (no `inf`, `nan`, hex floats or underscores); `fetch_unported.go` is gone because no URL kind is refused any more.

Proof:

- Twins named after the upstream titles: `local-video-oversize` (3), `youtube-extract-errors` (1) and the two `gemini-api-transport`
  cases, which now drive the whole upload, state and delete flow (no longer ADAPTED).
- `port/record-video-requests.mjs` records the original under Node with a stubbed `fetch` and shell scripts for the three programs
  (`testdata/video-wire.json`, 70 cases): the Gemini API, Perplexity and thumbnail requests (the endpoints are fixed, so there is no
  process-level differential for them), the commands started and every result text. `video_wire_test.go` runs the port against the
  same scripts and requires the same commands, requests and results.
- Scenarios `fetch-youtube-gemini` (a relay base), `fetch-youtube-frames`, `fetch-video-tools-missing`, `fetch-local-video-frames`
  and `fetch-local-video-upload-start` with canned programs. A YouTube URL on the local server (`<server>/youtube.com/watch?v=ID`)
  matches the original's unanchored pattern, so no DNS is needed. The upload start answer cannot carry an upload URL (route
  headers are not expanded with the server address), so the rest of the upload is proven by the recording only.

Deliberate differences:

- **os/exec, not Context.Exec.** The original calls `execFileSync`. `Context.Exec` cannot tell a missing program from a failed one
  (both code 1, and the original's messages differ: `ffmpeg is not installed`), returns stdout as a string (a JPEG does not
  survive it) and has no output limit. The port classifies ENOENT, ETIMEDOUT and ENOBUFS like Node and builds the same
  `Command failed: <program> <args>` text. SDK request: exec options with binary-safe stdout, a maximum output size and a
  distinguishable spawn failure.
- **Image and text block order.** A frame result is `[image, "Frame at <t>", image, ...]` then the text in the original (and a
  thumbnail is `[image, text]`). `sdk.ToolResult` is `Content string` plus `Images`, so the host receives the text first and the
  images after it. The tool output of the port is ordered like the original's (`TestFetchToolShowsFrames`); the scenarios for frames
  therefore use failing `ffmpeg` and `image.enabled: false`, where no image is in the result, and the frame results are proven by
  the twin and the recording. SDK request: ordered content blocks in `ToolResult` (upstream `content: (TextContent | ImageContent)[]`).
- A cancelled fetch kills the program (`execFileSync` cannot be cancelled) and the result is `Aborted`.
- Frames are read one after another, which is what the original's `Promise.all` over synchronous calls does.
- The file deletion after a Gemini upload ignores the caller's cancellation, as the original, with a 5 minute bound; its failure
  is dropped (the original prints it to the console).
- Config is read through the stat-validated cache of `config.go` (`readLenientConfig`), so a call does not re-read or re-parse
  `web-search.json` while it is unchanged; an edit is seen on the next call.
- The activity monitor is not ported, so nothing is logged around extraction.
