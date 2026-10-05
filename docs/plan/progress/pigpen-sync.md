# pigpen-sync: Pigpen's public main made current and pinned to PiG 0.4.0

Status: READY, reviewed (accepted with fixes, listed under "Review" below). Base: the porter re-verification plus its
review fixes. The public branch (one commit on `origin/main`) is described at the end.

## What changed vs public main

Public `main` (`55748b1`, 26 files) is the scaffold: the `pig-with-batteries` Piglet with an empty
`seed-check` extension, the index generator, the manifest validator and a validation-only workflow. This tree is the
real repository:

- **10 Piglets** (`a2a`, `acp`, `herdr`, `jev`, `pig-extension-porter`, `pig-games`, `pig-porter`, `pig-typesafe`,
  `pig-warden`, `pig-with-batteries`), each Go only, each building into a Piglet Binary.
- **21 component Packages** under `components/`: the herdr reporter, the pig games (`pig-play`, `pig-runner`,
  `angry-pigs`, `pig-snake`), `pig-doctor`, `session-ingest`, `context-info`, `dev-skills`, the ports (`a2a`, `acp`, `ahp`,
  `dirty-repo-guard`, `jev`, `typesafe`, `pi-typesafe`, `pi-typesafe-api`, `warden`, `websearch`) and the porting
  workflow (`extension-port` Skill and the `extension-equivalence` harness, `pigeq`). Every port keeps its upstream, pinned
  commit, license and credit (`provenance.json`, `CREDITS.md`, `ports/ports.json`).
- **Scripts and gates**: quality gates, Go-only gate, ports list, staging (`npm run stage`), Piglet and `pigeq` builds,
  the equivalence test (`test:port`), the real-pig tests (porter, tool scope, moved extensions, tmux games, ACP interop).
- **Docs**: README, RELEASE-NOTES, RELEASE-BLOCKERS, CONTRIBUTING, the roadmap and the progress records.

### This change (on top of the base)

1. **PiG 0.4.0 pin.** Every Go module requires the published `github.com/MichaelKinsy/PiG/extensions/sdk v0.4.0` (was
   `v0.0.0`; `go.sum` committed, so a module without a library dependency vets with plain `go`; PiG still substitutes its
   staged SDK in `pig` builds and in the scripts' `go.work`). `scripts/pig-requirement.json` is `0.4.0`. The CI validator
   is built from PiG's `v0.4.0` commit `76022638c15e2f68f1cee9c6b8984eee1baba620` (was `35280def`). Version text in the
   README, RELEASE-NOTES, the Skill, roadmap and component READMEs now says 0.4.0 (Pi 1.0.0); notes about PiG 0.4.1
   behavior are kept and marked.
2. **CI runs more.** `test:porter`, `test:tool-scope`, `test:moved` and `test:go-ports` join `npm run validate`
   (timeout 45 minutes). Not run on GitHub yet.
3. **D89 tests need a 0.4.1 SDK.** Tool renderers (`Extension.ToolRenderer`) are not in the `v0.4.0` SDK, so the fake-host
   self-tests for them moved to `fakehost_toolrenderer_selftest_test.go` behind the `pigsdk_tool_renderer` build tag;
   `scripts/go-modules.mjs` (`sdkBuildTags`) sets it only when the selected SDK has the method (checked against the
   0.4.1 content: both tests run and pass there, and are not compiled on 0.4.0). Unit-tested.
4. **`npm run test:go` was broken**: its `go.work` said `go 1.26` while a2a requires `go >= 1.26.0`. It now uses the shared
   `goWorkText`.
5. **Release blockers rewritten** for PiG 0.4.0 with the decisions and owner actions (below); a release-workflow draft
   (`docs/plan/release-workflow.draft.yml`, inert).
6. **herdr docs** say the SDK has the `pi.events` bridge and the reporter does not use it yet.
7. **Public hygiene**: internal branch names removed from the documents (port records, roadmap, progress records,
   CONTRIBUTING, RELEASE-BLOCKERS); `npm run quality` now refuses a Markdown file that names a `team/...` or `staging/...`
   working branch; a guard regex no longer spells a private path.

## Test evidence

pig: `0.4.0+1.0.0` (the release build, sha256 `4e08c2b3...600ddae`, and a build from the `v0.4.0` source commit that prints
the same version), Pi 1.0.0 (`@earendil-works/pi-coding-agent@1.0.0`), Go 1.27.1, Node 24.19.0, linux/amd64. Every run
used a temporary HOME, PIG_HOME and agent directory; `PIG_SOURCE_ROOT` was a checkout of the `v0.4.0` source.

| Check | Result |
|---|---|
| `npm test` | 156/156 (after the performance pass and the matrix) |
| `npm run check` | pass (index in sync, quality gates, Go-only, ports list: 24 ports) |
| `npm run validate` | pass: every Package, every staged Piglet, `pig install <dir> --validate-only --json` for 17 extension directories (all `valid: true`) |
| `npm run test:porter` | 2/2 |
| `npm run test:tool-scope` | 4/4 (RPC, print, JSON, and the control without the scope) |
| `npm run test:moved` | 1/1 |
| `npm run test:go-ports` | exit 0 (23 modules vetted and tested; the 11 ACP end-to-end scenarios run, none skipped) |
| `npm run test:go` | exit 0 |
| Harness tests (`extension-equivalence`, `pigeq`) | pass, in `test:go-ports`; the tagged D89 tests pass on the 0.4.1 SDK |
| `npm run build:piglet` | all 10 Piglets build on linux/amd64 (twice: before and after `go.sum`); each Binary reports `0.4.0+1.0.0`, starts in RPC mode and lists its commands |
| Declared targets | `go vet` for linux/arm64, darwin/arm64, darwin/amd64, windows/amd64: 23/23 modules clean (a type-check; native builder builds only the host) |
| `npm run test:pig` / `test:pig-snake` | 4/4, 9/9 (rebuilt Binaries) |
| `npm run test:acp-interop` | all checks pass (30 messages against schema 0.26.0) |
| `npm run test:doctor-mutations` | all mutations caught |
| `npm run test:port`, Pi 1.0.0 and `PIG_UPSTREAM` | **39 pass, 0 fail, 6 skipped** (structural: the 4 ports whose original is not TypeScript, 2 Go relocations and 2 self-recorded, have nothing to gap-scan, and the 2 self baselines have no original to re-run). Live re-runs of dirty-repo-guard, jev, pi-typesafe, warden, websearch under Pi and of angry-pigs, pig-runner against upstream Go all pass |
| `pigeq gaps --surface` on PiG's own tables | the D89 fixture: 1 non-blocking stand-in on the 0.4.1 table; blocked on the 0.4.0 table (`pi.registerToolRenderer` has no row), which is the correct reading of the 0.4.0 SDK |
| Source-add re-check on 0.4.0 | authored `piglets/herdr`: refused (closure path); committed staged tree: `herdr`, `pig-with-batteries` added; `pig-porter` (`extends`): refused |
| Publisher, stub `gh`, throwaway key (nothing uploaded) | dry run of all 10 Piglets: tag `<name>/v0.1.0`, pull `github:<owner>/<repo>/<name>@0.1.0`; a wrong `--tag-prefix` is refused; signed build then `publish --yes --artifacts` for one target produced a signed `piglet-release.json` and `SHA256SUMS` |
| Release workflow draft | parses as YAML; its `plan` script run with a good tag and three bad ones (wrong version, bare `vX.Y.Z`, unknown name) |
| CI rehearsal | the workflow's steps from a fresh clone of the committed branch, with the pig built from the `v0.4.0` source commit and empty Go caches: `npm ci`, `npm test` (119), `npm run check`, `build-matrix`, `validate`, `test:porter` (2), `test:tool-scope` (4), `test:moved` (1), `test:go-ports` (50 packages ok, 133 s): all pass. Not a hosted run |

Not run: anything on macOS, Windows or Linux arm64 beyond `go vet`; GitHub-hosted CI; `scripts/interop-kagent.mjs` (needs a
kagent checkout); `ports.mjs verify-pins` (needs network to every upstream); the websearch twin ledger.

## Release blockers and owner actions

Full text in [RELEASE-BLOCKERS.md](../../../RELEASE-BLOCKERS.md). In short, resolved by PiG 0.4.0: a public reviewed revision,
per-Piglet tags and namespace updates, print and JSON tool scope, the Go `pi.events` bridge, publisher receipts (the signed
`piglet-release.json`; its fields are listed there). Still blocking a first release:

- **Decisions** (Pigpen): Binary-only first release (recommended; source add is refused for authored manifests and for
  `extends` Piglets on 0.4.0); the index and website contract for `available` entries (the schema fixes `planned`); a2a
  vendoring or the Go proxy.
- **Owner actions**: signing keys per Piglet (`pig piglet keygen`) kept as environment secrets; GitHub environments
  `piglet-<name>` with a required reviewer; a `*/v*` tag ruleset; Actions allowed to create releases and attestations;
  public keys committed under `release-keys/` and pinned by the index generator; a throwaway repository and `gh` login for the
  end-to-end publication run; an npm organization only if Packages are to be installed as `npm:` references (not needed for a
  Binary-only release).
- **Pigpen follow-ups**: adopt and run the release workflow draft (remove the guard with it); extend `generate-index.mjs` to
  verify receipts against the pinned keys; use `Extension.Events()` in the herdr reporter and un-skip its two tests; run the
  equivalence and Binary checks on macOS, Windows and Linux arm64.
- **Inputs for PiG**: `extends` limits (add, `agentEnv`, non-local base), the implicit `go.work` contract for shared library
  Packages, and a `pig extension init` scaffold that writes the published SDK version instead of `v0.0.0`.

## Findings

- `pig extension init` on PiG 0.4.0 still writes `require ...sdk v0.0.0`; the published `v0.4.0` works with both pig builds
  and a plain `go build`.
- The `v0.4.0` SDK has no tool renderers; a port that needs `pi.registerToolRenderer` has to wait for a PiG that has them.
- `pigeq mutate` runs of the a2a port leave `a2a.test` workers behind when the run is killed from outside (seen when a `test:port` run was stopped). The review's complete `test:port` run also left one fake worker from
  `TestWorkerEscalatesToKillWhenTermIsIgnored` (environment `PIG_A2A_WORKER=1`, no harness marker) running after it
  finished, so a finished run is not always clean either; kill leftover `a2a.test --mode rpc` processes after a run.

## CI fixes (first hosted run of the PR)

The first hosted run of `validate` failed two tests, and running the workflow's steps again from a clean clone found a third.
Each was reproduced first, fixed at its root (no retry, longer timeout or skip), and had a red test before the fix.

1. **ahp `TestPiClientActionPolicy/rejects_capabilities_pi_does_not_implement`** ("state must not change"). The host attaches
   a session's backend on its own goroutine, so a new session is `creating` and then `ready`. The fixture snapshotted the
   session before that transition and compared after the rejected action, so the transition read as a change. Upstream's
   harness is ready synchronously in storage-only mode; the Go host is not. `createChannels` now waits for the host's
   `session/ready` action (or a subscribe snapshot that already says ready). Reproduced at `-cpu 1` (6 of 50). Red:
   `TestActionsFixtureHandsBackReadySessions` (200 sessions; fails at `-cpu 1` and 4). After: `-count=50` at `-cpu 1,2,4`, plain
   and `-race`, and the whole ahp module at `-count=20`: pass.
2. **pig-runner `TestFakeClassifierPlaysTheRunnerOverMCP`** ("only 1 obstacles came by in 9s"). Not slowness: it failed 2 of 3 at
   `-cpu 1` on an idle machine, and the game was frozen. `game_act` queues at most 64 actions for the next frame and drops the
   oldest when full; a player faster than the 60 frames a second (this one polls at about 4000 a second) overflows it and drops
   the action that answers the run's decision hold, so the run stays held with `decide_now` false for good. A real classifier
   answers in about 0.7 s and never overflows the queue, but the bug is real. The queue now never drops the answering action (key and
   release). Red: `TestAnswerToTheHoldSurvivesAFullQueue` (a first fix that kept only the release still lost the jump, and the pig
   crashed; the test asserts the jump too). The integration test no longer measures wall time: it plays until three obstacles
   have been passed (game progress), and fails at once on a crash (before, a crash was answered with `restart` and only the final
   state was checked). After: `-count=50` at `-cpu 1,2,4`, and `-race -count=20` at `-cpu 1,4`: pass.
3. **acp `TestE2EWriteToolCallEmitsDiff`** (found by the rehearsal; a known flake from the earlier 0.4.0 fixes). pig announces a
   tool and runs it without waiting for the adapter, and the adapter reads the file when it sees the announcement: under load the
   write lands first, the old and new text are equal and no diff is emitted. Failed 22 of 40 runs of the module's `go test ./...`
   on four CPUs. The adapter cannot be made to win: in this pig the tool call's events arrive only when the stream ends, just
   before the tool runs (an adapter-side earlier read was tried and does not help; it is not kept). The test now uses a gate
   extension (`testdata/gate`): pig runs a tool's `tool_call` hook after the announcement and before the tool, so a hook that
   blocks the write until the test has seen the adapter report the call in progress fixes the order. 0 failures in 40 runs of the
   same load. Not changed: the adapter's behavior under load for a real client (a diff can be missing when the write beats the
   adapter; a hook in pig, such as waiting for the client before running a tool, would be the real fix and is a PiG input).

CI rehearsal: `ci.yml`'s own `run` steps, executed verbatim from a clean clone of the commit, with the pig built from the `v0.4.0`
source commit and Go 1.27.1 (the `toolchain` line of PiG's `go.mod`, which `setup-go` honours), a clean environment, empty Go caches
and four CPUs. Before the fixes it failed at the `go test` step on the acp test (rehearsal of `6dfad38`). At `133f7a6` it
passed three times, once plain, once again and once with two busy-loop burners on the same four CPUs: `npm ci --ignore-scripts`;
`npm test` 119/119; `npm run check`; `node scripts/build-matrix.mjs`; then the workflow's build step: PiG built with
`go -C .pig-src build -trimpath`, `npm run validate` (17 extensions valid), `test:porter` 2/2, `test:tool-scope` 4/4,
`test:moved` 1/1, `test:go-ports` (23 modules, 50 packages ok, 0 failures). About six minutes. The three runs and the failing
one are not hosted runs: only a GitHub run can show that the workflow itself, the runner's images and the network behave.
After merging the performance pass (`8a95fb12`) the same rehearsal passed again: `npm test` 122/122, `test:porter` 2/2,
`test:tool-scope` 4/4, `test:moved` 1/1, `test:go-ports` 50 packages ok, 0 failures, about six minutes.

## AHP in Visual Studio Code

Owner addition before PR #1 merges (`components/ahp`). VS Code's Agents window connects to an arbitrary WebSocket AHP host
(`Agents: Add Remote Agent Host...` or `chat.remoteAgentHosts`), with the token as `?tkn=`.

- **Tests** (`internal/ws/vscode_test.go`): VS Code's own first message, field for field as read from VS Code's source
  (`agentHostProtocolClient.ts`, `connect`: id 1, the root channel, protocol versions `0.10.0, 0.9.0, 0.7.0, 0.6.0, 0.5.2, 0.5.1`,
  a UUID client id, the Agents window's `clientInfo`, the direct-WebSocket `_meta`, the root as the initial subscription), over a
  real WebSocket, negotiates `0.9.0` and returns the root snapshot; then the `root/configChanged` notifications VS Code sends next
  and its keep-alive `ping` get a text reply on the same connection. VS Code 1.140.0's offer (`0.9.0` first, no `0.10.0`) also
  negotiates `0.9.0`. Every frame the host sends is a text frame (VS Code drops other frames and closes with 4002 after ten); the
  address form without a slash before the query (`ws://127.0.0.1:<port>?tkn=`) is accepted; a wrong, empty, missing or
  mis-spelled token is refused at the upgrade with 403; the provider id is non-empty and dash-free. These pin behaviour the
  listener already had, except the status code (Review 2).
- **Notice** (red tests first, `TestListenNotice*`): `/ahp start`, `/ahp status` and `--ahp` print `ws://<host>:<port>?tkn=<token>`
  first, then the generic `/?token=` address; the token is query-escaped. Two tests run VS Code's own expressions over the notice
  (its SSH launcher's URL pattern and the Add Remote Agent Host parser). A real pig (`PIGPEN_AHP_REAL=1`) shows the notice, and
  `pig --mode rpc --ahp` prints it on standard output at once.
- **README** (`components/ahp/README.md`): "Visual Studio Code" (steps, settings, SSH, port forward, dev tunnel, debug logging, the
  limits that apply to this port) and "Other AHP clients", checked against VS Code 1.140.0 and main.
- **Not verified with a live VS Code.** Everything above comes from VS Code's source and from Pigpen's own tests.
- **Status code for a bad token: 403**, as the request said. VS Code's agent host, which the AHP docs name the reference server,
  answers a missing or wrong `tkn` with 403 (`src/vs/platform/agentHost/node/webSocketTransport.ts`); the AHP spec leaves it to
  the transport; VS Code's client cannot tell 401 from 403. pi-ahp writes 401, so this is deviation 15 in the port record.

## Platform build matrix

Owner addition before PR #1 merges. `.github/workflows/ci.yml` is now tiered (README, "Platform build matrix"):

- `validate` (every run) keeps today's checks and adds ONE linux/amd64 build and smoke of `pig-with-batteries`.
- `platform-matrix` (push to main, merge group, manual, weekly schedule, or a PR labelled `full-ci`) is one job per OS on a native
  runner. Each downloads the pinned PiG release for its OS (`scripts/fetch-pig.mjs`; the pin and each archive's SHA-256 are in
  `scripts/pig-requirement.json`, checked against a download of every archive), builds all ten Piglets for its own target
  (`scripts/platform-smoke.mjs`), smokes every Binary and uploads them for three days.
- `android-cross` compiles and vets all 23 modules for android/arm64 with cgo off (`scripts/cross-check.mjs`). Every module is
  cgo-free (only the standard library's optional cgo in `net` and `runtime/cgo`, which `CGO_ENABLED=0` drops). It is a compile
  check, not a Binary: PiG's builder is host-only.
- `ci-result` is the single required check; it is green when a tier is skipped and red when one that ran failed. Concurrency
  cancels a superseded PR run only. Any label re-runs the PR's checks (see Review 2).
- Hardening is a gate in `npm run quality` (`scripts/check-workflows.mjs`, like PiG's `make compliance`): every action pinned to a
  commit SHA, a read-only token, no secrets, no `pull_request_target`, no persisted checkout token, a timeout on every job, one PiG
  pin (the workflow's `PIG_COMMIT` equals the requirement file), a native runner for every target the manifests build. No test
  re-reads the workflow's tiers or triggers: a hosted run is their test.

Deviations from the request, and why: `macos-15-intel` instead of the retired `macos-13`; pinned `ubuntu-24.04` instead of
`ubuntu-latest` (as `scripts/build-matrix.mjs` already had); the faux turn is `-p "reply with exactly: ok"` because the test model
answers only scripted prompts and exits 1 on `hi`; the smoke asserts exit 0, the answer, and no `Failed to load extension` (the
message pig prints when an extension does not load, checked against a real broken extension).

Evidence (local, linux/amd64): `npm test` 159/159 (156 after Review 2 replaced the workflow test with a gate), `npm run check`; the validate job from a clean clone passed, with the
pig-with-batteries smoke; the matrix job's steps run the way the workflow runs them (fetch the release pig, verify, build and smoke
all ten Piglets, 52 s); the android cross-check (32 s); `ci-result`'s script over six outcome combinations. **Not run: darwin,
windows and linux/arm64, and no hosted run.** The first run is the lead's `workflow_dispatch` on the PR branch; macOS and Windows may
need a script fix (path handling in staging or building) that only a native run can show. Artifacts are about 62 MiB per Piglet,
about 620 MiB per OS, kept three days.

Found on the way: `go build ./...` over a module with one `main` package writes the executable into the module directory. The
first version of the android check left two ELF files in the tree (the quality gate caught them; they were never in the final
history). The check now builds with `-o` to the null device, with a test.

## Performance pass

Merged from the reviewed performance branch (full record, tables and method in
[pigpen-perf.md](pigpen-perf.md)). All 28 binaries (the ten Piglets and each component extension alone, against a no-op control) were
measured for startup, idle and memory on PiG 0.4.0, and the hot paths were profiled and benchmarked.

- **Pigpen's own code costs almost nothing at startup or idle.** Each extension adds 0 to 3 ms over the control (batteries, with
  fourteen, adds 62 ms), no socket, child process, file watcher, timer or ticker goroutine runs at idle, and idle CPU matches plain
  pig. The larger costs are PiG's (the host encodes the whole model catalog for any extension, about +115 ms in print mode, and
  pushes a state snapshot per delivered event); they are listed in the record as inputs for PiG.
- **Ten per-call or per-start costs fixed in Pigpen's code**, each with a failing test or allocation bound first: warden compiled
  about 100 regexps at start (init 3.1 to 1.4 ms) and more per tool call and per path, jev kept the whole tool input as its memo key,
  websearch's HTML to Markdown was quadratic (5.6 to 1.3 ms on a 40-section page), the typesafe client re-encoded and built log
  arguments for a dropped level, the a2a Agent Card was rebuilt per request, session-ingest copied each line, and the equivalence
  harness re-parsed the SDK table per call (1.73 to 0.44 ms).
- **Review fixes included**: two compiled test binaries that had been committed were removed and the quality gate now refuses any
  executable in the tree; the `-race` allocation bound in websearch; a jev mutation that still named the old memo key; the
  measurement harness no longer writes the user's `~/.pig` and no longer removes a run's home while pig is writing it.
- A merge of the branch leaves `npm test` at 122 and `test:go-ports` at 50 packages; the CI rehearsal passes with it (next paragraph of "CI fixes").

## Review

An adversarial review re-ran the checks with the `0.4.0+1.0.0` pig and a pig built from the `v0.4.0` commit's source
(`npm test`, `npm run check`, `validate`, `test:porter` 2/2, `test:tool-scope` 4/4, `test:moved` 1/1, `test:go-ports`,
`test:go`, `test:pig` 4/4, `test:pig-snake` 9/9, `test:acp-interop` (30 messages), `test:port` with Pi 1.0.0 and
`PIG_UPSTREAM` (39 pass, 0 fail, 6 structural skips once the three originals' dependencies are installed), all 10 Piglets
built and started in RPC mode, `go vet` for the five declared targets on all 23 modules, `go test -race -count=3` for
herdr and the equivalence harness, the tagged D89 tests on the 0.4.1 SDK, the go.sum hashes against the Go proxy) and
fixed:

- **Release workflow draft**: the publish job downloaded the Binaries into `dist/`, where `npm run stage` also writes
  `dist/staged`, so `pig piglet publish --artifacts dist` refused the directory ("contains staged, which is not a regular
  file"). They now go to `release-artifacts/`. The signing key and the release token now reach only the signing and
  publishing steps (PiG is built and the Piglet staged in a step without secrets), and runs per tag are serialized. The
  build and publish steps were run as written with a stub `gh` and a throwaway key.
- **Owner action**: the signing key must be an environment secret only, with each environment's deployments limited to its
  tags: GitHub creates a missing environment without protection on first use.
- **Internal branch names** remained in published documents (port records, the roadmap, CONTRIBUTING, RELEASE-BLOCKERS,
  the Skill and its credits); replaced by public revisions, with a quality gate against `team/...` and `staging/...`.
- **Stale text**: CONTRIBUTING still said CI does not run `test:porter`; RELEASE-BLOCKERS called ACP's one-command Binary an
  input for PiG 0.4.0 (it is not in 0.4.0) and labelled the warden `GetBranch` defect without saying it is still in `v0.4.0`
  (re-checked); the pig sha256 above was wrong.
- **Multi-tag `go test` flags**: `scripts/test-go.mjs` passed one `-tags=` flag per tag, of which Go keeps only the last; it
  now joins them with commas, as `go-modules.mjs` does (harmless with the one tag today).

Left as owner decisions: the ports list publishes its `lane` field and a few review-lane names appear in test comments and
port records (they reveal no host, path or company); and some historical non-public PiG commit ids remain as evidence.
The public commit has to be rebuilt from the merged tree (the earlier one is stale).
After the merge: `npm test` 119/119, `npm run check`, `validate`, `test:go`, `test:porter`, `test:tool-scope`, `test:moved`,
`test:go-ports`, `test:pig` 4/4 and `test:pig-snake` 9/9 pass again, and a tree scan for paths, host names and
working-branch names finds nothing.

## Review 2 (platform matrix and AHP in VS Code)

A second adversarial review of the matrix and VS Code commits, against VS Code's source (main at `32a12c70`, the `1.140.0` tag and
`release/1.141`) and the AHP repository. Re-run: `npm test` (156), `npm run check`, `npm run validate`, the release pig fetched and
its five pinned SHA-256 values compared with the release's `SHA256SUMS`, `platform-smoke` for all ten Piglets on linux/amd64 (53 s,
all pass; a Piglet whose extension panics at load fails the faux turn), the android cross-check (23 modules), the action SHAs
against their tags, and the ahp module's tests with `-race -count=3` (with the real pig). Fixed:

- **A label could turn `CI result` green.** Any label other than `full-ci` started a run whose tiers were all skipped and whose
  `CI result` passed at once: a fresh green check of the same name on the same commit, after a red one. Every `pull_request`
  event now runs the checks its labels ask for, in the pull request's concurrency group.
- **A test re-read the workflow.** `scripts/ci-workflow.test.mjs` restated `ci.yml`'s tiers, triggers and aggregate script. It is
  replaced by a hardening gate in `npm run quality` (`scripts/check-workflows.mjs`, PiG's `make compliance` checks plus the PiG pin
  and target drift checks), tested on small workflows of its own.
- **VS Code's SSH launcher would have found no token.** It takes the first `ws://127.0.0.1:<port>(?tkn=...)` in the output; the
  notice led with the `/?token=` form, and the VS Code form ended in `)`, which the launcher's token pattern keeps. The notice now
  leads with the `?tkn=` form.
- **401 became 403** for a missing or wrong token (above).
- **The handshake test was a guess**; it now sends VS Code's real first messages. The README's protocol line was right only for
  builds after 1.140, the dev-tunnel line claimed it works, and the command's window and the logging setting were vague.

## Consolidation (every open contribution into PR #1)

The owner asked for every open Pigpen contribution to go into PR #1, with the contributors credited. The lead adds a
`Co-authored-by` line for each in the squash. Contributors:

| Contributor | Email | Work | Item |
| --- | --- | --- | --- |
| Peder Munksgaard (@peder1981) | peder1981@gmail.com | `ollama-native` (closed pull request 3) | 1 |
| Yu Li (@liyu1981) | liyu1981@gmail.com | `herdr-agent-state` (pull request 2): failure diagnostics in `components/herdr` | 2 |

The email is the author of the pull request's commit (`2d22e3c0`). The brief guessed `peder@munksgaard.me`; the commit
does not carry it.

### Item 1: `components/ollama-native`

A rewrite of Peder's extension for Pigpen's rules, keeping his design (native `/api/chat`, models from `/api/tags`,
`ollama-native/<model>`, `OLLAMA_HOST`). `CREDITS.md`, `provenance.json` (authors) and the `LICENSE` (a second copyright
line) name him. It is a selectable Package, in no Piglet.

What changed from pull request 3, and why:

- **No network at start-up.** His extension called `/api/tags` inside the factory. Now it is a native SDK `Provider`
  whose catalog is empty until PiG calls `RefreshModels`. The offline pass restores the catalog the last network refresh
  stored (the host hands it back as `Stored`), so a one-shot `pig -p --model ollama-native/x` works with Ollama not asked.
  `/ollama refresh` refreshes on request. Test: `TestConstructionAndGetModelsMakeNoRequests`,
  `TestOfflinePassRestoresTheStoredCatalogWithoutRequests`.
- **Tool calls.** His code kept only the first call and dropped a call that had no id, and Ollama sends none. Now every call
  is kept and gets an id (a server id is kept). Test: `TestStreamToolCalls`.
- **Conversation.** His code sent only text, with no tool results and no system prompt. Now it sends the system message
  (instructions and sections, merged as PiG merges them), assistant tool calls, `tool` results with `tool_name`, and
  images. Tools come from the transcript's `toolsAdded`/`toolsRemoved`. Test: `TestChatRequestMapsTheTranscript`.
- **Timeouts.** His 120 s whole-request cap is gone (a cold model load or a long answer exceeds it); connecting is
  bounded to 5 s and cancelling a turn cancels the request. Test: `TestStreamAbort`.
- **Errors**: not running (names `ollama serve` and `OLLAMA_HOST`), model not installed (`ollama pull`), an error line
  in the middle of a stream (the text so far is kept), a truncated stream. Tests: one each.
- **Capabilities** (context window, `thinking`, `vision`, embedding-only models left out) come from `/api/show`, with
  defaults when it fails. `think` is sent only to reasoning models.
- `OLLAMA_API_KEY` adds a bearer header for a protected server; no key is sent otherwise.

Evidence: 22 tests in the module (`go test -race`), run by `npm run test:go-ports` (51 packages now). Real pig 0.4.0 against
a fake Ollama (`pig -e` on the source): refresh then `--list-models` in a later process (no request), a plain turn, and a
tool-call round trip (two calls, results sent back, final answer). `pig package validate` passes.

Limits to know:

- **Not run against a real Ollama.** None is installed here. The fixtures follow Ollama's documented wire format; they are
  not recordings. The owner's first run with a real model is the check.
- **Section order.** PiG 0.4.0's SDK gives a native provider the system prompt's sections as an unordered map, so they are
  sent in name order. Nothing is lost, but the order differs from PiG's own. A PiG input: pass the rendered system prompt
  (or ordered sections) to native providers.
- **First catalog.** PiG refreshes provider catalogs when an interactive session starts, not in print mode, so a fresh
  install needs `/ollama refresh` (or one interactive session) before `pig -p --model ollama-native/x` finds a model.

### Item 2: herdr pull request 2 (`herdr-agent-state`, Yu Li)

Compared feature by feature with `components/herdr`. One reporter stays; there is no second herdr extension. The pull
request adds `piglets/pig-with-batteries/extensions/herdr-agent-state` (Go, direct socket client, Windows named pipe).

| Feature | `components/herdr` | Pull request 2 | Result |
| --- | --- | --- | --- |
| `source` / `agent` | `custom:pig` / `pig`, no `herdr:` prefix (herdr reserves it) | `piglet:herdr-agent-state` / `pig`, no `herdr:` prefix; checked against a live herdr 0.9.3 (`agent: pig`) | Kept ours. Both avoid the reserved prefix and `pi`. The pull request's value was seen working on a real herdr; ours follows herdr's documented CLI contract but has not been seen on a live herdr here. A live check of `custom:pig` is a follow-up |
| Session reference | `agent_session_path`, else id; absolute paths only | the same | Same |
| idle / working | `agent_start` / `agent_settled`, mode `tui` only, `HERDR_ENV` gate | the same | Same |
| `blocked` | Reported while a PiG UI prompt (select, confirm, input, editor, custom) is open | Not reported: the pull request says PiG has no blocked state | Kept ours. PiG's UI-prompt events give a real blocked state |
| Resume command | Yes, with the herdr < 0.9.2 fallback | No | Kept ours |
| Release on quit | Yes, and silent on reload so a successor is not cleared | No | Kept ours |
| `seq` | Clock-seeded, monotonic per process | Clock-seeded, monotonic | Same |
| Transport | `$HERDR_BIN_PATH pane report-agent` (the documented CLI), 3 s bound, latest state wins | direct socket, one retry, queue drained at shutdown | Kept ours; the CLI is herdr's published contract and handles Windows itself |
| Windows | Through the CLI | Named pipe dial, compiled but not run | Kept ours; no dial code to maintain |
| Refused report | Dropped silently | Written to stderr, naming the cause | **Taken** |

Taken from pull request 2: the failure diagnostic. A call herdr does not take writes one line to stderr naming the cause
(`herdr: pane report-agent failed: ...`), once per distinct failure until a call succeeds. Tests:
`diagnostics_test.go` (a refused report, a repeat written once, a missing binary, and silence for a healthy herdr and for an
old herdr that refuses only the resume command). `components/herdr` module: `go test -race` passes.

Not taken (follow-ups, not release blockers): the direct-socket transport (one process spawn per report is the cost we
pay for using the documented CLI; `bench_test.go` measures it), the queue drain at shutdown, and a live run on herdr 0.9.3
to confirm `custom:pig` shows as agent `pig` in `herdr pane get`.

### Item 3: the stacked lanes (port-popular, pig-music M10a and M10b)

- **port-popular** (six ports, reviewed through its fifth review): merged as is. It adds the Packages `ask-user-question`,
  `goal`, `permissions`, `powerline`, `subagents` and `todo`, and the `pig-popular` Piglet (11 Piglets now). Only
  `ports/README.md` conflicted; it is generated, so it was regenerated.
- **pig-music**: the M10b review branch (M10a, M10b and the M10b review fixes) and the M10a review branch (its fixes), both
  reviewed ACCEPT-WITH-FIXES. M10c (lyrics), M10d (pages) and M10e (wide stage) are not in this tree. They have no review
  yet and are left for a later change. `pig-music` is in `pig-with-batteries` and does nothing until you type `/music`.
  Conflicts (test list in `package.json`, `README.md`, `RELEASE-NOTES.md`, `scripts/pig-requirement.json`, the pig-music
  progress note) were resolved by keeping both sides and this branch's pin and release block.
- **Pins.** The six port-popular modules still required SDK `v0.0.0` (they branched before the 0.4.0 pin). They now require
  `v0.4.0` with the same `go.sum` as every other module.
- **Names scrubbed.** The pig-music notes and two test comments named the build machine and working branches; the
  quality gate refuses branch names, and the tree must not carry either. They now say "the test machine" and the review
  names without the branch prefix.
- **Regenerated:** `index.json` and `ports/README.md` (28 ports; the six port-popular rows are `review`, as their lane left
  them). Counts in README, RELEASE-NOTES and RELEASE-BLOCKERS say 11 Piglets.

Gates on this head, linux/amd64 with pig 0.4.0: `npm test` 181/181, `npm run check`, `npm run test:go-ports` (76 packages ok),
`platform-smoke` built and smoked all 11 Piglets (58 s; `pig-with-batteries` 66 MiB), the android/arm64 compile and vet check
over 32 modules, no tracked file over 2 MB, no tracked ELF/Mach-O, and the tree scan for personal paths, host and company
names, and working-branch names finds nothing. Not run: macOS, Windows and linux/arm64, which are the first hosted
`workflow_dispatch`, and the 0.4.1 pin (item 4, waiting for the lead).

### Item 4: pinned to PiG 0.4.1

PiG `v0.4.1` is published (tag `v0.4.1` = commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`, `extensions/sdk/v0.4.1`).

- **SDK.** All 31 Go modules that require the SDK now require `github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1`, with the
  `go.sum` lines taken from the Go proxy and checked against the checksum database (`h1:Ql9sTkc6...`; the SDK's `go.mod` is
  unchanged, so its `/go.mod` line is the same as for 0.4.0). A build of three modules with `GOWORK=off -mod=readonly` against
  the proxy resolves.
- **pig.** `scripts/pig-requirement.json` says `0.4.1` with the `v0.4.1` tag, commit and the five target archives' SHA-256. I
  downloaded each archive myself and compared it with the release's `SHA256SUMS`: all five match. `pig --version` of the linux
  archive prints `0.4.1+1.0.3`. CI builds pig from `PIG_COMMIT` = the `v0.4.1` commit (a test keeps it equal to the requirement);
  the inert release-workflow draft pins the same commit.
- **Tool renderers (D89).** The build tag `pigsdk_tool_renderer` is set by `scripts/go-modules.mjs` for any SDK that has
  `Extension.ToolRenderer`, so with the 0.4.1 SDK it is on without a change (`GOFLAGS=-tags=pigsdk_tool_renderer`); the 3
  `extension-equivalence` fake-host tests behind it and the gap-scan test run and pass.
- **Found and fixed on this pin: `npm run validate` failed for `ollama-native`.** `pig install <dir> --validate-only` loads an
  extension in a host that binds no native provider registry, so an extension that registers a native `sdk.Provider` fails with
  `native provider registry is not bound` (the same on 0.4.0: I had run `pig package validate` and the tests for the new Package,
  not `npm run validate`, when I added it). That is a PiG limit (an input: bind the registry in the validate-only host). Pigpen's
  fix is a different load check for such a module: `loadsInPig` (in `scripts/platform-smoke.mjs`) runs pig on it with the test
  model and fails on a load error or a wrong answer (I checked that a broken extension fails it; `--list-models` does not, it
  ignores a load failure). `registersNativeProvider` (in `scripts/go-modules.mjs`) picks the module. 8 new tests.
- **A flaky pig-music test, found by the clean-clone rehearsal and fixed at its cause.** `TestDaemonLimitsClients` failed with
  "4 connections were refused, want 3". `startDaemon` waits for the daemon with a probe connection that connects and closes; the
  daemon frees its slot only when it notices the close, a moment later. A test that dials 19 connections right away then sees 15
  free slots and 4 refusals. Reproduced 20 of 20 on one CPU under load. The test now fills the slots until 16 connections
  actually answer a command (so it no longer depends on how fast the daemon notices), then requires the next 3 to be closed by
  the daemon (not left to time out) and the 16 to still answer. Under the same load it fails 0 of 100; weakening the daemon's
  limit by one makes it fail. No timeout was lengthened and nothing was retried.
- **A test that would break at every bump** hard-coded `0.4.0+1.0.0` for a fake Binary; it now derives the version from the pin.
- **Docs.** README, CONTRIBUTING, RELEASE-BLOCKERS and RELEASE-NOTES now say 0.4.1 where they describe what was run, and say
  "not repeated on 0.4.1" for what was run only on 0.4.0 (the publish dry run, `test:port` with Pi, the other cross targets,
  `interop-kagent`, `verify-pins`). Per-port evidence in the `PORT.md` files is left as recorded.

Gates on this head, linux/amd64, pig 0.4.1+1.0.3 (release build): `npm test` 189/189, `npm run check`, `npm run validate` (every Package, every staged Piglet, 24 extensions through `--validate-only`, `ollama-native` through the real-pig
load), `test:porter` 2/2, `test:tool-scope` 4/4, `test:moved` 1/1, `test:go` ok, `test:go-ports` 76 packages ok with the tag on,
`test:acp-interop` (30 messages against schema 0.26.0, with `pig-acp` and `pigeq` built here), `test:pig` 4/4, `test:pig-snake`
9/9, `test:doctor-mutations` (62 caught), `platform-smoke` 11/11 Piglets (`pig-with-batteries` 66.7 MiB), the android/arm64
check over 32 modules. Not run: macOS, Windows and linux/arm64 (first hosted run), `test:port` with Pi, and the publish dry run.

### Follow-ups left out on purpose (nice to have; none blocks the release)

- herdr: the direct-socket transport, the queue drain at shutdown, and a live check of `custom:pig` on herdr 0.9.3.
- ollama-native: a run against a real Ollama (the fixtures are written to the documented format); `/api/show` per model is
  N+1 requests at a refresh; the system prompt's sections arrive unordered from PiG 0.4.0 (a PiG input).
- pig-music: M10c to M10e once reviewed; the M10a review's open item 3 (a private `TMPDIR` for the yt-dlp fallback's library
  runs) lives on the M10d commit and comes with it; no live check of a signed-in library or of a real sound device.
- port-popular: the six ports stay `review` in the ports list until the owner accepts them; their `lane` and review-lane
  names in test comments and records are part of the open naming decision above.
- CI: the first hosted run on macOS, Windows and linux/arm64.

## Signing and the public branch

The commits are SSH-signed with the maintainer's GitHub key (`gpg.format=ssh`), authored and committed as Michael Kinsy
<mrkinsy5@gmail.com>, with a sign-off and no AI attribution. GitHub verifies them against the key registered to the account.

The public update is one commit on `origin/main` whose tree is this branch's tree. A scan of that tree for personal paths,
host names, company names and working-branch names finds nothing, and the commit message carries no tool attribution.

## PR description draft

**Title:** Bring Pigpen's main up to date: 10 Piglets, 21 Packages, pinned to PiG 0.4.0

Public `main` is still the initial scaffold. This brings in the work done since: ten Go-only Piglets and the component
Packages they select, the porting workflow with its equivalence harness, the ports list with upstream pins and credits, the
quality, Go-only and index gates, and the tests that drive a real pig.

Pinned to PiG 0.4.0 (`0.4.0+1.0.0`): every Go module requires `github.com/MichaelKinsy/PiG/extensions/sdk v0.4.0`, the scripts
require a pig of the same minor, and CI builds the pig from PiG's `v0.4.0` commit and runs the validation, porter, tool-scope,
moved-extension and Go module tests.

What it is not: a release. CI stays validation-only (no Binary builds, signing or publishing) and the release guard stays
closed. `RELEASE-BLOCKERS.md` lists what a first release needs and who has to act; the release workflow is a draft in
`docs/plan/` and has not run.

Verified locally with the 0.4.0 pig: `npm test` (156), `npm run check`, `npm run validate`, `test:porter`, `test:tool-scope`,
`test:moved`, `test:go-ports`, `test:pig`, `test:pig-snake`, `test:acp-interop`, `test:port` (39 pass, 0 fail, 6 structural
skips), all 10 Piglets built into Binaries on linux/amd64, `go vet` for the other four declared targets. No hosted CI run is
claimed. Details in `RELEASE-NOTES.md` and `docs/plan/progress/pigpen-sync.md`.

The first hosted run of the PR's CI failed two tests that depend on timing; both were reproduced and fixed at the cause, and
a third found by re-running the workflow was fixed too: the ahp client-action fixture took a snapshot before the host had
finished starting a session; the Runner's `game_act` queue could drop the action that answers the decision hold when a player
is faster than 60 frames a second (a real bug the test had been hiding, and the integration test now plays until the game has
produced three obstacles instead of for nine seconds); and the ACP write-diff test now orders the write after the adapter with a
gate extension instead of racing it.

An independent review found and fixed, before this PR: the release workflow draft could not publish (its Binaries and the
staged tree shared one directory) and let the repository's scripts see the signing key; the draft and the owner actions now
keep the key in its signing step and as an environment-only secret limited to its tags. Working-branch names were removed from
published documents and a quality gate now refuses them. Stale statements about CI, ACP and the warden defect were corrected.

Platform builds: CI builds all ten Piglets natively on five runners (linux amd64 and arm64, macOS arm64 and amd64, Windows) with
the pinned PiG release (checksum verified), smokes each Binary, and cross-compiles every module for android/arm64. A pull request
runs the quick lane plus one build and smoke; the full matrix runs on main, merge groups, weekly, on demand and for a `full-ci`
label. One aggregate check, `CI result`, is the required one. Not yet run on macOS, Windows or linux/arm64.

AHP in Visual Studio Code: the `ahp` listener prints the `ws://127.0.0.1:<port>?tkn=<token>` address VS Code's Agents window takes
("Agents: Add Remote Agent Host..."), refuses a bad token with 403 as VS Code's own agent host does, and is tested with the
messages VS Code's client sends. Its README has the VS Code steps. Not yet tried with a live VS Code.

Performance: a measured pass over every Piglet and extension found Pigpen's own startup and idle costs negligible and fixed ten
per-call or per-start costs (regexps compiled per call or at start, a memo keyed by the whole tool input, a quadratic HTML to
Markdown join, a table re-parsed per call, and others), each behind a test that failed first. The costs that are PiG's (the host
encoding the model catalog for every extension, a state snapshot per delivered event) are written up for PiG, not worked around.
The quality gate also refuses executables in the tree now.

Review notes: the diff is the whole tree (a single commit on `main`). Ports carry their upstream licenses; start with
`ports/README.md`, `RELEASE-BLOCKERS.md` and `.github/workflows/ci.yml`.
