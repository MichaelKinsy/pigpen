# MILESTONES — Questionnaire custom answers + all-options-in-frame

Date: 2026-08-10
Spec: `specs/2026-08-09-questionnaire-custom-answers-and-options/`

## t1 — Root-cause analysis

- **Bug A (custom-answer input not accepted)**: reproduced — in input mode the
  rendered editor contained no `CURSOR_MARKER` and `setShowHardwareCursor`
  stayed `[false]` for the whole dialog lifetime. Root cause: the custom-dialog
  container is not `Focusable` (no propagation to the embedded `Editor`; pi-tui
  only emits the marker when `focused === true`) + the hardware cursor is
  force-hidden at open and restored only in `submit()`. pi `docs/tui.md`
  (Focusable Interface / Container Components with Embedded Inputs) confirms
  this breaks IME input positioning. ASCII typing lands at the component level
  (probe), so the defect is cursor anchoring, not dispatch.
- **Bug B (options not all visible)**: reproduced at rows=24/baseFrame=19
  (bound 10) with 5 options + custom row — only options 1–4 rendered with
  `… +4 more · PgUp/PgDn scroll`; `fitDialogViewport` windows all dialog lines
  behind `scrollTop` (from `specs/2026-08-09-questionnaire-options-scroll`).
- Severities: A high (custom answers unusable for IME users / no cursor
  feedback), B medium-high (arrow-key scrolling required on every bounded
  questionnaire; the custom-answer row — last — is the most likely hidden).

## t2 — Input fix (commit `610e934`)

- Dialog component now implements `Focusable`: `focused` getter/setter
  propagates to the embedded `Editor` while input mode is active; `Editor`
  emits `CURSOR_MARKER` at the cursor position (verified: exactly 1 marker
  line in a focused input-mode render).
- Hardware cursor toggled with the editor surface: on while typing, off in
  select mode, restored to the pre-dialog value on close (submit).
- Tests: marker + cursor-call assertions, ASCII + CJK typing lands, custom
  answer flows to the submit summary as `(wrote)`; proposal dialog
  (`allowCustom: false`) regression-free.

## t3 — Options-in-frame fix (commit `62937c3`)

- New `fitOptionsInFrame`: all option lines + both borders always in frame;
  question/context yields first. Middle lines drop from the end (blanks →
  auditor → Current → context), then tail slack (blank, footer), then
  question wrap lines. Scroll viewport remains ONLY as a last resort when the
  option block + bottom border alone exceed the bound (verified: 12-option
  pathological case → viewport + PageDown reachability).
- Churn guard preserved: fitted dialog never exceeds `maxDialogLines`.
- Proposal confirmations keep `fitProposalPresentation` segment protection.
- Rewritten scroll-era tests → options-in-frame contract; added overflow +
  pathological tests. Questionnaire suite 30/30; related suites 103/103.

## t4 — Validation

- Manifest rewritten (56 unit, 1 integration, 1 e2e).
- tsc 0 errors; ESLint clean; **795/795 tests**; selfcheck OK; pack dry-run
  clean (53 files); audit 0 vulns; `bench:gate:naf` PASS; diff-check clean.
