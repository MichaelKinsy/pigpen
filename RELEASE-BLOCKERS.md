# PiG monorepo release blockers

**Publication is intentionally blocked, not implemented by a site workaround.**
Pigpen CI is **validation-only**: it checks the generated index, source quality gates,
unit tests, and validates every Package and staged manifest with a pinned PiG build.
It does not build/sign Piglet Binaries, handle signing keys, or upload artifacts.
A `<name>/v<release.version>` tag is recognized, then refused by the release guard
(`scripts/build-matrix.mjs`). The workflow has no signing secrets or write permissions.

This file is input for PiG 0.4.0 and for Pigpen's own release work. Each item names
its evidence. It separates what PiG core still needs from what Pigpen still needs.
No release gate has been waived.

## Status of the three PiG items that used to block publishing

The older inspection of PiG commit
[`35280defec22d384545be37a7f297e1a98f1ace1`](https://github.com/MichaelKinsy/PiG/commit/35280defec22d384545be37a7f297e1a98f1ace1)
(still Pigpen's CI pin, PiG 0.2.0) found three gaps. They were re-checked on 2026-09-29
against `staging/team/lead/release-all-next` at `63c6ba456` (PiG 0.3.0, Pi 0.87.1).

| Item | CI pin `35280de` | Release-all-next `63c6ba456` | Evidence |
|---|---|---|---|
| Per-Piglet release tags | `publishRelease.tag()` hard-coded to `"v" + version`; no prefix option | **Implemented.** `pig piglet publish ... --tag-prefix <name>/`; the prefix must equal the manifest name plus `/`; the default stays unprefixed; the tag slash is escaped as `%2F` in download URLs; the signed index records repository and prefix separately from `sourceRef` | `pig docs show piglets` ("Monorepo publication and named Binary updates"); `go test ./coding/pigletbuild ./coding/piglet ./coding/piglet/release -run 'Monorepo\|Namespace\|PublishGitHubNamed\|AddMonorepo'` passes, including `TestMonorepoPublishPullUpdateIsolation` (two Piglets, one repository, independent pull and update) |
| Namespace-aware updates | Named updates planned; only repository-wide releases addressable | **Implemented.** `pig piglet pull 'github:owner/repo/<piglet>@<version>'`; `pig piglet update <name>` selects the highest stable SemVer in that exact namespace, never `releases/latest`; refuses rollback, repository or namespace change, revoked keys and unpinned signer change | same docs section and tests |
| Subdirectory add | Only a root `piglet.yaml`; Package subdirectory support was not evidence for Piglet add | **Implemented (D18)** for `git:<url>@<full-sha>#subdirectory=<path>`, with the restrictions below | local fixture below |

These three are **no longer PiG blockers in the release-all-next tree**. What remains
is that Pigpen's CI pin and a public PiG release must catch up to that tree, plus the
items below. PiG's own monorepo test uses a local TLS server, not GitHub, so nothing
here is proof against a real release.

### Subdirectory add: restrictions that affect Pigpen

Checked with the release-all-next `pig` against local fixture repositories. No GitHub
URL or network was used: git's `url.<file>.insteadOf` mapped an
`https://example.test/fx/*.git` locator to local repositories, with
`GIT_TERMINAL_PROMPT=0`. The Git source needs an `owner/repository` path of at least
two segments.

- **Authored Pigpen manifests cannot be added.** A committed `piglets/herdr/piglet.yaml`
  that selects `../../components/herdr` fails with
  `error: Piglet closure path must be a portable relative path`. The documented closure
  copies only local paths beneath the Piglet's own directory and forbids parent
  traversal. A remotely added Piglet therefore cannot select shared `components/` by
  sibling path.
- **The staged tree adds.** The output of `npm run stage` (`dist/staged/`, gitignored),
  committed in a repository, adds with `git:...@<sha>#subdirectory=piglets%2Fherdr`.
  PiG copies the shared Package into the Piglet and rewrites its source. Because
  `dist/` is not committed, no Pigpen Piglet can be added remotely today. Pigpen must
  publish a committed, pinned tree (for example a generated branch or tag) or use
  portable Package references (`npm:` or `git:`) in its compositions. Source add also
  requires a clean checkout at the exact commit.
- This is a Pigpen release decision, not a PiG defect.

## PiG core items that still block publishing (input for PiG 0.4.0)

1. **A public, reviewed PiG revision containing the above.** The CI pin predates all of
   it. With that pin, Package validation and `npm run validate` pass, but
   `npm run test:porter` fails at direct Package Skill expansion, so CI does not run it.
   The scenario also asserts the print-mode tool-scope fix (`tools: []` must expose no
   model tools; staging `22755205a`, `04eab6dd2` in release-all-next). Move the pin to
   the reviewed release commit, then enable `test:porter` in CI.
2. **Piglet Binaries cannot include a Node extension (resolved for Pigpen by removing them).**
   Building a Binary from a Piglet that selects a Node extension fails in
   release-all-next. Selected as `local:`: `component "extension/herdr" node subprocess
   requires a runtime` (`coding/piglet/artifact/plan.go`), because a Binary component
   cannot declare a Node runtime. Selected from a Package member (`package:<alias>`):
   `lock extension "extension": selected origin is unavailable`, because the build names
   the Node cell after the entry file's basename, not the Piglet's extension name.

   Owner rule (2026-09-29): every Piglet extension is a Go SDK extension. The herdr
   reporter is now Go (`components/herdr/extensions/herdr`), so no Pigpen Piglet selects
   a Node extension. Verified on `63c6ba456` with `pig piglet build <staged manifest>
   --format binary` (native builder, `PIG_SOURCE_ROOT` a git checkout): both
   `dist/staged/piglets/herdr` and `dist/staged/piglets/pig-with-batteries` now build,
   and the built Binaries pass the tmux/fake-herdr scenario (`PIG_SOURCE_ROOT=... npm run
   test:pig`). The PiG defect itself is untouched: any future Node extension in a Piglet
   would hit it again. PiG 0.4.0 must either declare and verify a Node runtime for Binary
   components, and make Package-member names match the Piglet entry name, or state that
   Node extensions stay source-only.
3. **Shared Packages in remotely added sources.** Decide whether a Piglet may select a
   Package outside its own directory in a remotely added source. Today PiG rejects it.
   Pigpen's staging avoids the question locally but adds a publishing step.
4. **Publisher receipts for the catalog.** `generate-index.mjs` permits only planned
   entries. To mark an entry available it needs a machine-readable verified receipt per
   Piglet (release version, direct signed-index URL, targets, signer key ID). Confirm that
   PiG emits one that Pigpen can consume without inferring availability from tags or
   `build.targets`.

5. **Piglet composition (`extends`) limits, and a Go `pi.events` bridge.** Input for issue
   #92 and PiG 0.4.0. Checked on release-all-next `63c6ba456` while building
   `pig-extension-porter` and `pig-porter`:
   - `extends` with a `local:` base validates and **builds a Binary** (fused extension
     inherited). Good.
   - `pig piglet add` refuses an `extends` closure (`Piglet add cannot copy an extends
     dependency closure; run the Piglet from its source path`), so `pig-porter` cannot be
     added remotely even from a staged tree. Add support for copying the base's closure.
   - `extends.source` accepts only `local:` and contributed schemes; `npm:`/`git:` bases would
     let a published `pig-porter` name a published `pig-extension-porter`.
   - `extends` with `agentEnv` is not implemented (`coding/piglet/runtime.go`).
   - `release` and `build` are not inherited, so a derived Piglet repeats `build.targets` and
     `extensionRealization`. Acceptable, but say so in the docs.
   - A Go extension directory that holds both a factory and a `package main` is rejected
     (`contains both factory and standalone main packages`), so a command-line tool that
     shares code with an extension needs its own module beside it (`cmd/pigeq/go.mod`).
6. **Go SDK gaps found by real ports** (both are in `docs/extension-sdk-surface.md` as
   reviewed exceptions; ports hit them): no `pi.events.on/emit` bridge (herdr's
   `herdr:blocked`, so a Go herdr reports `blocked` for UI prompts only); no `withSession`
   for `newSession`, `fork` and `switchSession`, and a fresh context's UI calls after a
   replacement are not delivered by PiG's RPC UI. Host differences seen by the equivalence
   harness against Pi 0.87.1: PiG's RPC loop handles `new_session` inline, so a dialog raised
   by a `session_before_switch` handler during that command cannot be answered (Pi handles
   input lines concurrently); PiG does not raise Pi's stale-context error after a replacement.
   Details: `components/dirty-repo-guard/port/PORT.md`.

7. **Go modules shared between Packages** (found by lane `pigpen-games`, PiG 0.3.0
   `63c6ba456`). `pig piglet build` ignores a `replace` in an extension's `go.mod` and tries to
   download the library; only a `go.work` in the extension directory (read by
   `coding/extension/source/resolve.go`) makes PiG add the require and replace itself. Packages
   stage as `packages/<alias>`, so the library's alias must equal its directory name for the
   relative `use` path to hold. That works locally (`pig-games` builds a Binary with the three game
   extensions fused) but it is an implicit contract: PiG could accept a documented
   `replace`, or a `packages/`-relative workspace reference, and remote Piglet add
   (item 3) would have to copy the library Package with the extensions.
8. **Resolved: the batteries Binary builds.** It used to be blocked by the Node herdr extension
   (item 2). With the Go herdr port merged, `pig-with-batteries` builds a fused Binary with every
   member (verified on `63c6ba456` at the release-candidate review).
9. **Print and JSON mode ignore an extension's empty tool scope.** With `63c6ba456`, a Piglet
   entry `tools: []` hides the extension's tools in interactive and RPC mode, but `pig -p` and
   `--mode json` still offer them to the model: a scratch Piglet selecting only `pig-doctor`
   with `tools: []` reports `5/9 tools active` in print mode and sends `pig_doctor` to the
   provider, while RPC reports `8/9 tools active, 1 hidden` and interactive sends only the
   built-in tools. `pig-with-batteries` relies on that scope to keep the read-only `pig_doctor`
   tool off. The earlier print/JSON fix (`04eab6dd2`) covers a Piglet-wide empty scope, not a
   per-extension one. PiG 0.4.0 must apply the per-extension scope in every mode.

## Pigpen tasks that block publishing (not PiG core)

- Replace the CI validator pin and enable `npm run test:porter` in CI (item 1). Add
  `npm run test:go-ports` (Go tests for every extension module) and `npm run test:port`
  (equivalence check against the recorded Pi traces, needs a built `pig`) once the pin has the
  current Go SDK. Traces were recorded from Pi 0.87.1 with Node 24.19.0; rerun them on any Pi
  bump.
- The herdr Go extension needs the newer SDK (`Context.IsIdle() (bool, error)`,
  `GetSessionFile() (*string, error)`, the `sdk.Event*` constants), which the current pin
  `35280def` predates: with that pin `npm run validate` cannot build it. Also add
  `npm run test:go` to CI (`PIG_SOURCE_ROOT` selects the SDK).
- The `herdr:blocked` gap is accepted for now: PiG's Go SDK has no `pi.events` bridge, so
  the Go reporter no longer honors that bus event. The bridge is being ported in PiG 0.4.0
  (Pi 0.99.1 port, family 6F); when it ships, listen for `herdr:blocked` and un-skip the two
  named tests in `components/herdr/extensions/herdr/extension_test.go`.
- `components/ahp` vendors Microsoft's Go AHP client (`third_party/agent-host-protocol-go`, wired by
  `replace`). A fused Piglet Binary ignores that `replace`, so the module carries a `go.work`.
  `npm run test:go-ports` does vet and test `components/ahp` (checked at the release-candidate review). Proving a
  component in a Binary needs `scripts/stage-piglets.mjs` on a throwaway layout: a Piglet cannot select
  a Package outside its own directory. `ahp` is not yet selected by `pig-with-batteries`.
- Write the protected per-Piglet release workflow. Adapt PiG's native-runner
  [release workflow](https://github.com/MichaelKinsy/PiG/blob/35280defec22d384545be37a7f297e1a98f1ace1/docs/examples/piglet-release.yml)
  with the target matrix from `scripts/build-matrix.mjs`, per-Piglet protected
  environments and Ed25519 secrets. Publish only on `<name>/v<release.version>` after the
  tag/manifest equality check, with `pig piglet publish --tag-prefix <name>/`, native
  builders, provenance attestations and prebuilt `--artifacts`. Do not upload disposable
  test-key artifacts, wrap `gh release create` around the publisher's index, or sign on
  the site. Remove the release guard only together with that workflow.
- Choose and publish the source-add form (committed staged tree or portable Package
  references).
- Run an end-to-end publication against a real throwaway repository: two Piglets with the
  same version, pulled and updated independently, plus escaped slash tags, mismatched
  names, existing releases, tampered bytes and signer changes.
- Extend `generate-index.mjs` to consume verified publisher receipts. Generate and review
  `index.json`; the site's next authorized rebuild consumes it.
- a2a (row 16): a Package with a third-party Go dependency (`a2a-go/v2` v2.6.0 and its four transitive modules) is not
  vendored. `pig install` and a Piglet Binary build fetch the modules from the Go proxy, so a release build needs network or a
  `GOFLAGS=-mod=vendor`-style cache; decide whether release builds vendor (the licenses of the transitive modules then travel in
  the Package) or accept the proxy. Built and proven here against PiG 0.3.0 only (Binary `piglets/a2a`, listener hosted inside it,
  workers as the same Binary); `pig-with-batteries` selects it too and builds a Binary with it fused. Re-run the
  a2a tests, `scripts/interop-kagent.mjs` and the Binary tests (`PIG_A2A_BINARY`) on the 0.4.0 SDK (Pi 0.99.1) before
  publishing; a2a's worker reads PiG's RPC JSONL and CLI flags. Report upstream: a2a-go
  v2.6.0 `SubscribeToTask` on a live task does not check task ownership (Pigpen guards it in its call interceptor).
- Finish curation, keep every source pinned, and review the dependency closure and
  licenses of each Piglet.

Until these are done, do not claim live binaries or verified publication support for
Pigpen compositions. Local source validation and a working core publisher alone do not
establish an installable Pigpen release.

## ACP (`piglets/acp`, `components/acp`)

Evidence (2026-09-30, PiG 0.3.0 `63c6ba456`): the `acp` Piglet **builds a fused Go Binary** (57.6 MB, 44 s) and that Binary passes
the 11 end-to-end scenarios and the ACP reference client run when it is the `--pig` target of the companion executable
`pig-acp`. What is still open:

- **No one-command Binary.** ACP is JSON-RPC on the agent's own stdio, and a fused extension must not write protocol bytes to the
  host's stdout, so the Piglet Binary cannot be an ACP server. The editor runs `pig-acp --pig <Binary>`. Publishing needs the
  companion built per target and shipped with an explicit executable closure (Package and Binary versions that agree), or PiG
  support for it: a `--mode acp` in a Piglet Binary, or a Binary that carries a companion executable. This is an input for PiG 0.4.0.
- **Release workflow:** `scripts/build-matrix.mjs` builds Piglet Binaries only. It has no step that builds and attaches
  `pig-acp` (Go, no cgo, all targets cross-compile: `go vet` passes for linux, darwin and windows; only linux was run).
- **Editors:** Zed and JetBrains were not run (no editor available here). The ACP reference client is the interop evidence.
  A real editor session, including the terminal-login banner, is still to be verified before promising editor support.
- `pig-with-batteries` does not select the Package. That is no longer a build limit (batteries builds a Binary since the Go
  herdr port); adding `acp: local:../../components/acp` (and the `acp` extension entry, `tools: []`) is a two-line change
  that needs an owner decision and its own Binary proof; the fused build of the extension itself is proven.

## Open items from the warden port (`components/warden/port/PORT.md`)

- **SDK 0.4.0, family 6F-go**: `Context.GetBranch` drops an assistant message whose tool-call block carries `arguments`
  as a JSON object (Pi writes an object; the SDK reads a string), so the plan and sibling calls are lost and the
  intent-mismatch check is never asked. Reproduction: `components/warden/port/sdk-args_test.go.txt`. Skipped
  test: `TestSkippedSDKToolCallArgumentsAsObject`.
- **pigeq gaps found by the warden port**: `mutate --unit` cannot run a port that uses a shared
  library module (its temporary `go.work` lacks `components/typesafe`); the host check with `--builtin` also loads the port into the
  original's lane. PiG re-serializes tool-call arguments with HTML escaping (`>` as `\u003e`), unlike Pi (host difference,
  `port/PORT.md` finding 4).
- **Owner decisions**: which of the deferred pi-warden guards (rules file, slop, secret masking, context saver,
  conscience, subagent supervision, path and arming rules, SQL target classification, stuck evidence) to port next.
- **Two SDK generations**: the host getters return `(value, error)` on the release-all-next lineage and a single value
  on older `pig` builds. Warden targets the former; a Piglet Binary builds against the SDK of `PIG_SOURCE_ROOT`.
