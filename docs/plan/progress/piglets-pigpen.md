# piglets-pigpen: every Piglet built and tested, Pigpen measured

Base: public `main` at `b404f362` (12 Piglets) plus this branch. Pinned PiG: 0.4.1 (`scripts/pig-requirement.json`). Host: 196-CPU linux/amd64,
Go 1.27.1, load average about 240 during the measurements, so absolute times are noisy and the ratios are what to read.
Everything here is regenerable from a committed command, named in each section.

## 1. Inventory: every Piglet, its state

"Built" = `npm run platform-smoke` builds the Binary with pig 0.4.1 and it passes `--version` and a faux turn (linux/amd64).
"Other targets" = `npm run cross-link-check` compiled and linked it for linux/arm64, darwin/arm64, darwin/amd64 and windows/amd64
(it cannot run those; the native runners of `ci.yml` run them). "No Node/Go" = the Binary ran in `npm run test:piglet-e2e` with a PATH of
linked system tools only (`sh`, `git`, coreutils): no Node, no Go. "0.5" = builds against the SDK of the PiG 0.5.0 development tree as it is.

| Piglet | Extensions (all Go SDK) | Built, size (MiB) | Other 4 targets | No Node/Go, real-terminal run | 0.5 | README |
| --- | --- | ---: | --- | --- | --- | --- |
| a2a | a2a | 62.4 | linked | yes (`/a2a`) | no (ConfigHome) | yes |
| acp | acp | 61.2 | linked | yes (`/acp`) | yes | yes |
| herdr | herdr | 61.2 | linked | yes (fake herdr: idle, working, idle, release on quit) | yes | yes |
| jev | jev | 61.6 | linked | yes (`/jev`, consent dialog, No) | no (ConfigHome) | yes |
| pig-essentials | tasks, ask-user-question, web-tools, subagents, ponytail + 21 Skills | 62.2 | linked | yes (`/tasks`, `/agents`, ponytail footer) | yes | yes |
| pig-extension-porter | extension-equivalence + the porting Skill | 61.6 | linked | yes (`/skill:pigpen-pi-extension-port`) | yes | yes |
| pig-games | pig-runner, angry-pigs | 61.5 | linked | yes (both games played and quit) | no (SavedHighScore) | yes |
| **pig-music** (new) | pig-music | 62.4 | linked | yes (`/music` opens, Library tab, `q` hides; stand-ins for mpv and yt-dlp) | no (go.sum) | yes |
| pig-popular | todo, ask-user-question, powerline, goal, permissions, subagents | 62.3 | linked | yes (`/todos`, `/goal-list`, powerline) | yes | yes |
| pig-porter | extends pig-extension-porter | 61.6 | linked | yes (the Skill) | yes | yes |
| pig-typesafe | pi-typesafe | 61.7 | linked | yes (`/typesafe`) | yes | yes |
| pig-warden | warden | 61.8 | linked | yes (`/warden`, source dialog, Esc) | no (ConfigHome) | yes |
| pig-with-batteries | 13 extensions | 70.9 | linked | yes (`/cost`, `/tools`, `/a2a`, `/jev`, runner, Pig Snake, `/music`) | no | yes |

Stock pig 0.4.1 is 60.7 MiB (63,692,960 B); a one-extension Piglet adds 0.5 to 1.7 MiB, batteries 10.2 MiB.

Planned and not made here: `pig-tern` (PiG as Tern's agent). It is "coming soon" in the README and banner, belongs to the Tern product,
and has no manifest to build. The banner's `pig-music` is now a Piglet (`piglets/pig-music`, Go only, selects `components/pig-music`).
Public `main` has no Piglet for `ahp`, `ollama-native`, `pig-model-tweaks` or `hardening`; each stays a directly installable Package
(the first two are listed in README as standalone, `pig-model-tweaks` is selected by batteries).

PiG's own two: `piglets/standard` (login, runner, angry-pigs) builds a Binary with pig 0.5 (67.4 MiB, 7 s warm) and passes the same
terminal run (faux turn, `/runner` played and quit, `/angry-pigs` played and quit, `/quit` exits 0). `piglets/porter` cannot be a fused
Binary: its extension imports PiG's parity engine, so the fused vet reports `PIGLET_FUSED_PROCESS_HAZARD` for `os.Stdout` uses in
`coding/...` and `tui/...`, and PiG 0.5 accepts only `build.extensionRealization: fused`. It is source-only by design (`make porter`);
`make porter-check` passes and `make porter-smoke` was broken, now fixed (section 6).

## 2. PiG 0.5 readiness of Pigpen (finding, not fixed here)

Built against the SDK of the PiG 0.5.0 development tree (`PIG_SDK_DIR=<checkout>/extensions/sdk node scripts/cross-check.mjs linux/amd64`), 9 modules fail
`go build`: `a2a`, `angry-pigs`, `context-info`, `jev`, `pig-model-tweaks`, `pig-play` (and so `pig-runner`), `pig-snake`, `warden`.
`Context.ConfigHome()` returns `(string, error)` in 0.5 (`extensions/sdk/context.go`, from the single config-root resolver) and
`SavedHighScore` in pig-play's `gamemcp` takes a config home from it; `pig-music` fails to build its Piglet because its `go.sum` lacks
entries once the SDK is replaced. Seven of the 13 Piglets (acp, herdr, pig-essentials, pig-extension-porter, pig-popular, pig-porter, pig-typesafe)
build as they are; a2a, jev, pig-games, pig-music, pig-warden and pig-with-batteries need the migration. The migration cannot be done against 0.4.1 (the 0.4.1 SDK returns one value), so it belongs
with the pin bump. Compile-compatible code for both SDKs would hide the error, so none was written.

## 3. Real-terminal run per Piglet

`npm run test:piglet-e2e` (`scripts/piglet-e2e.test.mjs`, scenarios in `scripts/e2e/piglets.mjs`): 15 tests, 13 Piglets plus a check
that the PATH has no Node/Go and a check that every Piglet has a scenario. Each Binary runs in its own tmux server (`-L`), throwaway home
and XDG directories (a music player's socket and cache must not be shared between runs), `PIG_TEST_FAUX=1`, no network. It must show
the editor, print no `Failed to load extension`, answer `reply with exactly: ok`, run the scenario, and exit 0 on `/quit`.
Mutation: a games Binary named `a2a` fails the a2a scenario (`timed out waiting for a2a: listener off`). Two passes in a row, 15/15.
Runs on the Linux jobs of `ci.yml` (`PIGLET_E2E_BIN_DIR=dist/bin`). `pig-music` uses stand-ins for mpv (answers its IPC socket) and yt-dlp
(`--version` with today's date): the real ones need the network and a current yt-dlp (the one on this host is 914 days old, so the player
refuses to open, which is its documented behavior).

Not covered, and why: the music player's search and playback (network, mpv); the a2a listener and the ACP/AHP protocols (their own Go
suites and `npm run test:acp-interop` cover them; the Piglet Binary does not speak ACP by itself); Windows and macOS terminals (no tmux
there; the platform smoke covers start-up and a turn).

## 4. Builds for every target

`npm run cross-link-check` (`scripts/cross-link-check.mjs`): PiG's native builder accepts only the host target, but the fused extensions
link in one `go build`, so setting `GOOS`/`GOARCH` gets as far as the verification step, which cannot run a foreign Binary
(`exec format error`) and is counted as `linked`; any compile or link error fails. 13 Piglets x 4 targets, all linked (12 x 4 first,
then `pig-music` x 4). The container builder builds one Linux target only and no darwin or windows Binary can run here, so `linked`
is as far as this host goes.

## 5. Pigpen performance

Commands: `node scripts/perf/strip-matrix.mjs` (sizes, build time), `node scripts/perf/measure.mjs startup|idle` (startup, RSS).
Pigpen's own per-extension costs were measured by `pigpen-perf.md`; nothing here repeats them.

**Stock vs stripped vs full** (pig 0.5.0 development build, `-s -w`, linux/amd64; `taskset` 4 CPUs; 20 cold starts; the host was at load 240):

| Binary | Size | Print mode to first prompt, median / p90 ms | Editor drawn, median / p90 ms | Editor takes input, median ms | Idle RSS MiB (anon) |
| --- | ---: | ---: | ---: | ---: | ---: |
| stock pig (no Piglet) | 66.6 MiB | 86 / 106 | 123 / 158 | 136 | 58.5 (21.7) |
| empty Piglet, nothing stripped | 66.7 MiB | 87 / 102 | 121 / 157 | 137 | 60.8 (21.8) |
| empty Piglet, every binary built-in stripped | 36.7 MiB (-45%) | **59 / 72** | **95 / 106** | **113** | **43.4 (13.4)** |
| keep-core Piglet (4 tools, nothing else) | 36.7 MiB (-45%) | same binary | same binary | same | same |
| herdr Piglet | 67.2 MiB | 99 / 123 | 128 / 149 | 141 | 61.9 (21.6) |
| herdr, strip all | 37.3 MiB (-44%) | 74 / 93 | 103 / 125 | 118 | 46.4 (14.7) |
| PiG Standard (3 fused extensions, full pig) | 67.4 MiB | 108 / 141 | 126 / 153 | 148 | 63.4 (22.7) |

Reading: baking a Piglet in costs 49 KiB; one fused Go extension costs 0.6 MiB and about 12 ms of print-mode start; stripping every
binary built-in (`codemode`, `llama.cpp`, `mcp`, `pig-login`, `tool-search`, the three extra model APIs, the Node, Rust and Python
runtimes, syntax highlighting, export-html, self-update, `docs`, the Piglet builder and more) removes 30 MiB, 27 ms of print-mode
start (-31%), 15 MiB of idle RSS and about 25 ms to a drawn editor. Idle CPU (1.0 vs 1.6 ms/s), threads (10) and wakeups (about 58/s)
are the same or lower.

**Build time** (`piglet build --builder native`): cold Go cache 55 s for the empty Piglet (600 MiB of cache), 36 s for the stripped
variant on top of it (the stripped tag set recompiles the changed packages), 6 to 9 s per Piglet warm, 33 s for `pig-with-batteries`
warm (13 modules). The stripped Binary builds as fast as the unstripped one once its tag set is cached.

**What to do with it.** The strip lists need PiG 0.5 (`strip` is D92; 0.4.1 rejects the field). Candidates, all four compile on 0.5 today
(acp, herdr, pig-typesafe, pig-popular): a Piglet that needs no model APIs beyond OpenAI-style ones can strip `apis`; a Piglet with no Node
extension can strip `node-extensions`, `extension-sdk-rust` and `extension-sdk-python` (the fused Go extensions are linked in, and
herdr builds with `extension-sdk-go` stripped too, saving a further 1.1 MiB). The manifests were not changed: a strip list is
a product decision (it removes `/share`-class features users may expect) and the pin is still 0.4.1. The reproducible numbers are above.

### Per-ID cost of an opt-in strip

A strip is opt-in: a Piglet loses a built-in only when its manifest names it in `strip:`, and `discovery.extensions: []` strips nothing
(the stock `a2a` Binary still loads `pig -e hello.ts`). A request for a stripped runtime fails with the Piglet's name, the entry and how to
keep it. Measured on stock pig 0.5.0 (`-s -w`, linux/amd64, 65.84 MiB), one build tag at a time (`pig_strip_<id>`), so authors see what each
entry saves. Entries that are not tags (`themes`, `skills`, `prompt-templates`, tools, commands) switch the feature off at runtime and save no bytes.

| strip entry | saves | what the Piglet's users lose |
| --- | ---: | --- |
| `features: node-extensions` | 7.50 MiB | TypeScript and JavaScript extensions (`-e`, `pig install`) |
| `features: word-dictionaries` | 6.05 MiB | word navigation in CJK, Thai, Lao, Khmer and Burmese text |
| `extensions: codemode` | 3.36 MiB | the Codemode extension |
| `apis: bedrock-converse-stream` | 3.03 MiB | Amazon Bedrock models |
| `features: extension-sdk-go` | 2.55 MiB | building Go source extensions (fused Go extensions stay) |
| `extensions: mcp` | 1.02 MiB | MCP servers and `pig mcp` |
| `features: piglet-builder` | 0.89 MiB | `pig piglet build` and `pig piglet publish` |
| `features: extension-sdk-rust` | 0.83 MiB | building Rust source extensions |
| `features: mermaid` | 0.62 MiB | mermaid diagrams in code blocks |
| `features: syntax-highlight` | 0.59 MiB | code highlighting |
| `features: extension-sdk-python` | 0.37 MiB | running Python extensions |
| `features: docs` | 0.36 MiB | `pig docs` and the docs pointer in the system prompt |
| `features: export-html` | 0.32 MiB | HTML session export |
| `extensions: llama.cpp` | 0.31 MiB | llama.cpp router models |
| `apis: google-vertex` | 0.29 MiB | Google Vertex models |
| `features: changelog` | 0.23 MiB | `/changelog` and What's New |
| `features: self-update` | 0.14 MiB | `pig update` of pig itself (a Piglet Binary refuses it anyway) |
| `apis: mistral-conversations` | 0.09 MiB | Mistral Conversations models |
| `extensions: pig-login` | 0.04 MiB | the login extension |
| `extensions: tool-search` | 0.01 MiB | tool search |
| all of the above | 28.97 MiB | everything strippable (36.9 MiB left) |

The four runtimes together (`node-extensions` and the three SDKs) save 11.27 MiB; the entries are not additive to the byte (shared code).
Pigpen's own opt-ins, each proved by `node scripts/strip-inventory.mjs <piglet>` (stock and stripped Binary through the same probes;
the inventories differ in the probes of the stripped IDs and nowhere else): `a2a`, `pig-typesafe` and `pig-with-batteries` strip
`node-extensions`, the three SDKs and `self-update`, because each declares a closed extension surface. The user-discovering Piglets strip nothing.

## 6. Fixed on the way (red first)

- **PiG repo, `make porter-smoke` failed on a clean tree** (three causes: the caller's `PIG_CODING_AGENT_DIR` beat `PIG_HOME/agent`, so
  the isolated trust setting was not read; it waited for `Ready. Type a message`, which the startup never prints, and matched an old
  header format; it looked for the plan's totals, which the TUI scrolls away). `automation/porter/verify-pig-porter-local.sh` now sets
  its home, waits for the header's `[Extensions]` path lines and checks the members every group ends with; it uses its own tmux server.
- **`powerline-footer` flake**: the original's 200 ms git limit turned a slow `git symbolic-ref` into `<sha> (detached)` under load.
  Limits are variables the tests raise; `TestSlowSymbolicRefStillFindsTheBranchWithinTheTestTimeout` makes the failure deterministic.
- **`extension-equivalence` flake**: `TestMutateRunsMutantsInParallelAndKeepsTheirOrder` asserted `elapsed <= 1.5 s` and failed in both
  full runs under load. It now proves parallelism by rendezvous (red with `Jobs: 1`, 102 s; green with 4 jobs, 2 s).

- **`npm run rehearse:release` died from a branch checkout**: its bare clone carries every local branch, so the push of its own commit to
  `main` was rejected as non-fast-forward. The push is forced in the throwaway repository; `REHEARSAL PASSED (15 checks)` from a branch.

Gates run on the final tree: `npm test` 326/326, `npm run check`, `test:go` 65 packages and `test:go-ports` 95 packages (0 failures, after
the two fixes), `test:npm-piglets` 15/15, `test:pig` 4/4, `rehearse:release` 15 checks, `platform-smoke` 13/13, `test:piglet-e2e` 15/15
(twice), `cross-link-check` 13 x 4 linked.

## 7. Gates that only ratchet down

PiG (`Makefile`, named "ratchet" or "may only shrink"): `divergence-guard` (`automation/ci/divguard/baseline.toml`, 70 entries),
`port-lint` (`automation/ci/portlint/baseline.toml`, 1,182 entries), `any-trap` (`automation/ci/anytrap/baseline.toml`, 136 entries),
`slop-check` (`test/evals/budgets.toml` `[slop]`: verbosity <= 0.050, erosion <= 0.660). Related baselines that are also lists of
accepted debt: `test/parity/portmap-unproven-baseline.txt` (477 lines), `test/parity/sdk-surface-exceptions.toml` (108 lines),
`test/mcp-conformance/baseline.json` (413 lines), `test/parity/interface-closure/gaps/proof-class-baseline.tsv` (14 lines), and the
`perf-check` ceilings (`pig.startup` 80 ms and 48 MiB median). Pigpen: `go-only-exceptions.json` (empty; `scripts/go-only.mjs` fails
on a stale entry), and the ports list status counts (33 ports). The three big PiG lists are ratchets by construction; none was
edited here.

## 8. Not done

- The 0.5 migration of the seven modules (section 2) and strip lists in the manifests (section 5): both wait for the pin bump.
- A real darwin or windows run: no native runner here. `ci.yml`'s `platform-matrix` runs them; the first run is the maintainer's.
- `pig-tern`: not in this repository.

## Piglet Binary cold build time (2026-10-10, linux/amd64, pig 0.5.0 dev, host load ~230)

a2a Piglet (one fused Go extension with an external module), `pig piglet build --format binary --builder native`:

| Case | Time |
|---|---|
| cold Go build cache, module cache warm | 62 s: 2 s prepare (resolve, hash, vet of members, workspace checksums) + 59 s compile and link of PiG + 1 s verify |
| warm Go build cache | 7.9 s: 2 s prepare + 5 s link and verify |
| pig-warden / pig-music / batteries after a2a, shared cache | 10 s / 26 s / 21 s |

The compile cache is shared across Piglets: the cache keys (`-trimpath`, `-buildvcs=false`, link-only `-ldflags`) do not change between Piglets, so a second Piglet recompiles only its members. A strip list changes the build tags and recompiles the packages that carry them.

Fresh module cache and fresh build cache, network on (what a new machine pays on its first build), `go build ./cmd/pig` of the PiG checkout, same host, run back to back:

| Case | Time |
|---|---|
| `go build` alone, fetching modules as it needs them | 68.5 s |
| `go mod download` of the whole workspace graph, then an offline `go build` | 14.7 s + 57.1 s = 71.8 s |

A prefetch does not pay: Go already overlaps the module fetch with the compile, and the download is 15 s of the 68 s. An earlier run that showed a 64 s serial fetch (45 modules) against a 27 s targeted download was proxy latency noise on a warm compile cache, not a property of the build; it is retracted. No change to the builder is proposed for cold build time. The cost is the compile of PiG itself (about 55-60 s on this host), which the shared build cache removes for every later Piglet.

## Where the cold build time goes (2026-10-10, PiG checkout, linux/amd64, same host)

`go build` of cmd/pig with warm modules and an empty build cache, `-toolexec` timing every compile and link:

| Step | Time |
|---|---|
| module download (whole graph, once per module cache, parallel with the compile) | 15 s of a 68.5 s first build |
| compile: 586 package compiles, 219 s of work, 56.6 s wall | 94% of the 60 s build |
| link (the one that matters: `-s -w`) | 2.5 s |
| relink alone, compile cached | 4.1 s |
| assemble, cgo | 17 s span, 4.4 s of work, overlapped with the compile |

The compile is not CPU-bound: for 35.3 s of its 56.6 s at most one package was compiling. It is a dependency chain, and the longest single link of it is the `ai` package, 13.5-15.6 s on its own (everything above it waits). The next ones are `internal/codingagent` 5.5 s, `tui` 3.3 s, `coding/cli` 2.5 s.

Why `ai` takes 13.5 s: `ai/models_generated.go` (1.46 MB, 1563 models) holds 841 map literals and 2641 pointer literals, which the compiler cannot lay out statically and compiles as one very large initialisation function. With 20 of the 1563 entries the package compiles in 6.7 s, so the catalog costs about 7-8 s on the critical path of every cold build (12-13% of it). A generated catalog the compiler can lay out statically, or one that is embedded as data and decoded on first use, would remove most of it. Runtime cost of that init is small (2 ms, 364 KB, 7.8k allocations), so the gain is build time, not startup.

Caching the module download per lock: Go's module cache is already keyed by module version and checksum, so a CI cache keyed by the hash of go.sum is the right unit; it saves the 15 s. A cold Piglet build is otherwise the compile of PiG, which the shared build cache removes for every later Piglet (10-26 s).

### Startup finding from the same run

`GODEBUG=inittrace=1 pig --version` shows the largest package initialisation is `github.com/santhosh-tekuri/jsonschema/v6`: 5-9 ms and 1.7 MB per process start (median of 3: 6.8 ms). Its `init` (draft.go:127) compiles the metaschemas and vocabulary schemas of all five drafts. PiG links it into every path through `ai/validate.go` and `coding/piglet/schema.go`. Against `pig.startup` (80 ms, 48 MiB) that is about 8% of the time budget. Go cannot link a package lazily, so removing the cost means a patched copy of the library with lazy draft compilation, or asking upstream for it. Not done here: it is a dependency decision.

## Proposal: the generated model catalog on the build critical path (2026-10-10)

Problem: `ai/models_generated.go` (1563 models, 841 map and 2641 pointer literals) is one package-level composite literal, compiled as one very large initialisation function. The `ai` package compile is 13.6 s (three runs: 15.7, 13.7, 13.6); everything above it waits for it.

Measured, same host, `ai` package only, deps cached, compile wall time from `-toolexec`; runtime from `GODEBUG=inittrace=1 pig --version`, three runs:

| Layout | `ai` compile | `ai` init at every start |
|---|---|---|
| today: one package-level literal | 13.6 s | 1.6 ms, 364 KB |
| 20 of 1563 models (lower bound, not a layout) | 6.7 s | n/a |
| N package-level vars + `slices.Concat` (8, 16, 48 vars) | 13.9-15.4 s (no gain: still one init function) | 2.0 ms, 770 KB |
| function-local, `append` per model, 16 chunks | 6.3 s (5.5-7.3 at 48 chunks) | 3.5-4.2 ms, 1.49 MB |
| function-local, `return []GeneratedModel{...}` + `slices.Concat`, 16 chunks | 7.4 s | 3.1-3.7 ms, 1.95 MB |
| today, `-gcflags=ai=-c=1/2/4/8/16` | 19.2 / 14.6 / 13.5 / 15.1 / 12.9 s (backend concurrency does nothing: one function) | unchanged |

The gain is real (about 7 s, 54% of the `ai` compile, 12% of a cold build) and comes only from removing the single huge init function. Every layout that does so builds the catalog at run time, which costs +1.9 to +2.6 ms and +1.1 to +1.6 MB at every process start: today the compiler lays most of the literal out statically.

Recommendation:
1. Do not change the generated layout as it stands. A one-time 7 s on the first build of a machine does not justify +2.4 ms and +1.1 MB on every start against the 80 ms / 48 MiB `pig.startup` budget.
2. Remove the whole 55-60 s compile instead, with no code: key a `GOCACHE` cache on the hash of the PiG source tree plus the Go version (CI and the Piglet release workflows). A Piglet build with that cache is the 8 s warm case measured above.
3. If the 7 s is wanted anyway, make the catalog lazy so the cost leaves the start path: `var GeneratedModels` becomes `func GeneratedModels() []GeneratedModel` returning a `sync.OnceValue` of the 16-chunk `append` form, emitted by `cmd/gen-models/main.go:719`. That changes 32 call sites, and it only pays if the production start path does not read the catalog (readers: `ai/provider_strip.go:40`, `internal/codingagent/radius_models.go:98`, and the registry init, to be checked with a startup allocation test written first). Paths that do read it pay the same +2.4 ms once. I would build it behind a red startup budget test if you approve.
