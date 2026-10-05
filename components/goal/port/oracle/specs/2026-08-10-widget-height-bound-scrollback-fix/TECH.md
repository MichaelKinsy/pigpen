# Tech: Goal widget terminal-height bound — design

## Mechanism (verified against pi-tui 0.84.x + pi-coding-agent 0.84.x)

### Frame layout

pi's interactive mode builds (interactive-mode.js):

```js
const dock = new VStack([
  { component: pendingMessagesContainer, shrink: 1, minSize: 0 },
  { component: statusContainer,          shrink: 1, minSize: 0 },   // 1 line when spinner visible
  { component: widgetContainerAbove,     shrink: 1, minSize: 0 },   // the goal widget
  { component: editorContainer,          shrink: 1, minSize: 3 },
  { component: widgetContainerBelow,     shrink: 1, minSize: 0 },
  { component: footerContainer,          shrink: 1, minSize: 1 },
]);
const fullscreenLayoutRoot = new VStack([
  { component: transcriptScrollView, basis: 0, grow: 1, shrink: 1, minSize: 1 }, // chat
  { component: dock, basis: "auto", grow: 0, shrink: 1, minSize: 1 },
]);
```

`allocateStackSizes` (pi-tui components/stack.js) sizes each entry from its
intrinsic render height (`component.render(width).length` — for the widget,
whatever `GoalWidgetComponent.render` returns), then distributes overflow via
shrink weights. Entries with `size == minSize` are not shrink candidates, so
with a tall widget: transcript keeps 1 row, editor keeps 3, footer keeps 1,
status is squeezed toward 0, and the widget — the biggest entry — absorbs the
remaining shrink and is **clipped** (paintBox in layout.js only paints rows
inside the box rect ∩ clip; no CURSOR_MARKER in the widget, so no
lineOffset rescue).

### Scrollback wipe path (empirically confirmed by the headless harness)

`TuiMainScreen.doRender` (pi-tui tui-main-screen.js) renders into the main
terminal buffer (scrollback lives in the terminal emulator). The
`fullRender(true)` path emits `\x1b[2J\x1b[H\x1b[3J` — clear screen, home,
**clear scrollback** — on `heightChanged`, `widthChanged`, `clearOnShrink`,
and `firstChanged < prevViewportTop` (a changed line scrolled above the
viewport).

**The widget's own wipe path** (what the equal-height bug actually is, proven
by `experiments/scroll-repro/widget-height-bound.mjs` driving the real
TuiMainScreen): in regular (main-screen) mode pi mounts header, chat, status,
widget, editor, footer as plain Containers and the renderer concatenates
their lines; the viewport pins to the bottom, so everything above the last
`rows` lines lives in terminal scrollback. When the unbounded widget is
`terminalRows` tall, its top lines (header/status) sit **above the viewport**
(`firstChanged` would be `< prevViewportTop`), so **every goal update that
touches the widget's top lines triggers a full render with `\x1b[3J` —
wiping terminal scrollback**. The harness measures exactly this: an
unbounded widget at equal height emits `2J=1, 3J=1` on a goal update; the
bounded widget emits none (differential path). The user's "unable to scroll
up" is scrollback being wiped on every widget update while the frame fills
the terminal.

With the cap (`widget ≤ terminalRows − 6`), the widget's first line is always
≥ `viewportTop + 1` (viewportTop ≤ chat + widget + 5 − rows ≤ chat − 1, first
changed line ≥ chat + 2), so widget updates always take the differential path
and never wipe scrollback.

### Natural widget heights (measured, 12-task goal, width sweep)

| width | compact | expanded | audit | debug |
| --- | --- | --- | --- | --- |
| 120 | 13 | 24 | 8 | 36 |
| 80 | 13 | 24 | 8 | 36 |
| 60 | 11 | 23 | 8 | 34 |
| 40 | 9 | 21 | 8 | 32 |

So the compact dashboard fills a ~9–22-row terminal; the expanded dashboard
fills a ~21–24-row terminal (the F5 state on a common 24-row terminal is the
primary repro); debug mode fills ~32–36.

## Design: deterministic height bound

### Reserve constant

`WIDGET_HEIGHT_RESERVE = 6` — the minimum chrome below/around the widget that
must stay visible:

- 1 status line (working indicator row),
- 3 editor rows (pi's `minSize: 3`),
- 1 footer row (pi's `minSize: 1`),
- 1 chat row (a usable transcript sliver).

With the widget capped at `terminalRows − 6`, the dock intrinsic height is
`1 + (terminalRows − 6) + 3 + 1 = terminalRows − 1`, leaving ≥ 1 row for the
transcript — no shrink distribution is needed, so **nothing gets clipped**:
the widget renders exactly its cap lines and every row is painted.

Reserve kept at the minimum that still guarantees the chat row: a larger
reserve would slice dashboards that genuinely fit — e.g. the expanded
24-line dashboard on a 30-row terminal renders unchanged (24 + 5 chrome +
1 chat = 30).

### Slice strategy: keep the head

`boundWidgetRenderLines(lines, terminalRows)`:

```ts
export function boundWidgetRenderLines(lines: string[], terminalRows: number | undefined): string[] {
  if (!terminalRows || terminalRows <= 0) return lines;          // unbounded (mocks, headless, /goal-status)
  const cap = Math.max(1, terminalRows - WIDGET_HEIGHT_RESERVE); // WIDGET_HEIGHT_RESERVE = 6
  if (lines.length <= cap) return lines;                         // fits -> identical to today
  return lines.slice(0, cap);                                    // deterministic head slice
}
```

For `terminalRows ≤ 6` the cap floors at 1 (tiny-terminal edge; layout clips
whatever it must — the bound only guarantees the widget never makes it worse).

Head slice (not the questionnaire's tail slice) because the widget's content
priority is top-down: goal identity header → status → usage → progress → task
list → details → hints. The interactive task list and status survive; the
bottom chrome (secondary details, footer hints) is what drops when the
terminal is too short. The slice is a pure index function — deterministic,
stable across renders, so the widget height never oscillates.

### Where the bound applies

All widget states flow through one guard in `extensions/widgets/goal-widget.ts`:

- `GoalWidgetComponent.render(width)` reads
  `(this.tui as unknown as { terminal?: { rows?: number } }).terminal?.rows`
  (same cast pattern as the questionnaire churn guard; the mock TUI has no
  `terminal` by default → undefined → unbounded → existing test surface
  untouched) and applies `boundWidgetRenderLines` to the final line array —
  covering the audit-result-card early return and the debug-panel append.
- `renderGoalWidgetLines` gains an optional `terminalRows?: number` option
  (default undefined) and applies the bound before returning, so direct
  callers can opt in; the component passes it for the dashboard paths.
- The pure renderers (`renderCompactDashboard`, `renderExpandedDashboard`,
  `renderAuditorDashboard`, `renderAuditResultCard`,
  `renderUnfocusedDashboard`) are unchanged — `/goal-status` and golden tests
  stay byte-identical.
- No structural changes to the task-viewport row budgets; the expanded
  dashboard's task window is preserved (the slice may cut its tail on short
  terminals, with the `… +N more` indicator preserved when the window itself
  fits).

### Why this restores scroll-up in the equal-height case

- The widget stops at `terminalRows − 6`. The widget's first rendered line
  then always sits at or below `viewportTop + 1` (viewportTop ≤ chat + widget
  + 5 − rows ≤ chat − 1; first changed line ≥ chat + 2), so **every widget
  update takes the differential path** — the `firstChanged < prevViewportTop`
  bail-out that triggers the `\x1b[2J\x1b[H\x1b[3J` full render (scrollback
  wipe) is unreachable from the widget. The harness measures 0 wipes for the
  bounded widget vs 2J=1, 3J=1 for the unbounded one.
- The chat (and the widget's own head, when it overflows the viewport) stays
  in the terminal scrollback and is read by scrolling up; the editor/footer
  stay in the viewport when the chat is short. Nothing is wiped by the
  widget, so scroll-up keeps working.
- Height is deterministic (`min(natural, cap)`), so the widget never shrinks
  on its own (goal updates, spinner ticks, audit animation) — the
  `clearOnShrink` full-render path stays unreachable from the widget too.

## Validation (headless harness, real TuiMainScreen)

`experiments/scroll-repro/widget-height-bound.mjs` drives the REAL pi-tui
main-screen renderer with pi's real regular-mode frame layout (header, chat,
status, widget, editor, footer as plain Containers) and the real
GoalWidgetComponent at the equal-height repro. `--expect` mode asserts:

1. widget rendered ≤ `rows − WIDGET_HEIGHT_RESERVE`;
2. chat reachable by scrolling up (frame taller than the terminal → top lines
   in scrollback) or on screen;
3. editor + footer visible;
4. widget header + status line preserved (head slice);
5. a goal update emits **no** `\x1b[2J`/`\x1b[3J`/1049 (scrollback never
   wiped).

Measured (rows=24, expanded dashboard, chat=10): fixed widget renders 18
lines (cap 18), frame 33, viewportTop 9 — chat and widget header above the
viewport in scrollback, editor/footer visible, update emits 0 wipes. The
pre-fix comparison (unbounded widget) renders 24 lines, frame 39, viewportTop
15 — and the **same update emits 2J=1, 3J=1** (scrollback wipe: the bug).
Compact equal-height (13 rows) renders 7 ≤ cap 7 with 0 wipes; the fits case
(30 rows, expanded) renders 24 — byte-identical to the unbounded render.

## Implementation (done)

1. `extensions/widgets/goal-widget.ts`:
   - `export const WIDGET_HEIGHT_RESERVE = 6;`
   - `export function boundWidgetRenderLines(lines, terminalRows)` —
     deterministic head slice, no-op when rows unknown.
   - `GoalWidgetComponent.render` reads `terminalRows` from the TUI via the
     questionnaire's cast pattern and bounds both return paths
     (audit-result-card early return; dashboard/debug path).
   - `renderGoalWidgetLines` accepts optional `terminalRows?: number` and
     applies the bound (all dashboard variants incl. audit + unfocused).
2. `tests/tui-test-utils.ts`: `createMockTUI({ terminalRows?: number })` —
   adds `terminal: { rows }` only when provided (default: absent → unbounded,
   existing tests untouched).
3. Tests in `tests/goal-widget.test.ts` (10 new): `boundWidgetRenderLines`
   unit tests; component capped at equal height (compact 13-row, expanded
   24-row); fits case byte-identical; audit/result-card/debug/unfocused
   bounded; determinism.
4. Validation harness `experiments/scroll-repro/widget-height-bound.mjs`
   (report + `--expect`).
5. `npm test` 0 failures; `npm run check` clean (see MILESTONES).

## Risks / tradeoffs

- **Short terminals**: content below the cap is not written to the buffer
  (same accepted tradeoff as the questionnaire fix — pre-regression wrote it
  and then the close wiped everything).
- **Debug panel**: on short terminals the debug keybinding legend may be
  sliced; acceptable (dev-only surface, visible on normal terminals).
- **Reserve is a constant, not measured**: the layout could in principle
  differ from the 6-line estimate (other extensions adding below-editor
  widgets, pending-message rows). The bound guarantees the widget alone never
  exceeds `terminalRows − 6`; the layout then clips conservatively rather
  than the widget dominating the frame. The head slice keeps the primary
  content in frame even under unexpected chrome.
