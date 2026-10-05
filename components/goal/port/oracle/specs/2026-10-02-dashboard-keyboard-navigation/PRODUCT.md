# Dashboard keyboard navigation and single-press handling

Source: PR [#85](https://github.com/tmonk/pi-goal-x/pull/85) by `x12315` (montana), included on maintainer branch `incoming/pr-85`. The work is on a maintainer branch rather than in the pull request because commits cannot be added to a pull request from outside the contributor's fork.

## Problem

Three dashboard defects were present on `main`:

1. `GoalCore.goalWidgetComponentRef` was read for `update()`/`invalidate()` in nine places but only ever assigned `null`, so the terminal-input handler in `extensions/goal-widget.ts` never reached a live `GoalWidgetComponent`. The documented compact scroll chords and expanded-dashboard arrow navigation were inert, and widget refreshes after a state change fell through silently.
2. With the Kitty keyboard protocol active (pi requests flags 1/2/4 when the terminal supports them), one physical key press arrives as a press, then repeat and release events. `matchesKey` matches those too, so a single press of a Goal shortcut could toggle or scroll several times.
3. Dashboard keybindings were read once at listener registration, so a `/goal-settings` change to a binding took effect only after a session restart.

## Behavior

- Both widget-registration sites pass a `componentRef` to `makeGoalWidgetFactory`, and `clearGoalWidget` clears it, so the input handler reaches the live component and holds no stale one.
- Kitty repeat and release events that match a Goal-owned shortcut are consumed without acting; unrelated editor input is untouched. The check is a local helper rather than `pi-tui`'s `isKeyRepeat`/`isKeyRelease`, because the declared peer range (`>=0.83.0`) cannot be assumed to export them.
- Dashboard keybindings are read when a key arrives, so a settings edit applies without a restart.
- The viewport is anchored before the first scroll, compact scroll chords do not touch the hidden list while the dashboard is expanded, and the expanded dashboard owns the plain arrow keys even when its task list fits (it is modal while open).

## Boundaries

No command, setting, tool, lifecycle status, or scheduler-state field is added. Prompt text is unchanged, so no context baseline re-measurement is required. README and `specs/SPECS.yaml` are untouched; this directory is registered at merge time.