# Implementation and verification plan

This is a proposed implementation sequence, not a record of completed fixes. Read [PRODUCT.md](PRODUCT.md) for per-item necessity and disposition.

## Baseline and integration procedure

1. Preserve the current tracked and untracked changes, particularly `specs/2026-09-16-budget-visibility-and-recovery/` and the related implementation. Use an isolated branch/worktree from freshly fetched remote main for future implementation. Do not stash, reset or rebase the user's dirty checkout as part of planning.
2. Record main SHA, each selected PR head SHA, package/lock versions and baseline test results. The assessment snapshot is main `8b077aa6a1e9c83709bf639af79528501f8273d0`; refresh this before execution.
3. Integrate the local budget change as an intentional patch, resolving against #68's request-only prompt/cache changes. Its local context baseline predates remote 0.31.6 and must not replace the remote baseline wholesale.
4. Review requested fork workflow execution. At assessment time all five open PR heads had CI runs with `action_required`; no passing check rollup was available. After safe workflow approval, require checks on revised heads and the final integration branch.
5. Update each affected PRODUCT spec first for changed behavior, then TECH, implementation, tests and milestone records, in accordance with AGENTS.md.

## Work package A — deferred context lifetime (#78)

Primary files: `extensions/goal-state.ts`, `extensions/widgets/goal-widget.ts`; lifecycle cleanup in `extensions/goal-events.ts` as necessary.

- Capture the directory string while the registration context is valid at both widget-registration sites. Pass plain `{ cwd }` data to ledger readers and the string to settings readers; deferred getters must not dereference `ExtensionContext`.
- Review all widget callbacks, delayed UI work, timer callbacks, auditor completion callbacks and `lastUiCtx` handling for lifetime assumptions. Do not assume a microtask is safe solely because it was queued in the same tick; determine the host's invalidation ordering. Fix demonstrated remaining exposures with lifecycle cancellation/generation guards.
- Clear/rebind widget registration when a session is replaced, so a new session cannot inherit the old directory or state accidentally.

Acceptance:

- A test context whose accessors throw after invalidation can register focused and unfocused widgets; deferred rendering afterward does not access it.
- Focused, unfocused and expanded ledger views work before and after replacement, with correct settings/root.
- A supported Pi 0.86 smoke test exercises `/reload` with a visible widget. If the report's fatal path cannot be reproduced, record that separately from the regression proof.

## Work package B — declared host compatibility (#77)

Primary files: `package.json`, lockfile, `.github/workflows/ci.yml`, README compatibility notes and provider/SDK harnesses.

- In isolated installs, exercise the existing minimum 0.83.0, representative 0.84, and the reported 0.85.1/0.86.0 host versions. Resolve the actual current patch releases at implementation time and pin evidence to exact versions. Align the three Pi packages coherently.
- Validate extension registration, drafting, execution tools, reload, scheduler callbacks, context telemetry, `before_provider_request` payload transformation and Responses compatibility flags.
- If the matrix supports it, a bounded candidate is `>=0.83.0 <0.87.0`. If an old minor cannot be supported, explicitly narrow the lower bound and document the change instead of claiming untested compatibility. Do not silently expand Node requirements.
- Add CI for the declared minimum and newly admitted minor boundaries; keep the full suite on the primary SDK and use focused host/provider checks for additional versions where appropriate.
- Test the packed extension in a clean consumer install and through Pi's managed-install flow. Do not attempt to repair other extensions' pruned dependencies as part of this issue.

Exit: declared range, development dependencies, lockfile and documented support match the evidence; package installs and required runtime paths work on admitted versions.

## Work package C — questionnaire routing (#66)

Use `runGoalQuestionnaire` as the shared routing point. Retain the existing `hasUI` and explicit auto-confirm policies. Prefer rich UI when the host exposes a usable component surface; fall back to select/input when it cannot render. A cancelled rich dialog is not an unavailable rich dialog.

Tests cover rich RPC rendering and keystrokes, primitive RPC answers/custom answers, auditor-toggle selection, proposal confirmation, unavailable methods, synchronous/asynchronous errors and cancellation. Verify user input is not accidentally submitted twice after fallback. Avoid broad exception swallowing on terminal hosts. If notifications themselves fail on a disconnected host, return the existing actionable draft failure rather than claiming success.

## Work package D — budget recovery and model-facing accounting (#59/#74/#76)

Reuse the detailed local budget recovery PRODUCT/TECH specification. Main affected files are `goal-drafting.ts`, `goal-core-tools.ts`, `goal-state.ts`, `goal-policy.ts`, `goal-ledger.ts`, `goal-commands.ts`, `goal-events.ts`, `goal-accounting.ts`, `goal-compaction.ts`, `goal-auditor.ts` and `prompts/goal-prompts.ts`.

Budget mutation acceptance matrix:

| Starting condition / action | Required result |
| --- | --- |
| Create without budget | Persist unlimited; report `Budget: none`. |
| Explicit budget 1 | Persist 1, visibly report it; never reinterpret it as unlimited. |
| Tweak with omitted budget | Retain the existing budget and budget-limited state unless another legitimate transition applies. |
| Confirm positive safe integer / explicit null | Set/remove the lifetime cap with old/new values recorded. |
| Cancel, invalid input, stale revision or switched focus | No budget/lifecycle mutation; truthful failure. |
| Remove/raise exhausted cap | Preserve spent usage and allowance; admit continuation only through existing scheduler rules. |
| Lower an active cap to at/below usage | Stop under budget-limited policy; do not permit another substantive autonomous dispatch. |
| Other owner / interrupted or claimed dispatch / exhausted allowance | Preserve required explicit recovery; no ownership theft or allowance renewal. |
| Cross several warning thresholds in one charge | One warning and one ledger event; changed budget resets threshold tracking appropriately. |

Presentation rules:

- Model-facing text must distinguish “cumulative goal token usage across turns” from “current context occupancy.” Budget text explicitly identifies an optional lifetime token-spending limit.
- Show actual context telemetry only when the active host provides it. Handle null tokens after compaction, absent methods, non-finite data, zero/invalid window and model changes. Unknown is not zero.
- State automatic-compaction status only if a supported source makes it known. Avoid promising that compaction or continued work will always succeed; context information is not a completion signal.
- Read telemetry in a valid request-time context and pass plain data into formatters. Never add a deferred context capture while fixing #78.
- Keep human cumulative totals and all accounting math unchanged. Use separate model-facing formatting where shared helpers currently serve both humans and agents.
- Cover active banner, `get_goal`/`update_goal` output, compaction summary, budget-limited steering and auditor metadata. Preserve stable prompt prefixes; telemetry belongs in volatile observations.

Provider-boundary checks capture the real supported SDK serialization before network dispatch. Verify registered schemas for omitted budget/mode, objective reads without a task selector, and mutually exclusive single/batch task updates. Exercise default, true and false compatibility resolution. The report's live A/B is not replaced by a schema test; keep the upstream root cause and version-specific workaround explicitly qualified. The prepared local `UPSTREAM.md` is a draft, not proof of a filed upstream issue.

## Work package E — prompt control and cache integration (#75/#71/#79)

1. Land #71's targeted last-content lookup and negative tests.
2. Integrate #75's independent setting, preserving the stale-checkpoint branch ahead of the ordinary unfocused reminder.
3. Refactor #79's retained policy/counters to the final #59/#76 semantics. Budget value, run-limit value and actionable scheduler instructions belong in reset-on-change state or an equivalent explicit invalidation key. Counter observations can be retained only with unambiguous historical meaning.
4. Use one bounded session-lifetime mechanism for retained tails and matching provider transient metadata. Eviction, shutdown, replacement, no-goal state and unavailable stable session IDs must have explicit behavior. When identity is uncertain, prefer clearing retention over mixing sessions.
5. Preserve full-message validation against history edits, but benchmark hashing/serialization rather than assuming it is cheap. Retention counts, retained bytes and per-request work are separate budgets.

Combined validation matrix:

| Dimension | Required cases |
| --- | --- |
| Provider shapes | OpenAI Responses and Completions; Anthropic; Anthropic-compatible Completions; Bedrock shape tests and real adapter captures where available. |
| Explicit markers | Trailing effort-only message; split and retained live blocks; merged user content; foreign suffix/marker; existing history marker; short/long TTL; caching disabled. |
| Advancing requests | At least three real serialized turns; unchanged retries; growing assistant/tool history; multiple parallel calls/results; tool failures and delayed results. |
| State changes | Objective/tasks; budget removal/increase/decrease; allowance removal/change; ready/wait cleared; user takeover; paused/blocked/completed; unfocus and prompt setting toggles. |
| History/lifecycle | Compaction, middle-message edits, branch moves, empty history becoming real, reload, replacement, provider/model switch, interleaved sessions and missing identity. |
| Bounds | More than 32 changing tails; more than 16 sessions; large objectives/tools/images; no incremental memory leak in the transient map; no persisted full prompt per turn. |

Acceptance requires the current policy to remain correct even when a cache reset costs a miss. Preserve marker TTL/ownership, do not add paid retention, and never insert a live tail between a call and its results. If target history already has a marker, explicitly verify the resulting placement/TTL contract rather than relying on a marker-count assertion alone.

Run a 1,000-turn deterministic retention/context scenario and the existing context/benchmark gates. Compare CPU time, retained bytes, total serialized extension text and reset frequency against main. Use the project's existing NAF tolerance where applicable; choose and record new retention limits from measurements before merging. A live paired provider experiment can assess hit rates later, using a fixed workload and separate reporting of input, cache reads/writes and output. It is not required to make a deterministic bug-fix claim.

## Work package F — configurable storage root (#73)

Proposed first-version contract:

- `PI_GOAL_ROOT` > project `goalsRoot` > global `goalsRoot` > existing `<cwd>/.pi/goals`.
- Accept absolute paths and documented home expansion for overrides. Reject ambiguous relative overrides initially; the legacy default retains its cwd-relative meaning. Diagnose invalid or inaccessible configured roots rather than silently returning an empty pool or falling back to another location.
- An override selects a pool, not a working directory. It must never change where tools, auditing or project resources execute.
- Keep existing default paths byte-for-byte compatible. No automatic copy, merge, git-common-dir inference or global project-key default in this release.
- Root selection is pinned for a loaded session. Require reload/reopen to change it and explain that `/goal-refresh` refreshes the current pool; do not retarget active mutations while a setting changes.

Implementation:

1. Introduce a plain storage-location resolver/context shared by goal files, archive, ledger, locks, ledger checkpoint/index, pool snapshot, recovery/backup and debug artifacts. Audit scripts and tests for hardcoded `.pi/goals` and `.pi/.goals-pool-snapshot.json` references.
2. Preserve the legacy snapshot location for the default pool. For an override, colocate all pool-owned metadata beneath the configured root under documented names. Distinguish this storage-root change from session-JSONL recovery, which may remain separately scoped.
3. Key caches by normalized storage identity, not merely cwd. Carry that identity through refresh, ledger reads, goal service writes and deferred UI snapshots.
4. Preserve confinement and symlink protections at the selected root. Handle platform path normalization and permitted parent aliases deliberately; reject symlinked managed files/directories as appropriate without broadly disabling existing hardening.
5. Use the same lock and revision authority for two worktrees sharing a root. Scope restored session focus to the pool identity so an old session cannot target an unrelated same-ID goal after reconfiguration. Legacy focus entries retain existing default behavior.
6. Document explicit shared-root setup and the distinction between sharing goals and transferring execution ownership. Keep existing recovery tooling and error messages aware of the effective root.

Acceptance: unchanged old layout; explicit shared root across two worktrees; separate roots isolated; concurrent writes and stale revisions safe; archived files and recovery backups stay in the selected pool; no writes leak into the cwd default when an override is selected; unavailable/unsafe roots produce actionable diagnostics. Root deletion must not recreate or select a different pool silently during active work.

## Verification and release gates

For each final code change, run targeted regressions first, then the existing full checks once on the integrated tree:

```sh
npm ci
npm run check
npm run lint
npm run test:all
npm run test:selfcheck
python3 -B scripts/test-update-ranking.py
npm run context:gate
npm run context:provider-check
npm run bench:gate:naf
npm pack --dry-run
npm audit --omit=dev
git diff --check
```

Storage work also exercises the existing settings-race/checkpoint-recovery suites and shared-root multi-process tests. SDK-boundary work runs the compatibility matrix from package B. Context baselines are remeasured with `npm run context:measure` only after explaining intentional drift; do not accept regenerated baselines as validation by themselves.

The current CI runs Linux/Node 24 and does not include the context gate/provider check. Add those deterministic commands to CI as part of this program, with fixture counts/coverage checked so new tests cannot fall outside the manifest. The declared Node floor remains a separate compatibility obligation.

Before release, install the packed artifact into a disposable Pi environment and exercise goal creation, confirmed budget changes, pause/resume, reload, unfocused prompt toggles and supported host dialogs. Record the exact artifact/version and ensure release metadata is consistent. Do not close #59 as “provider normalization fixed” merely because budget repair is shipped.

## Rollback and containment

- Keep host fixes and cache retention separable. If #79 regresses protocol validity or performance, revert its retention layer while retaining #71's explicit-marker fix and clear usage labeling.
- Budget changes use existing goal records and additive ledger information; verify older readers tolerate the added event before release. A rollback must preserve goal identity and historical usage.
- Prompt hiding defaults off and does not rewrite history. Reverting it restores the old reminder behavior without goal migration.
- Storage overrides are opt-in with no automatic migration. Document that an older extension will not discover an external pool; preserve the pool and return to a compatible version rather than copying files behind the user's back.

## Implementation constraints

One integration branch from remote main; preserve the original dirty checkout. No command/tool additions, background watchers, new runtime dependencies, global storage migration or default policy changes. Use bounded in-memory structures and existing configuration/storage primitives. Retention must have measured byte/count bounds and no redundant per-request copies of the full prompt. Extend existing CI/test/benchmark scripts. Implement storage in the same PR with the backward-compatible opt-in contract above.

Storage implementation preserves the cwd-only low-level zero-settings-I/O contract. Host session contexts resolve/pin the configured root; detached storage readers explicitly snapshot it with goalStorageContext. Default-path writes preserve their existing filesystem-operation counts. Compatibility validation includes Pi 0.87.0 discovered during implementation.

CI performance comparisons must measure the PR base and candidate on the same runner with the same SDK installation and corrected B10 fixture. Keep historical campaign headroom targets for their original local before/after gate; use existing no-regression timing/operation tolerances for a current-base comparison.

Follow-up verification targets: a delayed tool result invalidating only a later retained counter anchor must reset all live tails, not duplicate an earlier policy block. Removing optional counters must discard obsolete observations. Extend the existing real-SDK sequence through retries, delayed/error results, audit filtering, policy edits, compaction and retention eviction; benchmark text, tool-output and image-bearing histories. Reuse existing test files for model/session identity, widget replacement and shared-root stale-revision checks.
