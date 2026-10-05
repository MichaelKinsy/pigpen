# PRODUCT — Stable widget height so pi stays scrollable

## Problem

When the expanded goal widget is larger than the terminal window, the user
cannot hold a scroll position to read the agent's chat output: the terminal
keeps jumping to the bottom. The user's own diagnosis:

> "The number of lines keeps changing in the terminal, causing it to always
> scroll to the bottom."
> "When you expand the terminal height more, more of the widget shows at the
> bottom. When we expand enough, we can scroll."

The prior fix (spec `2026-08-10-widget-height-bound-scrollback-fix`) caps the
widget's rendered height at `terminalRows − 6` (a head slice), which keeps
pi-tui on the differential render path and eliminates the `\x1b[2J\x1b[H\x1b[3J`
scrollback wipes. But the cap alone does not stop the rendered line count from
**changing over time**: the widget's natural height is not constant.

Measured on a 24-row terminal (cap = 18) while a goal runs, the rendered
height churns through 4 → 13 → 15 → 14 → 18 as the goal progresses
(activity-feed growth, current-task contract/evidence wrapping, goal
verification text, budget, task growth). The audit dashboard churns 8 → 11 →
13 → 8 while an audit animation runs.

Every height change alters the dock height, which alters the number of lines
pi writes into the terminal buffer. A different line count means a rewrite
that ends below the current viewport → the terminal scrolls to the bottom
(universal terminal behavior: a new line at the bottom pushes the viewport
down) → the user is yanked away from the chat they were reading.

## Emulator-level root cause (new session finding)

The 0.27.3 sticky cap only engages when the natural height exceeds the cap
(`natural > cap`). That leaves the **fits case** (terminal taller than the
widget but the frame still overflows via a long chat) rendering the *varying*
natural height — the same churn, just below the cap threshold. The user
confirmed this exact failure:

> "It is getting scrolled to the bottom even when the height is bigger than
the widget now!"
> "When the window is tall enough it does not snap back."

Reproduced at emulator level (real `TuiMainScreen` + ScrollView transcript +
VStack dock + real `GoalWidgetComponent` feeding `@xterm/headless`,
`experiments/scroll-repro/emulator-repro.mjs`):

- **Fits case (40-row terminal, cap 34, expanded)**: rendered height varies
  22 → 24 → 26 → 23 → 25 across goal-state updates → buffer `baseY` moves
  (2 yank triggers) → a multiplexer/emulator following the pane bottom
  re-pins. No 2J/3J (all in-place rewrites) — the churn alone is the yank.
- **Scrolled-up holds (Scenario D)**: user scrolled up 10 lines, then goal
  updates grow the buffer 57 → 59 → 61 (Δ4) → the pane-bottom-following
  multiplexer re-pins the viewport.
- **Capped case (resize below the widget, expanded 40→24 and unexpanded
  30→14)**: stable — widget constant 18/8, buffer constant, 0 churn after
  the resize. The resize itself emits one pi-tui-inherent `2J+3J` (pi's own
  height-change full render — out of scope).

So the cap-only latch fixes the at-cap regime but not the fits regime: the
rendered height must be **constant in every case**, not just at the cap.

## Full-stack identification: the user's "0/XXXX" indicator

Inside zellij, the pane frame title shows `SCROLL: 0/N` (zellij's own pane
scrollback indicator; `0` = viewport at the bottom, `N` = pane scrollback
line count). This is the number the user sees changing "non stop": zellij
follows the pane bottom whenever the pane's buffer line count changes, and
`N` grows whenever the pane **appends** lines.

Verified in a real full-stack capture (`experiments/scroll-repro/
zellij-driver.py`, PTY-driving real zellij → real pi):

- `SCROLL: 0/29 → 0/36` after a resize to 12 rows, `12/36` when scrolled up
  12 — then **constant** over seconds of idle: zellij's pane scrollback only
  changes when pi appends lines or when a height change rewrites the frame.
- pi's "Working…" spinner and the widget's in-place updates (usage ticks)
  never grow the pane buffer — verified against the real byte stream.
- pi's own resize full-render emits `2J+3J`, but zellij does **not** clear its
  pane scrollback from the pane's `3J` (the pane `SCROLL` count survived the
  resize wipe) — zellij-internal behavior, out of scope.

## Full-stack mock: a RUNNING goal under the bug conditions (the goal's key verification)

The user's environment is a goal that is actually **running** — not a paused
one. To replicate it exactly, `experiments/scroll-repro/seed-mock-goal.py`
seeds a mock goal with `status: "active"` (12 tasks, verification contracts,
subtasks, token budget, 48K used tokens, `autoContinue: false` to keep the
agent quiet) into a throwaway cwd, and `zellij-mock-driver.py` drives the
full stack (real zellij 0.44 → real pi → goal extension) through the bug
scenario: `/goal-focus` the active goal, expand the widget, resize the
terminal 24→12 (below the widget), scroll up, then trigger the debug
mock-audit animation repeatedly for continuous goal-state churn.

Results (PTY-captured, analyzed by replaying into `@xterm/headless`):

- The goal runs for real: the widget shows `goal: active [elapsed tokens]`
  with the elapsed counter ticking on every render (`30m17s → 30m31s` in the
  capture) via `liveDisplayGoal`.
- **The pane buffer line count is constant**: `zellij action dump-screen`
  returned exactly 30 lines from 32.6s to 57.3s — 25s of continuous audit
  churn (3 re-triggers) with zero appended lines and zero `2J/3J/1049`.
- **The on-screen indicator is constant**: `SCROLL: 0/22` across the entire
  churn window while the goal's elapsed counter kept ticking.
- **Scrolled-up holds**: with the pane scrolled up 3 lines, the position
  (`3/22`) held for seconds while the goal ticked (in-place rewrites only).
- The widget box stayed at its latched 6 lines the whole time; the audit
  dashboard replaced the expanded dashboard at the same latched height.
- **The only scrollback growth source is the agent itself**: in the
  `autoContinue: true` variant, the goal run resumed and the agent made a
  real model call (~13K tokens streamed into the chat) — the pane scrollback
  grew 22 → 65 only during that stream, then stopped. The widget contributed
  zero lines. Agent streaming is inherent pi behavior (out of scope); the
  user reads the chat between updates, and the widget no longer fights that.

## Second user finding: the widget block + chrome overflowing the terminal wipes the scrollback on every agent write

The user reported the snap-back persists even with the fixed code:

> "STILL it's forcing the window to the bottom when the agent is running text
> above, and the N keeps changing."
> "it's from any writing by the agent/COT in the window above working, not
> just when a goal is active"
> "the force back to bottom only happens when only the widget is viewable,
> and no working/output/cot"

Reproduced at emulator level (`experiments/scroll-repro/overflow-probe.mjs`,
real TuiMainScreen + ScrollView transcript + VStack dock + real
GoalWidgetComponent into @xterm/headless): **pi-tui full-renders
(`\x1b[2J\x1b[H\x1b[3J` — clearing the terminal scrollback) whenever the
first changed line sits above its tracked viewport top
(`previousBufferLength − terminalRows`)**. When the widget's block plus the
chrome below/around it (pending + status + editor + footer) exceeds the
terminal, the chat's appended lines and the status line land above the
viewport top — so with a typed message in the editor (which makes the chrome
> 6 lines), EVERY agent write and EVERY status/spinner tick wipes the
scrollback, forcing the viewport to the bottom and making N churn:

| change | below-chrome ≤ 6 (empty editor) | below-chrome > 6 (typed editor) |
|---|---|---|
| widget content tick (latched) | 0 wipes | 0 wipes |
| agent chat append | 0 wipes | **1 full 2J+3J wipe** |
| status/spinner tick | 0 wipes | **1 full 2J+3J wipe** |

**Fix: the widget sizes itself against the MEASURED dock chrome.** The
reserve is no longer a static 6 — `measureDockReserve()` renders the sibling
dock containers (pending + status + editor + footer) at the current width and
caps the widget at `terminalRows − (measuredChrome + 1)`, so the widget's
block plus the chrome NEVER exceeds the terminal. The chat's append point and
the status line stay within the viewport → agent writes and spinner ticks
become in-place diffs, never wipes. The latch re-evaluates when the measured
chrome changes (editor grew/shrunk, working status appears); the static
`WIDGET_HEIGHT_RESERVE` (6) remains the fallback for mock TUIs. Verified in
the full-stack mock: with the agent working ("⠏ Working…" animating), a typed
editor message, and a 14-row terminal, the pane total held constant and the
scrolled-up position held across the churn.

## Goal

Make the widget's **rendered height invariant to goal-state changes in every
case** (fits and capped), so the buffer's line count stops changing and the
terminal stops jumping to the bottom. The user scrolls **pi / the terminal**
(terminal scrollback in regular mode, the transcript in fullscreen mode) to
read the agent's chat output; the widget must not fight that scroll.

## Behavior (user-visible)

1. **The height never changes within a mode.** At the first render of each
   mode (regime), the widget latches its rendered height: the natural height
   if it fits, else the terminal cap. On every later render of that regime it
   renders exactly the latched line count — no matter how the goal state
evolves (usage ticks, task completions, activity-feed growth,
contract/evidence text, verification, budget). Growth is head-sliced (content
priority is top-down: identity → status → progress → tasks → details →
hints), shrink is blank-padded — the height never changes, so the buffer line
count never changes and scroll-up holds.

2. **Adapts to the terminal.** Resizing the terminal re-evaluates the latch:
growing the terminal reveals more of the widget at the bottom (up to its
natural height); once the whole widget fits, the latch is the natural height
and everything renders.

3. **Fits case is stable too.** When the widget fits, its rendered height is
now the first-render natural height for the mode (constant thereafter) — this
replaces the old varying-natural fits behavior (the source of the new
finding). `/goal-status` and golden renders stay unbounded (no terminal bound
at all when no terminal exists — mock TUIs, headless contexts).

4. **All widget states share the same rule**: compact, expanded, audit, audit
   result card, debug, unfocused.

5. **Sticky is per mode.** Toggling compact↔expanded, switching goals,
   entering/leaving the audit or result card, toggling debug mode, changing
   goal status, disabling tasks, or the goal's first task appearing
   re-evaluates from natural (the sticky height belongs to one mode/state).

## Out of scope

- Widget-internal scrolling (the user asked for pi/terminal scroll, not a
  widget scrollbar; the head-slice tail drop and the dashboard's own
  task-list scroll keys are unchanged).
- pi-tui's renderer and the terminal emulator's scroll-on-output behavior
  (pi/emulator-owned).
- pi's own status/spinner line and pending-messages container.
- Chat growth from agent output (inherent streaming — the user reads between
  updates; verified in the full-stack mock as the ONLY pane-scrollback growth
  source — the widget adds zero).
- zellij/ghostty internals, incl. zellij's pane scrollback not being cleared
  by pi's `3J` and zellij's own `SCROLL: 0/N` indicator (zellij-owned).
- The questionnaire/task-confirmation dialogs (already guarded by their own
  churn guard).
- Alternate screen (banned, unchanged).

## Non-negotiable constraints

1. No new timers, polling, or periodic renders.
2. Never emit DECSET 1049 or `\x1b[2J`/`\x1b[3J` from pi-goal-x.
3. The terminal bound stays optional (default unbounded) — `/goal-status` and
   golden renders byte-identical.
4. Within a regime with a terminal, the rendered height is constant — the
   first-render latch (natural or cap), never oscillating; resizes and regime
   changes are the only times the height may change.
5. Deterministic height: within a regime, the widget renders exactly the
   latched line count (natural or cap) until the mode/state or the terminal
   size changes — no oscillation, no per-update growth.
