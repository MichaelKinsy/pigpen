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
| 4 | `web-tools.ts registerWebSearchTool` + the ten provider `search()` arms | `(planned)` | 0 |
| 5 | `web-tools.ts registerWebFetchTool` + the fetch arms and `fetch-helpers.ts` | `(planned)` | 0 |
| 6 | `providers/interceptors/` — the GitHub interceptor and the chain | `(planned)` | 0 |

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
