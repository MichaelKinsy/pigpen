# Port record: websearch (pi-web-access 0.33.0, first slice)

Original: **pi-web-access 0.33.0** by Nico Bailon (MIT), commit `9a734ed195da2f4cccc2fb5e7128f6774a380f47`, kept
unmodified in [`oracle/`](oracle). Built against **PiG 0.3.0** (Pi 0.87.1 API), on the public Go SDK only.
Credits: [`../CREDITS.md`](../CREDITS.md).

## Status: the first slice, not the whole port

Search, fetch and source retention are ported and twinned. Everything else is a **named skipped test**:
`pigeq twins check --ledger port/upstream-tests.json --go extensions/websearch` accounts for all
875 upstream test titles (309 exact twins, 566 named skips, each with a reason). Do not call this port complete.

| Slice 1 (done) | Deferred (named skips) |
|---|---|
| Tools `web_search` (workflow `none`), `fetch_content` (readable/raw, images), `get_search_content`, `source_check`, `web_enable` and the `/search` command; dynamic activation with transcript-based selection; tool renaming and registration gates; schemas hash-identical to the original | Curator UI and every command but `/search`; `summary-review` / `auto-summary` workflows; fetch `answer` mode and query rewrite (need a model-completion call); activity widget and shortcut; TUI render hooks |
| Providers Exa (keyed and keyless MCP), Brave, Tavily (key pool), Perplexity, DuckDuckGo, SERPdive, Kagi (search); routing (`auto`, `all`, arrays, `searchRouting`, `allowedProviders`), error classification, credential sources | The other 26 providers (OpenAI/Codex hosted search, Gemini, xAI, Jina Search, Firecrawl, ...): in the provider table; selecting or reaching one is refused (see "Refusals") |
| SSRF guard, redirect limits, domain policy, proxy scoping, private 0600 fetch cache with TTL, retained sources restored from the session, inline data-URI sanitiser, config loading | PDF text extraction (config is read), YouTube/video/frames, GitHub clone, Defuddle fallback, hosted fetch providers other than Jina, authenticated fetch, Gemini Web and browser cookies |

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
| `<Label> fetch provider` | the original would call a hosted fetch provider at this point of `fetchRouting` (base URL or key present) | `extract.go` |

A batch of URLs is refused whole when one URL is. Not registered, so not advertised: the commands `/websearch`, `/curator`,
`/google-account`, the shortcuts and the activity widget. Known approximation: whether an unported provider is available
uses its credential names (OAuth, ADC and browser-cookie availability of OpenAI, Gemini, xAI and Kimi is not known here).

## Mapping (original file -> Go)

`storage.ts` storage.go - `utils.ts`/`ssrf-protection.ts`/`credential-source.ts` config.go, credential.go, ssrf.go, proxy.go,
transport.go - `gemini-search.ts` (routing) search.go, searchcommon.go - `brave/tavily/perplexity/exa/duckduckgo/serpdive/kagi.ts`
same names - `extract.ts` extract.go, htmlmd.go, imageproc.go, declared_links.go, featureconfig.go - `find`/`source-check`/`fetch-params`
find.go, sourcecheck.go, fetchparams.go - `data-uri-sanitize.ts` datauri.go - `index.ts` tools.go, tool_*.go, toolconfig.go,
schema.go, render.go, command.go, extension.go - `tool-activation.ts` tool_enable.go.

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
  fallback chunks with ordering and de-duplication, short, malformed). **The Defuddle fallback** is ported (see "Slice 12:
  Defuddle fallback" below); the four upstream `rsc-fallback` cases that reach it are twins.
- **Provider timeouts** are aborts, as in the original (`searchcommon.go timeoutFix`: a provider's own deadline reads "The operation
  was aborted due to timeout", so `searchRouting` does not fall through and the search stops). Twins: `timeout_test.go`. Providers
  with their own timeout text (SerpApi, Serply, You.com, XCrawl, Jina, Querit) keep it. Slice branches that record "own timeouts use the
  network-class timeout" (rest-providers PORT.md 'Differences recorded with fixtures', hosted-providers PORT.md 'Own timeouts') must
  drop that note when they are rebased on this change.
- **SSRF hardening beyond the original**: the address is checked again when the connection is made (a name that changes
  between validation and connect cannot reach an internal address); IPv6 multicast, NAT64 and 6to4-embedded IPv4 are blocked.
- **Config directory** is PiG-first (`PIG_CODING_AGENT_DIR`, then `PI_CODING_AGENT_DIR`, XDG `pig`, `$PIG_HOME/agent`), with Pi's
  locations as read fallbacks; a new config is written under `$PIG_HOME/agent`.
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

## Slice 12: Defuddle fallback

The original's `extract.ts` hands a page to Defuddle (`extractWithDefuddle`: a linkedom document, Defuddle 0.19.3 with
`{ markdown: true, useAsync: false }`) when Readability found nothing or its Markdown is shorter than 500 characters and the
RSC payload gave nothing. No Go port tracks Defuddle 0.19.3 byte for byte (the Go ports that exist build the Markdown with
html-to-markdown and score differently; one runs the JavaScript in an embedded engine), so the pipeline is a hand port, file by file
of `defuddle/dist`, proven against the original on a recorded corpus. The refusal (`the Defuddle fallback extractor`) is gone.

| Original (defuddle 0.19.3 and its dependencies) | Go |
|---|---|
| `extract.ts extractWithDefuddle` (console capture, the error message) | `defuddle.go` |
| `linkedom` 0.16.11 document, selectors (css-select), `URL` | `ldom*.go`, `whatwgurl.go` |
| `defuddle.js`, `constants.js`, `metadata.js`, `removals/*`, `scoring`, `standardize*`, `elements/*`, `footnotes*`, `math*` | `dfd_core.go`, `defuddle_constants.go` (generated by `port/gen-defuddle-constants.mjs`), `dfd_metadata.go`, `dfd_removals.go`, `dfd_scoring.go`, `dfd_standardize.go`, `dfd_elements.go`, `dfd_footnotes_a.go`, `dfd_footnotes_b.go`, `dfd_content_patterns.go`, `dfd_math.go` |
| `markdown.js` and `turndown` as Defuddle configures it | `dfd_markdown.go`, `dfd_markdown2.go`, `dfd_td_engine.go`, `dfd_td_parse.go` |
| `extractor-registry.js`, `extractors/_base.js`, `_conversation.js`, comments, 25 extractors (X, Twitter, X oEmbed, Gmail, Reddit, YouTube, Bilibili, Hacker News, ChatGPT, Claude, Grok, Gemini, GitHub, LinkedIn, Threads, Bluesky, Medium, C2 Wiki, Substack, NYTimes, Wikipedia, Mastodon, Discourse, LeetCode, LWN) and the BBCode data extractor | `dfd_ext_*.go`, `dfd_js_uri.go`, `dfd_json.go`, `dfd_util.go`, `dfd_re.go`, `dfd_jsre.go` (a JavaScript-flavoured backtracking regular expression engine for the patterns RE2 cannot run) |
| `mathml-to-latex` 1.8.0, `@xmldom/xmldom` 0.9.12, `temml` 0.13.4 with mhchem | `dfd_mathml*.go`, `dfd_xmldom.go`, `dfd_tm_*.go`, `dfd_temml_tables.go` |

Only the synchronous path exists: `extract.ts` passes `useAsync: false`, so `findAsyncExtractor`, `canExtractAsync`, `prefersAsync`,
`extractAsync`, the extractors' `fetch` (FxTwitter and oEmbed, the c2.com JSON, Reddit's Atom feed, the YouTube caption and player
requests, the Bilibili subtitle file) and everything reachable only through them are not ported, and are not reachable in the
original either: the Go port and the original agree on what these pages give. The same holds for the options extract.ts does not
set (`debug`, `profile`, `contentSelector`), media-query evaluation (linkedom has no `styleSheets`), `getComputedStyle` (the window is
a Proxy over Node's globals) and shadow DOM flattening (linkedom sets `shadowRoot` only from `attachShadow`).

**Proof.** `port/record-defuddle.mjs` runs the original's `extractWithDefuddle` over every HTML fixture and records the title, the
Markdown and the error; `TestDefuddleFixturesEqualTheOriginal` runs the port on the same bytes and compares them (433 fixtures of
`testdata/defuddle`: the fixtures of the Defuddle project (MIT), the 102 `ext--` pages of the extractors and 104 hand-made
`port--` pages from `port/gen-defuddle-fixtures.mjs` for removals, scoring, standardization, footnotes, code blocks, callouts,
malformed markup, large pages, invalid bytes and empty pages; plus the 25 Readability fixtures of `testdata/html`, recorded as
`<name>.defuddle.json`): 458 fixtures, 14 of them errors of the original that the port reproduces (the message included). The
fixture-count guard fails the test below 450. The four `rsc-fallback` cases that reach Defuddle are twins, and three scenarios
(`fetch-defuddle-null-readability`, `fetch-defuddle-short-readability`, `fetch-defuddle-rich-page`) record the tool result under Pi.
`decodeText` (the charset handling that feeds the pipeline) is proven on 806 recorded `TextDecoder` cases (`testdata/decode-text.json`).
Mutations of the key decisions of the pipeline, the registry, the Markdown rules and the math helpers are in `mutations.json`
(`dfd-*`, `decode-text-*`, `extract-defuddle-*`).

Defuddle 0.19.3 calls three third-party pieces for math, and all three are ported to Go and compared with the
original byte for byte on recorded fixtures.

**mathml-to-latex 1.8.0** (`dfd_mathml.go`, `dfd_mathml_tables.go`, `dfd_xmldom.go`). `dfdMathMLToLatex` is
`MathMLToLaTeX.convert(mathml)` inside the try/catch of its two callers (`false` where the original throws). The
library parses with `@xmldom/xmldom` 0.9.12 (`DOMParser`, `text/xml`, an `onError` callback that repairs value-less
attributes and re-parses), so the parser is ported too: the sax loop with its error recovery, the entity rules of
XML mode, namespaces and prefixes (an undeclared prefix is a `DOMException`, hence an empty result), comments, CDATA,
processing instructions and DOCTYPE with an internal subset (a regular expression port of the grammar). The lookup
tables are generated by `port/gen-defuddle-mathml-tables.mjs`.

**temml 0.13.4** (`dfd_tm_*.go`, `dfd_temml_tables.go`, `dfd_jsre.go`). `dfdRenderLatexToMathML` is
`temml.renderToString(latex, {displayMode, throwOnError: false})`. The executed code is `dist/temml.cjs`, which is
`src/` plus three things that are not in `src/`: mhchem (`\ce`, `\pu`, `\tripleDash` and friends), the texvc macros and
the physics package macros. All of it is ported: lexer, macro expander, namespaces, parser, the function and
environment tables (every `defineFunction` / `defineEnvironment`), `buildMathML`, the MathML tree, the style object, units,
`replace.js`/`stretchy.js`, `\ce` and `\pu` (the parser state machines are generated from the original tables, the
actions and the texify stage are ported by hand, on a small JavaScript-flavoured backtracking regular expression
engine because mhchem needs lookahead). The symbol, unicode and macro tables are generated by
`port/gen-defuddle-temml-tables.mjs` from the original sources.

Parse nodes are dynamic objects (`map[string]any`), as in the original, so a handler stays next to its JavaScript. Two
original quirks are reproduced on purpose because they show in the output: `new MathNode("mtd", [], [], style)` takes the
`Style` object itself as the CSS dictionary (array.js does it for padding cells, so a short row prints
`style="level:1;color:undefined;font:;..."`), and the style of those cells is one shared object that later assignments
(`paddingLeft`, `borderTop`) modify.

An error that Temml reports (a `ParseError`) comes back as the `<span class="temml-error">` the original builds,
message and position included. A JavaScript exception of the original (a `TypeError`, a thrown mhchem array) or a Go
run-time error standing for one comes back as the empty string; the caller finds no `<math>` element in both cases.

**decodeURI / decodeURIComponent / encodeURI** (`dfd_js_uri.go`): ECMA-262 `Decode` and `Encode`, including the URIError rules (bad hex, a
truncated or overlong or surrogate UTF-8 sequence), with the reserved set of `decodeURI`; the extractors share them.

**Evidence of the math helpers.**

`port/record-defuddle-math.mjs` (Node 24, the oracle's `node_modules`: mathml-to-latex 1.8.0, temml 0.13.4,
@xmldom/xmldom 0.9.12) records `testdata/defuddle-math/{mathml,latex,uri}-expected.json` (the 6.8 MB temml file is stored as `latex-expected.json.gz`, 370 KB, read by the test) from the input lists built by
`port/build-defuddle-math-inputs.mjs`:

- 25 082 temml renders (12 541 LaTeX strings, inline and display): 1 040 hand-written samples (formulas of the kind math
  pages carry, every command family, `\ce`/`\pu`, malformed input), the LaTeX of the Defuddle fixtures (data-latex,
  annotations, math scripts, latex images), every symbol, function, environment and macro of the original tables (also
  the texvc and physics macros) with arguments, and 4 000 seeded random token strings that exercise the error paths.
- 315 MathML documents (every converter, fences and tables, attribute and entity problems, namespaces, DOCTYPE,
  malformed XML, MathJax/KaTeX/LaTeXML/Wikipedia shapes, prototype-name tags, deep nesting).
- 61 URI strings for both decoders.

`TestMathMLToLatexEqualsTheOriginal`, `TestTemmlRenderEqualsTheOriginal` and `TestDecodeURIEqualsTheOriginal` compare
every case; `dfd_jsre_test.go` covers the regular expression engine. The fixtures were recorded twice (the developer
machine and the tern-ci container) with identical bytes.

**Differences of the math helpers that remain.**

- A lone surrogate cannot exist in a Go string. `&#xD800;` in MathML, `\char"D800` and similar come out as U+FFFD
  where the original has the lone surrogate.
- mhchem (`\ce`, `\pu`) counts characters outside the BMP as one where the original counts two UTF-16 units; the
  patterns decide on the same characters, so only a formula with such a character after a position-sensitive match can
  differ. The same holds for the `\Braket`/`\Set` helpers, which do use UTF-16 offsets.
- A `ParseError` message underlines a token per UTF-16 unit in the original; a surrogate pair is underlined once here.
  The message only ever lands in the discarded error span.
- A Go run-time error stands in for a JavaScript `TypeError` in the places where the original dereferences an
  undefined value. Every such place met by the 25 082 cases is reproduced; a rare path that does not throw in Go but
  would throw in JavaScript (or the reverse) is possible in code the corpus does not reach.
- `\includegraphics`, `\class`, `\id`, `\style`, `\data` and `\href`-like commands need `trust`, which Temml's defaults
  deny; they stop at the same ParseError after the same argument checks, and the image node is never built (the
  original's `Img` class is commented out as well).

**Differences from the original that remain** (each with the check that bounds it):

- **Stack depth.** The original runs out of stack on a page nested a few thousand elements deep ("Maximum call stack size exceeded" from
  the list or the Markdown stage, or turndown's "Partial conversion completed with errors"; 3000 nested lists and 3000 nested inline pairs
  were recorded and removed because the limit is a property of the Node stack). Go has no such limit. Pages nested 600 (lists, inline) and 2000
  (`div`) deep are recorded and equal (`port--malformed-deep-*`). A page nested 6000 deep did not finish in the original within 10 minutes (the work is quadratic) and was not recorded.
- **The Markdown stage's HTML parser.** The original uses domino, which has no depth limit; `x/net/html` refuses a tree nested more than 512
  elements deep. Such markup is parsed by the linkedom-like parser and copied (`dfd_td_parse.go`); on well-formed markup the trees agree (the
  600-deep fixtures), on misnested tables or formatting elements at that depth they could differ (no fixture reaches it). The same limit
  still applies to the Readability path of this port (`htmlmd.go`, `x/net/html` through go-readability: see "HTML to Markdown").
- **Legacy multi-byte charsets.** `decodeText` follows the Encoding Standard for UTF-8 (BOM, maximal invalid subparts), UTF-16 and the single-byte
  encodings (C1 controls). For Shift_JIS, EUC-JP, EUC-KR, Big5, GBK and gb18030 it uses `golang.org/x/text`, which decodes text of that
  encoding identically but replaces other bytes (another encoding's bytes, malformed input) and the private-use areas differently
  (`decodeTextRecordedDifference` in `decodetext_test.go`; 15 of 806 recorded cases, the test fails if they stop differing).
- **Lone surrogates and UTF-16 offsets.** A Go string cannot hold a lone surrogate: where the original has one (a numeric character reference
  to U+D800 to U+DFFF, `\char"D800`) the port has U+FFFD. Lengths and offsets are UTF-16 units where the original's code takes them
  (`jsLen`, `jsSlice`); the Temml and mhchem remainders are listed above.
- **Console output.** The port writes nothing to the console. `extractWithDefuddle` of the original swallows the Defuddle-prefixed `console.error`
  lines and turns "Error processing document" into the thrown error (reproduced); other console lines of Defuddle would go to the host's stderr
  and are not reproduced. The `rsc-fallback` case that asserts `console.warn` stays silent holds trivially.
- **The failure checklist.** An error of the Defuddle run ends in the hosted-provider "Fallback options" list of `extract.ts`; on this branch the list
  names the providers this branch ports, so it is shorter than the original's until the hosted, Gemini and cookie slices are in the stack
  (the `rsc-fallback` twin and `testdata/defuddle` pin the error line itself; the scenario for that page is therefore not recorded).
- **Timing.** The original's Defuddle run is synchronous and cannot be interrupted; the port checks the request's context before and after it.

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
| source_check fetchContent uses the explicit proxy for result pages | provider tavily instead of openai (not ported here); the curl arguments are asserted | `proxy_test.go` |
| fetch_content passes the explicit proxy through queued extraction | the page body is padded past the 500-character usefulness floor: this port's HTML pipeline (like the original's, which also rejects the short fixture body) reports shorter pages as incomplete | `proxy_test.go` |
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
| Twins | `pigeq twins check`: 309 exact + 566 named skips = 875 titles; exit 0 |
| Go tests | `go test -race -count=24` with `GOMAXPROCS=4` passes; `go vet` clean on linux, windows, darwin |
| Equivalence | 9 scenarios, replayed on PiG with the Go port: identical traces (`pigeq check --go`). The first 5 were recorded from the original under **Pi 1.0.1** (registration, schemas, system-prompt snippets, dynamic activation, renaming and the tool gates). Four were recorded under **Pi 1402973a** and `pigeq run` shows the original under Pi, the original under PiG's Node runtime and the port identical: `web-search-tavily` (the request to a fake Tavily server and the rendered answer), `fetch-text-page` and `fetch-raw-404` (the model's tool call names a fake page server through `{{server:web}}`), `fetch-private-address-refused`. They use the harness's `normalize` rule N7 for the `responseId` that `generateId` (storage.ts) draws on every run: `Date.now().toString(36)` plus 1 to 6 random base-36 characters (so 9 to 14 characters, and a leading `m` to `o` until 2029). One rule covers the id in text (`(responseId[": ]+)[0-9a-z]{9,14}\b`), one the id as a whole value (`^[m-o][0-9a-z]{8,13}$`); a bare 14-character rule was tried first and also hid `searchinfinity` and `simultaneously` in the recorded system text |
| Mutation | `mutations.json`: 47 mutants, all killed (`pigeq mutate --unit`) |
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
