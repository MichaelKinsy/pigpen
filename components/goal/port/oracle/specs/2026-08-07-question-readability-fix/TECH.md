# Tech: Height-bound slicing must never hide the question

Spec: `2026-08-07-question-readability-fix`

## Problem

`runGoalQuestionnaire` renders into pi's editor slot. The "churn guard"
(2026-08-04-goal-confirmation-scroll-fix) bounds the dialog to the terminal
height so the opened frame never exceeds the screen — taller content was
tail-sliced (kept the actionable options/footer, dropped the head). With the
goal panel consuming frame rows (and pi 0.84's fullscreen renderer not
exposing `previousLines`, so `computeDialogLineLimit` falls back to
`rows - 4`), the head — the top border and the question — got clipped in
normal use. The tail-slice prioritized the wrong end for questions.

## Design

Two behaviors conflict when space is tight:

1. **Agent questions**: the question is the content; the recommended top
   options matter. Head and top options must survive.
2. **Proposal confirmations** (`showProposalDialog`): the `context` block is
   the proposal text (large and sliceable — the durable proposal summary is
   written to the transcript regardless); the short Confirm/Continue/Cancel
   options and the footer are the actionable part. The pre-fix tail-keep was
   right for these.

A single priority order cannot satisfy both, so `render()` records a
structural `protectedCount` (lines up to the question line: top border, tabs,
question incl. wraps) and the slice asks whether the options block starts
immediately after the head (plain select-mode question: no `context`, not
input mode, not the submit tab). `fitDialogLines`:

- never returns more than `maxDialogLines` (frame-overflow invariant kept);
- `optionsImmediatelyAfterHead` → head + top options + footer + bottom border
  (leading blank separators are skipped so the budget goes to option lines);
- otherwise → head + tail-keep of the rest (context sliced from the head,
  options/footer/border intact).

Detection is structural, not content-based, so it survives themed (ANSI)
rendering — no regex over styled lines.

## Alternatives considered

- **Pure head-priority** (context first, options last): would cut
  Confirm/Continue/Cancel in tight proposal dialogs — a trapped user, worse
  than the reported bug. Rejected.
- **Pure tail-priority** (pre-fix): the reported bug. Rejected.
- **Scrollable options window / ▴▾ indicators**: user-rejected direction
  (2026-08-04-goal-confirmation-scroll-fix: "no overlay options, no
  windowing, no internal scrolling UI"). Not introduced.
- **Content-regex detection** of option lines: fragile with ANSI themes.
  Rejected in favor of the structural flag.

## Invariants

- `fitDialogLines` output length ≤ `maxDialogLines` in every branch — the
  options-top path reserves the tail (footer + bottom border, as many as fit)
  before spending any room, so degenerate budgets (e.g. 8-row terminals) can
  never overflow the bound.
- Under the bound, `render()` output is byte-identical to 383ae52 (the slice
  never runs).
- Input mode never takes the options-top path (the editor must stay visible).
- The bottom border is always the last rendered line.
