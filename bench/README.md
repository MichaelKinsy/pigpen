# Bench Piglets

Pinnable Piglets for pi-arena. Each is a Pigpen composition input under `piglets/` with a fixed `release.version`; `pins.json` lists them.

| Piglet | Members | PiG source |
|---|---|---|
| `bench-lean` | none; `strip.keep` keeps no extension, API or feature | original manifest |
| `bench-standard` | `pig-runner`, `angry-pigs`, `bun-runtime` (PiG Standard's other member, the sprite login, is core since 0.4.0) | `piglets/standard` |
| `bench-bun` | `bun-runtime` | `piglets/standard/extensions/bunruntime` |
| `bench-aot` | `aot-runtime` (experimental) | `piglets/aot` |
| `bench-ext10` | the ten bench-ext Go factories, fused | ext-matrix `bench/evals/fixtures/extensions-go` |
| `bench-ext10-aot` | `aot-runtime` and the ten bench-ext TypeScript files (the aot arm) | `piglets/aot`, `bench/evals/fixtures/extensions` |
| `bench-ext10-split` | `aot-runtime`, bench-ext-01..05 as Go factories fused, bench-ext-06..10 as TypeScript | both |

## Pinning

Until PiG 0.5.0 ships, pull the Pigpen commit that holds the manifest and build with a PiG source at or after `pigRevision` in `pins.json`. After 0.5.0, pull the Piglet by its pi-in-go.dev version and fill `version`.

## Where the Go sources come from

`sync-spec.json` names the PiG revision of each copied component. `node scripts/sync-bench-piglets.mjs --pig <PiG checkout>` rewrites them from it and `--check` fails on drift. `go.mod` and `go.sum` are Pigpen's own.

## Frozen dependencies

Go modules are locked by exact `go.mod` versions and `go.sum` hashes, with no `replace`. No bench component declares an npm dependency. A binary a Piglet fetches (Bun, Pi-Bolt) is pinned by version and SHA-256 in its source. `scripts/bench-pins.test.mjs` enforces all of it.

The two TypeScript Piglets (`bench-ext10-aot`, `bench-ext10-split`) need a PiG that locks a single-file extension (see `requires` in `pins.json`). The aot runtime compiles the TypeScript at first start into a slot image that is not part of the Binary.

## bench-lean strip proof

`npm run strip-inventory -- bench-lean` builds the Piglet stock (no `strip`) and stripped and drives both through the same probes. The keep-mode list expands to every probed feature (node-extensions, extension-sdk-go/rust/python, self-update, docs, piglet-builder); the result on 2026-10-12 (pig from PiG `team/imladris/piglet-single-file-lock`, linux/amd64): the probes differ for exactly those seven and nowhere else, and the Binary shrinks from 66.3 MiB to 38.5 MiB.
