# Port record: @juicesharp/rpiv-web-tools 2.12.0

## Identity

| Input | Value |
|---|---|
| Original | `@juicesharp/rpiv-web-tools` 2.12.0 by juicesharp, https://github.com/juicesharp/rpiv-mono, commit `7c9bc924c5bfd148f36d7ebc9f7bd0a9469d633f`, MIT |
| Oracle | the unmodified original at [`port/oracle`](oracle): 36 files, every one verified byte for byte against the pinned commit by git blob hash |
| Target | PiG 0.4.1 SDK (`github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1`), go1.26 |
| Original's tests | 274 titles in 7 files (`port/upstream-tests.json`, produced by `pigeq twins list --tests port/oracle`) |
| Kind | an extension: two tools (`web_search`, `web_fetch`), one command (`/web-tools`), ten providers and a URL-interceptor chain |

## Scope: slices

`pigeq twins check --ledger port/upstream-tests.json --go extensions/rpiv-web-tools` is the judge. Current state:

| Slice | Scope | Files | Titles |
|---|---|---|---|
| **1 (done)** | `providers/config.ts` — the typed config reader/writer | `providers/config` | **20 exact twins, 1 named skip** |
| **2 (done)** | `providers/types.ts` + `providers/index.ts` + `providers/factory.ts` — the provider contract, the ten PROVIDER_META entries, the factory dispatch; plus `web-tools.ts` credential resolution, provider selection and the `max_results` schema | `index` | **12 exact twins** |
| **3 (done)** | `web-tools.ts registerWebSearchConfigCommand` + `formatShowConfigMessage` — the picker, the two prompt flows, the legacy-key migration and the `--show` report | `index` | **8 exact twins** |
| **4 (done)** | the ten provider `search()` arms + `providers/fetch-helpers.ts` (the HTML pipeline, the content-type guards, the generic HTTP path) | `index` | **2 exact twins** |
| **5 (done)** | `web-tools.ts` fetch guard (`parseAndAssertHttpUrl`, `isPrivateOrLoopbackHostname`) + the four native fetch arms (tavily, exa, jina, firecrawl) + the generic path the other three delegate to | `index` | **27 exact twins** |
| **6a (done)** | `providers/interceptors/github.ts` — `parseGitHubUrl`, `resolveGitHubOptions`, `readUserGitHubConfig`, the non-code segments and the token env var | `providers/interceptors/github` | **22 exact twins** |
| **7 (done)** | `web-tools.ts renderCall`, `renderResult`, the two preview helpers, `instantiateProvider` and the per-tool guidance resolution | `web-tools.render`, `web-tools.guidance`, `index` | **33 exact twins** |
| 6b | `providers/interceptors/github.ts` — the clone and API paths (gh, git, size and timeout limits, tree and README rendering) + `interceptors/chain.ts` | `(planned)` | 0 |

## Slice 7: what is ported

The tool-call renderer, the per-call provider override and the per-tool guidance.

- **The renderer** — the call header (`WebSearch "query"`, ` via <provider>` when a per-call provider is named,
  `WebFetch <url>`), the running line (`Searching...` / `Fetching...`), the count line with its pluralization, the
  collapsed line that shows the count alone, the expanded preview capped at five titles with an overflow line, and
  the fetch line with its optional title suffix and `(truncated)` marker plus a fifteen-line content preview with the
  read-tool hint. The theme is an interface of styling functions rather than a TUI type, so the text is a pure
  function of the result and a theme the twins supply.
- **The four-tier override** — the per-call `provider` wins, then `WEB_SEARCH_PROVIDER`, then `config.provider`, then
  brave. The override is validated; the env var is validated **only when it is the tier that won**, so a bogus env
  var cannot defeat a valid per-call override, and whitespace-only reads as unset.
- **The per-tool guidance** — each tool resolves its own snippet and guidelines, so overriding one leaves the
  other at its defaults; a wrong-typed leaf was already dropped by the slice 1 salvage, and an empty snippet is no
  override. Saving an API key through `/web-tools` carries the guidance and the unknown keys along.

## Slice 6a: what is ported

The GitHub interceptor’s two pure halves: what a URL is, and whether the interceptor is on.

- **`parseGitHubUrl`** — the host check (github.com and www only), the two-segment minimum, the `.git` suffix, the
  non-code segment list that hands issues/pulls/actions/wiki and the rest back to the chain, the blob/tree action
  check with its ref segment, the full-SHA test that decides between the clone and API paths, percent-decoded path
  segments, and a segment that will not decode staying as it arrived instead of failing the whole URL.
- **`resolveGitHubOptions`** — the two-tier opt-in: an explicit user `false` beats a consumer `true`, the object form
  implies opt-in, an `enabled: false` inside an object is honored, and every default (350 MB, 30 s, a temp clone
  directory) is filled in from the object when it sets it.
- **`readUserGitHubConfig`** — reads the stanza off the canonical config through the slice 1 reader, so the
  orchestrator and the interceptor see the same parsed object.
- **The token variable** — `GITHUB_TOKEN`.

The clone and API halves that follow these need `gh`, `git` and the GitHub API, so they are slice 6b with the
chain; nothing in 6a touches the network or the disk.

## Slice 5: what is ported

The fetch half: the URL guard, the four native fetch arms and the generic path.

- **The URL guard** — the URL standard order: a bare word has no scheme and is unparseable, `file:` and friends report
  the protocol they were refused for, and localhost, the IPv6 loopback/unspecified/link-local/unique-local ranges
  and the IPv4 private blocks including 169.254.169.254 are refused by name. upstream: `parseAndAssertHttpUrl` and
  `isPrivateOrLoopbackHostname`.
- **The generic path** — the raw HTTP fetch with the shared UA and Accept headers, the content-type guard, the HTML
  pipeline and the `content-length` parse. upstream: `fetchViaGenericHtml`.
- **The four native arms** — Tavily on `/extract`, Exa on `/contents` with its character budget, Jina on the
  `r.jina.ai` reader, Firecrawl on `/v1/scrape`, each keeping its guard text, its `<Label> Fetch API error (<status>)`
  wrapper and its own content failure (`extraction failed for …`, `no content returned for …`, `success=false`).
- **Raw is ignored by the extraction providers** — Jina and friends return the vendor body untouched even when raw is
  false, because their body is already the extracted form. upstream: the `_raw` parameter they ignore.
- **Rendering** — the fetch header and the search results body, plus the no-results envelope. upstream:
  `formatFetchHeader`, `formatSearchResultsBody`, `buildEmptyResultsEnvelope`.

### Two behaviours the twins pinned down

- `htmlToText` does not remove `<title>`: only script, style and noscript go, so a page title stays inline in the
  text. The first draft of the twin expected it gone; the twin was wrong, and the port keeps the original.
- `url.Parse` accepts a bare word as a relative URL, where `new URL` throws. The port therefore treats an empty
  scheme as unparseable, which is what the standard says and what the original reports.

## Slice 4: what is ported

The ten search arms and the shared HTTP layer.

- **The seam** — Go has no global `fetch` to stub, so the arms take an `httpDoer`. The twins hand in a canned response
  and assert the request shape and the error text without a network; production hands in the net/http client. upstream:
  the global `fetch` each arm calls, and the `vi.fn()` doubles the upstream twins use.
- **The shared HTML pipeline** — `htmlToText` (drop script/style/noscript, block closers to newlines, remaining tags to
  spaces, decode the six named entities and every numeric one, collapse the whitespace), `extractTitle`, the binary
  content-type guard and the generic request headers. upstream: `providers/fetch-helpers.ts`.
- **The ten arms** — each keeps the original's guard (a missing key throws `<ENV> is not set. Run /web-tools to
  configure, or export the env var.` before any request), its method, headers and body shape, its status check
  (`<Label> Search API error (<status>): <body>`) and its vendor-field normalisation, where a missing field becomes an
  empty string instead of dropping the row.
- **The self-hosted pair** — SearXNG asks for one page and slices client-side (its API has no count or limit), and its
  403 and 401 carry the diagnostic hints; both strip every trailing slash from the base URL and send `Authorization`
  only when a key exists.
- **Vendor quirks kept** — Tavily folds a `failed_results` entry into the list instead of dropping it, Exa caps the
  snippet at 300 characters, Ollama picks the local or cloud search path from how it was built.

### Deliberate difference

`truncateRunes` caps at runes, where the original caps JavaScript code units. For ASCII snippets the two agree; for a
snippet with astral characters the port keeps whole graphemes where the original can cut one in half. Recorded here
because it is observable, and left as a gap rather than silently reproduced.

## Slice 3: what is ported

The `/web-tools` command, host-free: the picker, the prompt flows and the `--show` report. Writing it without the pi
context is what makes every branch the upstream twins drive through a mocked context a plain function call here.

- **Picker order** — the active provider first, the rest in declaration order; each row carries its markers, the active
  check and `(configured)`. upstream: `orderedMetas` + `labelOf`.
- **Configured test** — a self-hosted provider counts as configured once a URL is set by env or config; the bare default
  does not count, because it only hints the setting was never touched. upstream: `hasKey`.
- **Label round trip** — the picked row resolves back to its provider by matching the original label or a `"label …`
  prefix, so any marker suffix is safe. upstream: the `PROVIDERS.find` after the picker.
- **Legacy migration** — a save sets the active provider, merges the key and URL, and deletes the legacy top-level
  `apiKey`, which is migrated into `apiKeys`; unknown keys ride along untouched. upstream: the `toSave` construction.
- **Prompt outcomes** — a cancel saves nothing, an empty answer keeps the existing key, and an empty answer with no key
  saves nothing either. upstream: the `trimmed`/`keyToWrite` computation.
- **`--show`** — the config path, the active provider and its source, one masked line per provider naming the env and
  config sources separately, one URL line per self-hosted provider with its source, and the interceptor state. Key masking
  keeps the first and last four characters around an ellipsis. upstream: `formatShowConfigMessage` + `maskApiKey`.
- **Failure text** — a failed write names the config file, not the vendor, because the real cause is the disk. upstream:
  the failed `ctx.ui.notify`.

## Slice 2: what is ported

The provider contract, the ten-entry metadata table and the factory dispatch, with the orchestrator pieces the
`index` ledger measures directly:

- **Credential resolution** — env var, then `apiKeys[provider]`, then the legacy top-level `apiKey` for brave alone.
  Every candidate is trimmed, so an empty or whitespace-only value reads as unset. upstream: `resolveProviderApiKey`.
- **Base-URL resolution** — env, then `baseUrls[name]`, then the meta default; a hosted provider without a URL env
  var short-circuits to the empty string. upstream: `resolveProviderBaseUrl`.
- **Active provider** — env over config over `brave`, reported with the tier that named it and deliberately not
  validated, so a bogus `WEB_SEARCH_PROVIDER` still renders in `--show`. upstream: `resolveActiveProviderName`.
- **Two distinct unknown-provider errors** — the factory says `Unknown search provider: "x"`, the orchestrator says
  `Unknown web_search provider: "x". Valid providers: …`; they are not merged, because only the orchestrator knows
  the valid set. upstream: `factory.ts` vs `assertKnownProvider`.
- **`max_results` schema** — `min:1`, `max:10`, `default:5`, with the same description text. upstream:
  `registerWebSearchTool`.
- **The provider arms themselves** are not ported yet: `newSearchProvider` returns an identity-complete stub whose
  `Search` refuses with a loud error naming the slice that brings it. Slice 3 and slice 4 land the ten `Search` and
  `Fetch` implementations with the per-provider twins that exercise them.

### Note on the upstream comment in `factory.ts`

That comment says the non-search-only providers are "the other five". The META table declares **six** providers with
both roles (tavily, exa, youcom, jina, firecrawl, ollama); perplexity and searxng are search-only, brave and serper too.
The table is authoritative and the port follows it.

## Slice 1: what is ported

`providers/config.ts` is the single typed reader and writer for `~/.config/rpiv-web-tools/config.json`. It owns the
canonical schema, keeps every unknown key, and degrades fail-soft.

- **Fail-soft read** — a missing file, malformed JSON, a directory (EISDIR) and a literal `null` all become the empty
  config. upstream: `loadJsonConfig`, reached through `readConfig`.
- **Per-field salvage** — a schema violation drops exactly the offending JSON-pointer paths and keeps everything else,
  in at most five passes; a pass that deletes nothing, or a config that still fails afterwards, degrades to the empty
  config. An array element error widens to the whole array field rather than leaving a sparse hole. upstream:
  `salvageConfig` and `deleteAtPointer`.
- **Pass-through** — `additionalProperties: true` at every level: an unknown top-level key, an unknown guidance field
  and an unknown field inside the github stanza all survive load and save, which is what the released
  `/web-tools` legacy-`apiKey` migration depends on.
- **Path resolution** — an absolute `XDG_CONFIG_HOME` wins; a relative one is ignored and `~/.config` is used, matching
  `rpiv-config`'s `configPath`.

## Deliberate differences

- **TypeBox is not a dependency.** The schema is the `config` struct with explicit `MarshalJSON`/`UnmarshalJSON` that
  keep the additionalProperties. The one upstream case that asserts the schema object itself is therefore a named
  skip, not a twin: its shape is already covered by the salvage and round-trip twins.
- **Validation order.** TypeBox reports `Value.Errors` in schema order; Go maps have no order, so `validate` walks the
  schema's own field list and sorts the record keys. The deleted set is identical; only the enumeration order differs.
- **`@juicesharp/rpiv-config` is not vendored.** `port/oracle/node_modules` installs it from npm, as in the other
  rpiv ports; its `configPath` and `loadJsonConfig` semantics are reproduced in Go, not imported.

## Named gaps (slice 1)

| Upstream title | Reason |
|---|---|
| `exists and is a TypeBox object` | the port has no TypeBox: the schema is the config struct, and its object shape is covered by the salvage and round-trip twins |

## Not yet ported

Everything outside `providers/config.ts` (253 of the 274 titles) is work in slices 2 to 6 above. No title in those
slices is claimed yet, so `pigeq twins check` reports them as MISSING rather than as skips.
