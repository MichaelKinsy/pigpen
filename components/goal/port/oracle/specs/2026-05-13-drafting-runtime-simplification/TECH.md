# Tech Spec: Simplify goal drafting while hardening runtime execution and audit

Product spec: `specs/2026-05-13-drafting-runtime-simplification/PRODUCT.md`

## Context

Before this refactor, the code had already softened several drafting gates, but the orchestration still treated drafting as a runtime sub-state. The implemented direction is to keep only the minimal confirmation intent needed for user approval and mode consistency, while leaving execution/audit gates strict.

Relevant files after implementation:

- `extensions/goal.ts` - owns the thin `confirmationIntent`, command handlers, direct-set handlers, tool surface, proposal dialog, continuation, and strict lifecycle/audit hooks.
- `extensions/goal-draft.ts` - owns lightweight confirmation prompt text, plain-text confirmation report formatting, safe objective escaping, no-op drafting tool gate, and proposal validation.
- `extensions/goal-auditor.ts:186` - `runGoalCompletionAuditor()` is the independent completion gate and remains strict.
- `extensions/goal-policy.ts:26` - lifecycle validators protect completion, pause, abort, and resume transitions.
- `tests/goal-draft.test.ts` - covers missing confirmation intent, mode mismatch, empty objective rejection, deprecated `draftId` no-op compatibility, direct proposal for concrete topics, and lightweight confirmation prompt text.
- `docs/agent-flow-design.md` - describes the three-stage model: lightweight confirmation, strict execution, strict audit.

## Proposed changes

### 1. Introduce a thinner confirmation intent model

Replace the current `DraftingState` shape with a smaller session-local intent, for example:

```ts
interface GoalConfirmationIntent {
  focus: "goal" | "sisyphus";
  originalTopic: string;
  startedAt: number;
}
```

Implementation notes:

- Remove `draftId` and `questionsAsked` from the normal `/goals` and `/sisyphus` confirmation flow.
- Remove `draftingNudgesByDraftId` and `queueDraftingNudge()`.
- Keep the intent session-local; do not persist it to goal files or ledger.
- Keep `/goal-clear` and `/goal-abort` able to clear this intent.
- Do not let this intent affect active goal persistence, ledger reconstruction, or focus ownership.

Tradeoff: without draft ids, overlapping hidden drafting prompts are no longer treated as a hard runtime race. That is acceptable if `/goal-set` no longer depends on hidden prompt identity. The hard invariant moves to explicit user confirmation and mode validation.

### 2. Make `/goals` and `/sisyphus` send conversational intent instructions

Update `startGoalDrafting()`:

- Clear continuation/accounting for active goals as today.
- Store only the thin confirmation intent.
- Send a visible or normal follow-up/steer message that asks the executor to clarify or propose a draft.
- Avoid hidden custom-message prompts as the sole carrier of the drafting contract.
- Prefer instruction text that mirrors `pi-specs`: outcome, success criteria, constraints, and final shape, without over-prescribing internal process.
- For `/goals`, permit clarification and targeted read-only research that improves the goal contract.
- For `/sisyphus`, emphasize grilling the ordered plan, done criteria, blockers, and step boundaries before proposing.

The prompt should say:

- Do not begin substantive implementation before user confirmation.
- Ask focused questions only when needed.
- Fully specified requests may go directly to `propose_goal_draft`.
- `propose_goal_draft` opens the confirmation dialog.
- Direct `create_goal` remains rejected.

### 3. Keep `propose_goal_draft` stable and validator-gated

Update `validateGoalDraftProposal()` and tool behavior:

- Continue rejecting empty objectives.
- Continue deriving the expected Sisyphus mode from the latest confirmation intent when one exists.
- Reject mode mismatch when an intent exists.
- Decide the open question from PRODUCT.md: if no intent exists, either reject with a helpful message or allow explicit proposals that include enough user-request context. The conservative first implementation should keep rejection outside a user-initiated confirmation intent.
- Remove stale draft-id validation from the normal path.
- Update tool parameter schema to remove `draftId` unless kept temporarily for backward-compatible no-op acceptance.
- Keep `create_goal` registered but rejected.

Recommended compatibility step: accept optional `draftId` for one release but ignore it, so older transcripts or prompt residue do not fail solely because a draft id was supplied.

### 4. Add direct set commands

Add direct creation handlers:

- Register `/goals-set <objective>` for normal goals.
- Register `/sisyphus-set <objective>` for Sisyphus goals.
- Both commands trim the objective, reject empty objectives with a warning, clear any pending confirmation intent, call the existing `replaceGoal()` creation path, and start execution immediately when auto-continue is enabled.
- Keep direct set commands separate from `propose_goal_draft`; they are explicit user shortcuts, not agent tools.
- Remove or stop registering redundant creation aliases (`/goal-set`, `/goal-sisyphus`) so the user-facing command surface has one discussion pair and one direct-set pair.

### 5. Simplify tool-surface synchronization

Update `syncGoalTools()` and related tests:

- Always keep `PROPOSE_DRAFT_TOOL_NAME` in the active tool set.
- Keep `QUESTION_TOOL_NAME` and `QUESTIONNAIRE_TOOL_NAME` available when a confirmation intent or tweak drafting is active.
- Do not use drafting phase to hide lifecycle tools except where necessary to prevent accidental mutation of an existing focused goal before confirmation.
- Continue hiding/rejecting `CREATE_GOAL_TOOL_NAME`.
- Ensure active execution tools and lifecycle tools are restored immediately after confirmation.

### 6. Remove drafting-specific turn hooks

Update event hooks:

- Remove question counting in `tool_call` for normal goal confirmation.
- Remove the `turn_end` drafting nudge branch.
- Keep the execution empty-turn guard unchanged for active goals.
- Keep `beginAccounting()`, `accountProgress()`, and `queueContinuation()` from counting confirmation turns as active goal work.
- Keep `before_agent_start` execution-state logic for stale goal continuations, compaction reminders, and active prompts.
- Stop reinjecting a full drafting prompt every turn; use the normal conversation and stable tool instead.

### 7. Preserve strict execution and audit paths

Do not loosen these areas:

- `queueContinuation()` and stale continuation extraction.
- `runningGoalId` consistency checks in pause/abort/complete validators.
- `turnStoppedFor` post-stop blocking.
- goal file reconciliation and archival.
- `update_goal(status="complete")` auditor invocation.
- visible audit started/approved/rejected messages.
- auditor rejection memory in ledger/compaction prompts.

### 8. Update documentation

Update at least:

- `README.md` - document `/goals`, `/sisyphus`, `/goals-set`, and `/sisyphus-set`; remove old `/goal-set` and `/goal-sisyphus` examples from primary docs.
- `docs/agent-flow-design.md` - recast drafting as a thin conversational confirmation stage rather than a hard runtime phase and name the new commands.
- `docs/architecture.md` if it still describes draft ids, hard tool visibility gates, question counters, or old command names.

### 9. Tests

Expected test changes:

- `tests/goal-draft.test.ts`
  - Remove stale draft-id rejection expectations.
  - Remove question-counter-era prompt assertions.
  - Add tests for thin intent validation, empty objective rejection, mode mismatch rejection, and optional ignored `draftId` compatibility if implemented.
- `tests/goal-tool-names.test.ts`
  - Assert `propose_goal_draft` remains part of the stable tool surface semantics.
- `tests/goal-policy.test.ts`
  - Keep lifecycle completion/pause/abort validation coverage unchanged.
- `tests/goal-prompts.test.ts`
  - Update drafting prompt text expectations, if the helper remains.
- Integration-style extension tests if available
  - `/goals` starts confirmation without requiring question counters.
  - `/goals-set` creates a normal active goal directly and `/sisyphus-set` creates a Sisyphus active goal directly.
  - user confirmation creates a focused active goal.
  - Continue Chatting does not create a goal.
  - active goal auto-continues only after meaningful work.
  - completion still launches visible audit and requires approval.

## Testing and validation

Map to PRODUCT.md behavior:

- Behavior #1: unit tests for prompt/helper output; smoke/manual test `/goals` with a concrete topic and verify direct proposal path works.
- Behavior #2: unit tests for Sisyphus mode validation and prompt text; manual test ordered plan preservation.
- Behavior #3: tests or smoke coverage for `/goals-set` and `/sisyphus-set` direct creation; empty objective warning remains user-facing.
- Behavior #4: unit tests for `validateGoalDraftProposal()` empty objective, missing intent, mode mismatch, and optional `draftId` no-op compatibility.
- Behavior #5: existing or new tests around proposal dialog result handling: confirm creates, Continue Chatting does not.
- Behavior #6: existing tests plus `npm test` for active/paused/complete lifecycle, continuation, post-stop, compaction, and goal files.
- Behavior #7: existing auditor tests plus manual transcript inspection or focused tests for visible audit events.
- Behavior #8: `npm run check`, `npm test`, and `npm pack --dry-run`; grep shipped files for stale terms such as `questionsAsked`, `draftingNudgesByDraftId`, `draftId` in user-facing docs if removed.

## Risks and mitigations

- Risk: Removing draft-id hard gates could allow an old model turn to propose after a newer `/goal-set` intent.
  Mitigation: rely on the current thin intent plus explicit user confirmation; if a stale draft appears, the user can choose Continue Chatting or cancel. Keep mode mismatch rejection.

- Risk: Making confirmation too loose could let the agent start work before a goal is confirmed.
  Mitigation: prompt clearly says no substantive implementation before confirmation; runtime still prevents auto-continue/accounting from treating confirmation turns as active goal execution.

- Risk: Tool surface changes could expose lifecycle tools during confirmation.
  Mitigation: keep lifecycle validators strict and add tests for tool phases; direct `create_goal` remains rejected.

- Risk: Tests/docs might still encode old drafting state machine language.
  Mitigation: update tests first around desired behavior, then grep docs/tests for stale required-question, hidden drafting prompt, and draft-identity wording.

- Risk: Refactor touches already-dirty budget-removal work.
  Mitigation: review `git diff` carefully before implementation and avoid reverting unrelated changes.

## Follow-ups

- Decide whether `/goal-tweak` should use the same lightweight confirmation style in this refactor or a separate later pass.
- Decide whether `propose_goal_draft` should eventually support explicit proposals without a preceding `/goal-set` command.
- Consider a small runtime design note after implementation explaining the new three-stage model: lightweight confirmation, strict execution, strict audit.
