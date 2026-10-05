# Pigpen: first release notes (release candidate, not published)

Status of this file: the notes for the first Pigpen release candidate. Nothing here is published, signed or tagged. It states
only what was run (the final run is on **PiG 0.4.1**, listed below) and what the Packages' own READMEs and `provenance.json`
files say. Anything not run is listed under "Not verified".

## What Pigpen is

A repository of PiG **Packages** (installable Skills, extensions and libraries) and **Piglets** (compositions that select
Packages into a named agent). Every extension is a Go extension on the public PiG Go SDK, so a Piglet can be built into one
Piglet Binary with the extensions fused in. The ports of Pi (TypeScript) extensions carry their upstream, pinned commit,
license and credit in `provenance.json` and `CREDITS.md`; the same data is the ports list in
[`ports/README.md`](ports/README.md) (`ports/ports.json`), which `npm run check` validates.

## Install (what works today)

There is no published index, release Binary, signing key or remote install command yet
([RELEASE-BLOCKERS.md](RELEASE-BLOCKERS.md)). Use a local checkout and PiG 0.4.1 (`pig --version` prints `0.4.1+1.0.3`;
`go install github.com/MichaelKinsy/PiG/cmd/pig@v0.4.1` installs it).

```sh
cd <your checkout of this repository>
pig install ./components/<name>          # one Package, for example ./components/pig-snake
npm ci --ignore-scripts && npm run stage # stages the Piglets under dist/staged/
pig --piglet dist/staged/piglets/<name>/piglet.yaml
pig piglet build dist/staged/piglets/<name>/piglet.yaml --format binary --out ./<name>   # needs PIG_SOURCE_ROOT: a git checkout of the PiG source
```

The pig games (`pig-runner`, `angry-pigs`) need `components/pig-play` beside them; install them from a checkout, or
select the `pig-games` Piglet. The extension Packages build from Go source on first use unless fused into a Binary (every
run in these notes had a Go toolchain on `PATH`). PiG itself downloads `fd` and `ripgrep` on its first start when
they are not on `PATH` (a network step; a Binary's interactive start waits for it).

## Packages

Every Package is MIT. Whose copyright the `LICENSE` names depends on where the code came from:

- **Pigpen code, including the Packages moved from PiG** (the repository `LICENSE`, and the `LICENSE` of `a2a`, `angry-pigs`,
  `context-info`, `extension-equivalence`, `extension-port`, `herdr`, `pig-doctor`, `pig-play`, `pig-runner`, `pig-snake`,
  `session-ingest`): Copyright (c) 2026 Michael Kinsy. PiG is credited in each moved Package's `CREDITS.md` and `provenance.json`.
- **Ports of third-party upstreams** (`acp`, `ahp`, `dirty-repo-guard`, `jev`, `typesafe`, `warden`, `websearch`, `pi-typesafe`,
  `pi-typesafe-api`, `dev-skills`): the `LICENSE` lists the upstream copyright line(s) verbatim and then "Copyright (c) 2026 Michael
  Kinsy (the Go port)". The upstream's own `LICENSE` is kept unchanged next to the original source (`port/oracle/...`,
  `upstream/...`), and the credit is in each Package's `CREDITS.md`.

The `pig-login` Package (the sprite login header and `/sprite`, moved from PiG) was removed after this candidate was verified:
PiG 0.4.0 has the sprite login and `/sprite` built in, with every sprite `pig-login` had, and saves the choice in the same
file (`$PIG_HOME/state/pig-standard/login.json`) that the games read. The runs listed below predate the removal.

Apache-2.0 upstreams (a2a-go, the ACP typescript-sdk, agent-stuff, WorkflowEvals) keep their license files; none has a NOTICE file at
its pin. "Original" means written for Pigpen. Upstream credit and license for everything ported or moved:

| Package | What it is | Origin | Upstream, pinned commit, license, author |
|---|---|---|---|
| `extension-port` | Test-first Skill for porting Pi TypeScript extensions to Go | moved | PiG porter, [MichaelKinsy/PiG](https://github.com/MichaelKinsy/PiG) `d86eb93f217e`, MIT, Michael Kinsy |
| `extension-equivalence` | `pigeq`: differential harness (Pi vs PiG traces), SDK gap scan, mutation check | original | none |
| `dirty-repo-guard` | Go port of Pi's dirty-repo-guard example | port | [earendil-works/pi](https://github.com/earendil-works/pi) `f07218c4d4bb`, MIT, Mario Zechner |
| `herdr` | Reports PiG idle, working and blocked to the herdr terminal workspace manager, and the resume command herdr 0.9.2+ restores a pane with | original | none |
| `ollama-native` | Ollama as a model provider through its native `/api/chat`: models read from the server, streaming with tool calls, no network at start-up | original | none (started by Peder Munksgaard, pull request 3) |
| `acp` | Agent Client Protocol: the `pig-acp` companion and `/acp`; lets an editor such as Zed drive PiG | port | [svkozak/pi-acp](https://github.com/svkozak/pi-acp) `b0581c9c1d67`, MIT, Sergii Kozak; [Agent Client Protocol typescript-sdk](https://github.com/agentclientprotocol/typescript-sdk) `73bc30649b65`, Apache-2.0, Zed Industries |
| `pig-play` | Shared pig sprite, pixel rasterizer, arcade scenery, terminal plumbing (library) | moved | [MichaelKinsy/PiG](https://github.com/MichaelKinsy/PiG) `d86eb93f217e`, MIT, Michael Kinsy |
| `pig-runner` | PiG Runner, `/runner` and `/pig-runner` | moved | same as `pig-play` |
| `angry-pigs` | Angry Pigs, `/angry-pigs` | moved | same as `pig-play` |
| `pig-snake` | Pig Snake, `/pig-snake` and `/snake`: a herd forms behind the head | original game, adapted PiG code and art | [MichaelKinsy/PiG](https://github.com/MichaelKinsy/PiG) `d86eb93f217e`, MIT, Michael Kinsy |
| `session-ingest` | `read_session`: token-safe queries over Pi and PiG session transcripts | original | none |
| `context-info` | `/context`, `/tools`, `/cost`, `/prompts`, opt-in footer | original | none |
| `dev-skills` | Development Skills adapted from public sources | adapted | [mattpocock/skills](https://github.com/mattpocock/skills) `d81f3a183412`, MIT, Matt Pocock; [mitsuhiko/agent-stuff](https://github.com/mitsuhiko/agent-stuff) `0865c849befd`, Apache-2.0, Armin Ronacher (the pins are the reference copies kept in the Package; the earlier revision the adaptations were made from was not recorded, see its `CREDITS.md`) |
| `pig-doctor` | `/doctor`, read-only `pig_doctor` tool and a `pig-doctor` command line: find and fix cruft, with restorable backups | original | none |
| `pig-music` | `/music`: a full-screen YouTube Music player (mpv and yt-dlp; a pure-Go native engine in the separate `pigmusic` command), quick `/music` commands, cover art, a palette and a pulse that follow the track; off until `/music`, no tool | original | none (no Orpheus code) |
| `typesafe` | Shared Go client for the TypeSafe AI API plus an own-model backend (library) | port | [typesafe-ai/typesafe-sdk-js](https://github.com/typesafe-ai/typesafe-sdk-js) `66880ccded6c`, MIT, TypeSafe and evinism; [typesafe-ai/system-one-adapter-python](https://github.com/typesafe-ai/system-one-adapter-python) `e1d4cc938204`, MIT, TypeSafe AI, Erik Gafni and Daniel Gafni; [typesafe-ai/WorkflowEvals](https://github.com/typesafe-ai/WorkflowEvals) `0ac3b8ad8454`, Apache-2.0, Sam |
| `pi-typesafe` | `typesafe_evaluate` tool and `/typesafe` playground; off until enabled | port | [DevMortimer/pi-typesafe](https://github.com/DevMortimer/pi-typesafe) `ed439f834665`, MIT, Ryan Gapac (DevMortimer) |
| `pi-typesafe-api` | Typed Go API for extension authors (library half of pi-typesafe) | port | same as `pi-typesafe` |
| `websearch` | First slice of pi-web-access: `web_search`, `fetch_content`, `get_search_content`, `source_check`, `web_enable`; SSRF-guarded fetch, retained sources. The rest of pi-web-access is deferred (named in its PORT.md) | port | [nicobailon/pi-web-access](https://github.com/nicobailon/pi-web-access) `9a734ed195da`, MIT, Nico Bailon |
| `jev` | Typed judge for tool calls and output plus a `jev_ask` tool; off until `/jev on`, which shows what leaves the machine; fails open | port | [y0usaf/pi-jev](https://github.com/y0usaf/pi-jev) `88e5fb388894`, MIT, y0usaf |
| `ahp` | Serves the running session to remote Agent Host Protocol clients over WebSocket (reconnect, auth); no listener unless you ask | port | [Qusic/pi-ahp](https://github.com/Qusic/pi-ahp) `4065e98309b0`, MIT, Bang Lee; protocol: [microsoft/agent-host-protocol](https://github.com/microsoft/agent-host-protocol) `296b25e7b698`, MIT, Microsoft Corporation; vendors Microsoft's Go AHP client (`third_party/agent-host-protocol-go`, credited in its CREDITS.md) |
| `a2a` | A2A (Agent2Agent) server and client tools; the listener is off unless configured and refuses to start without authentication | original, on an upstream SDK | dependency: [a2aproject/a2a-go](https://github.com/a2aproject/a2a-go) `ebf17c56ef7e`, Apache-2.0, The A2A Authors |
| `warden` | Guardrails that steer instead of interrupt (irreversible, off-task, stuck, unverified done claims); opt-in | port | [DevMortimer/pi-warden](https://github.com/DevMortimer/pi-warden) `a12b2703b2c7`, MIT, Ryan Gapac; and pi-typesafe as above |

Data-flow notes that the Packages state themselves: `pi-typesafe` and `warden` send content to `api.typesafe.ai` unless the
own-model backend (the model PiG is configured with) is selected, or for `warden` its offline judge; `jev` defaults to that own-model backend and sends to
`api.typesafe.ai` only when its TypeSafe backend is chosen. All three are off until enabled and show what leaves the machine
before anything does. `websearch` sends a search to the keyless Exa MCP (`mcp.exa.ai`) when no provider is configured, as the
original does, once the model has called `web_enable` and then `web_search`; `fetch_content` fetches the URL it is given.
`a2a` sends to the A2A agents you configure and opens a listener only when configured.

## Piglets

| Piglet | What it selects |
|---|---|
| `pig-extension-porter` | `extension-port` and `extension-equivalence`: the Go-only porting workflow |
| `pig-porter` | umbrella that extends `pig-extension-porter` |
| `herdr` | the herdr reporter on its own |
| `acp` | `acp`; the editor runs `pig-acp --pig <pig or Binary>` (the Binary does not speak ACP itself) |
| `pig-games` | `pig-runner`, `angry-pigs` (nothing starts until you type a command) |
| `jev` | `jev` (the standalone Piglet; needs the shared `typesafe` client Package staged with it) |
| `a2a` | `a2a` |
| `pig-typesafe` | `pi-typesafe` |
| `pig-warden` | `warden` |
| `pig-with-batteries` | herdr, the games and Pig Snake, `session-ingest`, `context-info`, `pig-doctor`, `jev`, `a2a`, `websearch`, `pig-music` (`/music`, nothing until you type it), plus an empty build fixture (`seed-check`). Source only; not the finished batteries-included composition. `pig-doctor` is selected as a command only (`tools: []`), which PiG 0.4.0 and later honour in every mode (PiG 0.3.x ignored it in print and JSON mode; `npm run test:tool-scope`) |

## What was verified on the release candidate

Final run on **PiG 0.4.1** (`0.4.1+1.0.3`, the `v0.4.1` release build, checked against the release's `SHA256SUMS`; the tag is
commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`), Pi 1.0.3, with a temporary HOME and PiG
directories, go1.27.1, Node 24.19.0, linux/amd64. Every Go module requires the published `sdk v0.4.1`.
The first release candidate was verified on PiG 0.3.0 (`63c6ba456`) with Pi 0.87.1, and a later run on PiG 0.4.0; both are replaced
by this run, except where a line below says it was not repeated.

- `npm test` (189 cases), `npm run check` (index drift, quality gates, Go-only gate, ports list), `npm run stage`,
  `npm run validate` (every Package, every staged Piglet, and `pig install <dir> --validate-only` for the 24 Go extension
  directories, and a real-pig load of `ollama-native`, which that command cannot load), `npm run test:porter` (2), `npm run test:tool-scope` (4: RPC, print and JSON mode, and the control that
  offers the tool without the scope), `npm run test:moved`, `node scripts/build-matrix.mjs`: all pass.
- `go vet` and `go test` for all Go modules (`npm run test:go-ports`; 76 packages, with the tool-renderer build tag on; includes the 11 ACP end-to-end scenarios
  against the real pig), `npm run test:go` (the extensions only). `go build` and `go vet` for
  `android/arm64` (CGO off) pass over 32 modules (a compile check, not a run). The other cross targets were not repeated on 0.4.1.
- `npm run platform-smoke` built every one of the 11 Piglets into a Binary on linux/amd64; each reports `0.4.1+1.0.3` and answers
  a test-model turn with no extension load error. Sizes in MiB: `pig-with-batteries` 66.7, `a2a` 62.4, and 61.2 to 62.3 for the
  others. All are unsigned, local builds.
- Real-terminal and interop tests: `npm run test:pig-snake` (9 of 9), `npm run test:pig` (herdr Piglet and `pig-with-batteries`
  against a fake herdr, 4 of 4), the ACP reference client against `pig-acp` and the `acp` Binary (`npm run test:acp-interop`, 30
  messages checked against the pinned schema), `npm run test:doctor-mutations` (every mutation caught).
- (Run on 0.4.0, not repeated on 0.4.1.) `npm run test:port` with Pi 1.0.0 (`PI_BIN`) and `PIG_UPSTREAM` (a `git archive` of PiG `piglets/standard` at `d86eb93`):
  **39 pass, 0 fail, 6 skipped**, on the final tree after the review fixes were merged. Every port
  against its recorded traces, every mutation list, `gaps` for each port, and the live re-runs: the original under Pi still
  produces the recorded traces for dirty-repo-guard, jev, pi-typesafe, warden and websearch (the traces were recorded on Pi
  1.0.1 and Pi 1.0.0 reproduces them), and the upstream Go extensions still produce them for angry-pigs and pig-runner. The 6
  skips are structural: the 4 ports whose original is not TypeScript (2 Go relocations, 2 self-recorded) have nothing to
  gap-scan, and the 2 self-recorded baselines (a2a, pig-snake) have no original to re-run.
- Publisher flow with a throwaway key and a stub `gh` (nothing uploaded): `pig piglet publish --tag-prefix <name>/` dry run for
  all 11 Piglets, on 0.4.0 and not repeated on 0.4.1 (tag `<name>/v0.1.0`, pull `github:<owner>/<repo>/<name>@0.1.0`); a signed build, then `publish --yes --artifacts`,
  produced a signed `piglet-release.json` and `SHA256SUMS` for one target. Details in
  [RELEASE-BLOCKERS.md](RELEASE-BLOCKERS.md).
- The traces were re-recorded on Pi 1.0.1 (PiG 0.4.1 content) by the porter re-verification; their record is
  [`docs/plan/progress/porter-verify.md`](docs/plan/progress/porter-verify.md). Gap scans: the Go port gap table is PiG
  0.4.1's, and a port that needs tool renderers is no longer blocked: the 0.4.1 SDK has them.
- Not re-run on 0.4.1: `node scripts/interop-kagent.mjs` (needs a kagent checkout), `node scripts/ports.mjs verify-pins`
  (needs network), the websearch twin ledger. Their last runs were on the 0.3.0 candidate (kagent `e4516302`: four
  `TestInterop_*` tests pass; 21 pins exist upstream; 305 exact twins and 570 named skips).

## Build targets

Every Piglet manifest declares `build.targets: [linux/amd64, linux/arm64, darwin/arm64, darwin/amd64, windows/amd64]`, and
`node scripts/build-matrix.mjs` emits one native runner per target (`ubuntu-24.04`, `ubuntu-24.04-arm`, `macos-15`,
`macos-15-intel`, `windows-2025`). PiG's native Binary builder builds only the machine it runs on, so a local build names
that machine: `npm run build:piglet -- <name>` does it, and a direct `pig piglet build` needs `--targets <os>/<arch>`.

What is proven for each target:

- **linux/amd64**: built, started and run (everything under "What was verified").
- **CI** (`platform-matrix` in `.github/workflows/ci.yml`) builds all eleven Piglets on a native runner for each of the five targets
  and smokes every Binary (`--version`, one `PIG_TEST_FAUX` turn, no extension load error); `android-cross` compiles and vets every
  module for `android/arm64`. Locally the linux/amd64 job (release pig from the pin, all eleven Piglets, 58 seconds) and the android
  check passed; the other four platforms have not run yet (first run: a manual `workflow_dispatch`).
- **the other four, before CI ran there**: `go vet` type-checked every Go module for each of them on 0.4.0 (0 failures); not repeated on 0.4.1. On the 0.3.0 candidate every Piglet
  was also compiled and linked for each of them (40 of 40) with a private, uncommitted patch of that pig that skipped its check of
  running the built artifact; that was not repeated on 0.4.0 or 0.4.1. Neither proves anything about how a Binary runs there, and none was
  started (no arm64 emulation on the build host; no macOS or Windows host).

Binary sizes on 0.4.1, linux/amd64, in MiB: `a2a` 62.4, `acp` 61.2, `herdr` 61.2, `jev` 61.6, `pig-extension-porter` 61.6,
`pig-games` 61.5, `pig-popular` 62.3, `pig-porter` 61.6, `pig-typesafe` 61.7, `pig-warden` 61.8, `pig-with-batteries` 66.7.

Release Binaries per target need the protected release workflow that does not exist yet ([RELEASE-BLOCKERS.md](RELEASE-BLOCKERS.md));
the CI artifacts are unsigned, three-day inspection copies, not releases.

### What still has to be run on macOS, Windows and Linux arm64

Nothing below has been run. On each machine build natively, with `PIG_SOURCE_ROOT` a git checkout of the PiG source that the
`pig` was built from: `npm ci --ignore-scripts && npm run build:piglet -- <name>` for each of the 11 Piglets.

1. **macOS (darwin/arm64 and darwin/amd64), in Terminal.app and iTerm2**: every Binary starts and prints its version
   (`dist/bin/<name> --version`); `pig-with-batteries` starts, and `/pig-snake`, `/runner`, `/angry-pigs`, `/sprite` draw and accept
   keys (half-block art needs a UTF-8 terminal with true colour); `/doctor` runs; `/search`, `/a2a` and `/jev` report the
   expected "not enabled" or "needs a key" message when nothing is configured; `herdr` does nothing outside a herdr pane;
   `npm run test:port` (needs `PI_BIN` for the Pi re-runs) and `npm run test:go-ports`. Gatekeeper will refuse an unsigned
   Binary until it is signed or quarantine is cleared; that is expected and not a Pigpen defect.
2. **Windows (windows/amd64), in PowerShell and in Windows Terminal**: the same list; the Binary name ends in `.exe`. Extra
   things only Windows can show: the games' rendering and key handling in `conhost` and Windows Terminal, path separators and
   CRLF in the `warden` rules, `websearch`'s fetch cache and `herdr`'s socket path, and the `a2a` worker that the harness
   cannot reap there. `npm run` scripts assume a POSIX shell for some steps; run them in Git Bash or WSL if a step fails, and
   report which one.
3. **Linux arm64**: the same list as linux/amd64, on an arm64 host or a runner.

If any of them fails, the target is not shipped until it does not.

## Not verified

- Anything published: no release Binary, signature, tag, catalog or remote install command exists.
- Anything on macOS, Windows or Linux arm64 beyond `go vet` (see Build targets; the 0.3.0 candidate also compiled them).
- The recorded traces of the 5 ports without a TypeScript original, and every trace that was recorded rather than re-run: only the
  8 re-runs above compare against Pi or the upstream Go extension now.
- Zed and JetBrains themselves (only the ACP reference client was run).
- websearch's live run against real providers.
- `pigeq mutate` on macOS and Windows: it stops a mutant's leftover worker processes by a marker in their environment, which only
  exists for Linux; there a mutant that stops a2a's TERM-to-KILL escalation can leave an `a2a.test --mode rpc` behind.
- Binaries built with a PiG after 0.4.1: this run is PiG 0.4.1 (`0.4.1+1.0.3`).
- `ollama-native` against a real Ollama: its tests replay fixtures written to Ollama's documented format, and a real pig was run
  against a fake server only.

## Held

None of the accepted branches is held. The queued ports in `ports/README.md` are not part of this release. `ahp` has no
Piglet and `pig-with-batteries` does not select it.
