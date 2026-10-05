# TECH — Optional unfocused UI suppression

## Setting

`GoalSettingsLayer.hideUnfocusedBanner?: boolean`,
resolved `GoalSettings.hideUnfocusedBanner: boolean` (default false), with
per-leaf provenance from the layered resolver. Registered in the parser's
boolean keys so explicit false survives.

## Render logic

In `renderUI(ctx)` (extensions/goal-state.ts), the `!state.goal && totalOpen >
0` branch checks `loadGoalSettings(ctx.cwd).hideUnfocusedBanner`; when true it
calls `clearGoalWidget(ctx)` — which clears both the status hint and the
widget and unregisters — and returns. Otherwise the existing unfocused
widget/status rendering runs unchanged.

## Live refresh

Every settings mutation ends in the central side-effect hook, which now also
calls `core.updateUI(ctx)` (microtask-coalesced). The banner therefore
hides/restores before the menu action resolves; tests assert UI state directly
after awaiting the command handler, without relying on later turns or focus
events. Repeated toggles cannot double-register the widget because
clearGoalWidget resets `widgetRegistered`.

## Safety invariant

`before_agent_start` still injects `[PI GOAL UNFOCUSED]` with "Do not choose
or switch focus autonomously" whenever there is no focused goal and open goals
exist. A dedicated test pins this text against the setting in both states.
