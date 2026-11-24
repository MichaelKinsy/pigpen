# Upstream Reference: `internal/web`

Ported from **omp** (`github.com/can1357/oh-my-pi`, MIT, commit `4df653426f40`).

## Upstream Files Followed

- `packages/coding-agent/src/prompts/tools/web-search.md` — tool description (default branch: `xSearch` omitted / false because xAI grounded search is not ported).
- `packages/coding-agent/src/prompts/system/web-search.md` — system prompt sent to LLM-mediated search APIs (`perplexity`).
- `packages/coding-agent/src/web/search/index.ts` — `web_search` parameter schema, LLM result formatting (`formatForLLM`), central directive post-filter pipeline, fallback error formatting, and "No web search model configured." message.
- `packages/coding-agent/src/web/search/query.ts` — structured query parser (`ParseSearchQuery`), syntax formatter (`FormatQuery`, `FormatScraperQuery`), `MatchesSite`, and lenient constraint filter (`ApplyQueryConstraints`).
- `packages/coding-agent/src/web/search/provider.ts` & `packages/catalog/src/compat/rules/runtime/behavior.kdl` — provider selection order and failure formatting.
- `packages/coding-agent/src/web/search/types.ts` & `packages/tui/src/tools/web-search-types.ts` — unified `SearchResponse` / `SearchSource` / `SearchCitation` / `SearchUsage` types and provider labels.
- `packages/coding-agent/src/web/search/utils.ts` & `packages/coding-agent/src/web/search/providers/utils.ts` — age/count/recency helpers, HTTP error classification, capped body reading, charset decoding.
- `packages/coding-agent/src/web/response-body.ts` — `readBodyCapped` and `decodeBody`.
- Ported HTTP + env-key providers (in selection order):
  1. `parallel` (`PARALLEL_API_KEY`) — `web/search/providers/parallel.ts`, `web/parallel.ts`
  2. `exa` (`EXA_API_KEY`) — `web/search/providers/exa.ts`
  3. `firecrawl` (`FIRECRAWL_API_KEY`, optional `FIRECRAWL_BASE_URL` / `FIRECRAWL_API_URL`) — `web/search/providers/firecrawl.ts`, `web/firecrawl.ts`
  4. `searxng` (`SEARXNG_ENDPOINT`, optional `SEARXNG_BASIC_USERNAME`/`SEARXNG_BASIC_PASSWORD` or `SEARXNG_TOKEN`) — `web/search/providers/searxng.ts`
  5. `perplexity` (`PERPLEXITY_API_KEY`, optional `PI_PERPLEXITY_API_MODEL`) — `web/search/providers/perplexity.ts`
  6. `tinyfish` (`TINYFISH_API_KEY`) — `web/search/providers/tinyfish.ts`
  7. `jina` (`JINA_API_KEY`) — `web/search/providers/jina.ts`
  8. `kagi` (`KAGI_API_KEY`) — `web/search/providers/kagi.ts`, `web/kagi.ts`
  9. `tavily` (`TAVILY_API_KEY`) — `web/search/providers/tavily.ts`
  10. `brave` (`BRAVE_API_KEY`) — `web/search/providers/brave.ts`
  11. `deepseek` (`DEEPSEEK_API_KEY`) — `web/search/providers/deepseek.ts`
  12. `kimi` (`MOONSHOT_SEARCH_API_KEY` / `KIMI_SEARCH_API_KEY`, optional `*_SEARCH_BASE_URL`) — `web/search/providers/kimi.ts`
  13. `synthetic` (`SYNTHETIC_API_KEY`) — `web/search/providers/synthetic.ts`
  14. `ollama` (`OLLAMA_CLOUD_API_KEY`) — `web/search/providers/ollama.ts`
- `packages/coding-agent/src/tools/fetch.ts` & `packages/coding-agent/src/web/scrapers/types.ts` — URL fetcher (`Fetch`), 50 MiB read cap, redirects, user-agent rotation on bot-blocks, 429 retry, content-type dispatch (HTML, JSON, RSS/Atom feed, plain text, binary notice), page-specific `<link rel="alternate">` discovery, and data-URI image stripping.
- `crates/pi-natives/src/html.rs` — reference for HTML-to-Markdown conversion behaviour.

## Exported Fetcher API (for file tools in a later wave)

```go
func Fetch(ctx context.Context, rawURL string, raw bool) (text string, err error)
```

- Normalizes URLs (repairs collapsed `https:/` schemes; adds `https://` when scheme omitted; rejects non-`http`/`https` schemes except `pi-internal://`).
- Routes all requests through the single package-level `httpClient`.
- Follows HTTP redirects automatically via `http.Client`, reporting the final URL in the `URL:` header.
- Caps body reads at `FetchMaxBytes` (50 MiB), appending omp's mid-stream truncation note if exceeded.
- Rotates omp's 3 user-agents on Cloudflare/captcha 403/503 responses and retries HTTP 429 once honoring `Retry-After` (capped at 10s).
- Returns `[Binary content: <mime>, <size>] <finalURL>` for non-text MIME types and binary-sniffed payloads.
- When `raw` is `true`, returns the raw response text verbatim (with metadata header and `Method: raw`).
- When `raw` is `false`, pretty-prints JSON, converts RSS/Atom feeds to Markdown, passes plain text/Markdown through, follows page-specific `<link rel="alternate">` Markdown/feed links, and converts HTML to Markdown using the stdlib converter (`HTMLToMarkdown`).
- `HTMLToMarkdown` converts headings (`h1`-`h6`), paragraphs, blockquotes, links and images (preserving `href`/`src` verbatim as written, matching `html-to-markdown-rs` 3.12.4; stripping `data:` URIs while preserving alt text), bold/italic/strikethrough/inline code, nested `ul`/`ol` lists, fenced `pre`/`code` blocks with language class extraction, definition lists (`dl`/`dt`/`dd`), horizontal rules, and GFM pipe tables. It extracts `<head>` YAML frontmatter matching `html-to-markdown-rs` 3.12.4 (`base`, `canonical`, `meta-<name>`, `meta-<property>`, `title`, sorted alphabetically, omitted when `<head>` yields no keys). It drops `<script>`, `<style>`, `<noscript>`, `<template>`, `<nav>`, `<form>`, `<iframe>`, `<svg>`, `<canvas>`, `<video>`, `<audio>`, `<object>`, `<embed>`, `<button>`, `<input>`, `<select>`, `<textarea>`, non-metadata `<head>` elements, HTML comments, processing instructions, `javascript:`/`#` link targets, and inline `data:` image payloads.

## Tests Ported

- 34 top-level Go tests (including 34 subtests) covering query parsing/formatting/constraints (`query_test.go`), all 14 ported providers, provider selection/fallback/no-provider/HTTP errors/charset decoding (`providers_test.go`), and URL fetching for HTML (including exact-text parity against omp's `read` output, YAML frontmatter, and verbatim relative hrefs), plain text, JSON, raw mode, redirects, oversized bodies, non-text/binary content types, HTTP 204/429/503, RSS/Atom feeds, alternates, and data-URI stripping (`fetch_test.go`).
- Upstream test cases explicitly ported: 34 named upstream test cases from `query.test.ts`, `query-pipeline.test.ts`, `web-search-parallel.test.ts`, `web-search-exa.test.ts`, `web-search-firecrawl.test.ts`, `web-search-searxng.test.ts`, `perplexity.test.ts`, `web-search-tinyfish.test.ts`, `web-search-kagi.test.ts`, `web-search-tavily.test.ts`, `web-search-kimi.test.ts`, `ollama.test.ts`, `read-limited-text.test.ts`, `fetch-data-uri-images.test.ts`, `fetch-raw-mode.test.ts`, `fetch-http-status.test.ts`, `fetch-feed.test.ts`, and `fetch-alternates.test.ts`.

## Not Ported

| Feature / Provider | Reason |
|---|---|
| `web/hosted` (session model grounding swap) | Requires omp's `ModelRegistry` and active session chat model grounding metadata, not available as an HTTP+env-key provider. |
| Grounded LLM search providers (`gemini`, `anthropic`, `codex`, `xai`, `openrouter`, `openai`) | Grounded chat-model transports using provider SDKs / OAuth / broker credentials rather than simple search HTTP+key calls. |
| X search prompt branch (`{{#if xSearch}}`) | Depends on `xai` grounded provider availability; default branch (without X operators) is rendered. |
| `zai` search provider (`ZAI_API_KEY`) | Uses stateful multi-step MCP JSON-RPC session handshake (`initialize` + `notifications/initialized` + `tools/call` + SSE framing) rather than a single HTTP search call. |
| Keyless public MCP paths for `parallel` and `exa` | Use MCP JSON-RPC / SSE transport; REST API-key paths (`PARALLEL_API_KEY`, `EXA_API_KEY`) are ported. |
| Perplexity OAuth, cookie (`PERPLEXITY_COOKIES`), anonymous ask, and Responses API streaming | Require browser-style SSE ask protocol, OAuth token broker, or OpenAI Responses streaming client; direct `PERPLEXITY_API_KEY` `/chat/completions` JSON endpoint is ported. |
| HTML-scraping search engines (`duckduckgo`, `google`, `ecosia`, `startpage`, `mojeek`, `public`) | Scrape browser HTML pages (anti-bot headers, CAPTCHA walls, multi-engine fan-out) rather than HTTP+key APIs. |
| `AuthStorage` / SQLite credential broker / OAuth refresh / credential rotation | omp-specific credential store; ported providers read API keys from environment variables only. |
| Model-role configuration (`modelRoles.web`, `settings.yml`, per-request `model` selector, `searxng.*` YAML settings & `/config` shortcut map) | Depends on omp settings/config subsystem; providers use env vars and omp's default selection order. |
| `Exa` global request-pacing delay (`exa.searchDelayMs`) | Depends on omp settings; omitted so searches do not block on timers. |
| Site-specific URL scrapers (`web/scrapers/*`: GitHub, YouTube, Wikipedia, Reddit, ArXiv, etc. ~50 handlers) | Large site-specific scraper suite outside wave-1 `Fetch` scope. |
| External/remote HTML reader backends in `Fetch` (`trafilatura`, `lynx`, Parallel Extract, Firecrawl Scrape, Jina Reader `r.jina.ai`) | Subprocess tool discovery (`trafilatura`/`lynx`) and secondary paid reader APIs; `Fetch` uses the stdlib `HTMLToMarkdown` converter. |
| URL `.md` suffix probing, content-negotiation re-fetch, and `llms.txt` fallback probes | Extra speculative HTTP round-trips omitted from wave-1 `Fetch` to keep `Fetch` predictable (single request unless `<link rel="alternate">` matches). |
| Binary document converters in `Fetch` (PDF/DOCX via `markit`, inline image resizing/attachment, `.ipynb` notebook, SQLite table listing, ZIP/TAR archive listing) | Require external tools (`markit`), image codecs, or SQLite/archive subsystems; non-text payloads return omp's `[Binary content: ...]` notice. |
| Non-UTF-8/non-Windows-1252 legacy charsets (Shift_JIS, GBK, Big5, EUC-KR) in `decodeBody` | Stdlib-only constraint prohibits `golang.org/x/text/encoding`; UTF-8 (with BOM stripping) and Windows-1252/ISO-8859-1 are decoded natively, other charsets fall back to UTF-8 replacement. |
| URL line selectors (`:50-100`, `:raw`), head truncation/artifact persistence (`executeReadUrl`, `materializeReadUrlToFile`) | Owned by the `read` tool in the files lane; `Fetch` exposes the core URL-to-text primitive. |
| Custom TUI tool call/result renderers (`packages/tui/src/tools/web-search.ts`) | Frontend/renderer concern outside `internal/web`. |
