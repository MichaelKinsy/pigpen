# Product: The goal draft presents the complete goal — tasks included

## Status

Implemented and validated (goal `msj7phs4-dby1sy`, 2026-08-07).

## Motivation (bug report)

"The goal draft is not presenting tasks." When the agent proposed a goal draft
via `propose_goal_draft`, the confirmation dialog did not present the tasks:
under tight terminal heights the dialog's churn guard dropped the
"Tasks proposed for confirmation" section entirely, so the user was asked to
confirm a draft without ever seeing the task plan. (The example the user
pointed to — the 2026-08-07 question-readability fix — was replicated in
style: diagnose the root cause, reproduce it, fix it with red-green regression
tests, and write a complete goal contract.)

Two drafting refinements from the user shaped the fix:

1. **Nothing should be omitted/skipped when the goal is presented** — every
   section (objective, success criteria, boundaries, constraints, verification
   contract) and every task line.
2. **The user can just scroll** — the overflow mechanism is terminal
   scrollback, not paging and not new dialog chrome.

The churn guard stays: it exists to prevent pi-tui's shrink full-render
(`\x1b[2J\x1b[H\x1b[3J`) from erasing scrollback, so removing it would break
the very scrolling the user relies on. The omitted content must be guaranteed
in the buffer instead.

## Root cause (verified by reproduction)

- **Dialog frame:** `showProposalDialog` runs the draft through
  `runGoalQuestionnaire`, rendering the full confirmation text (objective box +
  "Tasks proposed for confirmation:" + task lines) as question context. When
  the churn guard engages, `fitDialogLines`' context-heavy branch kept the
  protected head (top border + "Confirm Goal Draft") and the tail
  (options/footer/bottom border) and discarded everything between — including
  the tasks section. Reproduced deterministically: `terminal.rows = 24` +
  `baseFrameLines = 19` → `maxDialogLines = 10`; a 26-line confirmation was
  sliced to head + last 8 lines ("Tasks header visible: false").
- **Scrollback:** `propose_goal_draft`'s tool-call display (`renderCall`)
  rendered only "propose_goal_draft <objective>" — no task list — and the
  durable proposal summary is written to the transcript only after the
  decision. Nothing testable exercised this: `goal-drafting.test.ts` never
  sets `terminal.rows` (0 "rows" references).

## Final behavior

- **In-frame:** the confirmation dialog keeps the protected head (top border +
  question), the tasks section (header + every task line), and the
  options/footer/bottom border within the height bound; only the objective-box
  middle is sacrificed in-frame (it is always in the scrollback presentation).
  The bound is never exceeded; content that fits renders byte-identical
  (383ae52 surface preserved); plain agent questions and other context-heavy
  dialogs keep their existing behavior.
- **Scrollback ("the user can just scroll"):** `propose_goal_draft`'s
  `renderCall` renders the complete goal — the full objective contract (all
  sections) plus the full task list ("Tasks proposed for confirmation:" for
  explicit tasks; "Tasks derived from the objective..." for the F2 derived
  fallback) — into the terminal buffer the moment the tool call starts,
  *before* the dialog opens, so the user can scroll up and re-read everything
  while deciding.
- **Drafting prompt:** `goalDraftingPrompt` requires the agent to write the
  complete goal (every section + the full task list) into its message before
  proposing; omission is forbidden.
- **No new dialog chrome**, no paging, no scroll indicators — the terminal's
  scrollback is the overflow mechanism.

## Success criteria (all met)

1. Render-level regression test (rows=24, baseFrame=19, multi-task
   confirmation): "Tasks proposed for confirmation:" header + every task line
   + Confirm/Continue/Cancel options + footer + bottom border visible within
   `maxDialogLines` — fails on pre-fix code, passes after.
2. Flow-level regression test: the complete goal presentation (every objective
   contract section + tasks header + every task line) is emitted to the
   transcript before the dialog decision — fails on pre-fix code, passes
   after.
3. No regression on the questionnaire surface: content that fits renders
   byte-identical; plain select-mode questions keep top options (recommended
   first); other context-heavy dialogs keep tail-keep; options/footer/bottom
   border always visible; the opened frame never exceeds the bound.
4. `npm run check` clean; `npm test` green (704/704 including the new tests).

## Boundaries

- **In scope:** `extensions/goal-questionnaire.ts` (`fitDialogLines` proposal
  mode + `findProposalPresentationSegments` + `optionsStartIndex` tracking),
  `extensions/goal-drafting.ts` (`renderCall` complete presentation),
  `extensions/goal-draft.ts` (drafting prompt), tests (render-level
  regression, flow-level regression, `fitDialogLines` unit cases, prompt-level
  assertion), spec docs.
- **Out of scope:** goal widget/panel layout changes; pi-tui / pi-coding-agent
  API changes (`renderCall` is existing `ToolDefinition` API); dialog paging,
  scroll indicators, or PgUp/PgDn/Home/End windowing (rejected in
  2026-08-04-goal-confirmation-scroll-fix — scrollback IS the overflow
  mechanism per user direction); non-proposal questionnaire dialogs; the
  2026-08-07 question-readability fix's behavior (must not regress).

## Constraints (kept)

- The opened dialog frame never exceeds the terminal height (churn guard
  invariant; scrollback never wiped).
- Nothing omitted: every section of the presented goal is present either in
  the dialog frame or in the scrollable buffer.
- No new dialog chrome; byte-identical rendering when content fits.
- AGENTS.md conventions: spec dir with PRODUCT.md / TECH.md / MILESTONES.md.

## Verification contract

`npm run check` (0 errors) ✓; `npm test` (0 failures, 704) ✓; the render-level
and flow-level regression tests fail on pre-fix code and pass after the fix
(red-green, see MILESTONES.md) ✓; every PRODUCT.md item maps to implemented
code and passing tests (re-read and confirmed in task 7) ✓.
