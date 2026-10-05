# MILESTONES — PR #24 review + SDK 0.84.1 upgrade

Free-form implementation log for reviewing/landing PR #24 and upgrading the
SDK line in full. Follows the project convention from the root AGENTS.md.

## 2026-08-07 — Task 1: PR #24 review verdict (DONE)

**PR**: #24 "Fix Pi 0.84 lifecycle and fullscreen regressions"
(johnrichardrinehart, fork `fix/pi-0.84-compat`, 2 commits, +49/−5, 4 files).

### Diff audit

1. `extensions/goal-events.ts` — goal auto-continuations are no longer queued
   inside `agent_end`; instead `agent_end` records `continuationAfterSettleFor =
   goal.id` and a new `pi.on("agent_settled")` handler performs
   `core.queueContinuation(ctx, true)` guarded by
   `core.isActionableContinuationGoal(goalId)`. The flag is cleared at the top
   of every `agent_end` and in `session_shutdown`.
2. `extensions/goal-questionnaire.ts` — `computeDialogLineLimit({ terminalRows,
   baseFrameLines })` extracted; preserves the 0.83 formula exactly
   (`min(rows, max(10, rows − base + 1))`, the new `Math.min` cap is a no-op
   for valid input since `base ≥ 1`) and adds a fullscreen fallback
   `min(rows, max(4, rows − 4))` with explicit tiny-terminal coverage.
3. `tests/goal-questionnaire.test.ts` — 7 assertions on the new pure function.
4. `tests/goal-stale-continuation-golden.test.ts` — renamed/updated the
   success-path golden test to the new agent_end → agent_settled contract;
   provider-error guard kept and extended to assert agent_settled stays inert.

### SDK lifecycle verification (pi 0.83.0 source, the peerDependency floor)

`agent-session.js` `_runAgentPrompt`:

```js
try {
    await this.agent.prompt(messages);
    while (await this._handlePostAgentRun()) { await this.agent.continue(); }
} finally {
    ...
    await this._emitAgentSettled();   // → extension emit {type:"agent_settled"}
}
```

`_handlePostAgentRun` drains retries (`_prepareRetry`), compaction
(`_checkCompaction`), and queued messages (`agent.hasQueuedMessages()`).
⇒ `agent_end` fires before pi finishes automatic work; `agent_settled` fires
once, in a `finally`, after all of it — exactly the PR's premise. The event
exists in 0.83.0 typings (`AgentSettledEvent`), and peerDependencies are
`^0.83.0`, so no compatibility break. Abort/provider-error/shutdown paths never
arm the flag; stale-goal protection is re-checked at settle time.

### Verification runs (worktree on PR head `a164490`, local SDK 0.83.0)

- `npm run check` — pass
- `npm test` — 691/691 pass
- `npm run test:serial` — 691/691 pass
- `npm run test:integration` — 28/28 pass
- GitHub merge state: MERGEABLE / CLEAN

### Verdict

**Merge as-is.** Two minor nits (not blockers):

1. `docs/architecture.md` still says "13 lifecycle event handlers" and omits
   `agent_settled` (now 14) — doc ships in the npm tarball; fix on main after
   the merge.
2. 0.84.0/0.84.1 runtime behavior is not reproducible locally (0.83.0
   installed); the fullscreen/no-frame-cache path rests on the author's
   validation. The SDK upgrade (task-2) provides a native 0.84.1 environment to
   re-verify this (task-3).

## 2026-08-07 — Task 2: SDK upgrade to 0.84.1 (DONE)

- `package.json` devDependencies: `@earendil-works/pi-ai`, `pi-coding-agent`,
  `pi-tui` `^0.83.0` → `^0.84.1` (peerDependencies stay `*`).
- `npm install`: 143 added / 5 removed / 3 changed; lockfile now resolves
  `pi-agent-core`, `pi-ai`, `pi-coding-agent`, `pi-tui` all to 0.84.1; zero
  `0.83.0` references remain. `pi-agent-core` now nests under
  `pi-coding-agent` (hoisting change, still 0.84.1).
- Full suite on 0.84.1, **no code changes required**: `tsc` pass; `npm test`
  690/690; `test:serial` 690/690; `test:integration` 28/28. (690 = main
  baseline; PR branch counts 691 due to its one added test.)
- Remaining `0.83` mentions are historical only (CHANGELOG, old spec logs).

## 2026-08-07 — Task 3: PR #24 re-verified on 0.84.1 (DONE)

Worktree at PR head `a164490` (matches GitHub headRefOid) with the upgraded
0.84.1 node_modules (symlink):

- `tsc` pass; `npm test` 691/691; `test:serial` 691/691; `test:integration`
  28/28.
- Native 0.84.1 check of the lifecycle claim: `agent-session.js` still calls
  `_emitAgentSettled()` in the `finally` of `_runAgentPrompt` after the
  retry/compaction/queued-message drain (`_handlePostAgentRun`), and
  `agent_settled` is in the 0.84.1 typings — identical contract to 0.83.0.

**Verdict (final): merge as-is.** Only outstanding nit remains the
`docs/architecture.md` lifecycle-handler list (task-5).

## 2026-08-07 — Tasks 4–5: merge + post-merge verification (DONE)

- PR #24 merged with a merge commit (repo convention): `24fde39` "Merge pull
  request #24 from johnrichardrinehart/fix/pi-0.84-compat", parents `620a13f`
  + `a164490`, 4 files +49/−5. GitHub state MERGED; no conflicts with the
  0.84.1 upgrade (disjoint files).
- `docs/architecture.md` fixed: lifecycle handler list now includes
  `agent_settled`, count 13 → 14 (matches the 14 registered handlers).
- Post-merge main on 0.84.1: `tsc` pass; `npm test` 691/691; `test:serial`
  691/691; `test:integration` 28/28.

Residual notes:
- 0.84.0 (intermediate line) not validated locally; 0.84.1 is the `latest`
  dist-tag and the supported floor is 0.83 via peerDeps `*`/devDeps `^0.84.1`.
- No version bump/release performed (out of scope); CHANGELOG entry can be
  added with the next release.

## 2026-08-07 — Final landing (DONE)

- Committed `60152a3` "chore: bump @earendil-works/pi-ai, pi-coding-agent,
  pi-tui to 0.84.1" (package.json + lockfile + docs/architecture.md +
  MILESTONES.md) on top of the merge `24fde39` and pushed to origin/main
  (fast-forward). Remote main = 60152a3; GitHub API confirms merge commit
  parents (620a13f, a164490).
- Post-merge final: tsc ✓, npm test 691/691, test:serial 691/691,
  test:integration 28/28. Working tree clean.
- Note: local `git log` omits merge-commit lines while `--merges`,
  `rev-list --parents`, `cat-file`, and a fresh clone all confirm the graph
  (60152a3 → 24fde39 → {620a13f, a164490}); fsck clean. Display-only quirk,
  no impact on pushed history. No version bump/release (out of scope).
