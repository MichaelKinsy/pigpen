# Milestones: Goal widget terminal-height bound — scrollback fix

## 2026-08-10 — Root cause confirmed, design decided (spec written)

- User report: "when the terminal window is *just* the height of the
  pi-goal-x ui, the user is unable to scroll up".
- Confirmed mechanism against pi-tui 0.84 / pi-coding-agent 0.84 sources:
  - In regular (main-screen) mode the frame is written top-to-bottom with the
    viewport pinned to the bottom; an unbounded widget pushes its own top
    lines (and the chat) above the viewport.
  - pi-tui's `TuiMainScreen.doRender` wipes terminal scrollback with
    `\x1b[2J\x1b[H\x1b[3J` on heightChanged (any resize), clearOnShrink, and
    `firstChanged < prevViewportTop` — the same mechanism the questionnaire
    churn guard (2026-08-04 spec) neutralized for dialogs.
  - Measured natural widget heights (12-task goal): compact 9–13, expanded
    21–24, audit 8, debug 32–36 (width 120→40). Expanded dashboard on a
    24-row terminal is the primary repro.
- Design: `WIDGET_HEIGHT_RESERVE = 6` (status 1 + editor 3 + footer 1 + chat
  1); widget caps its render at `terminalRows − 6` with a deterministic head
  slice; optional `terminalRows` param (default unbounded) keeps pure callers
  (`/goal-status`, golden tests) byte-identical; mock TUI gains optional
  `terminal.rows`.
- PRODUCT.md / TECH.md written.

## 2026-08-10 — Implemented + validated

- Implementation (`extensions/widgets/goal-widget.ts`): `boundWidgetRenderLines`
  + `WIDGET_HEIGHT_RESERVE = 6`; `GoalWidgetComponent.render` reads
  `tui.terminal.rows` (questionnaire cast pattern) and bounds every return
  path (audit result card, dashboard, debug); `renderGoalWidgetLines` takes
  optional `terminalRows`. Renderers untouched → `/goal-status` and golden
  tests byte-identical.
- Tests: `createMockTUI({ terminalRows })` (default absent → unbounded); 10
  new tests in `tests/goal-widget.test.ts` (bound helper, compact/expanded
  equal-height, fits-unchanged, audit/result-card/debug/unfocused, determinism).
- Harness `experiments/scroll-repro/widget-height-bound.mjs` drives the real
  TuiMainScreen + real widget at the equal-height repro. Key measured result:
  the **pre-fix unbounded widget emits 2J=1, 3J=1 on a goal update** — the
  scrollback wipe behind "can't scroll up" (widget top lines sit above the
  viewport → `firstChanged < prevViewportTop` → full render). Post-fix: 0
  wipes on every scenario; widget ≤ cap; chat reachable in scrollback;
  editor/footer visible; fits case byte-identical.
- `npm test`: 773 tests, 0 failures. `npm run check` clean. eslint clean.
- Note: the existing `before-after-churn.mjs` harness is broken against
  pi-tui 0.84 (`TUI` is no longer a runtime export); the new harness imports
  `TuiMainScreen` from the package index instead.
