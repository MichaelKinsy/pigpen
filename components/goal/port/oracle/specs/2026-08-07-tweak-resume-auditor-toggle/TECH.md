# Tech: Tweak auto-resume, auditor persistence through tweaks, and compact auditor toggle

## Files touched

| File | Change |
| --- | --- |
| `extensions/goal-drafting.ts` | Tweak resume on confirm; tweak-mode auditor default from `targetGoal.skipAuditor` |
| `extensions/goal-ledger.ts` | New `auditor_toggled` event type + validation case |
| `extensions/goal-activity.ts` | Recent-activity label for `auditor_toggled` |
| `extensions/goal-compaction.ts` | Optional recent-events line for `auditor_toggled` |
| `extensions/goal-state.ts` | New `toggleGoalAuditor(ctx)` on `GoalCore` |
| `extensions/goal-widget.ts` | `ctrl+shift+a` keybinding → `core.toggleGoalAuditor` |
| `extensions/widgets/goal-dashboard-model.ts` | `auditorEnabled` on `GoalDashboardModel` |
| `extensions/widgets/goal-dashboard-renderer.ts` | Compact auditor status: bottom-right border dot |
| tests: `goal-drafting.test.ts`, `goal-dashboard-model.test.ts`, `goal-dashboard-golden.test.ts`, `goal-widget.test.ts`, `goal-ledger.test.ts`, `goal-activity.test.ts` | Coverage |

## 1. Tweak confirm auto-resume (`goal-drafting.ts`)

In the `propose_goal_draft` confirm path (tweak branch, around the
`core.goalService.apply` call), compute the stalled→resumed transition inside
the `mutate`:

```ts
const stalled = goal.status === "paused" || goal.status === "blocked";
return {
  ...goal,
  objective: extracted.objective,
  verificationContract: extracted.verificationContract ?? goal.verificationContract,
  taskList, currentTaskId, skipAuditor,
  status: stalled ? "active" : goal.status,
  stopReason: stalled ? undefined : goal.stopReason,
  pauseReason: stalled ? undefined : goal.pauseReason,
  pauseSuggestedAction: stalled ? undefined : goal.pauseSuggestedAction,
  updatedAt: now,
};
```

After a successful apply, when `stalled` was true: `core.beginAccounting()`
and `core.queueContinuation(ctx, true)` (parity with `replaceGoal` at
`goal-state.ts` ~435) and append a `goal_resumed` ledger event
(`reason: "tweak"`, try/catch like `/goal-resume` in `goal-commands.ts`).
The existing `clearContinuationState()` + `updateUI` + `clearGoalDrafting`
stay.

## 2. Tweak auditor default (`goal-drafting.ts` `startGoalDrafting`)

Current (all modes):

```ts
const auditorEnabled = !loadGoalSettings(ctx.cwd).disabled;
```

New:

```ts
const auditorEnabled = mode === "tweak"
  ? !(targetGoal?.skipAuditor ?? loadGoalSettings(ctx.cwd).disabled)
  : !loadGoalSettings(ctx.cwd).disabled;
```

`targetGoal` is the `currentGoal` passed by the `/goal-tweak` command
(`goal-commands.ts:485`). The confirm path already writes
`skipAuditor = confirmation.auditorEnabled === false`, so the persisted value
survives the tweak unchanged when the toggle is untouched.

## 3. Compact auditor status + toggle

### Model (`goal-dashboard-model.ts`)

`GoalDashboardModel` gains `auditorEnabled: boolean`; derived in
`deriveGoalDashboardModel` from `!goal.skipAuditor` (undefined → true, the
global default). The expanded renderer ignores it.

### Renderer (`goal-dashboard-renderer.ts`, `renderCompactDashboard`)

No standalone content line. `boxFooter` gained a right-corner slot (`right`
param) rendered as ` ${right} ${frame(theme, H)}` before the `╯` corner,
mirroring `boxHeader`'s right-meta treatment; the left hint truncates when
tight, the right slot survives. `renderCompactDashboard` passes:

- right slot = `Ctrl+Shift+A: toggle auditor` (frame tone) + ` ●` colored
  dot — wide (≥100) and medium (70–99) only; the fill expands before the
  slot, so the note + dot are right-aligned by construction.
- right slot = just the colored dot — narrow (50–69) and minimal (<50).
- dot = `success` green when `model.auditorEnabled`, `muted` gray when off.

Other `boxFooter` callers (expanded, focus-required, audit footer) pass no
`right` → expanded dashboard byte-identical. `goal-status.ts` may pass a
custom left `footerHint`; the right slot is composed inside
`renderCompactDashboard` regardless of mode.

### Ledger (`goal-ledger.ts`, `goal-activity.ts`, `goal-compaction.ts`)

- `goal-ledger.ts`: add to the `GoalLedgerEvent` union:
  `| { type: "auditor_toggled"; goalId: string; enabled: boolean; at: string }`
  plus a validation case in the event-type switch.
- `goal-activity.ts`: `mapEvent` case →
  `{ at, kind: "goal", text: enabled ? "Turned the independent auditor on." : "Turned the independent auditor off." }`.
- `goal-compaction.ts`: optional recent-events case
  (`- auditor toggled ${enabled ? "on" : "off"}`); unknown types are skipped
  harmlessly if omitted.

### Toggle (`goal-state.ts` + `goal-widget.ts`)

`GoalCore` gains `toggleGoalAuditor(ctx: ExtensionContext): void`:

```ts
function toggleGoalAuditor(ctx: ExtensionContext): void {
  if (!state.goal) { ctx.ui.notify("No focused goal to toggle the auditor for.", "info"); return; }
  if (state.goal.status === "complete") { ctx.ui.notify("This goal is complete; the auditor no longer applies.", "info"); return; }
  const nextEnabled = state.goal.skipAuditor !== true;
  const result = goalService.apply(ctx, {
    reconcile: false, refreshFromDisk: true,
    mutate: (g) => ({ ...g, skipAuditor: !nextEnabled, updatedAt: nowIso() }),
    ledger: (written) => [{ type: "auditor_toggled" as const, goalId: written.id, enabled: nextEnabled, at: written.updatedAt }],
  });
  if (!result.ok) { ctx.ui.notify("Could not toggle the auditor: " + result.message, "error"); return; }
  goalService.flushTurn(ctx);
  updateUI(ctx);
  ctx.ui.notify(nextEnabled ? "Auditor enabled for this goal." : "Auditor disabled for this goal.", "info");
}
```

`goal-widget.ts` `processInput`: new branch after `ctrl+shift+t`, before the
debug keys:

```ts
if (matchesKey(data, "ctrl+shift+a")) {
  core.toggleGoalAuditor(ctx);
  core.goalWidgetComponentRef.current?.invalidate();
  return { consume: true };
}
```

The existing `goalModalDepth > 0` guard at the top of the handler already
covers the modal-open case. `ctrl+shift+a` is unbound in pi's keymap
(docs/keybindings.md) and does not collide with the widget's existing
chords (`escape`, `ctrl+shift+t`, `ctrl+shift+arrows`/`home`/`end`, debug
`ctrl+shift+x`/`n`/…).

## Width math (compact auditor footer)

- Wide/medium (inner ≥ 68 at the 70 boundary): `l` = `─ ` + hint (25 chars)
  = 28, `r` = ` ` + `Ctrl+Shift+A: toggle auditor ●` (30) + ` ─` = 33 →
  fill = inner − 61 ≥ 7 → hint fits fully even at the narrowest medium.
- Narrow/minimal: right slot is just ` ● ─` (4 cols); hint = `Ctrl+Shift+T:
  expand` (18) → fill = inner − 24 always ≥ 1.
- The dot + note always survive; only the left hint could truncate (it
  never does at current lengths).
- The compact layout's existing per-mode truncation rules apply; the line is
  pushed with `boxLine` and `fit()`-truncated only when it would overflow.

## 4. Objective length limit → configurable setting (`objectiveMaxChars`)

### Settings (`goal-settings.ts`)

- `GoalSettings.objectiveMaxChars?: number` — `0`/unset = no limit (default);
  positive values cap objective length.
- New parser `asNonNegativeInt` (>= 0; `asPositiveInt` rejects 0, which is a
  valid "no limit" value here).
- `ALLOWED_SETTINGS_KEYS`, `parseGoalSettings`, `loadGoalSettings` (env
  override `PI_GOAL_OBJECTIVE_MAX_CHARS`), `saveGoalSettingsFileConfig`
  (clean + persist), `envOverrideFor`, and `effectiveSettingsReport` row
  ("max objective length (0 = none)") all gain the key.

### Settings menu (`goal-commands.ts`)

- `SETTING_ROWS`: `{ key: "objectiveMaxChars", label: "max objective length
  (0 = none)", section: "Goal behavior", kind: "positiveInteger" }`;
  `settingsValue` default `"0"`; the row-driven lower bound is 0 (same as
  `stallTimeoutMinutes`).

### Enforcement points (all read `loadGoalSettings(ctx.cwd).objectiveMaxChars ?? 0`)

| Site | Change |
| --- | --- |
| `goal-core-tools.ts` create_goal | Reject only when `max > 0 && objective.length > max`; message reports the limit and given length; schema description drops the hard "1-4000" wording |
| `goal-drafting.ts` propose_goal_draft | Reject with `at most ${max} characters (N given)` when limited; empty check stays |
| `goal-commands.ts` /goal-tweak | Notify `exceeds ${max} characters` when limited and return without starting a draft |

Tests: `goal-core-tools.test.ts` (default accepts 5000 chars; configured 100
rejects 101 and accepts 100), `goal-settings.test.ts` (parse/load/save/env/
report), `goal-command-palette.test.ts` (menu row default + persist),
`goal-drafting.test.ts` (propose accepts long by default, enforces configured
limit, /goal-tweak command rejection), integration `extension.test.ts` (ten
settings rows).

## Validation

- `npm run check` clean.
- New unit tests: tweak-resume (paused→active, blocked→active,
  budget_limited unchanged, active unchanged, pause metadata cleared,
  `goal_resumed` event), auditor-default (tweak of `skipAuditor: true` goal
  → dialog defaults disabled; `false` → enabled; unset → global fallback),
  toggle (`skipAuditor` flips + persisted + `auditor_toggled` event +
  notification; no-goal/complete/modal guards).
- Golden/unit: compact footer shows the dot (green on / muted off) at
  40/50/80/100+; the `Ctrl+Shift+A: toggle auditor` note right-aligned
  before the dot in wide/medium only; expanded
  goldens unchanged.
- `npm test` (667 + new) and `npm run test:integration` (28) green.
