# Milestones: Tweak auto-resume, auditor persistence through tweaks, and compact auditor toggle

Implementation log. Round numbers match the goal task list.

## Round 1 — spec docs (2026-08-07)

- Created `specs/2026-08-07-tweak-resume-auditor-toggle/` with PRODUCT.md,
  TECH.md, and this log.
- PRODUCT.md: three behaviors (tweak auto-resume from paused/blocked only —
  user decision via goal questionnaire, budget_limited stays a hard gate;
  tweak confirmation defaults to the goal's persisted per-goal
  `skipAuditor`; compact `Auditor  on/off` line + `Ctrl+Shift+A` toggle).
- TECH.md: mapped the exact touch points:
  - `goal-drafting.ts` confirm path — mutate gains `status: stalled ? "active"
    : goal.status` + cleared `stopReason`/`pauseReason`/`pauseSuggestedAction`;
    `goal_resumed` (reason `tweak`) event + `beginAccounting` +
    `queueContinuation(ctx, true)` after apply.
  - `startGoalDrafting` — tweak-mode default
    `!(targetGoal?.skipAuditor ?? loadGoalSettings(ctx.cwd).disabled)`.
  - New `auditor_toggled` ledger event across `goal-ledger.ts` /
    `goal-activity.ts` / `goal-compaction.ts`.
  - `toggleGoalAuditor(ctx)` on `GoalCore` (goal-state.ts) + `ctrl+shift+a`
    branch in `goal-widget.ts` processInput; modal-depth/no-goal/complete
    guards.
  - Model `auditorEnabled` + compact-only muted line (wide/medium/narrow/
    minimal truncation of the key hint).
- Baseline: repo at v0.25.2 (released 2026-08-07), `npm run check` clean,
  `npm test` 667/667, `npm run test:integration` 28/28.

## Round 2 — tweak resume + auditor persistence (goal-drafting.ts) — DONE

- **Fixed a subtle bug during implementation**: `/goal-resume` sets
  `autoContinue: true` explicitly (pause sets it false); the tweak-resume
  mutate initially omitted that, which would have left a revived goal
  `active` but never auto-continuing. Added `autoContinue: stalled ? true
  : goal.autoContinue` to the resume branch.
- `goal-drafting.ts` confirm path: mutate now transitions paused/blocked →
  active (status + autoContinue + cleared stopReason/pauseReason/
  pauseSuggestedAction); budget_limited and active untouched. Post-apply
  glue (only when `resumed`): `goal_resumed` ledger event (reason `tweak`),
  then `clearContinuationState()` BEFORE `beginAccounting()` +
  `queueContinuation(ctx, true)` (clear cancels scheduled continuations, so
  ordering matters). Ledger append wrapped in try/catch so a failure cannot
  crash the tweak.
- `startGoalDrafting`: tweak mode defaults `auditorEnabled` from
  `targetGoal?.skipAuditor` with global-settings fallback; other modes
  unchanged.
- Tests: 8 new in `tests/goal-drafting.test.ts` (paused→active with
  metadata clearing + exactly one `goal_resumed` with reason `tweak`;
  blocked→active; budget_limited stays hard-gated with no resume event;
  active stays active with no resume event; per-goal skipAuditor true→
  draft defaults disabled; unset→on; unset+global disabled→off).
- Validated: `npm run check` clean; `npm test` 674/674.

## Round 3 — compact auditor toggle — DONE

- Model: `GoalDashboardModel.auditorEnabled` derived from
  `goal.skipAuditor !== true` (undefined → on).
- Compact renderer: muted `Auditor  on/off` line after the budget line;
  hint scales with layout (wide/medium ` · Ctrl+Shift+A: off/on`, narrow
  ` · Ctrl+Shift+A`, minimal none); `boxLine` + `fit()` keep it width-safe
  at every width. Expanded dashboard untouched (byte-identical goldens).
- Ledger/activity/compaction: `auditor_toggled { goalId, enabled, at }`
  union + validation + sanitize; activity label ("Turned the independent
  auditor on/off."); compaction recent-events line.
- `goal-state.ts`: `toggleGoalAuditor(ctx)` — guards (no goal → info
  notify; complete → info notify), computes `nextEnabled` from the CURRENT
  state (`skipAuditor === true`), applies per-goal `skipAuditor` flip via
  goalService.apply (revision-safe), emits the ledger event, flushTurn,
  updateUI, notify. **First attempt inverted nextEnabled** (`!== true`);
  caught by the new F7 test.
- `goal-widget.ts`: `ctrl+shift+a` branch after `ctrl+shift+t` (before
  navigation/debug keys) → `toggleGoalAuditor` + widget invalidate; the
  existing `goalModalDepth > 0` guard makes it inert while a goal modal is
  open.
- Tests: model derivation; golden test pinning `Auditor  on · Ctrl+Shift+A:
  off` at 80/100, narrow `· Ctrl+Shift+A` (no `: off`) at 60, minimal
  `Auditor  on` (no hint) at 40, off-state `Auditor  off · Ctrl+Shift+A:
  on`, expanded does not render `Auditor`; widget keybinding + modal-guard
  tests; F7 core tests (flip + persist to the goal file + 2
  `auditor_toggled` events + notifications + no-goal/complete guards).
  **Test pitfall: `parseGoalFile` needs `path.join(cwd, activePath)` —
  activePath is relative.**
- Validated: `npm run check` clean; `npm test` 681/681;
  `npm run test:integration` 28/28.

## Round 4 — objective length limit → setting — DONE

- `goal-settings.ts`: `objectiveMaxChars` (0/unset = no limit) across
  interface, `asNonNegativeInt` parser, allowed keys, parse/load with
  `PI_GOAL_OBJECTIVE_MAX_CHARS` env override, save/clean, envOverrideFor,
  effectiveSettingsReport row.
- `goal-commands.ts`: settings-menu row under Goal behavior
  ("max objective length (0 = none)", min 0), `/goal-tweak` command rejects
  oversized replacements per the configured limit (no draft starts).
- `goal-core-tools.ts` create_goal: reject only when a positive limit is
  configured; schema description drops the hard 1-4000 wording.
- `goal-drafting.ts` propose_goal_draft: dynamic `at most N characters`
  rejection; default accepts arbitrarily long objectives.
- Tests: core-tools (default accepts 5000; configured 100 rejects 101 /
  accepts 100), goal-settings (parse/load/env/save/report incl. 0), menu
  row test, drafting (long objective accepted by default; configured limit
  enforced; /goal-tweak command rejection), integration settings-menu row
  count 9 → 10.
- Validated: `npm run check` clean; `npm test` 690/690;
  `npm run test:integration` 28/28.

## Round 5 — tests/docs/validation — DONE

- CHANGELOG [Unreleased]: Changed (tweak auto-resume; tweak auditor default;
  objective length limit → setting) + Added (compact auditor line +
  Ctrl+Shift+A toggle).
- README.md: compact example gains the `Auditor  on · Ctrl+Shift+A: off`
  line; Configuration section documents `objectiveMaxChars` /
  `PI_GOAL_OBJECTIVE_MAX_CHARS` (0 = no limit, the default).
- docs/unified-dashboard.md: compact example + row list (renumbered) +
  keybinding table gain the auditor line/toggle; layout variants documented
  (wide/medium suffix, narrow chord-only, minimal bare).
- `goal-settings.ts` header documents the new env var.
- grep audit: zero `exceeds 4000` / `between 1 and 4000` / `1-4000` stale
  refs (only descriptive test names remain); zero `Subtasks  [`/`Tasks  [`.
- Final: `npm run check` clean; `npm test` 690/690 (baseline 667 → +23);
  `npm run test:integration` 28/28.

_Planned._ README.md + docs/unified-dashboard.md compact examples refreshed,
CHANGELOG entry under [Unreleased], `npm run check` clean, `npm test` +
`npm run test:integration` green, grep confirms no stale references.

## Round 6 — user rework: auditor indicator → bottom-right border dot (v0.25.3)

User steered mid-release: the standalone `Auditor  on · Ctrl+Shift+A: off`
content line was too loud. Final shipped design:

- The content line is removed entirely. The auditor status is now a single
  dot integrated into the bottom-right of the compact box border — `●`
  green (`success`) when the focused goal's independent auditor is on,
  muted gray when off — drawn right before the `╯` corner.
- `boxFooter` gains a right-corner slot (mirrors `boxHeader`'s right-meta
  treatment: ` ● ─` before the corner); the dot always survives, the hint
  truncates. All other `boxFooter` callers (expanded, audit, focus-required)
  are unchanged → expanded dashboard stays byte-identical.
- The ` · Ctrl+Shift+A` chord hint is appended to the footer hint in
  wide/medium layouts only; narrow/minimal keep the dot alone.
- Toggle semantics, `auditor_toggled` ledger event, persistence, and inert
  guards are untouched. Golden test rewritten with a color-capturing theme
  (asserts `success ●` on / `muted ●` off); README, unified-dashboard doc,
  and CHANGELOG 0.25.3 entry describe the dot design.

## Round 7 — post-release steer: right-aligned toggle note (v0.25.4)

User feedback after v0.25.3 shipped: the ` · Ctrl+Shift+A` chord glued to
the left hint was easy to miss and didn't say what it does. Final design:

- The chord no longer appends to the left hint. The wide/medium footer
  right-aligns a `Ctrl+Shift+A: toggle auditor` note (same frame tone as the
  hint) directly left of the colored border dot — the `boxFooter` right slot
  pushes both to the right edge by construction, so it is right-aligned in
  every wide/medium width (≥70; the 28-char note fits with 7+ fill dashes
  even at the narrowest medium).
- The note states explicitly that the shortcut turns the auditor on and off.
- Narrow/minimal keep just the dot. Toggle behavior, keybinding,
  `auditor_toggled` ledger event, and inert guards unchanged; expanded
  dashboard byte-identical (all other `boxFooter` callers pass no right).
- PRODUCT.md/TECH.md updated to the dot design (Round 6 had only logged in
  MILESTONES); golden test asserts note adjacency `…toggle auditor ● ─╯`,
  right-alignment (`expand tasks─+ Ctrl+Shift+A`), and dot-only narrow.
  Validation: `npm run check` clean, 690/690 unit, 28/28 integration.
