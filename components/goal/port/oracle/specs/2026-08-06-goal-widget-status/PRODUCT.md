# Product: Move goal status from the pi footer into the goal widget

## Status

Implemented + validated on `feat/goal-widget-status` (2026-08-06): renderer
status lines, footer removal, tests/goldens updated, full suite + tsc green,
committed on the branch.

## Motivation

The goal status currently lives in two places: the goal widget (above the
editor) shows a status line with `Focused: yes` / `Other goals: N`, and the
bottom status line (`setStatus("goal", ...)`) shows the richer footer text:

```
goal: running [49h49m36s 19M] - # REQUIREMENTS — `pystata-x` ... (+41 open)
```

The `Focused:` / `Other goals:` bits carry little information. Per user
decision (goal questionnaire), the widget becomes the single home for goal
status: the widget status line shows the footer-status info and the
focused-goal bottom line is removed.

## Behavior

- **Compact + expanded dashboard status lines** (previously
  `<symbol> <label> · Focused: yes · Other goals: N`) become:
  `goal: <statusLabel> [<elapsed> <tokens>] (+N open)` — footer-status
  formatting (`statusLabel` + usage bracket), with **no objective preview**
  (the header title already shows it) and the open-goals count appended only
  when `N > 0`.
- The header's right-side usage (`49h 49m 36s · 19M`) is removed — the usage
  bracket now lives in the status line, avoiding duplication.
- **Focused-goal footer line removed**: `renderUI` stops calling
  `setStatus("goal", ...)` when a goal is focused. The unfocused hint
  (`goal: unfocused [N open] - /goal-focus`) is kept — it has no widget
  equivalent.
- Applies to every renderer that used the `Focused:` / `Other goals:` bits
  (compact dashboard, expanded dashboard, and therefore `/goal-status` which
  shares the renderer).

## Validation

- `npm run check` (tsc --noEmit): clean.
- `npm test`: full suite green (664+ pass).
- Grep: no `Focused:` / `Other goals:` text remains in the renderers or
  tests; `footerStatus(displayGoal)` composition gone from goal-state.ts.
- Regression guards: updated goldens pin `goal: running [12m47s 18.2K]
  (+2 open)` in compact + expanded; model tests pin `footerLabel` and
  `footerBits`; no-status-refresh-timer test pins the footer removal.

## Scope

In scope: `extensions/widgets/goal-dashboard-renderer.ts` (status lines),
`extensions/goal-state.ts` (footer removal), any now-unused model fields /
spec options, tests + goldens, CHANGELOG, spec docs.

Out of scope: pi's own footer segments (token stats, model, git branch),
the unfocused hint, task-list layout, the `/goal-status` verbose mode, and
any other renderer sections.
