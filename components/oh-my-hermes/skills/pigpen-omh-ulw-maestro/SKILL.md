---
name: pigpen-omh-ulw-maestro
description: "[omh] Coding owner already chosen, handoff pending: prepare a structured coding_executor_handoff/v1 prompt and artifact via omh_maestro without silently selecting an owner or auto-merging."
license: MIT
author: "oh-my-hermes maintainers"
metadata:
  hermes:
    tags: [workflow, oh-my-hermes, execution]
    category: execution
    phase: external-handoff
    role: handoff-guide
    quality_tier: handoff-gated
    related_skills: [pigpen-omh-ulw-work, pigpen-omh-dag-ulw]
---

# OMH Maestro (External Coding Handoff)

Hermes-native `ulw-maestro` workflow skill adapted for `pig-hermes` + `oh-my-hermes-pig`.

## Why This Exists

`ulw-maestro` prepares and optionally dispatches structured handoffs (`coding_executor_handoff/v1`) for an explicitly chosen external coding agent (`codex`, `claude`, etc.) via `omh_maestro`. Absent an explicit owner choice in the current request, coding stays inside the Hermes harness (`delegate_task` + `patch` + `terminal`) and `ulw-maestro` never picks an external CLI on the user's behalf.

## Do Not Use When

- No external coding owner was explicitly chosen in this request; ask the user to choose or stay in the Hermes harness.
- The user wants advice on which coding agent to pick; answer directly without preparing a handoff.
- The request is multi-lane decomposition or a full delivery cycle; use `pigpen-omh-ulw-work` (which invokes `ulw-maestro` only for external-owner lanes).

## Handoff Protocol

1. **Verify explicit owner & authority**:
   - Confirm the external coding owner (`owner`) was named in this request (an earlier turn's owner or a plan mention does not carry over automatically).
   - Separate preparation authority from dispatch authority: "prepare a handoff" calls `omh_maestro(action="prepare_handoff")` only; "run/dispatch with <owner>" permits `omh_maestro(action="dispatch")` after preparation.
2. **Compose the 10-section executor prompt**:
   - Include in order: `Goal`, `Do`, `Don't`, `Known context`, `Unknowns and decision rule`, `Expected result`, `Test`, `Progress and blockers`, `Evidence boundary`, `Task`, plus a `Docs consulted:` block.
   - Keep the invariant prompt prefix stable across re-dispatches with only the task tail varying.
3. **Persist the handoff artifact via `omh_maestro`**:
   - Call `omh_maestro(action="prepare_handoff", owner=..., goal=..., write_scope=[...], acceptance_checks=[...], verify_command=..., prompt=..., workdir=..., model=...)`.
   - The persisted record under `$OMH_HOME/runtime/maestro/<id>.json` starts in state `prepared_not_observed`.
   - Pass any user-specified `model` verbatim without silent fallback.
4. **Optional dispatch & observation**:
   - Only when the user explicitly authorized dispatch, invoke `omh_maestro(action="dispatch", handoff_id=...)` (or run the explicitly approved CLI command via `terminal`).
   - Record observed exit status, output, and any session/thread ID. Inspect results with `omh_maestro(action="get", handoff_id=...)` or `omh_maestro(action="list")`.
5. **Verify integration without auto-merging**:
   - Collect returned unit results, run integration verification via `terminal`, and report merged vs. unmerged state honestly. Dispatch never merges branches.

## Safety & Evidence Boundaries

- Never auto-select an external coding owner.
- `prepared_not_observed` is never dispatch, execution, verification, review, CI, or merge evidence (`omh_completion`).
- Never silently fall back to a different model when a requested model fails.
- Close with `omh_run_summary` (or explicit `run-summary not_available`).

## Completion Checklist

- Explicit coding owner was confirmed before handoff preparation.
- Handoff artifact was persisted with `omh_maestro` (`coding_executor_handoff/v1`) including write scope and acceptance checks.
- Prepared, dispatched, verified, and merged states are reported separately.
- No branch was auto-merged and `omh_run_summary` was included.
