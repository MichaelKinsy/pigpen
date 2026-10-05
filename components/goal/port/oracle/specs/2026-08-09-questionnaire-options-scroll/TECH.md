# TECH — Questionnaire bounded-fit audit and fix

Date: 2026-08-09
Spec: `specs/2026-08-09-questionnaire-options-scroll/`

## Where the fit happens

`extensions/goal-questionnaire.ts`:

- `computeDialogLineLimit` (L37): `maxDialogLines` from terminal rows and the
  base frame (`rows - baseFrame + 1`, floor 10, or the `rows - 4` fullscreen
  fallback).
- `runGoalQuestionnaire` render (L495–725): builds the full dialog lines —
  top border, tabs, question (protected head), context, auditor line, options
  (`renderOptions`, L633, records `optionsStartIndex`), footer hint, bottom
  border — then applies `fitDialogLines(lines, maxDialogLines, protectedCount,
  optionsImmediatelyAfterHead, proposalSegments)` (L712) when bounded.
- `fitDialogLines` (L123): the fitter. Three paths:
  - proposal segments → `fitProposalPresentation` (segment protection);
  - `optionsImmediatelyAfterHead` (select-mode question with no context) →
    keep head + TOP options + footer/border;
  - otherwise (context-heavy / input / submit) → keep head + tail-from-end.

## Loss-vector audit (V1–V7)

| # | Vector | Location | Severity | Status |
|---|--------|----------|----------|--------|
| V1 | Select-mode fast path keeps only the **TOP** options: budget spent on option 1's wrapped lines; options 2+ dropped entirely. **Reproduced**: question(1) + opt1(4 wrapped lines) + opt2(3) + footer + border = 12 lines, bound 9 → opt2 invisible. This is the reported symptom. | `fitDialogLines` L139–156 | **Critical** | Fixed (F1) |
| V2 | Context-heavy path keeps `rest.slice(rest.length - budget)` (tail-from-end): can slice **into the middle of the options block** and drop the first/recommended option when options are long. | `fitDialogLines` L157–158 | High | Fixed (F1) |
| V3 | Input-mode option hints (rendered before the editor) can be dropped by the tail-keep when bounded; editor is correctly prioritized, so severity is low — hints are advisory only. | render L652–656 | Low | By design (editor stays; hints reachable after scroll in select view) |
| V4 | Multi-question tabs each render independently; any long tab has the same V1/V2 exposure. | render tabs L615–631 | High | Fixed (F1) |
| V5 | No scrollback fallback: `renderCall` emits only the tool name; dropped options are unrecoverable (blind options). | `goal-drafting.ts` L292 | Critical (amplifier) | Fixed (F1 — in-dialog scroll; no fallback needed) |
| V6 | Submit summary tab (all questions + answers) overflows with many questions; tail-keep drops the top answers. | render L657–665 | Medium | Fixed (F1 — viewport scroll applies) |
| V7 | ANSI-styled lines: mock themes emit no ANSI, so render tests built with `createMockTheme` never exercise the fit on real styled lines (same blind spot as the proposal fix). | tests/tui-test-utils.ts | Test blind spot | Fixed (F2 — ANSI-styled regression tests) |

## Fix F1 — in-dialog viewport scrolling (select-mode + submit tabs)

Replace the option-slicing paths with a `less`-style viewport:

- State: `scrollTop` (persisted per render, reset on tab switch / input mode),
  `needsFollow` flag, `optionRanges: Array<[start, end]>` recorded by
  `renderOptions`.
- Full content `L` is always built completely (never truncated).
- If `L.length <= maxDialogLines`: unchanged behavior (all existing tests for
  the unclipped path still hold).
- Else, viewport over `L`:
  - content window `c = maxDialogLines - 1` rows; `scrollTop ∈ [0, len-1-c]`
    (the final border line is the reserved bottom edge).
  - bottom edge row: `… +N more · PgUp/PgDn scroll` (dim) when
    `hiddenBelow > 0`, else the bottom border. The indicator never replaces
    option content.
  - top indicator: `▲ N more` (dim) replaces the first content row when
    `hiddenAbove > 0` (dashboard precedent: "↑ N more task(s)").
- Keys (select mode only; editor owns input mode):
  - `Key.pageUp`/`Key.pageDown`: page the viewport by `c`.
  - `Key.ctrl("up")`/`Key.ctrl("down")`: line-scroll.
  - `↑`/`↓`: selection move sets `needsFollow`; render() nudges `scrollTop` so
    the selected option's range is inside the window.
- Input mode: unchanged tail-keep (editor priority) — the fit no longer
  special-cases select-mode top-options; input mode keeps its current path.
- Proposal confirmations: `fitProposalPresentation` unchanged (segment
  protection + scrollback fallback already guarantee task + auditor + option
  visibility).

### Churn-guard proof

At any `scrollTop`, output length = content window `c` + 1 edge row =
`maxDialogLines` exactly when clipped, or `L.length` when it fits — never more
than `maxDialogLines`.

## Fix F2 — regression tests with ANSI-styled lines

- Unit tests on `fitDialogLines` with the new viewport signature: default
  top-aligned view, bottom-edge indicator, end-of-content border, page/line
  scroll adjustment, selection auto-follow, never exceeds bound, no option
  dropped mid-block.
- Render-level test with a real-ANSI theme (like the proposal test): bounded
  questionnaire with long question + 2 options → option 2 reachable after
  scroll, question fully present in the logical buffer, indicators shown.
- Update the existing select-mode `fitDialogLines` unit tests (L233–248) to
  the corrected viewport contract.
