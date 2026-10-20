# websearch (Go port of pi-web-access, first slice)

A Go extension for PiG that gives the agent web research tools:

| Tool | What |
|---|---|
| `web_search` | One or more searches through a configured provider; sources are retained and a `responseId` is returned. |
| `fetch_content` | Fetches URLs as readable Markdown or the raw body, through the SSRF guard; large pages are cached, not inlined. |
| `get_search_content` | Reads back retained search or fetch content by `responseId` (offset/limit paging, `findText` search). |
| `source_check` | Checks a claim against searched sources and returns an evidence artifact. |
| `web_enable` | Dynamic activation: the web tools stay out of the model's tool list until it asks for them. |

Commands: `/search` (browse, view and delete stored search results), `/websearch` (the search
curator), `/curator` (toggle the curator workflow) and `/google-account` (the Gemini Web account).
Shortcuts: `ctrl+shift+s` (open the curator of a search that is still running) and `ctrl+shift+w`
(the web activity widget); both are configurable under `shortcuts` in `web-search.json`.

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

## PDFs

`fetch_content` converts a PDF (an `application/pdf` response or a `.pdf` path) to Markdown, saves it
as `<tmp>/pi-web-pdf/<title>.md` and returns the path with the page and character counts; with
`mode: "answer"` the Markdown itself is what the answer is drawn from. `pdf.provider` (`auto`,
`datalab`, `gemini`, `unpdf`) picks the converter. `auto` tries Datalab (`datalabApiKey` or
`DATALAB_API_KEY`; `DATALAB_API_BASE`, `DATALAB_MODE` and `DATALAB_PROCESSING_LOCATION` apply), then the
Gemini API, then the local extractor, which is pure Go (`github.com/ledongthuc/pdf`, no Node) where the
original uses unpdf. `pdf.enabled`, `pdf.maxSizeMB` (20, at most 50), `pdf.maxPages` (100),
`pdf.datalabMode` and `pdf.datalabTimeoutMs` (120000, at most 300000) follow the original. Where the
local extractor's text differs from unpdf's, `port/PORT.md` ("Slice: PDF text extraction") lists the
fixture that shows it.

## GitHub URLs

`fetch_content` treats `github.com` URLs as the original does. A repository, `tree` or `blob` URL
is cloned (`gh repo clone`, else `git clone`, shallow and single-branch, into a private
directory under `githubClone.clonePath`, default `/tmp/pi-github-repos`) and answered with the
tree, the README, a directory listing or the file; a repository over `githubClone.maxRepoSizeMB`
(350), a commit SHA and a clone that fails or exceeds `githubClone.cloneTimeoutSeconds` (30) fall
back to the `gh api` view, and `forceClone: true` clones a large repository anyway. An issue or
pull request URL is rendered from `gh issue view` / `gh pr view` and the review comments, or from
the REST API of `api.github.com` when `gh` is missing or fails. `githubClone.enabled` and
`githubPrIssue.enabled` switch the two extractors off. `gh` and `git` run as child processes of
this extension, with prompts disabled; see [`port/PORT.md`](port/PORT.md).

## Pages Readability cannot read

`fetch_content` extracts a page as the original does: Readability and a Turndown conversion first, then the
payload of a React Server Components page, then the Defuddle fallback (a Go port of Defuddle 0.19.3 with its
extractors for sites such as YouTube, Reddit, X, Wikipedia and Hacker News, and its math handling) when
Readability found nothing or less than 500 characters. Defuddle runs locally and makes no request. A page that
all of them give up on ends in the "Fallback options" list of the hosted providers; see
[`port/PORT.md`](port/PORT.md) "Slice 12: Defuddle fallback".

## What is refused until it is ported

Each of these returns a tool error with one message, which names the working alternative, and
never falls back or runs part of the call:

> @pi-in-go/pigpen-websearch (Go port) does not support `<feature>` yet. Install the original
> extension, npm:pi-web-access@0.33.0, which runs unchanged in PiG.


## The search curator

`web_search` takes a `workflow`: `none` (the default: raw results, no model call, no browser),
`summary-review` (open the curator with an auto-generated summary draft) or `auto-summary`
(generate a summary without opening the curator). `/curator` sets the configured workflow
(`on`, `off`, `none`, `summary-review`, `auto-summary`, or no argument to toggle); it is read on
every `web_search` call, and an explicit `workflow` parameter wins.

The curator is a local web page (an HTTP server on `127.0.0.1` with a session token) where the
results stream in as the searches finish. You select results, add searches, switch the provider,
let the model rewrite a query, generate and edit the summary, and approve it; the approved summary
(or the selected raw results) goes back to the agent. **Approve + auto-summary remaining searches
for this prompt** approves the draft and makes later searches that inherit `summary-review` use
`auto-summary` until the run settles. When nobody connects or the idle timeout (`curatorTimeoutSeconds`,
20 seconds, 60 when remote) passes, the curator submits and falls back to a deterministic summary
if no approved draft is present. `/websearch react hooks, next.js caching` opens the curator
directly with comma-separated queries; the submission arrives as a follow-up message.

The page is the original's, byte for byte (`curator_page.html`). The port opens it with `open`
(macOS), `cmd /c start` (Windows) or a detached `xdg-open` (everything else); on macOS the original
prefers a Glimpse window when the optional `glimpseui` package is installed, which a Go extension
cannot load, so the port always uses the default browser there.

If the curator cannot open a browser automatically, such as in Docker, WSL, SSH, or headless
environments, the running curator URL is shown in the tool output. Copy it into a browser that can
reach the Pi host, or use a tunnel/port-forward when needed.

Remote curator access is opt-in: `curatorRemote: true` binds `0.0.0.0` and names the machine's
host name in the URL, `{ "host": "h", "bind": "b" }` sets either. Remote curator sessions print the URL
instead of trying to open a browser by default; set `autoOpenBrowser: true` to launch one anyway,
or `autoOpenBrowser: false` to print the URL for a local curator too. The only access control is the
unguessable session token over plain HTTP, so bind to one private interface and treat the URL as a
secret.

`/google-account` reports the Google account Gemini Web is signed in with. It needs the browser
cookie reader of the authenticated-fetch slice; until that is installed it answers that cookie
access is off, or, when it is on, with the port's refusal message.

The activity widget (`ctrl+shift+w`) lists the last ten search API calls and page fetches with
their status and duration, and the rate limit Perplexity reports.

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
  "fetch": { "allowedModes": ["readable", "raw", "answer"], "defaultMode": "readable" },
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

## Answer mode

`fetch_content` with `mode: "answer"` and a `prompt` fetches each page as usual and replaces its
content with a model's answer to the prompt, using only that page's text (untrusted: the model is
told never to follow instructions inside it). The model is, in order: the call's `answerModel`
(`provider/model-id`), the `fetch.answerProvider` + `fetch.answerModel` pair of `web-search.json`
(both or neither, non-empty strings), or the session's current model. It must accept text, be
enabled in the session's model scope and have credentials. Configured defaults can send page text
to another provider and cost money, so they are opt-in; a per-call `answerModel` is resolved first
and works even when the configured pair is malformed. A page that cannot be answered carries
`Page answer failed: <reason>`; the stored result keeps the fetched page, not the answer.

```json
{ "fetch": { "answerProvider": "openai", "answerModel": "gpt-5.6" } }
```

Summary generation (the model call behind the curator's summaries) has a hard deadline,
`summaryGenerationDeadlineMs` (default `30000`, an integer, capped at `600000`); on expiry or when
no model is usable it falls back to a deterministic summary. The curator workflows use it.

```json
{ "summaryGenerationDeadlineMs": 30000 }
```

## Security

- Every remote fetch goes through an SSRF guard: private, loopback, link-local, multicast,
  NAT64 and 6to4 addresses are refused (also after redirects, and at dial time for direct
  connections), and redirects are limited and re-validated. Page bodies are capped at 5 MB and
  images at 64 megapixels before decoding. `ssrf.allowRanges` exempts CIDR ranges you choose (TUN / fake-IP proxies).
- No browser profile is read unless you opt in (`allowBrowserCookies` in `web-search.json`, or
  `PI_ALLOW_BROWSER_COOKIES=1`). Authenticated fetch (`auth`) and Gemini Web then read your Chromium
  cookies through `security`, `secret-tool`, `kwallet-query`, `powershell.exe`, `sqlite3` and `python3`,
  exactly as the original does; cookies are sent only to the origin they belong to.
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
