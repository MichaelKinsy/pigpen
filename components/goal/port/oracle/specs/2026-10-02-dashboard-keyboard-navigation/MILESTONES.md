# Milestones — dashboard keyboard navigation and single-press handling

## Provenance

PR #85 by `x12315` (montana), two commits imported unchanged via a maintainer merge commit on `incoming/pr-85`:

- `7b2a0a1` fix(widget): connect dashboard scroll keys to live component
- `cc7ffff` fix(widget): handle dashboard key event lifecycle

The maintainer commit adds only the CHANGELOG entry and this spec directory. Commits cannot be added to the pull request from outside the contributor's fork, so the contributor's commits are included here unchanged instead; the branch is otherwise ready to merge.

## Defect evidence on `main` (verified 2026-10-02)

```
$ rg -n "goalWidgetComponentRef\.current\s*=" extensions tests
extensions/goal-state.ts:577:		goalWidgetComponentRef.current = null;

$ git log -S"current = component" --all -- extensions/     # only this PR's commit
```

The assignment had never existed, so `handleNavigationKey` (`extensions/goal-widget.ts:183`) and `handleCompactScrollKey` (`:189`) always ran against a null reference.

## Validation on the branch tip

- `npx tsc --noEmit` — clean.
- `npx eslint .` — clean.
- `node scripts/run-unit-tests.mjs` — 1050/1050 pass.
- `node scripts/run-unit-tests.mjs integration` — 31/31 pass (same as `main`).
- `node scripts/run-unit-tests.mjs e2e` — 20/20 pass.
- `npm run context:gate` — PASS (24 fixtures, no baseline re-measurement needed).

The contributor's report of unit 1043 / integration 31 / e2e 20 was reproduced independently on this machine rather than taken on trust; the unit count differs only because it was measured against the current `main`.

## Design-philosophy audit

- New user command: none.
- New setting: none.
- Scheduler-state or lifecycle-status change: none.
- Prompt growth: none. `extensions/prompts/` is untouched and the context gate passes against the unmodified baseline.
- Filesystem complexity: none.
- Host assumptions: unchanged. The Kitty repeat/release check is a local helper precisely because the declared peer range cannot be assumed to export `isKeyRepeat`/`isKeyRelease`; no new host API is required.
- README, `specs/SPECS.yaml`, `package.json`, and the host range: untouched.
- Smallest effective change: yes. The only product code is the component reference wiring, one input guard, one settings-read relocation, and three scroll-handler corrections, each tied to an observable defect. No refactor is bundled.

## Accepted consequences

- While the dashboard is expanded it owns the plain arrow keys even when its task list fits, so the editor cursor cannot be moved with arrows in that state. This matches the pre-existing contract asserted by `tests/goal-widget.test.ts` and is left explicit rather than implicit.
- `loadGoalSettings()` now runs per keypress. It is cached (`extensions/goal-settings.ts:706`); the copy on each call is acceptable for keyboard input.