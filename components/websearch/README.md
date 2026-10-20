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
DuckDuckGo, SERPdive, Kagi. Selection, `auto` order, `"all"`, provider arrays,
`searchRouting` fallback and `webSearch.allowedProviders` follow the original. The other
providers of the original are in the provider table; selecting one (or reaching a configured
one in `auto`, `"all"` or `searchRouting`) is refused as a whole.

## What is refused until it is ported

Each of these returns a tool error with one message, which names the working alternative, and
never falls back or runs part of the call:

> @pi-in-go/pigpen-websearch (Go port) does not support `<feature>` yet. Install the original
> extension, npm:pi-web-access@0.33.0, which runs unchanged in PiG.

- the 26 search providers not listed above (a provider that is selected, listed, configured, or
  reached in `auto`, `"all"` or `searchRouting` with a credential set);
- `web_search` workflow `summary-review` and `auto-summary` (the curator and generated summaries);
- `fetch_content` `auth` (authenticated fetch);
- `fetch_content` URLs the original hands to an extractor: YouTube, local video files, video
  `frames` and `timestamp`, GitHub repositories, issues and pull requests (while their config
  sections are not disabled), and PDFs;
- a hosted fetch provider the original would call (Firecrawl and Crawl4AI with a base URL, TinyFish,
  Search1API, Querit, Kagi, Ollama, Parallel, Bright Data, Gemini with a key);
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
no model is usable it falls back to a deterministic summary. The curator workflows that use it are
not ported yet.

```json
{ "summaryGenerationDeadlineMs": 30000 }
```

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
