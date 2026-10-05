# Product: Agent questions stay readable in the questionnaire dialog

## Status

Implemented and validated (goal `msj6kyoh-hae8qi`, 2026-08-07).

## Motivation (bug report)

While a goal run was active in the texdist project, the agent raised a
`goal_question` ("via uv too?") through `runGoalQuestionnaire`. The pi-goal-x
goal panel + chat frame consumed most of the terminal, and the dialog's
terminal-height "churn guard" tail-sliced the top of the dialog: the top
border and the question text were clipped, leaving only option fragments and
the footer. The user could not read the question the agent had asked.

Reproduced deterministically: with `terminal.rows = 24` and a 19-line
pre-dialog frame (goal panel + chat + footer), the dialog is bounded to 10
lines (`computeDialogLineLimit`). The pre-fix render ended with

    lines = lines.slice(lines.length - maxDialogLines);

which keeps the tail (options/footer/bottom border) and discards the head —
top border first, then the question text. The mock TUI never set
`terminal.rows` / `previousLines`, so no test exercised the slice.

## Final behavior

`runGoalQuestionnaire`'s render keeps a protected head — top border + tabs
(multi-question dialogs) + the question line (including wraps) — and spends
the rest of the terminal-height budget as before:

- **Plain agent questions** (options start right after the question, no
  context block): the top options are kept (the recommended/first option
  stays visible) plus the footer hint and the bottom border.
- **Context-heavy dialogs** (proposal confirmations: `showProposalDialog`
  passes the whole proposal as `context`): the head and the tail are kept —
  Confirm/Continue/Cancel options, footer, and bottom border remain visible
  exactly as the pre-fix tail-slice behaved; the context block is sliced from
  its head.
- **Input mode and multi-question tabs**: the question, the editor, and the
  footer stay readable (tail-keep).
- Content that fits renders byte-identical to the pre-regression (383ae52)
  UI; the dialog never exceeds the terminal-height bound, so closing it can
  never trigger pi-tui's shrink full-render / scrollback erasure.

## Files

- `extensions/goal-questionnaire.ts` — new exported `fitDialogLines(lines,
  maxDialogLines, protectedHead, optionsImmediatelyAfterHead)`; `render()`
  tracks `protectedCount` structurally (after the top border, the tabs line,
  and the question line in each branch) and replaces the raw tail-slice with
  `fitDialogLines`.
- `tests/goal-questionnaire.test.ts` — render-level regression test at the
  reported geometry (rows 24 / baseFrame 19, long wrapped options,
  recommended option) asserting question text, top border, recommended
  option, and bottom border are visible; four unit tests for `fitDialogLines`
  (under-limit unchanged, tail-keep for context dialogs, options-top-keep for
  select questions, and no-overflow guarantees at degenerate budgets).

## Validation

- `npm run check` (tsc --noEmit): 0 errors.
- `npm test`: 696/696 pass, including the new regression + unit tests.
- The regression test fails (red) on pre-fix code and passes after the fix.
- No-overflow guarantees covered by unit tests at degenerate budgets
  (maxDialogLines 1–4), including an 8-row-terminal case.
- Post-fix render dumps verified for: the reported repro (question + top
  border + recommended option visible), proposal confirmation (all three
  options visible), free-text input mode (question + editor + footer), and a
  multi-question tab questionnaire.
