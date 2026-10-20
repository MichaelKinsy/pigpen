# websearch (Go port of pi-web-access, first slice)

A Go extension for PiG that gives the agent web research tools:

| Tool | What |
|---|---|
| `web_search` | One or more searches through a configured provider; sources are retained and a `responseId` is returned. |
| `fetch_content` | Fetches URLs as readable Markdown or the raw body, through the SSRF guard; large pages are cached, not inlined. |
| `get_search_content` | Reads back retained search or fetch content by `responseId` (offset/limit paging, `findText` search). |
| `source_check` | Checks a claim against searched sources and returns an evidence artifact. |
| `web_enable` | Dynamic activation: the web tools stay out of the model's tool list until it asks for them. |

Command: `/search` (browse, view and delete stored search results).

This is a port of [pi-web-access](https://github.com/nicobailon/pi-web-access) 0.33.0 by
Nico Bailon (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime.

**This is the first slice, not the whole port.** Search + fetch + source retention are
complete and twinned against the original's tests. The rest is deferred and every missing
upstream test is a *named skipped test* with a reason; see [`port/PORT.md`](port/PORT.md).

## Install

```sh
pig install ./components/websearch
```

The extension builds from source on first use (Go toolchain required) or fuses into a
Piglet Binary (it is part of `pig-with-batteries`). It is a Package:
`pig package validate ./components/websearch`.

## Providers in this slice

Exa (keyed, and the keyless Exa MCP), Brave, Tavily (numbered key pool), Perplexity,
DuckDuckGo, SERPdive, Kagi (search and extract), and the keyed REST providers Serper, SerpApi, SerpBase, Serply,
Valyu, Bocha, Baizhi (MCP over HTTP), You.com, AnySearch (works without a key), XCrawl,
SearXNG (your own instance, through the SSRF guard) and Jina Search, Gemini (API key, Cloudflare AI Gateway, or Google Application
Default Credentials on Vertex AI; the Gemini API also serves the `gemini` fetch provider, URL
context, and PDF transcription), and the hosted providers TinyFish, Search1API, Searchinfinity, Querit,
Parallel, Parallel MCP, Ollama, Firecrawl (a configured instance) and Bright Data (SERP zone), and the model-backed providers OpenAI (hosted search through an
API key or the Codex/OpenAI login of the model registry, alpha/search, `openaiUseProviderBaseUrl`, OpenCode
attribution), xAI (Grok subscription or key, web and X search), Kimi (Kimi Code login) and Mistral. Selection, `auto` order,
`"all"`, provider arrays, `searchRouting` fallback and `webSearch.allowedProviders` follow the
original; every search provider of the original is ported.

| Provider (`provider` value) | Credential (config key, environment variable) | Notes |
|---|---|---|
| `serper` | `serperApiKey`, `SERPER_API_KEY` | explicit or `searchRouting` only |
| `serpapi` | `serpapiApiKey`, `SERPAPI_KEY` | explicit or `searchRouting` only |
| `serpbase` | `serpbaseApiKey`, `SERPBASE_API_KEY` | explicit or `searchRouting` only |
| `serply` | `serplyApiKey`, `SERPLY_API_KEY` | explicit or `searchRouting` only |
| `valyu` | `valyuApiKey`, `VALYU_API_KEY` | explicit or `searchRouting` only |
| `bocha` | `bochaApiKey`, `BOCHA_API_KEY` | in `auto` and `"all"` |
| `baizhi` | `baizhiApiKey`, `BAIZHI_API_KEY` | explicit or `searchRouting` only; returns the MCP payload as the answer |
| `you` | `youApiKey`, `YDC_API_KEY` | explicit or `searchRouting` only |
| `anysearch` | `anysearchApiKey`, `ANYSEARCH_API_KEY` (optional) | explicit or `searchRouting` only; available without a key |
| `xcrawl` | `xcrawlApiKey`, `XCRAWL_API_KEY` | explicit or `searchRouting` only |
| `searxng` | `searxngBaseUrl`, `SEARXNG_BASE_URL`; optional `searxngHeaders` | first in `auto` when configured; the `ssrf` settings apply |
| `jina` | `jinaApiKey`, `JINA_API_KEY` | search through `s.jina.ai`; in `auto` and `"all"`; with `includeContent` the pages come back inline |

## What is refused until it is ported

Each of these returns a tool error with one message, which names the working alternative, and
never falls back or runs part of the call:

> @pi-in-go/pigpen-websearch (Go port) does not support `<feature>` yet. Install the original
> extension, npm:pi-web-access@0.33.0, which runs unchanged in PiG.

- `web_search` workflow `summary-review` and `auto-summary` (the curator and generated summaries);
- `fetch_content` `mode: "answer"` and `auth` (authenticated fetch);
- `fetch_content` URLs the original hands to an extractor: YouTube, local video files, video
  `frames` and `timestamp`, GitHub repositories, issues and pull requests (while their config
  sections are not disabled), and PDFs;
- Gemini Web (browser cookies) when browser cookie access is switched on (`PI_ALLOW_BROWSER_COOKIES`, `allowBrowserCookies`): the Gemini API is ported, the browser path is not;
- a page the original would hand to its RSC payload and Defuddle fallbacks after the readable
  extraction fails.

Not registered, and so not advertised: the original's commands `/websearch`, `/curator` and
`/google-account`, its activity widget and its shortcuts (they belong to the curator and the
activity monitor, which are not ported).

## Configuration

`web-search.json` in the agent directory (`PIG_CODING_AGENT_DIR`, then
`PI_CODING_AGENT_DIR`, then `$PIG_HOME/agent`; Pi's own locations are read as fallbacks).
API keys come from the file, from the named environment variable, or lazily from a command
(`!command` sources), exactly as in the original. Example:

```json
{
  "provider": "tavily",
  "tavilyApiKey": "tvly-...",
  "toolActivation": "dynamic",
  "toolNames": { "webSearch": "web_search" },
  "tools": { "sourceCheck": { "enabled": false } },
  "commands": { "search": { "enabled": true } },
  "fetch": { "allowedModes": ["readable", "raw"], "defaultMode": "readable" },
  "fetchContent": { "domainPolicy": { "deny": ["internal.example"] } },
  "ssrf": { "allowRanges": [] },
  "maxInlineContentChars": 30000
}
```

Registration gates: each tool has `"tools": { "<tool>": { "enabled": false } }` and each
command has `"commands": { "<command>": { "enabled": false } }`; a disabled tool or command is
neither registered nor advertised.

Pi restart is required for tool and command registration changes (the host reads registrations once at startup).

`toolNames` can opt into alternate public tool names; the names are validated (pattern,
uniqueness, and `web_enable` is reserved for the loader).

## Security

- Every remote fetch goes through an SSRF guard: private, loopback, link-local, multicast,
  NAT64 and 6to4 addresses are refused (also after redirects, and at dial time for direct
  connections), and redirects are limited and re-validated. Page bodies are capped at 5 MB and
  images at 64 megapixels before decoding. `ssrf.allowRanges` exempts CIDR ranges you choose (TUN / fake-IP proxies).
- No browser cookies or credentials are read. Authenticated fetch, Gemini Web and Chrome cookie
  extraction are not part of this port.
- No paid provider is called without configuration. With nothing configured, `web_search`
  (provider `auto`) sends the query to the keyless Exa MCP (`mcp.exa.ai`), as the original does;
  that is also what `exa` uses with no key. Exclude it with `webSearch.allowedProviders` or
  configure another provider. The Jina Reader fetch fallback is off for remote URLs unless
  `fetchRouting.allowRemoteHostedProviders` is true.
- Credentials are redacted from every error.

## Proof that it behaves like the original

`port/` ships with the Package: the unmodified original and its tests (`port/oracle/`), the
ledger of upstream test titles (`port/upstream-tests.json`), and [`port/PORT.md`](port/PORT.md)
with the mapping, the deliberate differences and the deferred features. The Go tests in
`extensions/websearch` are twins of the upstream tests (same titles); check the ledger with
`pigeq twins check`.

MIT. Original © Nico Bailon; port © Michael Kinsy.
