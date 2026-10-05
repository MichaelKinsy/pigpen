# Milestones — compact dashboard task counts, header bar, and subtask markers

Spec: `2026-08-07-compact-task-counts`

## 2026-08-07 — Spec + confirmation

- User request: counts in the compact "Tasks" header row; make subtasks clear.
- Goal questionnaire confirmed: `✓N done · M open` (skipped = done) in the
  header; remove the standalone compact progress line (bar stays in
  expanded); per-row `▸ done/total` markers; keep the current-task subtask
  bar.
- Refinement: header carries a simple progress bar too; the bar sits at the
  **end** of the header row (counts first).
- Goal confirmed (id `msilpn44-qqa4fa`); PRODUCT.md / TECH.md written.
- Scope mapping: renderer (`goal-dashboard-renderer.ts`), model
  (`goal-dashboard-model.ts`), goldens + e2e + model tests, CHANGELOG.

## Implementation log

### 2026-08-07 — Round 1: header counts + bar, subtask markers (goal `msilpn44-qqa4fa`)

- **Model**: `DashboardTaskNode` gains required `totalSubtasks` /
  `completedSubtasks` (direct children, skipped counts as done);
  `flattenTaskTree` computes them while walking. Header counts reuse
  `model.taskProgress` (`done = completed`, `open = total - completed`).
- **Renderer**: `boxSectionRule` gained an optional right component (label
  truncates, right survives); `LayoutSpec` gained `headerBarWidth`
  (8/6/5/4 for wide/medium/narrow/minimal). Compact header is now
  `Tasks · ✓N done · M open` + compact progress bar at the end of the row;
  the standalone `Tasks  [bar] X/Y · %` line is removed; compact task rows
  with subtasks show a muted `▸ done/total` marker (title budget accounts
  for it). Expanded dashboard and `renderTaskRow` untouched.
- **Tests**: model tests pin the new node fields (parent 2/3, leaves 0,
  skipped-as-done); goldens updated (header format + bar at end, marker on
  t3, leaf rows clean, standalone line gone, complete state `✓5 done · 0
  open`); palette test pins muted header counts + marker; e2e and
  goal-widget/goal-status/goal-features assertions moved to the header
  format.
- **Docs**: README and docs/unified-dashboard.md compact examples refreshed
  to the current layout (status line, header counts + bar, subtask marker);
  CHANGELOG entry added under Unreleased.
- **Validation**: `npm run check` clean; `npm test` 667/667 green;
  `npm run test:integration` 28/28 green; grep confirms no
  `Tasks  [` standalone line remains in extensions/tests.

## Follow-up goal (2026-08-07) — subtask bar beside the task bar

- User tweak: the current task's subtask progress bar should sit **next to**
  the task progress bar in the same Tasks header row; the standalone compact
  `Subtasks  [bar] 2/3 · 67%` line is removed.
- Layout choice (goal_question, option B): `├─ Tasks · ✓N done · M open ─
  [task bar] · Sub done/total [subtask bar] ┤` — short `Sub` label, fraction
  inline, subtask segment only when the current task has subtasks.
- Goal re-proposed (the first goal had completed and archived mid-draft,
  invalidating the /goal-tweak flow) and confirmed as `msim9m81-5aa98r`
  with three tasks: spec docs, renderer, tests + docs + validation.

### 2026-08-07 — Implementation (follow-up)

- **Design notes (width math)**: the full counts label is 25 cols; with a
  second bar and the `Sub 2/3` text the header cannot coexist with the full
  counts at width 50 (28 + 24 > 48 inner) or 40 (28 + 19 > 38). Resolved:
  narrow (50–69) drops the word `Sub` (` · 2/3 [██]`, sub bar 2) so
  counts + both bars fit exactly at 50; minimal (< 50) omits the subtask
  segment entirely (counts + task bar only — same gate as the removed
  standalone line; per-row `▸` markers keep subtasks visible).
- **Renderer**: `LayoutSpec` gains `subtaskBarWidth` (4/3/2/2); the compact
  Tasks header right side becomes task bar + subtask segment
  (` · Sub done/total ` + sub bar, wide/medium; ` · done/total ` narrow)
  when the current task has subtasks and the mode is not minimal;
  the standalone compact `Subtasks  [bar] 2/3 · 67%` line is deleted.
  `boxSectionRule` hardening: fill `Math.max(0, …)` and truncation budget
  `-3` — the old `Math.max(1, …)` overflowed a row by 1 char when
  label + right exactly filled the inner width (now reachable with two
  bars). Expanded dashboard and `renderCurrentTaskBlock` untouched.
- **Validation**: `npm run check` (tsc --noEmit) clean; `npm test`
  667/667 green; `npm run test:integration` 28/28 green; width-safety
  goldens at 40/50/60/80/100/140 green; grep returns no standalone compact
  `Subtasks  [` line and no `Tasks  [` line in extensions/tests; README and
  docs/unified-dashboard.md compact examples refreshed (70/90 cols,
  header with counts + both bars, no standalone subtask line); CHANGELOG
  updated under Unreleased.
