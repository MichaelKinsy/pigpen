# Milestones — agent questions readable in the questionnaire dialog

Spec: `2026-08-07-question-readability-fix`

## 2026-08-07 — Bug report → repro → fix → validation

- User report: unable to read a question raised by the agent during an active
  goal run (texdist goal). The pasted transcript shows the goal panel plus a
  `goal_question` dialog whose top border is missing and whose question text
  was clipped.
- Root-cause analysis: the churn-guard tail-slice in
  `extensions/goal-questionnaire.ts` render() keeps the tail and drops the
  head (top border → question). `7bc07ee` (same day) tightened the height
  bound for pi 0.84's fullscreen renderer, and the goal panel + chat frame
  consume most rows, so the slice triggers in normal use.
- Reproduction (standalone script): rows=24 + baseFrame=19 →
  `maxDialogLines=10`; the question "via uv too?" was fully clipped and the
  first rendered line was mid-option text. The mock TUI never sets
  `terminal.rows` / `previousLines`, so no existing test exercised the slice.
- Goal confirmed (`msj6kyoh-hae8qi`) with 3 tasks; drafting decision: keep
  the 383ae52 surface, guarantee the question, add no new dialog chrome.
- Task 1 (red): render-level regression test in
  `tests/goal-questionnaire.test.ts` (rows=24 / baseFrame=19, long wrapped
  options, recommended=0) asserting question text, top border, recommended
  option, and bottom border are visible; verified failing on pre-fix code;
  committed.
- Task 2 (green): `fitDialogLines` + structural `protectedCount` in render;
  the raw tail-slice is replaced. Unit tests added for the helper. Verified
  post-fix render dumps: reported repro (question + top border + recommended
  option visible), proposal confirmation (all three options visible), input
  mode (question + editor + footer), multi-tab questionnaire.
- Hardening during review: the options-top branch could overflow the height
  bound at degenerate budgets (e.g. an 8-row terminal → maxDialogLines=4,
  keeping footer + border + leading blank exceeded the bound). Reworked to
  reserve the tail (footer + border, as many as fit) first, then spend the
  remaining room on the top options; added a dedicated no-overflow unit test
  suite for budgets 1–4.
- Task 3 (validate/docs): full `npm test` 695/695 green, `tsc --noEmit` 0
  errors; PRODUCT.md / TECH.md / MILESTONES.md written; committed.
- Rejected approaches (documented in TECH.md): pure head-priority (cuts
  proposal options — trapped user), content-regex option detection (ANSI
  fragility), scrolling/windowing UI (user-rejected direction).
