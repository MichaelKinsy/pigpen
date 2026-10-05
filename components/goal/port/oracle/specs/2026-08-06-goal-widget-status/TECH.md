# Tech: Move goal status from the pi footer into the goal widget

## Affected files

| Change | File | Detail |
|---|---|---|
| Status lines | `extensions/widgets/goal-dashboard-renderer.ts` | Compact (`renderCompactDashboard`) + expanded (`renderExpandedDashboard`) status lines show `goal: <statusLabel> [<elapsed> <tokens>] (+N open)`; drop `Focused:` / `Other goals:` bits |
| Usage fold-in | `extensions/widgets/goal-dashboard-renderer.ts` | `usageRight` header slot removed (usage moves into the status line) |
| Footer removal | `extensions/goal-state.ts` | `renderUI` no longer calls `setStatus("goal", ...)` for a focused goal; unfocused hint kept |
| Shared helpers | `extensions/goal-core.ts` | Reuse `statusLabel` + the usage-bits logic from `footerStatus` (extract a `footerUsageBits`-style helper if cleaner) |

## Design

### New status-line text

Shared `statusLine(theme, model)` helper in `goal-dashboard-renderer.ts`:

```ts
// compact + expanded, non-complete status:
//   goal: running [49h49m36s 19M] (+3 open)
// complete status keeps its own "✓ All required work is complete." line.
const usage = model.usage.footerBits ? ` [${model.usage.footerBits}]` : "";
const open = model.otherOpenGoals > 0 ? ` (+${model.otherOpenGoals} open)` : "";
return `${theme.fg(color, `goal: ${model.status.footerLabel}`)}${muted(theme, `${usage}${open}`)}`;
```

- `footerLabel` (new on `DashboardGoalStatus` and the inline `status` type):
  `goal-core.statusLabel(goal)` — `running` (active+autoContinue), `paused
  (agent)`, `blocked`, `budget limited`, `active`, `complete`, with a
  `sisyphus ` prefix — the footer's label set, unifying the widget's
  previously divergent `In progress`/`Idle`/… labels with the footer.
- `usage.footerBits` (new): compact duration + compact token count
  (`49h49m36s 19M`) — same formatting as `footerStatus`; empty when the goal
  has no usage. `elapsedLabel`/`tokenLabel` were removed (the header
  usage-right slot they fed is gone; usage now lives in the status line).
- `STATUS_SYMBOL` removed (the status line is literal footer text, no symbol);
  `STATUS_COLOR` stays for label coloring.
- Dead spec options removed: `showFocused`, `showOtherGoals`, `statusLine`.
  `model.focused` stays (verbose `/goal-status` uses it).

### Footer removal

`renderUI` (goal-state.ts) no longer composes the focused-goal footer line
(`footerStatus(displayGoal) + (+N open)`); it clears the segment with
`ctx.ui.setStatus("goal", undefined)` so a stale unfocused hint or earlier
status never lingers once a goal is focused. The unfocused branch still sets
`goal: unfocused [N open] - /goal-focus`.

## Tests

- `tests/goal-widget.test.ts` — replace `● In progress · Focused: yes` and
  `Other goals: 2` pins with the new `goal: ...` format (compact).
- `tests/goal-dashboard-golden.test.ts` — update `● In progress · Focused:
  yes · Other goals: 2` golden line; add expanded-dashboard status-line pin.
- `tests/goal-status.test.ts` — update any `/goal-status` text that flowed
  through the shared renderer.
- `tests/goal-service.test.ts` — fix any status-line pins in service-level
  render assertions.
