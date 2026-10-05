# MILESTONES — 2026-08-09 questionnaire options scroll

Implementation log for `specs/2026-08-09-questionnaire-options-scroll/`.

## Audit (2026-08-09)

- Reproduced the reported bug at the fit level: `fitDialogLines` select-mode
  fast path (options right after the head) spends the whole budget on option
  1's wrapped lines and drops options 2+ entirely; the context-heavy path
  keeps tail-from-end and can slice into the middle of the options block,
  dropping the first/recommended option.
- Confirmed the amplifier: `renderCall` for `goal_questionnaire` emits only
  the tool name, so there is no scrollback fallback — a hidden option is a
  blind option.
- Confirmed the test blind spot: mock themes emit no ANSI, so the fit was
  only ever exercised on unstyled lines.
- Audit table V1–V7 documented in TECH.md (severity, location, status).
- **User decision** (goal_question in the guided draft): when bounded, keep
  the full question (never truncate) and make the dialog internally
  scrollable so all options are reachable — the churn-guard bound stays.

## Implementation (F1 — viewport scrolling)

- `fitDialogLines` gains a `scroll?: DialogScrollState` parameter (scrollTop /
  needsFollow / optionRanges / followIndex) and a `dimStyle` callback; the
  select-mode top-options fast path and the boolean 4th arg are removed.
- New `fitDialogViewport`: content window = `maxDialogLines - 1` rows, the
  last row reserved as the bottom edge — `… +N more · PgUp/PgDn scroll`
  (dim) when clipped below, else the bottom border. `▲ N more` replaces the
  first content row when scrolled down (dashboard precedent). Selection
  auto-follow nudges scrollTop so the selected option's range is visible.
- Render path: `optionRanges` recorded in `renderOptions`; scroll state
  passed for all non-input tabs (select-mode questions + submit summary);
  input mode keeps the legacy tail-keep (editor priority).
- Input: `Key.pageUp`/`Key.pageDown` page by the window, `Key.ctrl("up")`/
  `Key.ctrl("down")` line-scroll — both without moving the selection; `↑`/`↓`
  still select and set `needsFollow` for auto-follow.
- `enterSubmitTab()` helper resets scrollTop/needsFollow when entering the
  submit tab (the earlier `else optionIndex = 0` sites left a stale viewport
  position); `enterQuestion` resets per question tab.
- Proposal confirmations untouched: `fitProposalPresentation` still keeps
  head + tasks + auditor + options within the bound (its tests pass
  byte-identical).

### Implementation notes / findings

- **Dead code avoided**: a footer-hint augmentation loop ("• PgUp/PgDn
  scroll" appended to the visible footer) was added then removed — geometry
  proves the footer can never be in-window while clipped (it sits directly
  above the border, which is the reserved bottom edge). The bottom-edge
  indicator is the scroll advertisement.
- **Degenerate bound**: `viewport[0] = ...` on an empty viewport (bound 1,
  contentWindow 0) silently created a phantom element — guarded with
  `viewport.length > 0`.
- **Test-data gotcha**: questions that include "Write your own answer..." in
  `options` render it twice — `displayOptions` appends the custom option
  itself when `allowCustom !== false`. New tests omit the sentinel.

## Tests (F2 — ANSI-styled regression)

- Unit: `fitDialogLines` viewport — top-aligned default view + bottom-edge
  indicator, clamped end-of-content view (border), reachability union over
  all scroll positions, selection auto-follow nudge (below-window and
  visible cases), `▲` indicator, never-exceeds-bound across the scroll range
  at degenerate budgets.
- Flow (real component, ANSI-emitting theme, bound 10):
  - bounded agent question (the reported repro shape): question fully
    visible at the top, recommended option visible, bottom edge advertises
    "… +N more · PgUp/PgDn scroll"; PageDown reaches option 2, PageUp
    returns; union over 12 PageDowns covers every option label; Ctrl+↓
    line-scrolls within the bound.
  - multi-question tabs: scroll on question 1, Tab to question 2 opens from
    the top (no stale viewport); question 2 options reachable via PageDown.
  - selection auto-follow: ↓ walk past the fold keeps the selected option
    visible with its `> N.` marker.
  - input mode: editor prompt + submit hint visible, dialog bounded
    (tail-keep unchanged).
- Existing tests updated: the two select-mode `fitDialogLines` unit tests
  rewritten to the viewport contract; the `false` boolean 4th args removed
  from proposal tests; the rows=24/baseFrame=19 "agent question stays
  readable" regression now asserts the bottom edge is the border OR the
  scroll indicator.

## Validation

- `node scripts/run-unit-tests.mjs` (all): 720 unit + 28 integration + 2 e2e
  pass, 0 failures.
- `npm run check` (`tsc --noEmit`): clean.
- Test manifest self-check: OK.
- CHANGELOG updated.
