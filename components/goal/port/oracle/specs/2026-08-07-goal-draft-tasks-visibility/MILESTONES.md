# Milestones — goal draft presents the complete goal (tasks included)

Spec: `2026-08-07-goal-draft-tasks-visibility`

## 2026-08-07 — Bug report → repro → fix → validation

- User report: "The goal draft is not presenting tasks." The example pasted by
  the user (the 2026-08-07 question-readability goal) was to be replicated in
  style — diagnose, reproduce, fix with red-green tests — and the draft must
  present tasks. Two drafting refinements followed: "nothing should be
  omitted/skipped when the goal is presented" and "the user can just scroll"
  (scrollback is the overflow mechanism; no paging, no new chrome; the churn
  guard stays because removing it would erase scrollback).
- Root-cause analysis: (1) `fitDialogLines`' context-heavy tail-keep drops the
  middle of the proposal confirmation text — the tasks section; (2)
  `propose_goal_draft`'s `renderCall` showed only the objective, no tasks; (3)
  `goal-drafting.test.ts` never sets `terminal.rows` (0 "rows" references), so
  the proposal dialog's slice was untested.
- Reproduction (standalone script with the real
  `buildDraftConfirmationText` + `renderConfirmationTasks` context):
  rows=24 + baseFrame=19 → `maxDialogLines=10`; the 26-line confirmation was
  sliced to head + last 8 lines; "Tasks header visible: false".
- Goal confirmed (`msj7phs4-dby1sy`) with 7 tasks; drafting decisions: nothing
  omitted (frame or scrollback); scrollback is the overflow mechanism; churn
  guard invariant kept.
- Task 1 (red): render-level regression test — `renderProposalDialog` harness
  (rows=24 / baseFrame=19, real draft context, 2-task list) asserting tasks
  header + every task line + options/footer/border within the bound. Verified
  failing on pre-fix code at "tasks header must be visible"; 10/11 in file
  (only the new test failing).
- Task 2 (red): flow-level regression test — `/goal` + `propose_goal_draft`
  with a task list; asserts the complete presentation (every objective section
  + tasks header + every task line) is emitted to the transcript before the
  dialog decision (via the tool's `renderCall`). Verified failing on pre-fix
  code at "tasks header must be in the transcript presentation".
- Task 3 (green): `fitDialogLines` proposal mode
  (`ProposalPresentationSegments` param) + `findProposalPresentationSegments`
  + `optionsStartIndex` tracking in `render()`; proposal confirmations keep
  head + tasks + options/footer/border in-frame. Verified the post-fix frame:
  10 lines — border, "Confirm Goal Draft", tasks header, both task lines,
  options, footer, border. task-1 regression green; questionnaire 11/11.
- Task 4 (green): `propose_goal_draft` `renderCall` now renders the complete
  goal (objective verbatim + full task list, with the F2 derived fallback);
  `goalDraftingPrompt` requires the complete goal in the agent's pre-proposal
  message (prompt-level assertion test added in `tests/goal-draft.test.ts`).
  task-2 regression green; drafting 41/41.
- Task 5: `fitDialogLines` unit tests — byte-identical fits; head+tasks+tail
  within budget (12); blank-strip then task-drop (10); degenerate budgets
  (6/3/1) never exceeding the bound; `findProposalPresentationSegments`
  locating/negatives.
- Hardening during unit tests: the first fallback implementation could exceed
  the bound at degenerate budgets (head + full tail + tasks overflowed);
  reworked to cap the tail from its end (border/footer first) before spending
  room on tasks; added degenerate-budget assertions.
- Task 6 (docs): PRODUCT.md / TECH.md / MILESTONES.md written, including the
  emission channel decision (renderCall — documented in TECH.md).
- Task 7 (validation): full `npm test` 704/704 green, `tsc --noEmit` 0 errors;
  red-green re-verified against the pre-fix code (stash demo: both regression
  tests fail pre-fix, pass post-fix); PRODUCT.md re-read and every item
  mapped to implemented code + passing tests.
- Rejected approaches (documented in TECH.md): `sendMessage`/`appendEntry`
  pre-dialog emission (not available on the tool context), post-decision-only
  summary (too late), paging/windowing UI (user-rejected), removing the churn
  guard (breaks scrollback).

## Residual risks / notes

- The derived-task (F2) branch of the `renderCall` presentation is asserted at
  the render level (explicit steps → derived lines); it is not driven through
  the full dialog flow.
- The render-level regression test fails at module load on pre-fix code
  (the `findProposalPresentationSegments` export does not exist there); the
  original assertion-level failure ("tasks header must be visible") was
  observed before that export existed. The flow-level regression test fails at
  a clean assertion on pre-fix code.
- The `renderCall` task block renders flat `[ ] id: title` lines for explicit
  tasks (no subtree nesting); the dialog's `renderConfirmationTasks` still
  shows the tree. Every task line is present in both.
- Very narrow widths that wrap option labels still rely on the structural
  `optionsStartIndex` anchor for the tail; the fit never exceeds the bound
  (covered by degenerate-budget unit tests).
