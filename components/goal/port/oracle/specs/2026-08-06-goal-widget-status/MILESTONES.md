# Milestones: Move goal status from the pi footer into the goal widget

## Plan

1. Branch `feat/goal-widget-status` off `main` (15aa826) + spec scaffold.
2. Renderer: status lines become `goal: <statusLabel> [<elapsed> <tokens>]
   (+N open)` in compact + expanded; drop `Focused:` / `Other goals:` bits
   and the header usage-right slot.
3. Footer: focused-goal `setStatus("goal", ...)` removed; unfocused hint kept.
4. Tests/goldens updated + new pins for compact and expanded.
5. Docs (CHANGELOG Unreleased, PRODUCT/TECH/MILESTONES), full validation,
   commit on the branch.

## 2026-08-06 — branch + scaffold

- `git checkout -b feat/goal-widget-status` off `main` (15aa826, post v0.25.1).
- Scaffolded `specs/2026-08-06-goal-widget-status/`.
- User decisions (goal questionnaire): status line shows
  `goal: <status> [<elapsed> <tokens>] (+N open)` (no objective preview;
  header title covers it); focused-goal bottom line removed, unfocused hint
  kept.

## 2026-08-06 — renderer + model + footer removal

- `goal-dashboard-model.ts`: `status.footerLabel` = `statusLabel(goal)`
  (footer label parity); `usage.footerBits` = compact duration + compact
  tokens; removed now-dead `elapsedLabel`/`tokenLabel`.
- `goal-dashboard-renderer.ts`: shared `statusLine()` helper; compact +
  expanded status lines show `goal: <label> [<usage>] (+N open)`; header
  usage-right slot removed; `STATUS_SYMBOL` and dead spec options
  (`showFocused`, `showOtherGoals`, `statusLine`) removed.
- `goal-state.ts`: focused-goal footer line replaced with
  `setStatus("goal", undefined)`; unfocused hint untouched.
- Findings along the way: `formatDashboardDuration` is still used by verbose
  `/goal-status` and the auditor dashboard (kept); `model.focused` still used
  by verbose mode (kept); `statusLine` spec option was already dead config.

## 2026-08-06 — tests + validation

- Updated pins: goal-widget.test.ts (7 edits, sisyphus fixture →
  `goal: sisyphus running [1m05s 2.5K]`), goal-dashboard-golden.test.ts
  (5 compact + new expanded status-line pin), goal-status.test.ts, and
  goal-dashboard-model.test.ts (`footerLabel` deepEquals + `footerBits`).
- New guard in no-status-refresh-timer.test.ts: footer removal pinned
  (no `footerStatus(displayGoal)`, focused branch clears, unfocused hint
  stays).
- `npm run check` clean; full `npm test` green.
- CHANGELOG: `## [Unreleased]` → Changed entry. Committed on
  `feat/goal-widget-status`.
