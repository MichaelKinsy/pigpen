---
name: pigpen-omh-ulw-work
description: "[omh] Accepted plan awaiting implementation: split work into disjoint parallel lanes with per-lane acceptance criteria, verification commands, and owners without overlapping file writes."
license: MIT
author: "oh-my-hermes maintainers"
metadata:
  hermes:
    tags: [workflow, oh-my-hermes, execution]
    category: execution
    phase: parallel-delivery
    role: handoff-guide
    quality_tier: handoff-gated
    related_skills: [pigpen-omh-ulw-plan, pigpen-omh-dag-ulw, pigpen-omh-ulw-maestro, pigpen-omh-ulw-loop]
---

# OMH Ultrawork (Parallel Delivery)

Hermes-native `ulw-work` workflow skill adapted for `pig-hermes` + `oh-my-hermes-pig`.

## Why This Exists

`ulw-work` executes an accepted implementation plan via single-owner, ordered-dependency, or disjoint parallel lanes while preserving strict write-scope ownership, per-lane verification, integration testing, and observed evidence boundaries (`omh_completion`, `omh_run_summary`).

## First Steps

- Initialize the phase checklist in `omh_todo` (`action=set`) in delivery order: bootstrap, one item per implement/verify lane, independent review, and integration/cleanup close. Keep exactly one item active at a time.
- Do not auto-start `ulw-work` from a plan or brief unless the user explicitly invoked it or gave explicit go-ahead.

## Do Not Use When

- Two parallel writers would touch the same files; collapse to one owner or add an ordering edge (`pigpen-omh-dag-ulw`).
- The plan is unaccepted or lacks acceptance criteria and verification commands; use `pigpen-omh-ulw-plan` first.
- The goal requires open-ended discovery/feedback iterations; use `pigpen-omh-ulw-loop`.
- The change is a single trivial edit; apply it directly with `patch` and verify with `terminal`.

## Execution Protocol

1. **Resolve dependency topology & file ownership**: every concurrently runnable lane MUST have a disjoint write scope. Any shared file requires an explicit ordering edge or a single integration owner.
2. **Coordinate lanes**:
   - Track multi-lane teams with `omh_team` (register lanes, ownership, and status) or run a full graph via `pigpen-omh-dag-ulw` (`omh_dag`).
   - For Hermes-native lanes, set the category route via `omh_delegate_route` (`quick`, `deep`, `ultrabrain`, `architect`, `visual-engineering`, `writing`, `review`) before calling `delegate_task`, and name each lane's routed category and model in status updates.
   - For lanes assigned to an explicitly chosen external coding CLI, delegate handoff preparation to `pigpen-omh-ulw-maestro` + `omh_maestro` rather than `delegate_task`.
3. **Node prompt contract**: every lane prompt includes `TASK`, `DELIVERABLE`, `SCOPE` (read/write boundaries), `VERIFY` (exact command), and `STOP WHEN`.
4. **Incremental verification (PIN → RED → GREEN → SURFACE → CLEAN)**:
   - When TDD is requested, write the failing test before implementation.
   - After lane results return, run integration verification via `terminal` and exercise the real user surface.
   - Clean up any temporary QA resources or processes.
5. **Evidence-gated completion**: record observed verification in `omh_completion` and close with `omh_run_summary` (or an explicit `run-summary not_available` line — never estimate tokens or elapsed time).

## Safety & Evidence Boundaries

- Never run two concurrent lanes with overlapping write scopes.
- Keep prepared handoffs (`prepared_not_observed`), dispatch, execution, verification, review, CI, and merge as distinct evidence states.
- If a lane fails or blocks, recover locally (inspect error, retry, or amend contract) rather than duplicating its owner.

## Completion Checklist

- `omh_todo` was initialized before work and every task reached a terminal state.
- All concurrent lanes had disjoint write scopes and explicit `VERIFY` commands.
- Every Hermes-native lane was routed with `omh_delegate_route` prior to `delegate_task`.
- Integration verification ran after lane results and `omh_completion` / `omh_run_summary` reported observed evidence.
