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
| 2 | `providers/types.ts` + `providers/index.ts` + `providers/factory.ts` — the provider contract, the ten `PROVIDER_META` entries and `createSearchProvider` | (planned) | 0 |
| 3 | `web_search` — schema, routing, provider search paths, error classification | (planned) | 0 |
| 4 | `web_fetch` — fetch modes, helpers, content handling | (planned) | 0 |
| 5 | `providers/interceptors/` — the GitHub interceptor and the chain | (planned) | 0 |
| 6 | `/web-tools` command, guidance text, rendering | (planned) | 0 |

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
