# Upstream Reference: `internal/web`

Ported from **omp** (`github.com/can1357/oh-my-pi`, MIT, release `v19.1.1`, commit `31ffee513f`).

## Upstream Files Followed

- `packages/coding-agent/src/prompts/tools/web-search.md` — tool description (default branch: `xSearch` omitted / false because xAI grounded search is not ported).
- `packages/coding-agent/src/prompts/system/web-search.md` — system prompt sent to LLM-mediated search APIs (`perplexity`).
- `packages/coding-agent/src/web/search/index.ts` — `web_search` parameter schema, LLM result formatting (`formatForLLM`), central directive post-filter pipeline (strict scope constraints + relaxation of non-scope directives), `SearchResultDetails.Failures` + `fallbackNotes`, 5-minute HTTP 429 cooldown with `skipped: rate-limited (429) within the last 5 minutes` failure notes, exact `site:news.ycombinator.com` routing to Hacker News, and `providers.webKeyless` gating.
- `packages/coding-agent/src/web/search/query.ts` — structured query parser (`ParseSearchQuery`), syntax formatter (`FormatQuery`, `FormatScraperQuery`), `MatchesSite`, `ApplyQueryConstraints`, and `ScopeConstraints`.
- `packages/coding-agent/src/web/search/provider.ts` & `packages/catalog/src/compat/rules/runtime/behavior.kdl` — provider registry and default `web` role chain.
- `packages/coding-agent/src/mcp/json-rpc.ts` — stateless JSON-RPC 2.0 over HTTPS (JSON + SSE) used by Parallel public MCP.
- `packages/coding-agent/src/web/search/types.ts` & `packages/tui/src/tools/web-search-types.ts` — unified `SearchResponse` / `SearchSource` / `SearchCitation` / `SearchUsage` / `SearchFailureDetail` / `SearchResultDetails` types and provider labels.
- `packages/coding-agent/src/web/search/utils.ts` & `packages/coding-agent/src/web/search/providers/utils.ts` — age (`formatAge` including years)/count/recency helpers, `keywordQuery` 400 validation, HTTP error classification, capped body reading, charset decoding.
- `packages/coding-agent/src/web/response-body.ts` — `readBodyCapped` and `decodeBody`.
- Ported search providers:
  - Default `web` role chain entries (matching `behavior.kdl:521-533` order for ported engines):
    1. `parallel` (`PARALLEL_API_KEY` REST API, or keyless public MCP `https://search.parallel.ai/mcp` when no key is set) — `web/search/providers/parallel.ts`, `web/parallel.ts`
    2. `exa` (`EXA_API_KEY`) — `web/search/providers/exa.ts`
    3. `firecrawl` (`FIRECRAWL_API_KEY`, optional `FIRECRAWL_BASE_URL`/`FIRECRAWL_API_URL`, or keyless hosted mode when unconfigured) — `web/search/providers/firecrawl.ts`, `web/firecrawl.ts`
    4. `searxng` (`SEARXNG_ENDPOINT`, optional `SEARXNG_BASIC_USERNAME`/`SEARXNG_BASIC_PASSWORD` or `SEARXNG_TOKEN`) — `web/search/providers/searxng.ts`
    5. `duckduckgo` (keyless HTML search frontend `https://html.duckduckgo.com/html/`) — `web/search/providers/duckduckgo.ts`
  - Port extension (because PiG has no `modelRoles.web` selector yet; these credentialed providers are appended after the default chain and only run when their env key is set — note this differs from omp's strict `role-defaults "web"` chain where they require explicit role configuration):
    6. `perplexity` (`PERPLEXITY_API_KEY`, optional `PI_PERPLEXITY_API_MODEL`) — `web/search/providers/perplexity.ts`
    7. `tinyfish` (`TINYFISH_API_KEY`) — `web/search/providers/tinyfish.ts`
    8. `keenable` (`KEENABLE_API_KEY`; `Search` also supports the keyless public endpoint if invoked directly, though `IsAvailable` requires `KEENABLE_API_KEY` in the automatic chain, matching `keenable.ts:186-188`) — `web/search/providers/keenable.ts`
    9. `sofya` (`SOFYA_API_KEY`) — `web/search/providers/sofya.ts`
    10. `jina` (`JINA_API_KEY`) — `web/search/providers/jina.ts`
    11. `kagi` (`KAGI_API_KEY`) — `web/search/providers/kagi.ts`, `web/kagi.ts`
    12. `tavily` (`TAVILY_API_KEY`) — `web/search/providers/tavily.ts`
    13. `brave` (`BRAVE_API_KEY`) — `web/search/providers/brave.ts`
    14. `deepseek` (`DEEPSEEK_API_KEY`) — `web/search/providers/deepseek.ts`
    15. `kimi` (`MOONSHOT_SEARCH_API_KEY` / `KIMI_SEARCH_API_KEY`, optional `*_SEARCH_BASE_URL`) — `web/search/providers/kimi.ts`
    16. `synthetic` (`SYNTHETIC_API_KEY`) — `web/search/providers/synthetic.ts`
    17. `ollama` (`OLLAMA_CLOUD_API_KEY`) — `web/search/providers/ollama.ts`
  - Site-routed provider:
    18. `hackernews` (keyless Algolia HN Search API; auto-routed first for queries where every `site:` host is exactly `news.ycombinator.com`) — `web/search/providers/hackernews.ts`
- `packages/coding-agent/src/tools/fetch.ts` & `packages/coding-agent/src/web/scrapers/types.ts` — URL fetcher (`Fetch`), 50 MiB read cap, per-request 30 s timeout, status-based user-agent rotation on HTTP 403/429/503/999 (rotating before waiting out `Retry-After` on 429), bot-wall detection (`Cloudflare`, `JavaScript challenge`, `DataDome`, `PerimeterX`, `network security block`) with `Method: blocked` / `Method: failed`, verbatim raw mode (`raw=true` returns the response body with no header or trimming), content-type dispatch (HTML, JSON, RSS/Atom feed, plain text, binary notice), `<link rel="alternate">` markdown (using alternate's `finalUrl`), feed alternate as fallback only when rendering fails or is low quality, 100-char minimum + JavaScript-gate check, front-matter pruning (`pruneFrontMatter` with `meta_omitted`), and data-URI image stripping.
- Ported site-specific URL scrapers (`scrapers.go`):
  - GitHub (`github.ts`): blob (`github-raw`, raw URL, `text/plain`, `Fetched raw: <url>`), repo (`github-repo`, metadata + `## Files` tree + API `## README`), issue/PR (`github-issue`/`github-pr`, metadata + body + paginated `## Comments`).
  - npm (`npm.ts`): registry metadata + parallel 5s-capped weekly downloads + JSON-ordered dependencies + README.
  - PyPI (`pypi.ts`): JSON API + parallel 5s-capped pypistats weekly downloads + JSON-ordered project URLs + dependencies + description.
  - crates.io (`crates-io.ts`): crate metadata + recent versions + 5s-capped docs.rs README.
  - Stack Overflow / Stack Exchange (`stackoverflow.ts`): question + top 5 answers via Stack Exchange API v2.3.
  - Wikipedia (`wikipedia.ts`): REST v1 summary + `mobile-html` article sections (skipping References/External links/See also/Notes/Further reading).
  - arXiv (`arxiv.ts`): Atom API metadata + abstract.
  - Hacker News (`hackernews.ts`): story/item with relative timestamps and concurrent depth-2 nested replies (`Fetched HN item N with top-level comments (depth 2)`), plus front-page `/`, `/news`, `/newest`, `/best` top-20 listings.
- `crates/natives/src/html.rs` — reference for HTML-to-Markdown conversion behaviour (note: omp `v19.1.1` uses `html-to-markdown-rs` 3.18.3 with `hiddenContent: "reachable"`, while the stdlib Go converter in `html2md.go` implements the core HTML-to-Markdown rules and front-matter extraction).

## Exported Fetcher API

```go
func Fetch(ctx context.Context, rawURL string, raw bool) (string, error)
```

- Normalizes URLs (repairs collapsed `https:/` schemes; adds `https://` when scheme omitted; rejects non-`http`/`https` schemes except `pi-internal://`).
- Routes all requests through the single package-level `httpClient`.
- When `raw` is `true`, returns the fetched response body verbatim with no metadata header and no trimming (matching `tools/fetch.ts:1473-1484, 1978`).
- When `raw` is `false`, dispatches site-specific scrapers first, then loads the URL with status-based UA rotation (403/429/503/999) and bot-wall detection, returning `Method: blocked` / `Method: failed` / `Method: no-content` / `Method: binary` / `Method: json` / `Method: feed` / `Method: text` / `Method: alternate-markdown` / `Method: alternate-feed` / `Method: native` (with `pruneFrontMatter`) / `Method: raw-html`.

## Tests Ported

- 49 top-level Go tests (including 65+ subtests) with zero network access (`httptest` / refusing dialer):
  - `query_test.go`: named upstream cases from `test/web/search/query.test.ts` (parsing, syntax formatting, site matching, strict scope vs lenient relaxation).
  - `providers_test.go`: named upstream cases from `test/web/search/query-pipeline.test.ts`, `test/tools/web-search-parallel.test.ts`, `test/tools/web-search-exa.test.ts`, `test/tools/web-search-firecrawl.test.ts`, `test/tools/web-search-searxng.test.ts`, `test/web/search/perplexity.test.ts`, `test/tools/web-search-tinyfish.test.ts`, `test/tools/web-search-kagi.test.ts`, `test/tools/web-search-tavily.test.ts`, `test/tools/web-search-kimi.test.ts`, `test/web/search/ollama.test.ts`, `test/web/search/read-limited-text.test.ts`.
  - `v19_test.go`: named upstream cases from `test/web/search/query-pipeline.test.ts` (strict scope miss, 429 cooldown skip note, HN-only vs multi-site routing), `test/tools/web-search-keenable.test.ts` (8 cases), `test/tools/web-search-sofya.test.ts` (7 cases), `test/web/search/hackernews.test.ts` (4 cases + plain-query `optionalWords` check), `test/tools/fetch-user-agent-retry.test.ts` (bare 403 rotation, 429 rotation before sleep), `test/tools/fetch-http-status.test.ts` (200 JS challenge `Method: blocked` + `:raw`), `test/tools/fetch-alternates.test.ts` (post rendered natively vs empty app falling back to feed alternate), plus tests for Parallel public MCP (JSON & SSE), DuckDuckGo HTML, `formatAge` years, `pruneFrontMatter`, and exact format parity for GitHub, npm, PyPI, Wikipedia, and Hacker News scrapers.
  - `fetch_test.go`: named upstream cases from `test/tools/fetch-data-uri-images.test.ts`, `test/tools/fetch-raw-mode.test.ts`, `test/tools/fetch-http-status.test.ts`, `test/tools/fetch-feed.test.ts`, `test/tools/fetch-alternates.test.ts`.
  - `offline_settings_test.go`: refusing-dialer offline tests (`PI_OFFLINE`, `PIG_OFFLINE`, `models.offline` setting) and `web.Setting` hook tests (`providers.webKeyless`, `providers.webSearchTimeoutSeconds`, `exa.enabled`, `searxng.*`).

## Settings Read

| omp Setting | Default | Honoured | Notes |
|---|---|---|---|
| `models.offline` | `false` | Yes | Read via `web.Setting` hook (wired to `settings.Any`) and PiG's `PI_OFFLINE` / `PIG_OFFLINE` process env (`coding/cli/main.go:1875`). Under offline all external search providers are skipped with zero network dials, returning omp's `"No web search backend is available on the active model's provider under --offline."` (`web/search/index.ts:330-341,510`). |
| `providers.webKeyless` | `true` | Yes | Read via `web.Setting` hook (default `true`; no env var, matching omp `session/settings.ts:1522-1533`). |
| `providers.webSearchTimeoutSeconds` | `60` (max `300`) | Yes | Read via `web.Setting` hook and clamped to `MaxWebSearchTimeoutSeconds` (`web/search/index.ts:307-315`). |
| `exa.enabled` | `true` | Yes | Read via `web.Setting` hook (`web/search/providers/exa.ts:458-467`). |
| `searxng.endpoint` / `searxng.token` / `searxng.basicUsername` / `searxng.basicPassword` | unset | Yes | Read via `web.Setting` hook with omp's env fallbacks (`SEARXNG_ENDPOINT`, `SEARXNG_TOKEN`, `SEARXNG_BASIC_USERNAME`, `SEARXNG_BASIC_PASSWORD`). |
| `searxng.categories` / `searxng.engines` / `searxng.language` / `searxng.safesearch` | unset | Yes | Read via `web.Setting` hook (note: `/config` engine-shortcut resolution is not ported; `searxng.engines` is passed verbatim). |
| `providers.fetch` | `auto` | No | Only the `native` in-process backend + site scrapers are implemented; `trafilatura`/`lynx`/`parallel`/`firecrawl`/`jina` readers are not ported. |
| `retry.fallbackChains.web` | `[]` | No | Uses `defaultProviders`; custom fallback chain configuration is not ported. |

## Not Ported

| Feature / Provider | Reason |
|---|---|
| `web/hosted` (session model grounding swap) | Requires omp's `ModelRegistry` and active session chat model grounding metadata, which PiG's extension SDK does not expose. |
| Grounded LLM search providers (`gemini`, `anthropic`, `codex`, `xai`, `openrouter`, `openai`) | Grounded chat-model transports using provider SDKs / OAuth / broker credentials rather than HTTP search APIs. |
| X search prompt branch (`{{#if xSearch}}`) | Depends on `xai` grounded provider availability; default branch (without X operators) is rendered. |
| `zai` search provider (`ZAI_API_KEY`) | Uses stateful multi-step MCP JSON-RPC session handshake (`initialize` + `notifications/initialized` + `tools/call`) rather than stateless search calls. |
| Keyless public MCP path for `exa` | Exa's REST API (`EXA_API_KEY`) is ported; keyless Exa MCP is not yet wired alongside Parallel public MCP and DuckDuckGo. |
| Perplexity OAuth, cookie (`PERPLEXITY_COOKIES`), anonymous ask, and Responses API streaming | Require browser-style SSE ask protocol, OAuth token broker, or OpenAI Responses streaming client; direct `PERPLEXITY_API_KEY` `/chat/completions` JSON endpoint is ported. |
| Additional HTML-scraping search engines (`startpage`, `ecosia`, `google`, `mojeek`, `public`) | Require browser/CAPTCHA fallback (`puppeteer-core` / `browserFetch` + ALTCHA/challenge solvers); DuckDuckGo no-JS HTML and Parallel public MCP cover keyless search. |
| DuckDuckGo `lang:` -> `kl` locale mapping (`localeToKl`) | `duckduckgo` currently defaults `kl` to `us-en`; `lang:` locale mapping is omitted. |
| `AuthStorage` / SQLite credential broker / OAuth refresh / credential rotation | omp-specific credential store; ported providers read API keys from environment variables only. |
| Model-role configuration (`modelRoles.web`, per-request `model` selector, SearXNG `/config` shortcut map, `retry.fallbackChains.web`) | Depends on omp's `ModelRegistry` / role-chain model selector, which PiG's extension SDK does not expose. |
| `Exa` global request-pacing delay (`exa.searchDelayMs`) | Depends on omp settings; omitted so searches do not block on timers. |
| GitHub scraper sub-views: `tree`, `commit`, `issues`/`pulls` lists, `actions-run`, `actions-job`, `discussions` | `blob`, `repo`, `issue`, and `pull` (with comments) are ported; remaining GitHub sub-views fall back to generic `Fetch` + `HTMLToMarkdown`. |
| arXiv PDF full-text conversion (`markit`) | Requires external `markit` document converter; arXiv scraper returns Atom metadata + abstract. |
| Remaining site-specific URL scrapers (`web/scrapers/*`: artifacthub, aur, biorxiv, bluesky, brew, cheatsh, chocolatey, choosealicense, cisa-kev, clojars, coingecko, crossref, devto, discogs, discourse, dockerhub, docs-rs, fdroid, firefox-addons, flathub, github-gist, gitlab, go-pkg, hackage, hex, huggingface, iacr, jetbrains-marketplace, lemmy, lobsters, mastodon, maven, mdn, metacpan, musicbrainz, nuget, nvd, ollama, open-vsx, opencorporates, openlibrary, orcid, osv, packagist, pub-dev, pubmed, rawg, readthedocs, reddit, repology, rfc, rubygems, searchcode, sec-edgar, semantic-scholar, snapcraft, sourcegraph, spdx, spotify, terraform, tldr, twitter, vimeo, vscode-marketplace, w3c, wikidata, youtube) | Long-tail site scrapers; URLs fall back to generic `Fetch` + `HTMLToMarkdown`. |
| External/remote HTML reader backends in `Fetch` (`trafilatura`, `lynx`, Parallel Extract, Firecrawl Scrape, Jina Reader `r.jina.ai`) | Subprocess tool discovery (`trafilatura`/`lynx`) and secondary paid reader APIs; `Fetch` uses the stdlib `HTMLToMarkdown` converter + 8 site-specific scrapers. |
| Content-negotiation re-fetch (`Accept: text/markdown`) and `llms.txt` fallback probes in `Fetch` | Extra speculative HTTP round-trips omitted to keep `Fetch` predictable (markdown/feed `<link rel="alternate">` discovery is ported). |
| Binary document converters in `Fetch` (PDF/DOCX via `markit`, inline image resizing/attachment, `.ipynb` notebook, SQLite table listing, ZIP/TAR archive listing) | Require external tools (`markit`), image codecs, or SQLite/archive subsystems; non-text payloads return omp's `[Binary content: ...]` notice. |
| Non-UTF-8/non-Windows-1252 legacy charsets (Shift_JIS, GBK, Big5, EUC-KR) in `decodeBody` | Stdlib-only constraint prohibits `golang.org/x/text/encoding`; UTF-8 (with BOM stripping) and Windows-1252/ISO-8859-1 are decoded natively, other charsets fall back to UTF-8 replacement. |
| `html-to-markdown-rs` 3.18.3 `hiddenContent: "reachable"` exact parity | `html2md.go` is a pure-stdlib Go converter rather than Rust `html-to-markdown-rs` 3.18.3; matches core markdown and front-matter structure without cgo/Rust FFI. |
| URL line selectors (`:50-100`, `:raw`), head truncation/artifact persistence (`executeReadUrl`, `materializeReadUrlToFile`) | Owned by the `read` tool in the files lane; `Fetch` exposes the core URL-to-text primitive. |
| Custom TUI tool call/result renderers (`packages/tui/src/tools/web-search.ts`) | Frontend/renderer concern outside `internal/web`. |
