# MILESTONES — Stable widget height so pi stays scrollable

## 2026-08-11 — Root cause confirmed and design decided

- User reported: with the expanded goal widget larger than the terminal, the
  terminal "always scrolls to the bottom" and cannot be scrolled up to read
  the agent's chat; growing the terminal reveals more of the widget and
  scrolling eventually works. Explicitly: pi itself (terminal scrollback /
  transcript), NOT widget-internal scrolling.
- Verified against pi-tui 0.84.1:
  - Regular mode (`TuiMainScreen`, the default; `getTuiMode()` returns
    "regular") has no layout tree — `render(width)` concatenates every child's
    full render into one buffer. Overflow lives in the terminal's scrollback;
    the user reads the chat by scrolling the terminal. `renderLayoutFrame`
    (paint-based, with ScrollView clipping) exists only on `TuiAltScreen`
    (fullscreen mode).
  - The widget (dock, `widgetContainerAbove`) is the last dynamic content in
    the buffer; its rendered line count sets the dock height → the buffer's
    line count → any change appends/removes lines at the bottom → the terminal
    scrolls to keep the bottom in view → the scrolled-up chat position is
    lost. In fullscreen mode a dock height change shrinks the transcript
    viewport and re-engages follow-end, resetting the chat scroll.
- Measured natural-height variability (`experiments/scroll-repro/
  widget-height-variability.mjs`, width 120): expanded dashboard natural
  spans 4..31 across goal-state changes (activity feed 0→5, current-task
  contract/evidence wrapping, verification text, budget, task growth). On a
  24-row terminal (cap 18) the rendered height churns 4→13→15→14→18; the
  audit dashboard churns 8→11→13→8. Each change = a buffer line-count change
  = a bottom-scroll.
- Design decided: **sticky cap** — once the widget's natural height exceeds
  the terminal cap in a regime, render exactly `cap` lines on every later
  render of that regime (head slice when natural > cap, deterministic blank
  padding when natural dips below). Terminal resizes clear the latch and
  re-evaluate `min(natural, newCap)` (grow reveals more widget); regime
  changes (goal id/status, state kind, compact↔expanded, debug, tasks
  disabled) clear the latch. Fits case stays byte-identical; unbounded
  without a terminal (mock/harness/status) unchanged.
- Spec written: `specs/2026-08-11-stable-widget-height/PRODUCT.md`, TECH.md.

## 2026-08-11 — Implementation

- `extensions/widgets/goal-widget.ts`:
  - Added `applyStableHeightBound(lines, terminalRows, state, regime)` —
    pure given the latch state, exported for tests.
  - `GoalWidgetComponent` now holds a `stableHeightState` latch
    (stickyCap / stickyRegime / stickyTerminalRows); `render()` renders the
    current branch unbounded (`renderNatural`) and applies the sticky bound.
    All widget states (focused, audit, result card, unfocused, none, debug
    panel) flow through the single bound; the previous double-bound
    (renderGoalWidgetLines + component) is replaced by one sticky bound at
    the component level.
  - `boundWidgetRenderLines` unchanged (pure head slice, still used by
    `renderGoalWidgetLines` for direct callers).
- No new timers; no 2J/3J/1049 emissions (the widget never writes directly).

## 2026-08-11 — Validation

- New unit tests in `tests/goal-widget.test.ts` (8): latch on cap crossing
  up; deterministic padding on crossing down; fits byte-identical and never
  latches; resize clears the latch and adapts; regime change re-evaluates;
  unbounded without a terminal; component-level height constancy across a
  goal-state sequence; grow-reveals-more / collapse-re-latches.
- `experiments/scroll-repro/widget-height-bound.mjs` rewritten on the real
  geometry (ScrollView transcript + VStack dock + real GoalWidgetComponent +
  real TuiMainScreen): steady-state stability (height + buffer length
  constant after the latch, 0 wipes), fits byte-identical, resize
  adaptation, regime reset, audit dashboard sticky on a small terminal, and
  the retained 2026-08-10 invariants (bounded ≤ cap, chat reachable,
  editor/footer visible, no wipes).
- Full suite: 781/781 pass; `tsc --noEmit` clean; eslint clean.

## Open items

- User validation of the terminal scroll-up experience in a real terminal
  (the goal's final success criterion).

## 2026-08-11 — New finding: the fits case still snaps back; design revised to latch at regime start

- User report (superseding 0.27.3 validation): "it is getting scrolled to the
  bottom even when the height is bigger than the widget now!" and "when the
  window is tall enough it does not snap back" — i.e. the snap-back persists
  whenever the frame overflows the terminal, including when the widget itself
  fits. Also: "we're still unable to scroll up when the current terminal size
  is smaller than the widget … it just keeps scrolling to the bottom of the
  terminal, so something is being updated". Reference environment: **zellij
  inside ghostty** (multiplexer follows the pane bottom when the pane's
  buffer line count changes).
- Emulator-level reproduction demanded by the user ("PLEASE reproduce in
  full — emulate a terminal in some way"), built with `@xterm/headless`
  6.0.0 (devDependency; same VT engine as xterm.js-class emulators):
  `experiments/scroll-repro/emulator-repro.mjs` drives the real
  `TuiMainScreen` + ScrollView transcript + VStack dock + real
  `GoalWidgetComponent` writing into a real xterm emulator.
  - Scenario A (fits, 40-row terminal): rendered height varies
    22 → 24 → 26 → 23 → 25 across goal updates → buffer `baseY` churns
    (2 yank triggers) → the pane-bottom-following emulator/multiplexer
    re-pins. Root cause of the new finding: the 0.27.3 latch only engages
    when natural > cap, so the fits case keeps rendering the *varying*
    natural height.
  - Scenarios B/C (resize below the widget, expanded 40→24 and unexpanded
    30→14): stable after the resize (constant 18/8, buffer constant, 0
    wipes). The resize write itself carries one `2J+3J` — pi-tui's own
    height-change full render, pi-owned and out of scope.
  - Scenario D (user scrolled up 10 lines, fits): goal updates grow the
    buffer 57 → 59 → 61 (Δ4) → the multiplexer would re-pin.
- Design revision (goal 2026-08-11 session, recorded in PRODUCT.md/TECH.md):
  the rendered height latches at the **first render of each regime**
  (`min(natural, cap)`) in every case — fits and capped — so the buffer line
  count never changes; growth is head-sliced, shrink blank-padded. The regime
  key gains **task presence** (0→1 task re-latches a structurally empty
  goal). The old "fits case byte-identical" contract is replaced by
  "first-render byte-identical, then latched" (the varying-natural fits
  render WAS the bug). `/goal-status`, golden renders, and mock TUIs without
  a terminal stay unbounded.

## 2026-08-11 — Revised implementation (latch at regime start)

- `applyStableHeightBound` rewritten: on the first render of a regime
  `stickyCap = min(natural.length, cap)`; every later render in that regime
  renders exactly `stickyCap` lines (head slice when natural grows, blank
  padding when it shrinks). Resizes and regime changes clear the latch as
  before. Capped-case behavior unchanged; the fits case is now constant.
- `stableHeightRegime()` adds `hasTasks` (taskList length > 0) to the regime
  key.
- Doc comments updated from "sticky-cap" to the stable-height-per-regime
  wording.
- No new timers; no 2J/3J/1049 from pi-goal-x.

## 2026-08-11 — Revised validation

- Emulator repro: all four scenarios now pass in `--expect` mode — A (fits)
  constant at 22 with buffer b57/y17 locked across all goal updates, 0
  churn, 0 wipes; B/C constant 18/8; D scrolled-up viewport holds (no buffer
  growth).
- `widget-height-bound.mjs`: steady-state scenario now asserts constancy
  from the first render (heights 13→13→…→13); fits-case scenario extended
  with a growth step that must keep the latched height.
- `resize-repro.mjs` (built this session): resize scenarios count
  `2J`/`3J`/crlf and frame churn; all pass.
- Unit tests updated to the new semantics (first-render latch fits+capped,
  fits-case growth head-slice / shrink padding, regime re-latch incl. task
  presence, resize re-latch) — 782/782 pass; `tsc --noEmit` clean; eslint
  clean; all three harnesses pass `--expect`.

## Open items (revised)

- User validation of the scroll-up experience in zellij/ghostty: reading the
  chat holds across goal updates at any terminal height where the frame
  overflows (the goal's final success criterion).

## 2026-08-11 — Full-stack mock: a RUNNING goal under the bug conditions (user demanded "mock a running goal and it works fully")

- The user reported the symptom persists with a **running** goal ("the
  0/XXXX number keeps changing non stop") and demanded the repro replicate
  the exact bug environment. Prior harnesses used a paused/static goal.
- **The indicator identified**: zellij's pane frame title shows
  `SCROLL: 0/N` (0 = position at bottom, N = pane scrollback line count) —
  this IS the user's "0/XXXX". It changes when the pane appends lines.
- Built `experiments/scroll-repro/seed-mock-goal.py` (seeds a mock goal with
  `status: "active"`, 12 tasks, verification contracts, subtasks, token
  budget, `autoContinue: false` — the pool only loads
  `active_goal_*.md`, which also bit us first: a wrong filename silently
  produced an empty pool and no widget) and `zellij-mock-driver.py`
  (full-stack: real zellij → real pi → goal extension, `zellij action`
  control, `dump-screen` sampling, byte + timeline capture). Analyzers:
  `zellij-dump.mjs` (screen snapshots at timestamps) and
  `zj-scroll-timeline.mjs` (per-second `SCROLL: 0/N` + 2J/3J correlation).
- First mock run failed silently (goal file name didn't match the pool
  pattern → no goal, no widget). Fixed the filename; the widget then
  appeared and focused correctly.
- **Run 1 (autoContinue: true, `/tmp/zj-mock2.bin`)** — the active goal's
  run resumed and the agent made a REAL model call: "Working…" spinner,
  tokens 48.2K → 61K, chat streamed. The pane scrollback grew 22 → 65
  **only during the stream** (33-40s), then held constant at 65 through
  further audit re-triggers (41-54s). The widget box stayed at its latched
  6 lines throughout (in-place updates). Scrolled-up `3/22` held while the
  goal ticked (29-31s).
- **Run 2 (autoContinue: false, `/tmp/zj-mock3.bin`)** — no agent stream;
  the churn is purely the widget's: elapsed ticked live
  (`goal: active [30m17s 107.1K]` → `[30m31s]`), the debug mock audit
  re-triggered 3×. Result: `zellij action dump-screen --full` = **exactly
  30 lines from 32.6s to 57.3s** (25s constant); `SCROLL: 0/22` unchanged
  across the churn; zero `2J/3J/1049` from the widget (the only `2J` is
  pi-tui's at the resize; zellij kept its pane scrollback through it —
  zellij-internal).
- **Conclusion**: the widget adds zero scrollback under a running goal with
  churn; the only pane-scrollback growth in the user's environment is the
  agent's own streaming (inherent pi behavior, out of scope) — the user
  reads between updates, and the widget no longer fights that.
- PRODUCT.md now records the indicator identification + the streaming
  boundary; TECH.md gained the full-stack verification section.

## 2026-08-11 — Second user finding: wipes on every agent write when the widget + chrome overflow; fixed with an adaptive reserve

- User (running the fixed code): "STILL it's forcing the window to the bottom
  when the agent is running text above, and the N keeps changing." Then: "it's
  from any writing by the agent/COT in the window above working, not just
  when a goal is active" and "the force back to bottom only happens when only
  the widget is viewable, and no working/output/cot".
- Root cause: pi-tui full-renders (`2J+3J`, clearing the terminal scrollback)
  whenever the first changed line is above its tracked viewport top
  (`previousBufferLength − height`). When the widget's block plus the dock
  chrome (pending + status + editor + footer) exceeds the terminal, the
  chat's appended lines and the status line sit above the viewport top →
  every agent write and every status/spinner tick wipes the scrollback →
  forced to the bottom + N churns. The earlier harnesses missed it: their
  editor was empty (chrome ≤ 6) and the user's typed message in the editor
  pushed the chrome past 6. The widget's own latched tick stays safe (its
  changing line is the block's 2nd line).
- Built `experiments/scroll-repro/overflow-probe.mjs` (real TuiMainScreen +
  real GoalWidgetComponent + VStack dock into @xterm/headless): with
  below-chrome 7, chat append → 1 full 2J+3J, status tick → 1 full 2J+3J,
  widget tick → 0; with chrome ≤ 6 all 0.
- Fix: `measureDockReserve(width)` renders the sibling dock containers
  (pending + status + editor + widgetBelow + footer) at the current width and
  caps the widget at `terminalRows − (measuredChrome + 1)`; the latch
  re-evaluates when the measured chrome changes (new `stickyReserve` key).
  The widget's block plus the chrome never exceeds the terminal → the chat
  append point and the status line stay in the viewport → agent writes and
  spinner ticks are in-place diffs, never wipes. Static 6-row reserve remains
  the fallback (mock TUIs, unbounded).
- Validated: overflow-probe wipes 1 → 0 in every geometry (rows 11-20,
  empty/5-line editors); widget renders natural when the chrome is small,
  shrinks when the editor/status grow; full-stack mock at 14 rows with a
  typed editor and the agent working ("⠏ Working…" animating) — pane total
  constant, scrolled-up position held across the churn; 786/786 unit tests
  (new: adaptive cap, reserve-change re-latch, component-level measured
  reserve), tsc + eslint clean, all three harnesses pass --expect with the
  new cap (`rows − (measuredChrome + 1)`).
