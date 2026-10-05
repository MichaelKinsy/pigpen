# Product: Goal widget terminal-height bound — restore scroll-up in the equal-height case

## Status

Implemented and validated. Root cause empirically confirmed (headless
harness on the real TuiMainScreen); design and measurements in TECH.md;
implementation log in MILESTONES.md.

## The bug (user report)

> When the terminal window is *just* the height of the pi-goal-x ui, the user is
> unable to scroll up — fix this.

Repro: run pi with a focused goal so the goal widget (aboveEditor placement)
is visible, then size the terminal so its height equals the widget's natural
rendered height (e.g. the expanded dashboard on a ~24-row terminal, or a
compact dashboard on a ~13–22-row terminal). The widget fills the screen, its
top lines and the chat sit above the viewport (reachable only via terminal
scrollback), and scroll-up is dead: the scrollback is wiped on every goal
update (see root cause).

## Root cause (summary)

The goal widget renders an unbounded number of lines. In pi's regular
(main-screen) mode the frame (header, chat, status, widget, editor, footer)
is written top-to-bottom with the viewport pinned to the bottom, so when the
widget is `terminalRows` tall:

1. **The widget's top lines sit above the viewport** (in terminal scrollback)
   while the widget fills the visible screen; the chat and widget head are
   only reachable by scrolling up.
2. **Every goal update wipes the scrollback.** Any update touching the
   widget's top lines (usage ticks, status changes) hits pi-tui's
   `firstChanged < prevViewportTop` differential-render bail-out, which
   triggers a full render with `\x1b[2J\x1b[H\x1b[3J` — a **scrollback erase**
   (confirmed empirically by the headless harness: the unbounded widget
   emits 2J=1, 3J=1 on a goal update). So scroll-up never has anything to
   show: the scrollback is wiped on every widget update.
3. **The editor/footer fall below the fold** when the chat is empty (the
   frame exceeds the terminal but the viewport shows the widget, not the
   chrome below it).

This is the same pi-tui full-render mechanism the questionnaire churn guard
(spec `2026-08-04-goal-confirmation-scroll-fix`) neutralized for dialogs —
the widget is the remaining unbounded render path.

## Fix (behavior)

The goal widget must **never** render taller than the terminal can hold while
leaving pi's own chrome usable. Concretely:

- The widget reads the terminal height at render time (`tui.terminal.rows`)
  and caps its rendered line count to
  `terminalRows − WIDGET_HEIGHT_RESERVE` (reserve = status line + editor
  minimum + footer + a chat row, so the editor and at least one chat row
  always stay visible).
- When the natural render exceeds the cap, the widget deterministically keeps
  the top of the dashboard (header, status, progress, task window — the
  content-priority order of the dashboard is top-down) and drops the bottom
  overflow. The dropped rows are secondary chrome/details; the goal identity,
  status, and the interactive task list stay in frame.
- The cap is per-render and deterministic: the rendered height is
  `min(natural, cap)` with no oscillation, so the widget never shrinks on its
  own in a way that triggers pi-tui's clearOnShrink full-render
  (`\x1b[2J\x1b[H\x1b[3J`) — the scrollback wipe path.
- Pure render callers that are NOT TUI widgets (`/goal-status` notify text,
  golden-test renders) pass no terminal height and stay **unbounded** — byte
  identical to today.

Net user-visible result in the equal-height case: the editor and chat rows
remain visible and scrollable, the widget's top content (identity/status/task
list) is fully visible instead of clipped, and no scrollback wipe is caused by
the widget.

## Scope

**In scope**

- `extensions/widgets/goal-widget.ts` — `GoalWidgetComponent.render` and the
  `renderGoalWidgetLines` entry point; the deterministic height bound helper.
- All widget states flow through the same bound: compact dashboard, expanded
  dashboard, audit dashboard, audit result card, debug panel, unfocused panel.
- Tests: mock TUI gains configurable `terminal.rows`; new unit tests for the
  bound; full suite green.

**Out of scope**

- pi-tui's own renderer (shrink/clearOnShrink behavior, resize full-renders)
  — the widget must simply never be the trigger.
- The questionnaire/task-confirmation/escape dialogs — already guarded
  (spec `2026-08-04-goal-confirmation-scroll-fix`).
- Alternate-screen behavior (DECSET 1049): stays banned, unchanged.
- `/goal-status` and any non-TUI render path: unchanged, unbounded.

## Constraints (hard rules)

1. Follow the established questionnaire churn-guard pattern: read
   `tui.terminal.rows` at render time; deterministic bound; never emit
   DECSET 1049 or `\x1b[2J` / `\x1b[3J` from pi-goal-x.
2. The bound is an optional parameter (default unbounded) so pure callers
   (`/goal-status`, notify text) are unchanged.
3. No new timers, polling, or periodic renders.
4. Content that fits renders exactly as today (no churn on normal terminals).
