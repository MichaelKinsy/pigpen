# Pigpen release blockers

**Nothing is published yet; the release path is built and rehearsed, and waits for the owner.**
Pigpen CI **validates and smoke-tests**: it checks the generated index, source quality gates, unit tests, and validates every
Package and staged manifest, runs the porter, tool-scope, moved-extension, Go module and Package-install tests and a
rehearsal of the release workflow, all with a pig built from PiG `v0.4.1`. A platform matrix builds all eleven Piglets natively on
`linux/amd64`, `linux/arm64`, `darwin/arm64`, `darwin/amd64` and `windows/amd64` and smokes every Binary (`--version` and one
`PIG_TEST_FAUX` turn, no extension load error); `android/arm64` is compiled and vetted. CI holds no secret and no write permission.

Releases are two workflows (see [OWNER-ACTIONS.md](OWNER-ACTIONS.md)). A Package tag `components/<name>/v<version>` runs
`release-package.yml` (no secret): it validates the Package and installs it from the pushed tag. A Piglet tag
`<name>/v<release.version>` runs `release.yml`: it builds every target on its native runner, signs, attests and publishes one
GitHub Release, with `PIGLET_SIGNING_KEY` readable only by steps that run the pinned pig, in the `release` environment.
`npm run quality` enforces those rules. No hosted run is claimed: the workflow's own steps were run locally against a bare
repository, a stub `gh` and a throwaway key (`npm run rehearse:release`). No release gate has been waived.

This file says what still stands between the repository and a first release, and who has to act.
Evidence was gathered with PiG 0.4.1 (`0.4.1+1.0.3`, tag `v0.4.1`, commit
`3ee745c8cda3c1a9a8d71112c140790c4d64d78d`), Go 1.27.1, Node 24.19.0, linux/amd64.

## What PiG 0.4.0 and 0.4.1 settled

| Earlier blocker | State on PiG 0.4.1 | Evidence |
|---|---|---|
| A public, reviewed PiG revision with per-Piglet tags, namespace-aware update and subdirectory add | **Resolved.** `v0.4.1` is public; CI pins its commit; `npm run test:porter` is enabled in CI | `pig piglet publish --tag-prefix <name>/`; the prefix must equal the manifest name plus `/` (`--tag-prefix a2a/` for the herdr Piglet is refused). `pig docs show piglets`, "Monorepo publication and named Binary updates" |
| Print and JSON mode ignored a per-extension `tools: []` | **Resolved.** | `npm run test:tool-scope`: RPC, print and JSON mode pass, with a control case that offers `pig_doctor` without the scope |
| No Go `pi.events` bridge | **Resolved in the SDK** (`Extension.Events()`). Pigpen's herdr reporter does not use it yet (see below) | PiG `extensions/sdk/event_bus.go` |
| Go SDK pin older than the herdr extension needs | **Resolved.** Every module requires the published `sdk v0.4.1` (checked against the Go checksum database) | `go.sum` is committed; a module with no library dependency vets with plain `go vet` (`GOWORK=off`) |
| Publisher receipts for the catalog | **Answered**: PiG emits one (below) | a real `piglet-release.json` captured from `pig piglet publish --yes` (with a stub `gh`) |
| Node extension in a Piglet Binary | Not re-checked. No Pigpen Piglet selects a Node extension (owner rule: Go only) | The PiG defect is untouched |

Tool renderers (`pi.registerToolRenderer`, `Extension.ToolRenderer`, D89) are in the 0.4.1 SDK. The scripts set the build tag
`pigsdk_tool_renderer` for an SDK that has them, so on this pin the fake-host tests for them are compiled and run
(`extension-equivalence`: 3 tests, and the gap scan treats `RegisterToolRenderer` as supported). A port that needs them is no
longer blocked.

One limit found on this pin, a PiG input and not a Pigpen blocker: `pig install <dir> --validate-only` loads an extension in a
host that binds no native provider registry, so an extension that registers a native `sdk.Provider` (Pigpen's `ollama-native`)
fails it with `native provider registry is not bound` on 0.4.0 and 0.4.1. `npm run validate` loads such a module in a real
pig with the test model instead (`scripts/platform-smoke.mjs` `loadsInPig`; it fails on a load error), and
`pig package validate` passes for it.

## Local evidence on PiG 0.4.1

Run with a temporary HOME, PIG_HOME and agent directory, `PIG_BIN` the `0.4.1+1.0.3` release build (checksum verified):

- `npm test` (189), `npm run check`, `npm run validate` (24 Go extension directories pass
  `pig install <dir> --validate-only`, and `ollama-native` is loaded in a real pig instead, see above; every Package passes `pig package validate`; every staged Piglet passes
  `pig piglet validate`), `test:porter` 2/2, `test:tool-scope` 4/4, `test:moved` 1/1, `test:go-ports` (76 Go packages
  vetted and tested with the tool-renderer tag on), `test:go`, `test:acp-interop` (30 messages
  checked against schema 0.26.0), `test:pig` 4/4, `test:pig-snake` 9/9, `test:doctor-mutations` (62 mutations caught).
- `platform-smoke` builds all 11 Piglets on linux/amd64 and each Binary reports `0.4.1+1.0.3` and answers the test-model turn
  with no extension load error. `go build` and `go vet` for android/arm64 pass over 32 modules (a compile check, not a run;
  PiG's native builder builds only its host target). Not re-run on 0.4.1: the other cross targets, and the publish dry run below.
- Publish, dry run, all 11 Piglets (run on 0.4.0, not repeated on 0.4.1; stub `gh`, throwaway key, host target): tag `<name>/v0.1.0`, `pull github:<owner>/<repo>/<name>@0.1.0`.
  A full `publish --yes` with `--artifacts` against the stub produced a signed `piglet-release.json` and `SHA256SUMS`.

## What still blocks a first release

### Pigpen has to decide or do

1. **Source-add form. Recommendation: Binary-only first release; no remote source add.** Re-checked on 0.4.0 with local
   fixtures (git `url.<file>.insteadOf`, no network):
   - An authored Piglet cannot be added from source: `piglets/herdr` fails with `Piglet closure path must be a portable
     relative path` (it selects `../../components/herdr`).
   - A committed staged tree adds (`dist/staged` committed in a repository: `herdr`, `pig-with-batteries` added), but
     `dist/` is not committed, and publishing a generated tree is a second source of truth.
   - `pig-porter` (uses `extends`) cannot be added even from a staged tree (`Piglet add cannot copy an extends dependency
     closure`).
   Binaries do not depend on any of this: `pig piglet pull github:<owner>/pigpen/<name>@<version>` installs the signed
   Binary. If the owner wants source add later, either commit a generated tree on a release branch or move compositions to
   `npm:`/`git:` Package references (the latter needs a Package published from its own repository or tag).
2. **The protected release workflow.** Adopted: `.github/workflows/release.yml` (Piglets) and `release-package.yml` (Packages), the
   release guard in `scripts/build-matrix.mjs` is gone. Rehearsed locally with PiG `v0.4.1` (`npm run rehearse:release`: plan,
   android check, one native build, publish; a Binary altered after signing, a second publish of the tag and a wrong-version tag
   are refused; the key file is removed after each step and is in no log). The rehearsal found one defect the draft had (the
   quality gate refused the downloaded Binaries inside the checkout; they are now outside it). Not run on GitHub: environments,
   secrets, attestations, artifact transfer and the four non-linux/amd64 targets. PiG 0.4.x has no `pig piglet sign`, so the
   signing key is read by each build job and the publish job (OWNER-ACTIONS.md, section 8).
3. **Index generation from receipts.** Done. `scripts/receipts.mjs` verifies the signed `piglet-release.json` (DSSE, payload type
   `application/vnd.pig.piglet-release+json`) with the Ed25519 key pinned in `release-keys/`, never the key in the payload,
   and requires the Piglet, version, repository, tag prefix `<name>/` and Binary URLs to equal the tag; a receipt written by
   pig 0.4.0 is a test fixture (written by that release). `generate-index.mjs` lists a Piglet `available` only for a verified committed receipt
   (`npm run record -- piglet <name> <version>`) and a Package `available` for a recorded tag; `index.schema.json` requires a
   release for an available Piglet. The matching pi-in-go.dev change is a patch in `docs/site-change/` (not applied; the owner
   applies it to the site repository). Still needs a real release for a real receipt.
4. **herdr `herdr:blocked`.** The SDK bridge exists; the reporter does not listen yet and two named tests stay skipped
   (`components/herdr/extensions/herdr/extension_test.go`). Not a release blocker; a Pigpen follow-up (the herdr fake host needs the
   bus wire frames).
5. **a2a third-party Go modules** (`a2a-go/v2` v2.6.0 and four transitive modules) are fetched from the Go proxy at build
   time. Decide: vendor them (licenses then travel with the Package) or accept the proxy for release builds. Also report
   upstream: a2a-go v2.6.0 `SubscribeToTask` on a live task does not check task ownership (Pigpen guards it in its call
   interceptor).
6. **Shared library Packages** (`pig-play`, `typesafe`, `pi-typesafe-api`) reach a Piglet through a `go.work` in the extension
   directory, and a staged Package's alias must equal its directory name. This works (the Binaries above were built that way),
   but it is an implicit contract. PiG could document a `replace` or a `packages/`-relative reference.
7. **Piglet composition limits** (`extends`; PiG items, input for PiG): `pig piglet add` refuses an `extends` closure
   (re-checked on 0.4.0); `extends.source` accepts only `local:` and contributed schemes; `extends` with `agentEnv` is not
   implemented; `release` and `build` are not inherited, so a derived Piglet repeats `build.targets`. A Go extension directory
   with both a factory and a `package main` is rejected, so a command line shares code through a module of its own
   (`cmd/pigeq/go.mod`). Not re-checked on 0.4.0 beyond the add refusal.
8. **Curation and licenses.** Finish curation, keep every source pinned, review the dependency closure and licenses of each
   Piglet (the Package READMEs and `provenance.json` hold what is known).
9. **A real end-to-end publication** on a throwaway repository (needs the owner's actions below): two Piglets with the same version,
   pulled and updated independently, escaped slash tags (`%2F`), a mismatched name or prefix, an existing release, tampered
   bytes, a signer change. PiG's own tests cover the same cases against a local TLS server, not GitHub; the dry runs and the stub
   `gh` publish above exercise only Pigpen's side.
10. **Targets without a native host here.** CI now builds and smokes every Piglet on native runners for all five targets
    (`platform-matrix` in `ci.yml`, and `build` in `release.yml`), but nothing has run on darwin, windows or linux/arm64 yet: the first hosted run
    (`workflow_dispatch`) is the first evidence. The smoke proves a Binary starts and completes a faux turn there, not that the
    games render or the terminal integrations work (see "What still has to be run" in `RELEASE-NOTES.md`). `android/arm64` is a
    compile and vet check only: PiG's native builder cannot build a fused android Binary from another OS.

### Owner actions (who must act)

The exact commands are in [OWNER-ACTIONS.md](OWNER-ACTIONS.md): create the signing key and commit its `.pub` to
`release-keys/`, create the `release` environment (tags `*/v*` only) with the environment secret `PIGLET_SIGNING_KEY`, a tag
ruleset for `*/v*` and `components/*/v*`, then push the Package tags and the Piglet tags one at a time and record each
release. Run the platform matrix once on a branch (`workflow_dispatch`) before the first Piglet tag. Apply the site patch.
An npm scope is not needed: Packages install from Git tags. PiG maintainers: items 6 and 7, a `pig piglet sign`, keyless
(GitHub OIDC) signing and an android build target are inputs for PiG; none blocks the first release.

Until these are done, do not claim live binaries or verified publication support for Pigpen compositions. Local source
validation and a working core publisher alone do not establish an installable Pigpen release.

## ACP (`piglets/acp`, `components/acp`)

Evidence (2026-09-30, PiG 0.3.0 `63c6ba456`; re-run on PiG 0.4.0: the Binary builds, the 11 end-to-end scenarios and the 30-message reference client run pass): the `acp` Piglet **builds a fused Go Binary** (57.6 MB, 44 s) and that Binary passes
the 11 end-to-end scenarios and the ACP reference client run when it is the `--pig` target of the companion executable
`pig-acp`. What is still open:

- **No one-command Binary.** ACP is JSON-RPC on the agent's own stdio, and a fused extension must not write protocol bytes to the
  host's stdout, so the Piglet Binary cannot be an ACP server. The editor runs `pig-acp --pig <Binary>`. Publishing needs the
  companion built per target and shipped with an explicit executable closure (Package and Binary versions that agree), or PiG
  support for it: a `--mode acp` in a Piglet Binary, or a Binary that carries a companion executable. This is an input for a PiG after 0.4.1 (its `--mode` is text, json or rpc).
- **Release workflow:** `scripts/build-matrix.mjs` builds Piglet Binaries only. It has no step that builds and attaches
  `pig-acp` (Go, no cgo, all targets cross-compile: `go vet` passes for linux, darwin and windows; only linux was run).
- **Editors:** Zed and JetBrains were not run (no editor available here). The ACP reference client is the interop evidence.
  A real editor session, including the terminal-login banner, is still to be verified before promising editor support.
- `pig-with-batteries` does not select the Package. That is no longer a build limit (batteries builds a Binary since the Go
  herdr port); adding `acp: local:../../components/acp` (and the `acp` extension entry, `tools: []`) is a two-line change
  that needs an owner decision and its own Binary proof; the fused build of the extension itself is proven.

## Open items from the warden port (`components/warden/port/PORT.md`)

- **Go SDK, in `v0.4.0` (not re-checked on `v0.4.1`)** (found by running the skipped test against it): `Context.GetBranch` drops an assistant message whose tool-call block carries `arguments`
  as a JSON object (Pi writes an object; the SDK reads a string), so the plan and sibling calls are lost and the
  intent-mismatch check is never asked. Reproduction: `components/warden/port/sdk-args_test.go.txt`. Skipped
  test: `TestSkippedSDKToolCallArgumentsAsObject`.
- **pigeq gaps found by the warden port**: `mutate --unit` cannot run a port that uses a shared
  library module (its temporary `go.work` lacks `components/typesafe`); the host check with `--builtin` also loads the port into the
  original's lane. PiG re-serializes tool-call arguments with HTML escaping (`>` as `\u003e`), unlike Pi (host difference,
  `port/PORT.md` finding 4).
- **Owner decisions**: which of the deferred pi-warden guards (rules file, slop, secret masking, context saver,
  conscience, subagent supervision, path and arming rules, SQL target classification, stuck evidence) to port next.
- **Two SDK generations**: the host getters return `(value, error)` from the PiG 0.3.0 pre-release builds on (and in
  `v0.4.0`) and a single value on older `pig` builds. Warden targets the former; a Piglet Binary builds against the SDK of `PIG_SOURCE_ROOT`.
