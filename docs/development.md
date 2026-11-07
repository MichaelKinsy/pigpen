# Development

## Run locally

From the checkout root:

```sh
npm ci --ignore-scripts
npm run stage
pig piglet validate dist/staged/piglets/pig-porter/piglet.yaml
pig --piglet dist/staged/piglets/pig-porter/piglet.yaml
```

Each authored Resource has one owner under `components/` or a Piglet's private Resource directories. An authored composition may reference a shared Package:

```yaml
packages:
  extension-port: local:../../components/extension-port
skills:
  - name: pigpen-pi-extension-port
    origins: [package:extension-port]
```

PiG does not accept that sibling path directly. Staging materializes the selected Package inside `dist/staged/piglets/<name>/packages/<alias>/` and rewrites only its Package source. The output is an ordinary PiG manifest with no sibling paths. The original manifest and Package remain unchanged. Several Piglets can select the same authored Package without a copied source.

Staging copies local source, licenses, and executable permissions. It excludes `.git`, `node_modules`, and `dist` trees. Shared references must name a direct `components/<name>/` directory. Staging rejects escaping references, symlinks, missing local Packages, invalid YAML, and generated-destination collisions. PiG still validates the resulting manifests and selected members.

Every Piglet manifest lists five build targets: `linux/amd64`, `linux/arm64`, `darwin/arm64`, `darwin/amd64` and `windows/amd64`. `pig piglet build` builds only the machine it runs on, so a local Binary build names that machine. `npm run build:piglet -- <piglet-name>` does this for you. A direct `pig piglet build ... --format binary` needs `--targets <os>/<arch>`.

CI builds all eleven Piglets on one native runner per target and smokes every Binary (see [Platform build matrix](#platform-build-matrix)). The release build uses the same runners (`npm run build-matrix`). The release workflow has built and smoked every target on its native runner on GitHub (the `Release a Piglet` run of 2026-10-23 succeeded for darwin, windows and linux/arm64 as well). The local runs recorded for the first release in [CHANGELOG.md](../CHANGELOG.md) are linux/amd64 only, and it says what was and was not verified.

Edit source, then stage again and restart the selected Piglet. Restaging replaces `dist/staged/`, removing stale files. A failed preflight leaves the previous stage intact, so never treat an old stage as verification of a failed edit. Do not edit or commit generated copies. This is a local development process, not a publisher.

## Maintain and validate

Use Node.js 24 and PiG 0.7.0 (CI builds the pig from the pinned PiG commit). PiG is the authority for the closed Piglet manifest and resource contract.

```sh
npm ci --ignore-scripts
npm test
npm run generate
npm run check
PIG_BIN=pig npm run validate
PIG_BIN=pig npm run test:porter
PIG_BIN=pig npm run test:go-ports -- -race     # go vet and go test for every Go extension module
npm run oracles:fetch                         # the originals of the ports (ports/oracles.lock.json): fetched at their pinned commits, digest-checked
PIG_BIN=pig npm run test:port                  # ports vs recorded Pi traces (PI_BIN=pi re-runs Pi)
KAGENT_GO_DIR=<kagent>/go PIG_BIN=pig node scripts/interop-kagent.mjs   # a2a against kagent's A2A server package
```

`pig install --validate-only` takes an extension directory (`components/<name>/extensions/<dir>`). A Package root is checked by `pig package validate`.

`PIG_BIN` is required by every script and test that runs a real pig, and it must be the pig Pigpen targets (`scripts/pig-requirement.json`: the same `major.minor`, at least that patch). Pigpen never falls back to whatever `pig` is on `PATH`. An older or newer pig stops with a message that names it and says how to build the right one (`go build -o /path/to/pig ./cmd/pig` in the PiG checkout). `PIG_BIN=pig` therefore works only when the `pig` on `PATH` is that build.

The generator checks duplicate YAML keys and presentation metadata. Names and descriptions come from the manifests, and the index records their SHA-256 identities. `catalog/index.json` is committed alongside source changes for predictable static-site consumption. A manifest's intended targets never become binary availability claims. The generator permits only planned entries until verified publisher receipts can supply real release metadata.

### What each test command covers

- `npm test` runs the quality-gate cases, the Go-only gate, the ports-list cases, and the fake-host template and port-layout checks. `npm run check` also runs the Go-only gate and the ports-list check.
- `PIG_BIN=pig npm run test:go` runs the herdr reporter's Go tests. `PIG_BIN=pig npm run test:go-ports` runs `go vet` and `go test` for every Go extension module. Both need a Go toolchain and PiG's SDK. `test:go-ports` runs in CI, `test:go` does not.
- `PIG_BIN=pig npm run test:pig` drives a real `pig` in tmux against a fake herdr, once for the `herdr` Piglet and once for `pig-with-batteries`. With `PIG_SOURCE_ROOT` set it also builds each into a Binary and runs that. It needs tmux and Go, stages first, and is not run in CI.
- `PIG_BIN=pig PIG_SOURCE_ROOT=<PiG checkout> npm run test:pig-snake` plays Pig Snake in a real terminal: title, play, pause, crash, resize, quit, and an agent stream under the open game. It builds the `pig-with-batteries` Binary first, because that Piglet strips the Go SDK and cannot run from its manifest. You can pass `PIG_SNAKE_BINARY=<a Piglet Binary>` instead. It needs tmux and is not run in CI.
- `PIG_BIN=pig npm run test:pig-music` drives the pig-music hello overlay (`/music hello`) in a real terminal (fullscreen and regular TUI: size, keys, timer, resize, close). It needs tmux and Go and is not run in CI.
- `PIG_BIN=pig npm run test:pig-music-player` drives the player itself with real mpv (`--ao=null`) and yt-dlp over the network. It also needs tmux and is not run in CI.
- `PIG_BIN=pig npm run test:pig-music-batteries` builds the `pig-with-batteries` Binary and checks pig-music in it: nothing starts until `/music`, the startup comparison (set `PIG_BATTERIES_BASELINE` to a Binary built without it) and, with `PIG_MUSIC_SMOKE=1`, a real play, hide, `/reload` and `/quit`.
- `PIG_BIN=pig npm run test:tool-scope` checks that a Piglet entry's `tools: []` keeps pig-doctor's `pig_doctor` tool from the model (RPC, print and JSON mode all pass on PiG 0.4.0; they failed on 0.3.x; needs Go).
- `npm run govulncheck` scans every Go module for vulnerabilities its code can reach. CI runs it on every run.

CI checks source quality, generated catalog data, component Packages, and staged manifests. `npm run validate` stages before invoking PiG. The porter scenario exercises independent Package installation and two staged Piglets against a local fixture provider. It checks exact Skill delivery, selection, source preservation, restaging, and path safety. It does not measure an LLM's ability to follow the Skill.

The scenario also checks that an explicit `tools: []` reaches the provider with no tools. It passes on PiG 0.7.0 and the assertion stays as a regression guard. CI runs `npm run validate`, `test:porter`, `test:tool-scope`, `test:moved` and `test:go-ports` with the pig it builds from the pinned PiG commit.

Hosted CI (`Validate Pigpen`) has passed on `main` on GitHub (latest push run 2026-11-02, on the previous pin). The PiG 0.7.0 steps were run locally with the 0.7.0 pig, and no hosted run on this pin is claimed until the branch is merged.

CI also runs `test:packages`, `test:npm-packages`, `test:npm-piglets` and the release rehearsal, each in its own parallel `slow-checks` job (one serial job ran past its 45-minute timeout). CI does not sign Piglet Binaries or publish releases (`release.yml` does, on a Piglet tag). It uploads the Binaries it builds as short-lived artifacts for inspection.

## Platform build matrix

`.github/workflows/ci.yml` has tiers, so a pull request stays quick and every platform is still covered:

| Tier | Runs on | What it does |
| --- | --- | --- |
| `validate` | every run | tests, checks, the PiG validator over every manifest, and **one** linux/amd64 build and smoke of `pig-with-batteries` |
| `slow-checks` | every run, in parallel with `validate` | one job each for `test:packages`, `test:npm-packages`, `test:npm-piglets` and the release rehearsal, each with its own timeout |
| `platform-matrix` | push to `main`, merge group, manual run, the weekly schedule, and a pull request labelled `full-ci` | one job per OS on a native runner: `linux/amd64` (`ubuntu-24.04`), `linux/arm64` (`ubuntu-24.04-arm`), `darwin/arm64` (`macos-15`), `darwin/amd64` (`macos-15-intel`), `windows/amd64` (`windows-2025`). Each downloads the pinned PiG release for its OS, checks its SHA-256, builds **every Piglet** for its own target and smokes each Binary. The Linux jobs also drive each Binary in a terminal (below). Binaries are kept as artifacts for three days |
| `android-cross` | with `platform-matrix` | `go build` and `go vet` of every module for `android/arm64` (Termux) with `CGO_ENABLED=0`. A compile check, not a Binary: pig's builder builds only the machine it runs on |
| `ci-result` | always | the one required check: green when `validate` and every `slow-checks` job passed and the other tiers passed or were skipped |

The smoke runs each Binary in a throwaway home with `PIG_TEST_FAUX=1` (no network, model or credentials). `--version` must match the pinned pig, and `-p "reply with exactly: ok" --model test-faux/faux-1 --no-session` must exit 0, answer `ok` and print no `Failed to load extension`. That proves a Binary starts on the platform, loads every fused extension and completes a turn. What each extension does is the Go tests' job. Run it yourself with `npm run platform-smoke` (set `PIG_BIN` and `PIG_SOURCE_ROOT`; `-- --only <name>` for one Piglet) and the android check with `npm run cross-check` (set `PIG_SDK_DIR` or `PIG_BIN`).

### Each Binary in a real terminal

`npm run test:piglet-e2e` (tmux; POSIX only) starts every Piglet Binary in its own tmux server with a PATH of a few linked system tools and nothing else. There is no Node and no Go, because a Go-only Binary must run alone. `bench-ext10-aot` and `bench-ext10-split`, which carry TypeScript extensions, also get the Node that runs the test. Each runs under a throwaway home and runtime directory and is driven through the keyboard for the features its Piglet is for:

- `/a2a` and `/acp` status, the Jev and warden consent dialogs, `/tasks` and `/agents`, `/todos` and `/goal-list`, `/typesafe`;
- the porter Skill (found in the command list, then run);
- the two games and Pig Snake, played and quit;
- the music player opened (with stand-ins for mpv and yt-dlp: nothing is played);
- the herdr reporter's idle/working/release requests to a fake herdr socket;
- `/cost`, `/tools` and the rest for `pig-with-batteries`.

The benchmark Piglets are driven too: `/aot-runtime` and `/bun-runtime` status in a home with nothing installed, the ten `bench-01` to `bench-10` commands each listed once, the games of `bench-standard`, and for `bench-lean` the core's own commands present and the stripped `/mcp` absent.

Each run also answers a faux turn, must print no `Failed to load extension` and must exit 0 on `/quit`. A Piglet without a scenario in `scripts/e2e/piglets.mjs` fails the suite. Use `PIGLET_E2E_BIN_DIR=dir` for Binaries you already built and `PIGLET_E2E_ONLY=a2a,pig-games` for a few.

The numbers for every Piglet, the 0.5 readiness list and the stripped-vs-stock measurements are in [`plan/progress/piglets-pigpen.md`](plan/progress/piglets-pigpen.md). `node scripts/perf/strip-matrix.mjs` regenerates the sizes (needs PiG 0.7).

### Targets this machine cannot run

PiG's native builder builds only its own OS and architecture, but the fused extensions link into one `go build`. `npm run cross-link-check` (set `PIG_BIN` and `PIG_SOURCE_ROOT`; `-- --only <name>`, `--targets <os/arch,...>`) builds every Piglet with `GOOS` and `GOARCH` set to each other release target. It counts a Binary that links but cannot run here as `linked`. That proves the Piglets compile and link for darwin and windows from Linux. It does not prove they run there: the native runners above do.

The pin lives in `scripts/pig-requirement.json` (`release`: tag, source commit, and each archive's SHA-256). `npm run fetch:pig` downloads and verifies it. Add the `full-ci` label to a pull request to run the whole matrix before merging. `darwin/amd64` ran on `macos-13` until GitHub retired it. `macos-15-intel` replaces it.
