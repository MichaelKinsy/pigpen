---
issue: 970
issue_title: "pi-permission-system: raise the pi-coding-agent peer floor and devDependency pin past 0.86"
---

# Require Pi 1.0.0: raise the peer floor and devDependency pin together

## Release Recommendation

**Release:** mid-batch — defer (batch "pi-1.0 prompt options"); confirm at ship time

This issue is in no roadmap batch, and the batch is named here by the operator's call during planning.
On its own, the floor raise is a major bump that gives users nothing except a stricter requirement.
Holding it lets one major carry both the floor and the [#999] `<mcp_servers>` fix, released in the same dispatch as [#999], which is the tail. [#1009] (pi-subagents) still releases ahead of that dispatch, as the [#999] plan requires.

## Problem Statement

The package declares `>=0.79.0` for `@earendil-works/pi-coding-agent` and `@earendil-works/pi-tui`, and type-checks against a `0.79.1` devDependency.
Pi has since shipped the section-shaped prompt (0.86) and reached 1.0.0.
The [#999] fix will state prompt changes through `systemPromptOptions` and is planned against the 1.0 event types, with no string path for older hosts, so it needs a floor and a pin at 1.0.0 first.
The repo convention is to move the floor and the pin together, so `tsc` checks against the oldest Pi the package admits.

## Goals

- Raise both peer floors to `>=1.0.0` and both devDependency pins to `1.0.0`, in `packages/pi-permission-system` only.
- Admit the new dependency family to the workspace install: the `esbuild` build decision, plus any `minimumReleaseAgeExclude` entries pnpm writes.
- Confirm that every SDK symbol the package uses resolves against 1.0.0 (`tsc`, the full suite, `verify:public-types`).
- Tell upgrading users what changed, in the README's `## Upgrading` section.

Breaking-change classification: **breaking**.
A user on Pi 0.79–0.99 gets a peer-dependency conflict on upgrade and stays on the current major.
The commit is `feat(pi-permission-system)!:` with a `BREAKING CHANGE:` footer.
That follows the repo's prior pure floor raise, `feat!: raise minimum Pi dependency to v0.75.0` (`10683290`).
The two `fix!:` floor raises the issue cites each carried a behavior fix that the floor made true, and this one carries none.

## Non-Goals

- **Deleting the ≤0.85 header layout in `src/exposure/tool-surface-prompt.ts` and its fixtures.**
  The issue proposed it, and the operator moved it to [#999] during planning.
  The [#999] plan deletes the whole relocation (`renderToolSurface`, both layouts, `detectPromptLayout`, the removal functions) and already lists "whatever header-layout residue [#970] left".
  Deleting it here would rewrite about 40 header-shaped tests in `test/exposure/tool-surface-prompt.test.ts` (lines 166–689) and about 10 in `test/handlers/before-agent-start.test.ts`, and [#999] would then delete or retarget the same tests again.
  On Pi ≥1.0 the header branch cannot be reached, because Pi writes a `<cwd>` section in both renderer branches, so leaving it adds no behavior difference, only dead code for one release window.
- **Replacing the handler's lean `BeforeAgentStartPayload` with Pi's `BeforeAgentStartEvent`.**
  The [#999] plan does that ("Use Pi's `BeforeAgentStartEvent` (typed after [#970])").
- **Dropping the `extensionGuidelines` filter's ≤0.85 rationale or its 0.85 test** (`test/exposure/tool-surface-prompt.test.ts` line 997): same reason, [#999] owns that file.
- **Raising `engines.node`.**
  Pi 1.0.0 declares `node >=22.19.0` and this package declares `>=22`, but a host on an older Node cannot run Pi 1.0 at all, so the narrower range is enforced upstream.
- **Other packages' floors.**
  Each package keeps its own floor; [#1000] (pi-nocd) and [#1009] (pi-subagents) decide their own.
- **Editing the [#999] plan.**
  It lives on the `issue-999` branch; its prerequisite line "the header layout is gone" becomes "the header layout is still there, delete it", which its Module-Level Changes already covers.
  The retro records this for the [#999] session.

## Background

- `packages/pi-permission-system/package.json`: `peerDependencies` `>=0.79.0` for both packages, `devDependencies` `0.79.1` for both.
- The SDK imports in `src/`, checked with `grep`:
  - `@earendil-works/pi-coding-agent` from 13 modules (`index.ts`, `permission-session.ts`, `service-lifecycle.ts`, `tool-call-boundary.ts`, `session-turn-prep.ts`, `lifecycle.ts`, `policy-loader.ts`, `config-store.ts`, `local-user-authorizer.ts`, `forwarding-manager.ts`, `authorizer.ts`, `authorizer-selection.ts`, `before-agent-start.ts`).
  - `@earendil-works/pi-tui` from `line-fitting.ts` and `config-modal.ts`.
- The only version-conditional code in `src/` is the header layout in `tool-surface-prompt.ts` (a grep for `0.8x`/`through 0.`/`from 0.` found nothing else outside the `tree-sitter-bash` grammar version notes).
- `pnpm-workspace.yaml`:
  - `minimumReleaseAgeExclude` already carries the `0.84.4` family, which was added for pi-subagents.
  - `trustLockfile: true` lets CI's frozen install accept a reviewed lockfile holding a fresh release (`releasing` skill, `## Same-day sibling bumps`).
- `@earendil-works/pi-coding-agent@1.0.0` was published `2026-10-01T19:15:22.967Z` (`pnpm view … time`), and pnpm 11's default `minimumReleaseAge` is 24 hours.
- The README `## Upgrading` section has one `### <version> — <change>` entry per breaking release that needs user action (22.0.0, 16.0.0).

## Design Overview

### Spike (measured at planning time, reverted)

With both devDependencies set to `1.0.0` and `pnpm install` run at 2026-10-01T21:45Z:

- pnpm itself added 8 `minimumReleaseAgeExclude` entries: `chord`, `pi-agent-core`, `pi-ai`, `pi-codemode`, `pi-coding-agent`, `pi-mcp`, `pi-telemetry`, and `pi-tui`, all at `1.0.0`.
  It merged the five names already excluded at `0.84.4` into `'<name>@0.84.4 || 1.0.0'` and appended the three new ones.
- pnpm failed with `ERR_PNPM_IGNORED_BUILDS` for `esbuild@0.28.2` and wrote the placeholder `esbuild: set this to true or false` into `allowBuilds`.
  `pnpm why esbuild` traces it to `@earendil-works/chord@1.0.0` (a new 1.0 dependency of `pi-coding-agent`) and to `vite` under `vitest`.
  With `esbuild: false`, the install completed.
- `pnpm --filter @gotgenes/pi-permission-system run check` was clean.
- `vitest run` passed: 175 files, 5174 tests.
  A first run under concurrent load reported 2 failures plus 5 unhandled timeout errors, then passed clean twice with no change, matching the skill's note that forwarding-liveness wall-clock tests flake under host load.
- `verify:public-types` reported both checks OK.

So the change compiles and passes with no `src/` edit.

### Decisions

- **`esbuild: false`.**
  Nothing in this package's test, type, or lint path runs esbuild's postinstall, and the spike passed with it denied.
  `allowBuilds` is an allowlist of install-time scripts, and denying is the least-privilege answer.
- **Release-age entries are pnpm's, not authored.**
  If the implementation runs after `2026-10-02T19:15Z`, the 1.0.0 family is past the 24-hour gate and pnpm writes no entry.
  Commit whatever `pnpm install` writes and nothing more.
  Do not hand-edit the entries or hand-merge the `||` ranges.
- **Version number in the README.**
  It comes from `./scripts/release/next-version.sh pi-permission-system` after the breaking commit lands, never authored.

## Module-Level Changes

- `packages/pi-permission-system/package.json`: `peerDependencies` `@earendil-works/pi-coding-agent` and `@earendil-works/pi-tui` → `>=1.0.0`; `devDependencies` for both → `1.0.0`.
- `pnpm-lock.yaml`: regenerated by `pnpm install`.
- `pnpm-workspace.yaml`: `allowBuilds` gains `esbuild: false`; `minimumReleaseAgeExclude` gains whatever pnpm writes (see Decisions).
- `packages/pi-permission-system/README.md`: a new `### <version> — requires Pi 1.0.0` entry at the top of `## Upgrading`, saying the package now needs Pi 1.0.0 or later and that users on an older Pi stay on 36.x.
- Predicted unchanged, each with the claim it rests on:
  - Every `src/` and `test/` file: the spike type-checked and passed with no edit.
  - `src/exposure/tool-surface-prompt.ts` and its tests: deferred to [#999] (Non-Goals).
  - `docs/configuration.md` line 1332 ("through pi 0.85 … from pi 0.86"): it still describes the code, which keeps both layouts until [#999].
  - `docs/architecture/architecture.md` line 1271: an open-issue sweep disposition, a historical record of the triage call, not a description of current behavior.
  - `docs/decisions/0001-project-trust-adoption.md` (mentions 0.79.x): records when the trust API arrived, and is still true.
  - `.pi/skills/package-pi-permission-system/SKILL.md`: a grep for `0.79`/`peer floor`/`peer range` matched only the unrelated model-judge `>=27.0.0` line.
  - The Upstream assumptions table: it names Pi source files, not versions.
  - Other packages' manifests: a pnpm workspace keeps per-package dependency versions, so the lockfile keeps their `0.79.1`/`0.84.4` entries.

## Test Impact Analysis

No test is added, removed, or changed.
The verification surface is the toolchain, re-run from the spike:

```bash
pnpm install                                            # completes; no ERR_PNPM_IGNORED_BUILDS
pnpm --filter @gotgenes/pi-permission-system run check  # tsc clean
pnpm --filter @gotgenes/pi-permission-system run test   # spike: 175 files, 5174 tests passed
pnpm --filter @gotgenes/pi-permission-system run verify:public-types  # two OK lines
pnpm --filter @gotgenes/pi-permission-system run lint
pnpm fallow dead-code
grep '"version"' packages/pi-permission-system/node_modules/@earendil-works/pi-coding-agent/package.json  # "1.0.0"
```

The handler and exposure tests use synthetic events and hand-written prompt fixtures, so they check this package's logic, not Pi 1.0's runtime shape (skill, `## Upstream assumptions`).
`tsc` against the 1.0.0 declarations is the check this change adds.

## Invariants at risk

| Invariant                                                      | Constituency                 | Pinned by                                                                                                    |
| -------------------------------------------------------------- | ---------------------------- | ------------------------------------------------------------------------------------------------------------ |
| `tsc` checks against the oldest Pi the package admits          | maintainers, users           | floor and pin both `1.0.0` in one commit; a reviewer reads both lines                                        |
| `dist/public.d.ts` stays self-contained for external consumers | downstream extension authors | `verify:public-types` (packs the tarball and type-checks an external consumer)                               |
| ≤0.85 output byte-identical ([#962])                           | users on Pi ≤0.85            | **retired** by the floor: no admitted host writes the footer shape; the tests stay until [#999] deletes them |

## TDD Order

This is a dependency and docs change with no test cycle, so run it with `/build-plan`.

1. **`feat(pi-permission-system)!: require Pi 1.0.0 or later`**
   - Edit `package.json` (both peers to `>=1.0.0`, both devDependencies to `1.0.0`), run `pnpm install`, and set the `allowBuilds` placeholder to `esbuild: false`.
   - Run `pnpm install` again until it completes clean.
   - Run every command in Test Impact Analysis.
   - Stage `package.json`, `pnpm-lock.yaml`, and `pnpm-workspace.yaml`.
   - Commit body: name the 1.0 dependency family the install admits, and say no `src/` change was needed.
   - Footer: `BREAKING CHANGE: @earendil-works/pi-coding-agent and @earendil-works/pi-tui must now be 1.0.0 or later. On an older Pi, stay on pi-permission-system 36.x.`
   - Refs line: `Refs #970` (the ship step closes the issue).
2. **`docs(pi-permission-system): note the Pi 1.0.0 requirement under Upgrading`**
   - Run `./scripts/release/next-version.sh pi-permission-system` and use the version it prints in the heading.
   - Add the `## Upgrading` entry above `### 22.0.0`.
   - Lint with `pnpm --filter @gotgenes/pi-permission-system run lint:md`.

## Risks and Mitigations

- **Release-age gate on a fresh 1.0.0.**
  CI runs `--frozen-lockfile`, and `trustLockfile: true` skips the lockfile's re-verification, so a reviewed lockfile with the 1.0.0 family installs either way.
  Locally, pnpm writes the exclude entries itself, as the spike showed.
- **Downstream peers.**
  `@gotgenes/pi-permission-model-judge` declares `>=27.0.0` on this package and `>=0.84.3` on Pi.
  A user who upgrades this package to the new major on Pi <1.0 gets a peer warning from this package, not from the judge.
  The judge's range is unchanged and still correct for 36.x.
- **Releasing before [#999] by mistake.**
  The `mid-batch — defer` marker makes `/ship` ask before releasing.
  If the major goes out alone anyway, the only cost is a major with no fix; nothing breaks for users on Pi ≥1.0.
- **A flaky full-suite run is read as a 1.0 regression.**
  Re-run a failing `test/authority/` forwarding-liveness file alone before investigating, as the package skill directs.

## Open Questions

- None.
  The floor (`>=1.0.0`) was set by the operator's comment on this issue, and the scope and release questions were settled in planning.

[#962]: https://github.com/gotgenes/pi-packages/issues/962
[#970]: https://github.com/gotgenes/pi-packages/issues/970
[#999]: https://github.com/gotgenes/pi-packages/issues/999
[#1000]: https://github.com/gotgenes/pi-packages/issues/1000
[#1009]: https://github.com/gotgenes/pi-packages/issues/1009
