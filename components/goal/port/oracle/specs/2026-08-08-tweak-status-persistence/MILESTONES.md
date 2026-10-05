# Milestones: Tweak status persistence + proposal presentation fixes

## 2026-08-08 — Audit + spec

- Confirmed the goal: `/goal-tweak` completion-status persistence, auditor
  toggle at propose, single-task-set presentation.
- Audited the full tweak path (merge → apply → disk → reload). V1–V7 verified
  correct; gaps G1–G4 identified:
  - G1: auditor status line dropped by the bounded-dialog fit (ANSI-unaware
    task scan collapses the tasks section to its header; auditor line sits in
    the sacrificed middle). Reproduced with rows=24/baseFrame=19 (10-line
    bound) and an ANSI-emitting theme; the mock theme hides the class.
  - G2: renderCall shows a derived phantom task list for tweaks without
    explicit tasks (dialog correctly shows the retained list).
  - G3: renderCall/proposalText derive from the raw objective while apply uses
    the contract-stripped objective.
  - G4: empty `┌─ TASKS ─┐` box in the bounded dialog (same root cause as G1).
- Wrote PRODUCT.md and TECH.md documenting the audit (V-table, G1–G4, by-design
  list, F1/F2 fix plans, test plan).
- Design decision: keep the churn-guard bound; protect the auditor line and
  task lines within it (no unbounded dialogs).

## 2026-08-08 — Fixes shipped + validation

- **F1 (G1/G4) — auditor toggle + task lines restored in the bounded dialog**
  (`extensions/goal-questionnaire.ts`):
  - `findProposalPresentationSegments` strips ANSI SGR sequences before the
    task-line scan, so styled `[ ]` lines extend the tasks section instead of
    collapsing it to the header; the box-drawn `└──┘` bottom border after the
    last task line is kept so the `┌─ TASKS ─┐` box renders complete.
  - The interactive auditor toggle line ("press 'a' to toggle") is protected:
    the tail start is pulled back to it, and the tight-path fit keeps it even
    when task lines give way (they remain in the scrollback presentation).
  - Churn-guard invariant preserved: the dialog never exceeds the terminal
    height; the bound is not removed.
- **F2 (G2/G3) — single task set per proposal** (`extensions/goal-drafting.ts`):
  - `renderCall` is draft-mode-aware: a tweak without explicit tasks shows
    "Current task list (retained unchanged):" (mirroring the dialog preview),
    never a derived-from-objective phantom; explicit tasks unchanged; the new-
    draft derived fallback derives from `extractVerificationContract(objective)
    .objective` — the same input the apply path persists.
  - `proposalText` derives from the same extracted objective.
- **E2E persistence regression tests** (`tests/goal-tweak-status-persistence.test.ts`):
  completed task status/evidence/completedAt survive a task-list tweak through
  disk reload; no-task-list tweak retains the list; subtask completion
  survives; `currentTaskId` survives while pending and clears when removed.
- **Auditor-restore regression tests** (`tests/goal-questionnaire.test.ts`):
  ANSI-aware segment scan (plain + box-drawn TASKS), tight-budget auditor-line
  protection, real-TUI ANSI render keeps tasks + toggle, `a` toggles with
  visible feedback.
- **Single-task-set regression tests** (`tests/goal-drafting.test.ts`): tweak
  renderCall shows the retained list (no phantom), new-draft derived preview
  equals persisted ids.
- **Validation**: 715 unit + 28 integration + 2 e2e tests pass, `tsc --noEmit`
  clean, test manifest regenerated (51 unit files).
- CHANGELOG updated.

## Planned

- None (all audit gaps G1–G4 fixed; merge/apply/persistence verified sound by
  audit + e2e tests — `fix-gaps` skipped as a hard contradiction).
