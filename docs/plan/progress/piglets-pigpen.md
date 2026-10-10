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
