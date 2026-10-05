# TECH — Stable widget height so pi stays scrollable

## Mechanism (verified against pi-tui 0.84.1)

pi's default interactive mode is **regular mode** (`TuiMainScreen`,
`settings-manager.js getTuiMode()` defaults to `"regular"`). Regular mode has
no layout tree: `TuiBase.render(width)` concatenates every child's full render
into one buffer — `documentContainer` (header + **all** chat lines) + pending +
status + goal widget + editor + footer — and writes the diff to the terminal.
Overflow past the terminal height lives in the terminal's own scrollback; the
user reads the chat by scrolling the **terminal**. (`renderLayoutFrame`, the
paint-based layout resolver with ScrollView clipping/scrollbars, exists only
on `TuiAltScreen` — fullscreen mode.)

The goal widget is the last dynamic component in the buffer (via
`widgetContainerAbove` in the dock). Its rendered line count therefore sets
the dock height, and the dock height sets the buffer's line count. When the
widget's rendered line count changes:

- a line is appended/removed at the bottom of the buffer → the terminal
  scrolls to keep the bottom in view (universal emulator behavior) → the
  user's scrolled-up position is lost;
- in fullscreen mode the transcript ScrollView's viewport height changes →
  `updateLayout` re-engages follow-end → the chat scroll resets to the bottom.

The 2026-08-10 fix removed the `\x1b[2J\x1b[H\x1b[3J` wipes by capping the
widget to `terminalRows − 6` (head slice), but the cap value is constant only
when the *natural* height is constantly above it. Natural height varies with
goal state (see measurements below), so the rendered height still churns at
the boundary and the buffer still grows/shrinks.

### Measured natural-height variability (experiments/scroll-repro/widget-height-variability.mjs)

Width 120, terminal rows 24 → cap 18:

| state | natural expanded | rendered expanded |
|---|---|---|
| goal created, 3 pending tasks | 4 | 4 |
| +1 task complete (activity grows) | 13 | 13 |
| +2 complete, feed 3 items | 15 | 15 |
| current task gains contract+evidence | 14 | 14 |
| goal verification contract added | 21 | **18** |
| token budget configured | 24 | 18 |
| 12 tasks, 5 complete | 25 | 18 |
| activity feed capped at 5 | 31 | 18 |

Rendered height changes on 4 of 8 transitions. Compact natural spans 4..14
(over a 7-line cap on 13-row terminals). Audit dashboard natural: 8 → 11 →
13 → 8 while the animation runs.

### Emulator-level root cause (experiments/scroll-repro/emulator-repro.mjs)

Real `TuiMainScreen` + ScrollView transcript + VStack dock + real
`GoalWidgetComponent` writing into a real VT emulator (`@xterm/headless`
6.0.0, the same VT engine as xterm.js-class emulators):

| scenario | widget height across updates | buffer churn | result |
|---|---|---|---|
| A fits (40-row term, cap 34, expanded) | 22, 24, 26, 23, 25 | baseY 17→19→21 (2 yanks) | **FAIL — churns** |
| B resize below (expanded 40→24) | constant 18 | 0 | pass (stable) |
| C resize below (unexpanded 30→14) | constant 8 | 0 | pass (stable) |
| D scrolled-up + updates (fits) | — | buffer 57→59→61 (Δ4) | **FAIL — re-pins** |

The yank in A/D is pure line-count churn (0 `2J`/`3J`/1049 emitted): the
widget renders the varying natural height in the fits case (no latch), so the
buffer line count changes and any pane-bottom-following multiplexer (zellij)
or emulator re-pins the viewport. The at-cap regimes (B/C) are already stable
— the cap-only latch works there — but the fits regime is the reported bug.

The resize write in B/C contains one `2J+3J`: pi-tui's own height-change full
render (`heightChanged → fullRender(true)`), pi-owned and out of scope; the
widget adds no further wipes (0 after the resize).

## Design: stable height per regime (latch at regime start)

### Component state (`GoalWidgetComponent`)

```ts
private stickyCap: number | undefined;            // committed rendered height once latched
private stickyRegime: string | undefined;         // regime key when latched
private stickyTerminalRows: number | undefined;   // terminal rows when latched
```

Regime key: `goalId | goalStatus | stateKind | expanded | debug | disableTasks
| hasTasks` where `stateKind ∈ { focused, audit, result, unfocused, none }`
and `hasTasks = taskList.length > 0`. Task presence is part of the regime so
that a structurally empty goal (0 tasks → tiny dashboard) re-latches when its
first task appears — a big structural jump, not steady-state churn, and rare
(goals are usually proposed with tasks).

### Algorithm (`applyStableHeightBound`)

```
natural = unbounded render of the current branch (incl. debug panel)
if no terminalRows -> return natural (unbounded; mock/harness/status)
cap = max(1, terminalRows - WIDGET_HEIGHT_RESERVE)

if stickyTerminalRows != terminalRows: reset sticky   // resize: adapt to new height
if stickyRegime != regime:             reset sticky   // mode/state change

if stickyCap == undefined:
    stickyCap = min(natural.length, cap)              // FIRST RENDER: latch, fits or capped
if natural.length > stickyCap:
    return natural.slice(0, stickyCap)                // growth: head slice, height never changes
if natural.length < stickyCap:
    return natural padded with "" to stickyCap        // shrink: pad, height never changes
return natural                                        // exactly the latch: unchanged
```

Properties:

- **Latch at regime start**: the first render of each regime commits the
  height (`min(natural, cap)`), and every later render of that regime renders
  exactly that many lines — fits case *and* capped case. Growth (activity
  feed, contract/evidence, budget, verification) is head-sliced; shrink is
  blank-padded. The buffer line count is constant in every case → the
  terminal never scrolls on widget updates.
- **Adapt**: on resize the latch is cleared and `min(natural, newCap)` rules
  again — growing the terminal reveals more of the widget; shrinking re-caps.
- **Determinism**: the latch is a pure function of (natural height, regime,
  terminal rows) — no timers, no Date, no randomness; the same goal state on
  the same terminal renders the same lines every time.
- **Padding**: blank lines (empty strings) after the box footer; the diff
  writes them once and never again (they never change). Honest filler — the
  dashboard's `… +N more` markers are only used for real hidden content.

### UX tradeoff (accepted)

In the fits case the widget's height is now constant at the first-render
natural height, so later content growth is head-sliced even though the
terminal has room. This is the same top-down priority tradeoff already
accepted at the cap in 0.27.3 — the alternative (varying natural height in
the fits case) is exactly the yank bug being fixed. The slice only affects
content that arrives *after* the mode's first render (activity feed entries,
new contract/evidence text); the dashboard's core (identity → status →
progress → tasks) is rendered at first render and stays visible.

### Integration

- `boundWidgetRenderLines(lines, terminalRows)` **stays as-is**: pure head
  slice, no-op when `terminalRows` missing. Still applied inside
  `renderGoalWidgetLines` when a caller passes `terminalRows` (pure-function
  contract for direct callers / tests).
- `renderGoalWidgetLines` keeps its `terminalRows` option; the component no
  longer passes it — the component renders **natural** (unbounded) and applies
  `applyStableHeightBound` at the end, so it can distinguish
  natural>cap (latch) from natural≤cap (fits/sticky-pad) and can append the
  debug panel before bounding.
- All widget branches flow through the single sticky bound in the component's
  `render()`: focused/audit/unfocused (via `renderGoalWidgetLines`),
  result card, and the debug panel.
- Regular mode: constant buffer line count → no bottom-scroll. Fullscreen
  mode: constant dock height → constant transcript viewport → no chat-scroll
  reset. Both renderers are covered by the same widget-side fix.

## Why not the alternatives

- **Fixed-structure dashboard** (constant natural height by construction, e.g.
  fixed row budgets for every section): changes the visual design (truncation
  instead of wrapping) and is a much larger renderer change; the per-regime
  latch preserves today's visuals and only intervenes to keep the height
  constant.
- **Never re-render the widget at the cap** (freeze content): hides real goal
  state changes (task completions, usage) — misleading.
- **Ratchet (grow-only latch)**: every growth moment re-pins the viewport
  (yanks while the activity feed fills) — the user's complaint is continuous
  re-pinning, so a ratchet only delays it.
- **Latch only when the frame overflows** (widget sensing chat length): the
  widget cannot know the transcript length; a universal constant height is
  simpler and harmless when everything fits.
- **Padding before the box footer / continuation markers**: re-flowing the box
  is complex; `…`/`↑ N more` markers would falsely imply hidden content.

## Full-stack verification (real zellij + real pi + a mocked RUNNING goal)

`experiments/scroll-repro/seed-mock-goal.py` seeds a mock goal with
`status: "active"` (12 tasks, verification contracts, subtasks, token
budget; `autoContinue: false` to keep the agent quiet) into a throwaway cwd.
`experiments/scroll-repro/zellij-mock-driver.py` PTY-drives real zellij
0.44 (session `mockrepro`) with pi in a pane from that cwd, using
`zellij action` for precise control (`write-chars`/`write` keystrokes,
`switch-mode`, `scroll-up`, `dump-screen`), capturing zellij's rendered UI
bytes + a timeline. `experiments/scroll-repro/zellij-dump.mjs` /
`zj-scroll-timeline.mjs` replay the capture into `@xterm/headless` and
sample the pane screens and the `SCROLL: 0/N` frame indicator over time.

Sequence: start pi (2s) → `/goal-focus` (14s) → select the goal (17s) →
expand the widget (20s) → resize the PTY 24→12, below the widget (24s) →
scroll up 3 (28s) → enable debug mode + start the debug mock-audit
animation (32s) → re-trigger the audit every ~7s and sample the pane dump
until the run ends (~58s).

Measured results (capture `/tmp/zj-mock3.bin`):

- **Goal runs for real**: `goal: active [30m17s 107.1K]` →
  `[30m31s]` across screens 14s apart — `liveDisplayGoal` ticks
  `activeSeconds` on every render (status `active`), so the widget's content
  genuinely changes the whole time.
- **Pane dump constant**: `zellij action dump-screen --full` returned
  exactly **30 lines from 32.6s to 57.3s** (25s, 3 audit re-triggers) —
  zero appended lines.
- **Indicator constant**: `SCROLL: 0/22` from 33s to 57s, unchanged across
  the churn.
- **Zero wipes from the widget**: the only `2J` in the entire capture is
  pi-tui's own at the resize (24s, `2J=1 3J=0` in zellij's output; zellij's
  pane scrollback survived it — zellij-internal). No `3J`, no 1049.
- **Scrolled-up hold** (capture `/tmp/zj-mock2.bin`, autoContinue variant):
  `SCROLL: 3/22` held while the goal ticked; the widget box stayed at its
  latched 6 lines.
- **Streaming is the only growth**: in the autoContinue variant the goal run
  resumed and the agent streamed a real model call (~13K tokens) — the pane
  scrollback grew 22 → 65 only during that stream, then stayed constant
  through further audit re-triggers. The widget added zero lines.

## Second finding: the widget block + chrome overflowing the terminal wipes on every agent write (and the fix)

pi-tui's diff renderer full-renders (`\x1b[2J\x1b[H\x1b[3J`, scrollback
wipe) whenever the **first changed line is above its tracked viewport top**
(`tui-main-screen.js`: `if (firstChanged < prevViewportTop) → fullRender(true)`, where
`prevViewportTop = previousBufferLength − height` when the frame overflows).
With the widget's block plus the dock chrome below/around it — pending +
status + editor + footer — exceeding the terminal rows, the chat's appended
lines and the status line are above the viewport top, so **every agent write
and every status/spinner tick wipes the scrollback**: the terminal clears and
redraws, the user is forced to the bottom, and N churns. The widget's own
latched tick stays safe (its changing line is the 2nd line of the block),
which is why the earlier harnesses (empty editor, chrome ≤ 6) missed it —
the user's typed message in the editor pushed the chrome past 6.

`experiments/scroll-repro/overflow-probe.mjs` measures it against the real
renderer (real TuiMainScreen + real GoalWidgetComponent into
@xterm/headless): with below-chrome = 7 (status 1 + 5-line editor + footer 2
minus the widget), a chat append emits a full 2J+3J every time, and a status
tick emits one too; the widget's own latched tick emits none.

### Fix: adaptive reserve from the measured dock chrome

`GoalWidgetComponent.render()` now measures the dock chrome at the current
width before bounding:

```ts
const reserve = this.measureDockReserve(width) ?? WIDGET_HEIGHT_RESERVE;
return applyStableHeightBound(natural, terminalRows, this.stableHeightState, regime, reserve);
```

`measureDockReserve(width)` finds the widget's container in `this.tui.children`
(regular mode: `[document, pending, status, widgetContainerAbove, editor,
widgetContainerBelow, footer]`; fullscreen mode: the dock VStack nested under
`[transcript, dock]`), renders the sibling dock containers at the current
width, sums their line counts (pending + status + editor + widgetBelow +
footer — the document container is skipped: its height cancels out of the
viewport math), and returns that + 1 slack. Any failure (mock TUIs, sibling
render errors) falls back to the static `WIDGET_HEIGHT_RESERVE` (6) — mock
TUIs, /goal-status, and unbounded contexts are unchanged.

`applyStableHeightBound` gains an optional `reserve` parameter (default
`WIDGET_HEIGHT_RESERVE`) and a `stickyReserve` latch key: when the measured
chrome changes (editor grew while typing, the "Working…" status appears, the
footer changes), the latch clears and re-evaluates at the new cap — so the
widget's block plus the chrome never exceeds the terminal, and the chat's
append point and the status line always stay within the viewport. Agent
writes and spinner ticks become in-place diffs; the scrollback is never
wiped by the widget's presence.

Verified: `overflow-probe.mjs` — chat-append and status-tick wipes 1 → 0 in
every geometry (rows 11-20, empty and 5-line editors); the widget renders its
natural height when the chrome is small and shrinks when the editor/status
grow; full-stack mock at 14 rows with a typed editor and the agent working
showed a constant pane total and a held scrolled-up position across the
churn. Unit tests cover the adaptive cap and the reserve-change re-latch;
the harnesses assert the new cap (`rows − (measuredChrome + 1)`).

## Validation

- `experiments/scroll-repro/widget-height-variability.mjs` — natural-height
  measurement (root-cause evidence, above).
- `experiments/scroll-repro/emulator-repro.mjs` — real TuiMainScreen + real
  frame + real widget into `@xterm/headless`; scenarios A (fits — must be
  constant after the fix), B/C (resize below the widget), D (scrolled-up
  viewport must hold); asserts 0 yank triggers (buffer length/baseY) and 0
  `2J`/`3J`/1049 on goal updates.
- `experiments/scroll-repro/widget-height-bound.mjs` — drives the real
  `TuiMainScreen` frame through the goal-state sequence at fixed rows,
  asserting: widget rendered height constant (after first-render latch),
  buffer line count constant, 0 `\x1b[2J`/`\x1b[3J`/1049; fits case constant;
  resize adaptation; regime reset.
- `experiments/scroll-repro/resize-repro.mjs` — resize scenarios (expanded
  40→24, unexpanded 30→14, spinner ticks post-resize); counts `2J`/`3J`/crlf
  and frame churn.
- `experiments/scroll-repro/seed-mock-goal.py` + `zellij-mock-driver.py` +
  `zellij-dump.mjs` / `zj-scroll-timeline.mjs` — full-stack mock of a
  RUNNING goal (real zellij → real pi → goal extension): pane dump line
  count constant ≥20s of audit churn with the terminal below the widget and
  the goal's elapsed ticking; `SCROLL: 0/N` unchanged across the churn; 0
  `2J/3J/1049` from the widget.
- `tests/goal-widget.test.ts` — unit tests for `applyStableHeightBound`
  (first-render latch in fits and capped cases, growth head-slice, shrink
  padding, resize reset, regime reset incl. task presence, unbounded-without-
  terminal determinism).
- `npm test` (0 failures), `npm run check` + eslint clean.
