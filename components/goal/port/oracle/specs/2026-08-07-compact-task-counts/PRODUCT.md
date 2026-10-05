# Product: Compact dashboard task counts, header bar, and subtask markers

## Status

Confirmed goal (2026-08-07), implementation in progress.

## Motivation

The compact dashboard's "Tasks" section is opaque at a glance: the section
rule is a plain `├─ Tasks ────┤` header, the only counts live on a separate
progress line (`Tasks  [bar] 3/5 · 60%`) above the list, and subtasks are
invisible in the task list itself (only the current task gets a subtask bar
below). Per user decisions (goal questionnaire, then a follow-up tweak):

1. The compact "Tasks" header row should carry the task counts.
2. A simple progress bar should sit at the **end** of that header row.
3. The standalone compact progress line is redundant and should be removed
   (the expanded dashboard keeps its Progress section unchanged).
4. Tasks that have subtasks should show a per-row `▸ done/total` marker so
   subtasks are clear without expanding.
5. Follow-up: the current task's subtask progress bar moves **next to** the
   task progress bar in the same header row (short `Sub done/total` label),
   and the standalone compact `Subtasks  [bar] 2/3 · 67%` line is removed.

## Behavior

### Tasks header row (compact only)

The compact section rule changes from:

```
├─ Tasks ──────────────────────────┤
```

to a single row with counts first, then a compact progress bar, then the
current task's subtask bar beside it:

```
├─ Tasks · ✓3 done · 2 open ──── [█████░] · Sub 2/3 [██░░] ┤
```

- **Counts** (first): `✓N done · M open`, muted gray (part of the frame
  tone, like the section label). `N` = top-level tasks complete **or
  skipped** (a skipped task counts as done); `M` = top-level tasks pending.
  Both are always shown (`· 0 open` when every top-level task is done).
- **Task bar**: compact progress bar after a short dash run, before the
  closing frame. Neutral-gray fill, dim empty cells (same visual system as
  the content bars). Width per mode: wide 8, medium 6, narrow 5, minimal 4.
- **Subtask segment** (end of the row, beside the task bar): `· Sub
  done/total` followed by the current task's compact subtask bar. Rendered
  only when a current task exists **and** it has subtasks; omitted when the
  current task is a leaf or there is no current task (all done). Width per
  mode: wide 4, medium 3, narrow 2 (minimal omits the segment, see below).
  The fraction uses the same "done = complete or skipped" rule as the
  current-task subtask bar.
- **Width rules**: the counts and both bars always survive; only the dash
  run and the muted label may shrink. In **narrow** mode the segment drops
  the word `Sub` (`· 2/3 [██]`) so the full counts fit from width 50; in
  **minimal** mode (< 50) the segment is omitted entirely — the counts and
  task bar alone cannot share 38 inner columns with a second bar, and the
  pre-existing rule already hid subtask progress at minimal (the per-row
  `▸ done/total` markers keep subtasks visible there).

### Standalone progress lines removed (compact only)

- The §9.1 line `Tasks  [bar] X/Y · %` is gone — the header row now carries
  both the counts and the task bar.
- The compact `Subtasks  [bar] 2/3 · 67%` line below the current task is
  gone — the header's subtask segment now carries the current task's subtask
  progress.

The expanded dashboard is unchanged: its `Progress` section
(`[bar] X/Y tasks · %`) and the Current-task block's
`Subtasks  [bar] 2/3 · 67%` line stay byte-identical.

### Per-row subtask markers (compact list only)

Top-level task rows in the compact list that have direct subtasks gain a
marker at the end of the row:

```
· t3  Add the download button ☑ ▸ 2/3
```

- Format `▸ done/total` over the task's **direct** children (complete or
  skipped counts as done — same rule as the current-task subtask bar).
- Muted gray, appended after the title and the contract mark; the title
  truncation budget accounts for the marker so rows stay width-safe.
- Leaf tasks (no subtasks) show no marker.
- The expanded tree is unchanged (subtasks are already inline there, so
  markers would be redundant).

## Scope

In scope: `extensions/widgets/goal-dashboard-renderer.ts` (header row with
counts + task bar + subtask segment, standalone-line removals, marker),
golden/unit/e2e tests, README + docs examples, CHANGELOG, spec docs. The
model layer already provides everything needed (`DashboardTaskNode`
subtask counts, `model.taskProgress`, `model.currentTask` subtask fields) —
no model change this round.

Out of scope: the expanded dashboard (Progress section, Tasks tree, Current
task block), the audit dashboard, the status/footer lines, task-list
semantics (plan order, viewport/scroll behavior), `/goal-status` verbose
mode, and any other renderer sections.

## Validation

- `npm run check` (tsc --noEmit): clean.
- `npm test`: full suite green; goldens pinned to the new header format
  (`· Sub 2/3 [bar]`) and the absence of the standalone compact Subtasks
  line.
- `npm run test:integration`: green.
- Width safety: every rendered line stays within the terminal width across
  wide/medium/narrow/minimal modes and all dashboard states.
- Grep: no standalone compact `Subtasks  [bar]` line remains in
  extensions/tests (the expanded Current-task line and per-row markers are
  allowed).
