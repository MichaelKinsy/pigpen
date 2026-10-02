# Pigpen: first release notes (release candidate, not published)

Status of this file: the notes for the first Pigpen release candidate. Nothing here is published, signed or tagged. It states
only what was run on the `pigpen-release` branch (the release lane keeps the per-merge log outside the repository) and what
the Packages' own READMEs and `provenance.json` files say. Anything not run is listed under "Not verified".

## What Pigpen is

A repository of PiG **Packages** (installable Skills, extensions and libraries) and **Piglets** (compositions that select
Packages into a named agent). Every extension is a Go extension on the public PiG Go SDK, so a Piglet can be built into one
Piglet Binary with the extensions fused in. The ports of Pi (TypeScript) extensions carry their upstream, pinned commit,
license and credit in `provenance.json` and `CREDITS.md`; the same data is the ports list in
[`ports/README.md`](ports/README.md) (`ports/ports.json`), which `npm run check` validates.

## Install (what works today)

There is no published index, release Binary, signing key or remote install command yet
([RELEASE-BLOCKERS.md](RELEASE-BLOCKERS.md)). Use a local checkout and a PiG build that has the public Go SDK (verified with
PiG 0.3.0 + Pi 0.87.1, release-all-next `63c6ba456`).

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
| `acp` | Agent Client Protocol: the `pig-acp` companion and `/acp`; lets an editor such as Zed drive PiG | port | [svkozak/pi-acp](https://github.com/svkozak/pi-acp) `b0581c9c1d67`, MIT, Sergii Kozak; [Agent Client Protocol typescript-sdk](https://github.com/agentclientprotocol/typescript-sdk) `73bc30649b65`, Apache-2.0, Zed Industries |
| `pig-play` | Shared pig sprite, pixel rasterizer, arcade scenery, terminal plumbing (library) | moved | [MichaelKinsy/PiG](https://github.com/MichaelKinsy/PiG) `d86eb93f217e`, MIT, Michael Kinsy |
| `pig-runner` | PiG Runner, `/runner` and `/pig-runner` | moved | same as `pig-play` |
| `angry-pigs` | Angry Pigs, `/angry-pigs` | moved | same as `pig-play` |
| `pig-snake` | Pig Snake, `/pig-snake` and `/snake`: a herd forms behind the head | original game, adapted PiG code and art | [MichaelKinsy/PiG](https://github.com/MichaelKinsy/PiG) `d86eb93f217e`, MIT, Michael Kinsy |
| `session-ingest` | `read_session`: token-safe queries over Pi and PiG session transcripts | original | none |
| `context-info` | `/context`, `/tools`, `/cost`, `/prompts`, opt-in footer | original | none |
| `dev-skills` | Development Skills adapted from public sources | adapted | [mattpocock/skills](https://github.com/mattpocock/skills) `d81f3a183412`, MIT, Matt Pocock; [mitsuhiko/agent-stuff](https://github.com/mitsuhiko/agent-stuff) `0865c849befd`, Apache-2.0, Armin Ronacher (the pins are the reference copies kept in the Package; the earlier revision the adaptations were made from was not recorded, see its `CREDITS.md`) |
| `pig-doctor` | `/doctor`, read-only `pig_doctor` tool and a `pig-doctor` command line: find and fix cruft, with restorable backups | original | none |
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
| `pig-with-batteries` | herdr, the games and Pig Snake, `session-ingest`, `context-info`, `pig-doctor`, `jev`, `a2a`, `websearch`, plus an empty build fixture (`seed-check`). Source only; not the finished batteries-included composition. `pig-doctor` is selected as a command only (`tools: []`), which PiG 0.3.0 honours in interactive and RPC mode but not in print and JSON mode, where the model is still offered the read-only `pig_doctor` tool ([RELEASE-BLOCKERS.md](RELEASE-BLOCKERS.md), PiG item 9; `npm run test:tool-scope`) |

## What was verified on the release candidate

Final run on the release-candidate tip, with a temporary HOME and PiG directories, PiG 0.3.0+0.87.1 (release-all-next
`63c6ba456`), go1.27.1, Node 24.19, linux/amd64:

- `npm test`, `npm run check` (index drift, quality gates, Go-only gate, ports list), `npm run stage`, `npm run validate`,
  `npm run test:porter`, `node scripts/build-matrix.mjs`: all pass.
- `npm run test:port` (without `PI_BIN` and `PIG_UPSTREAM`): 35 pass, 0 fail, 15 skipped. It checks every port against its recorded traces and runs every port's
  mutation list (a2a: 109 mutants, all killed). Of the 15 skips, 8 are the re-runs that need real Pi (`PI_BIN`, 5) or an
  export of the upstream repository (`PIG_UPSTREAM`, 3); the other 7 do not apply: the 5 ports whose original is not
  TypeScript (3 Go relocations, 2 self-recorded) have nothing to gap-scan, and the 2 self-recorded baselines have no original
  to re-run.
- `go vet` and `go test` for all 24 Go modules (`npm run test:go-ports`), and the same tests with `-race` (GOMAXPROCS=4,
  50 packages): pass, no data race reported. `go vet` with `GOOS=windows GOARCH=amd64`, `GOOS=darwin GOARCH=arm64` and
  `GOOS=linux GOARCH=arm64` (CGO off): 0 failures in 24 modules (vet type-checks; it is not a run on those systems).
- A `pig piglet build --format binary` of every Piglet, each succeeded, and each Binary starts and reports
  `0.3.0+0.87.1`: `pig-with-batteries` (62 MB), `a2a`, `acp`, `herdr`, `jev`, `pig-extension-porter`, `pig-games`,
  `pig-porter`, `pig-typesafe`, `pig-warden` (58 MB each). All are unsigned, local builds.
- Real-terminal and interop tests on the final Binaries: Pig Snake in tmux against the `pig-with-batteries` Binary (9 of 9);
  `npm run test:pig` (herdr Piglet and `pig-with-batteries` against a fake herdr, 4 of 4); `npm run test:moved`
  (session-ingest, context-info, dev-skills in a real pig); the ACP reference client against `pig-acp` and the `acp` Binary
  (`npm run test:acp-interop`, 30 messages checked against the pinned schema).
- The Pi re-runs, on the same tip, with real Pi 0.87.1 (`@earendil-works/pi-coding-agent` 0.87.1 from npm, `PI_BIN`) and the
  originals built in place (`npm install --ignore-scripts && npm run build` for websearch, warden and pi-typesafe): `npm run test:port`
  with `PI_BIN` and `PIG_UPSTREAM` (a `git archive` of PiG `piglets/standard` at `d86eb93`) is **43 pass, 0 fail, 7 skipped**. The 8 re-runs
  that had been skipped all ran and pass: the original under Pi still produces the recorded traces for dirty-repo-guard, jev,
  pi-typesafe, warden and websearch, and the upstream Go extensions still produce them for angry-pigs, pig-login and pig-runner.
  The 7 remaining skips are the structural ones above. The first attempt failed only in the static `exec-coverage` check of the
  three larger originals (a development launcher, SQL statements handed to `node:sqlite`, and websearch features the first slice
  defers: GitHub clone, video frames, browser cookies, curl through a proxy); each command is now excused by name, with its reason, in
  that port's `port/accepted-gaps.json` under `exec:<name>`, and `pigeq` lists it in the result.
- The kagent interop of `a2a` (`node scripts/interop-kagent.mjs`, kagent `e4516302`): the four `TestInterop_*` tests pass.
- Every pinned upstream commit in the ports list exists at its upstream (`node scripts/ports.mjs verify-pins`, 21 pins, run
  with network access).
- Re-run independently at the whole-release review, on a fresh `git clone` of the candidate with a temporary HOME, PIG_HOME
  and agent directory (never the user's): the install commands above exactly as written (`pig install` of three game
  Packages, which then register their commands; `npm ci --ignore-scripts && npm run stage`; `pig --piglet` for each of the 10
  staged Piglets; `pig piglet build` of all 10, each Binary reporting `0.3.0+0.87.1`, listing its commands over RPC and
  offering its tools to a scripted local model), the gates above with the same counts (`test:port` 35/0/15, `test:go-ports` 24 modules and `-race` 50 packages,
  cross-target vet 0 failures), `test:pig-snake` 9/9 against the rebuilt batteries Binary, `test:pig` 4/4, `test:moved`,
  `test:acp-interop` (30 messages) and `verify-pins` (21). `npm run test:tool-scope` (added by that review) passes for RPC
  and records the print and JSON mode defect named under Piglets.
- Also run once at the merge that brought each in: the websearch twin ledger (`pigeq twins check`: 305 exact twins, 570 named
  skips for the deferred parts).

## Build targets

Every Piglet manifest declares `build.targets: [linux/amd64, linux/arm64, darwin/arm64, darwin/amd64, windows/amd64]`, and
`node scripts/build-matrix.mjs` emits one native runner per target (`ubuntu-24.04`, `ubuntu-24.04-arm`, `macos-15`,
`macos-15-intel`, `windows-2025`). PiG's native Binary builder builds only the machine it runs on, so a local build names
that machine: `npm run build:piglet -- <name>` does it, and a direct `pig piglet build` needs `--targets <os>/<arch>`.

What is proven for each target:

- **linux/amd64**: built, started and run (everything under "What was verified").
- **the other four**: every Piglet was **compiled and linked** for each of them (40 of 40 built) with a private, uncommitted
  patch of the reviewed pig that skips its check of running the built artifact (the host cannot run them) and
  `GOOS`/`GOARCH` set. That proves the code builds for those targets; it proves nothing about how it runs. Those Binaries are
  unsigned, their records say linux/amd64, and none was started (no arm64 emulation on the build host; no macOS or Windows
  host). `go vet` for windows/amd64, darwin/arm64 and linux/arm64 type-checks all 24 Go modules.

Binary sizes in MB (linux/amd64 from the real builds; the rest are the compile-only builds above):

| Piglet | linux/amd64 | linux/arm64 | darwin/arm64 | darwin/amd64 | windows/amd64 |
|---|---|---|---|---|---|
| `a2a` | 58.9 | 55.5 | 56.9 | 59.8 | 59.8 |
| `acp` | 57.6 | 54.4 | 55.7 | 58.5 | 58.5 |
| `herdr` | 57.7 | 54.5 | 55.7 | 58.6 | 58.5 |
| `jev` | 58.1 | 54.9 | 56.1 | 59.0 | 59.0 |
| `pig-extension-porter` | 58.1 | 54.7 | 56.1 | 59.0 | 58.9 |
| `pig-games` | 57.9 | 54.6 | 55.9 | 58.8 | 58.7 |
| `pig-porter` | 58.1 | 54.7 | 56.1 | 59.0 | 58.9 |
| `pig-typesafe` | 58.2 | 54.9 | 56.2 | 59.1 | 59.1 |
| `pig-warden` | 58.3 | 55.1 | 56.4 | 59.3 | 59.2 |
| `pig-with-batteries` | 62.1 | 58.5 | 59.9 | 63.1 | 63.0 |

Release Binaries per target need the protected release workflow that does not exist yet ([RELEASE-BLOCKERS.md](RELEASE-BLOCKERS.md)).

### What still has to be run on macOS, Windows and Linux arm64

Nothing below has been run. On each machine build natively, with `PIG_SOURCE_ROOT` a git checkout of the PiG source that the
`pig` was built from: `npm ci --ignore-scripts && npm run build:piglet -- <name>` for each of the 10 Piglets.

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
- Anything on macOS, Windows or Linux arm64 beyond compiling (see Build targets).
- The recorded traces of the 5 ports without a TypeScript original, and every trace that was recorded rather than re-run: only the
  8 re-runs above compare against Pi or the upstream Go extension now.
- Zed and JetBrains themselves (only the ACP reference client was run).
- websearch's live run against real providers.
- `pigeq mutate` on macOS and Windows: it stops a mutant's leftover worker processes by a marker in their environment, which only
  exists for Linux; there a mutant that stops a2a's TERM-to-KILL escalation can leave an `a2a.test --mode rpc` behind.
- Rebuild against PiG 0.4.0 (MCP built in): Pigpen ships alongside it, and every Binary here was built with PiG 0.3.0+0.87.1.

## Held

None of the accepted branches is held. The queued ports in `ports/README.md` are not part of this release. `ahp` has no
Piglet and `pig-with-batteries` does not select it.
