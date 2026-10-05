# Tech: Compact dashboard task counts, header bar, and subtask markers

Spec: `2026-08-07-compact-task-counts` (see PRODUCT.md for behavior).

## Model (`extensions/widgets/goal-dashboard-model.ts`)

Unchanged this round — the previous milestone already added everything the
header needs:

- `DashboardTaskNode.totalSubtasks` / `completedSubtasks` (direct children,
  skipped counts as done) for the per-row `▸ done/total` markers.
- `model.taskProgress` (`deriveTopLevelTaskProgress`): `done = completed`
  (complete + skipped), `open = total - completed` for the header counts.
- `model.currentTask.completedSubtasks` / `totalSubtasks` /
  `subtaskPercentage` for the header's subtask segment.

## Renderer (`extensions/widgets/goal-dashboard-renderer.ts`)

### Section rule with a right component

`boxSectionRule` keeps its optional `right` argument rendered at the end of
the row, before the closing `┤`, with the fill dashes between label and
right:

```ts
function boxSectionRule(theme: Theme, width: number, label: string, right = ""): string
```

Two hardening changes (latent overflow when label + right exactly fill the
inner width — now reachable because the right side carries two bars):

- fill becomes `Math.max(0, inner - visibleWidth(l) - visibleWidth(r))` (was
  `Math.max(1, …)`, which pushed a row 1 char over when l + r == inner);
- the truncation budget becomes `inner - visibleWidth(r) - 3` (was `-2`),
  so `l = "─ " + fit(label, budget) + " "` always satisfies `l + r ≤ inner`.

### Compact header row (both bars)

In `renderCompactDashboard`, the `Tasks` section rule becomes:

```
├─ Tasks · ✓N done · M open ──── [█████░] · Sub 2/3 [██░░] ┤
```

Composition (counts and `Sub` text muted — the header stays one frame-tone
block):

- label: `Tasks · ✓N done · M open` (single string; muted via `frame()`)
- right: task bar `progressBar(theme, pct, headerBarWidth)`
  (8/6/5/4), then, when `model.currentTask` exists with
  `totalSubtasks > 0` and `mode !== "minimal"`, the subtask segment:

```ts
const subBar = progressBar(theme, model.currentTask.subtaskPercentage, spec.subtaskBarWidth);
headerRight += `${muted(theme, ` · ${mode === "narrow" ? "" : "Sub "}`)}${muted(theme, `${c}/${t}`)} ${subBar}`;
```

- `subtaskBarWidth`: wide 4, medium 3, narrow 2, minimal 2 (unused — the
  segment is omitted at minimal; at 40 cols the counts + task bar alone
  leave no room for a second bar, and the pre-existing rule already hid
  subtask progress at minimal).
- **narrow** (50–69) drops the word `Sub` (` · 2/3 [██]`): the full counts
  label (25 cols) + task bar (5) + `Sub 2/3` (7) + sub bar cannot coexist at
  width 50 (28 + 24 > 48); the short form (28 + 20 = 48) fits exactly.
- The old standalone `Tasks  [bar] X/Y · %` block and the compact
  `Subtasks  [bar] 2/3 · 67%` line are deleted. The expanded dashboard's
  Progress section and `renderCurrentTaskBlock`'s Subtasks line are
  untouched.

### Per-row subtask markers (compact list only)

`renderCompactTaskRows` appends a muted ` ▸ n/m` marker to rows where
`node.totalSubtasks > 0` (already implemented; unchanged this round). The
title budget subtracts the marker's visible width so rows stay width-safe;
`fit` still truncates as the last line of defense. Expanded `renderTaskRow`
is untouched.

## Tests

- `tests/goal-dashboard-model.test.ts`: unchanged (node fields already
  pinned by the previous milestone).
- `tests/goal-dashboard-golden.test.ts`:
  - compact 100: header regex becomes
    `/├─ Tasks · ✓3 done · 2 open ─+ \[.*\] · Sub 2\/3 \[.*\] ┤/`; the
    `Subtasks \[.*\] 2\/3 · 67%` assertion moves from the standalone line to
    the header segment (the standalone compact line is gone);
  - minimal 40: unchanged (no segment at minimal);
  - palette test: the muted fraction `2/3` now comes from the header
    segment (still a standalone muted span);
  - width safety at 40/50/60/80/100/140: unchanged — must stay green with
    the two-bar header.
- `tests/e2e/goal-lifecycle-dashboard.test.ts`, `tests/goal-widget.test.ts`,
  `tests/goal-status.test.ts`, `tests/goal-features.test.ts`: any assertion
  on the standalone compact `Subtasks` line moves to the header segment.

## Validation

`npm run check` clean, `npm test` green, `npm run test:integration` green,
grep for the standalone compact `Subtasks  [bar]` line returns nothing in
extensions/tests, CHANGELOG entry added, README + docs examples refreshed.
