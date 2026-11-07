# Changelog

Pigpen releases Packages and Piglets one by one. A Package release is the Git tag `components/<name>/v<version>`, a Piglet release is the tag `<name>/v<version>` with signed Binaries, and both are published to npm under `@pi-in-go`. This file records what the repository released and what changed for users. The owner's release steps are in [docs/maintainers/OWNER-ACTIONS.md](docs/maintainers/OWNER-ACTIONS.md), and what is published now is in [docs/releases.md](docs/releases.md).

## Unreleased

- The README is a short front page. The internals moved to `docs/`, the owner's runbooks and open plans to `docs/maintainers/`, and finished process notes were deleted (they remain in the Git history).
- The first release notes were folded into this file. The licence split they described is now in [docs/components.md](docs/components.md#licences-and-credits).
- The generated index and its schemas moved from the repository root into `catalog/` (`catalog/index.json`, `catalog/index.schema.json`, `catalog/listing.schema.json`, `catalog/catalog.config.json`). A reader that fetches the index by URL now uses `.../main/catalog/index.json`. The site's loader patch in `docs/maintainers/site-change/` was updated to match.
- The benchmark Piglets (`bench-*`) and the components only they select (`aot-runtime`, `bun-runtime`, `bench-ext10`, `bench-ext10-ts`) moved under `bench/`. Their npm package names and versions are unchanged, and they are still released and published, but `catalog/index.json` no longer lists them. The Go-only exception list is gone with them.

## 0.1.x: the first releases

### Release history

Dates are those of the Git tags.

| Date | Release |
|---|---|
| 2026-10-05 | Signed Piglet Binaries `0.1.0` for `a2a`, `acp`, `herdr`, `jev`, `pig-extension-porter`, `pig-games`, `pig-popular`, `pig-porter`, `pig-typesafe`, `pig-warden` and `pig-with-batteries`. Twenty-nine Package tags `components/<name>/v0.1.0` were pushed the same day. |
| 2026-10-23 | `pig-tern` `0.1.0` (the first Binary of that Piglet). `0.1.1` for `a2a`, `acp`, `herdr`, `jev`, `pig-essentials`, `pig-extension-porter`, `pig-games`, `pig-music`, `pig-popular`, `pig-porter`, `pig-typesafe` and `pig-warden`. `0.1.2` for `pig-with-batteries`. The hosted `Release a Piglet` run of this date succeeded for all five targets (linux/amd64, linux/arm64, darwin/arm64, darwin/amd64 and windows/amd64). |
| 2026-11-06 | Every Package (41) and every Piglet source (21) is on npm as `@pi-in-go/pigpen-<name>` and `@pi-in-go/pigpen-piglet-<name>`, checked against the registry that day. |

Pigpen targets PiG 0.7.0 (`scripts/pig-requirement.json`).

### Security fixes after the release candidate

- **`websearch` 0.1.1.** `golang.org/x/image` moved from v0.41.0 to v0.46.0, which closes GO-2026-5061 (VP8 alpha size mismatch panic) and GO-2026-6222 (VP8L allocation), both reachable from `fetch_content` on a hostile WebP, and GO-2026-4961 (a WebP panic on 32-bit platforms, none of which Pigpen ships). The fetch, multi-query search and all-provider search fan-out goroutines and the background prefetch of `web_search` each have a panic boundary, so a panic on a hostile page or provider response comes back as that URL's, query's, provider's or prefetch's error instead of ending the agent process. A decoded image whose pixels disagree with its header is refused, and full image decodes run one at a time. `pig-with-batteries` fuses `websearch`, so a Binary built before this fix should be rebuilt or pulled again. An installed Package keeps its tag, so Package users reinstall from the new tag.
- **`ahp` 0.1.1.** Client images are checked with `image.DecodeConfig` and refused above 64 megapixels before they are decoded (a 420 KiB 20000 by 20000 PNG used to allocate 459 MiB and take 8 s of CPU). The cap equals `websearch`'s `maxDecodePixels`, and a test keeps the two equal. A panic while the chat driver hands a client's prompt or steering message to the agent now fails that turn or message instead of ending the host.

### What the release candidate was verified with

This is the record of the last local verification before the first release, so read each statement as of that run: PiG 0.4.1 (`0.4.1+1.0.3`, commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`, checked against the release's `SHA256SUMS`), Pi 1.0.3, go1.27.1, Node 24.19.0, linux/amd64, a temporary HOME and PiG directories. Every Go module required the published `sdk v0.4.1`. The candidate was first verified on PiG 0.3.0 and again on PiG 0.4.0.

- `npm test` (189 cases), `npm run check`, `npm run stage`, `npm run validate` (every Package, every staged Piglet, `pig install <dir> --validate-only` for the 24 Go extension directories and a real-pig load of `ollama-native`), `npm run test:porter` (2), `npm run test:tool-scope` (4), `npm run test:moved` and `node scripts/build-matrix.mjs` passed.
- `go vet` and `go test` for all Go modules (`npm run test:go-ports`, 76 packages with the tool-renderer build tag on), `npm run test:go`, and `go build` plus `go vet` for `android/arm64` (CGO off) over 32 modules. The android run is a compile check.
- `npm run platform-smoke` built all 11 Piglets into a Binary on linux/amd64. Each reported `0.4.1+1.0.3` and answered a test-model turn with no extension load error. Sizes were 66.7 MiB for `pig-with-batteries`, 62.4 MiB for `a2a` and 61.2 to 62.3 MiB for the others, all unsigned local builds.
- Real-terminal and interop tests: `npm run test:pig-snake` (9 of 9), `npm run test:pig` (4 of 4), the ACP reference client against `pig-acp` and the `acp` Binary (`npm run test:acp-interop`, 30 messages checked against the pinned schema) and `npm run test:doctor-mutations` (every mutation caught).
- On PiG 0.4.0 and not repeated on 0.4.1: `npm run test:port` with Pi 1.0.0 passed 39, failed 0 and skipped 6. The skips are structural: four ports have an original that is not TypeScript and two are self-recorded baselines.
- The traces were re-recorded on Pi 1.0.1 (PiG 0.4.1 content) by the porter re-verification.

### Not verified at the release candidate

- Anything published, since nothing was published when it was written. The Binaries, signatures, tags and npm packages exist now, and no statement in the record above covers them.
- Anything on macOS, Windows or Linux arm64 beyond `go vet`. The hosted release run of 2026-10-23 built and smoke-tested those targets.
- The recorded traces of the five ports without a TypeScript original, and every trace that was recorded rather than re-run.
- Zed and JetBrains themselves (only the ACP reference client was run).
- `websearch` against real providers, and `ollama-native` against a real Ollama (its tests replay fixtures).
- `pigeq mutate` on macOS and Windows, which stops a mutant's leftover worker processes by a marker in their environment that exists only for Linux.
- Binaries built with a PiG after 0.4.1.
