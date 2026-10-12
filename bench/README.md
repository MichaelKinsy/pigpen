# Bench Piglets

Pinnable Piglets for pi-arena. Each is a Pigpen composition input under `piglets/` with a fixed `release.version`; `pins.json` lists them.

| Piglet | Members | PiG source |
|---|---|---|
| `bench-lean` | none; `strip.keep` keeps no extension, API or feature | original manifest |
| `bench-standard` | `pig-runner`, `angry-pigs`, `bun-runtime` (PiG Standard's other member, the sprite login, is core since 0.4.0) | `piglets/standard` |
| `bench-bun` | `bun-runtime` | `piglets/standard/extensions/bunruntime` |
| `bench-aot` | `aot-runtime` (experimental) | `piglets/aot` |
| `bench-ext10` | the ten bench-ext Go factories, fused | ext-matrix `bench/evals/fixtures/extensions-go` |

## Pinning

Until PiG 0.5.0 ships, pull the Pigpen commit that holds the manifest and build with a PiG source at or after `pigRevision` in `pins.json`. After 0.5.0, pull the Piglet by its pi-in-go.dev version and fill `version`.

## Where the Go sources come from

`sync-spec.json` names the PiG revision of each copied component. `node scripts/sync-bench-piglets.mjs --pig <PiG checkout>` rewrites them from it and `--check` fails on drift. `go.mod` and `go.sum` are Pigpen's own.

## Frozen dependencies

Go modules are locked by exact `go.mod` versions and `go.sum` hashes, with no `replace`. No bench component declares an npm dependency. A binary a Piglet fetches (Bun, Pi-Bolt) is pinned by version and SHA-256 in its source. `scripts/bench-pins.test.mjs` enforces all of it.
