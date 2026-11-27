---
name: pigpen-omh-ulw-loop
description: "[omh] Ambitious project goal needing repeated build-verify cycles: durable interviewer -> planner -> researcher -> builder -> reviewer loop tracked in omh_loop until a real gate stops it."
license: MIT
author: "oh-my-hermes maintainers"
metadata:
  hermes:
    tags: [workflow, oh-my-hermes, goal-loop]
    category: goal-loop
    phase: continuous-goal-loop
    role: planner
    quality_tier: loop-gated
    related_skills: [pigpen-omh-ulw-work, pigpen-omh-ulw-plan, pigpen-omh-ulw-research]
---

# OMH Goal Loop (`ulw-loop`)

Hermes-native `ulw-loop` workflow skill adapted for `pig-hermes` + `oh-my-hermes-pig`.

## Why This Exists

`ulw-loop` drives long-horizon goals whose right implementation must be discovered through bounded cycles of definition, action, verification, and revision — tracking durable state in `omh_loop` without confusing planned ticks with observed progress.

## Do Not Use When

- The request is a single bounded delivery cycle; use `pigpen-omh-ulw-work`.
- The goal is a trivial one-edit fix; patch and verify directly.
- The goal depends purely on external waiting (adoption, stars, revenue) with no observable local next action.
- The permission profile forbids repeated research, execution, or verification cycles.

## Loop Lifecycle Protocol

1. **Assess & classify first**: classify the goal as `task`, `project`, `north-star ambition`, `external-wait`, or `unclear`. Reframe a north-star ambition into a bounded arena, observable problem, next loop goal, and concrete verification signal without shrinking the ambition.
2. **Manage durable state via `omh_loop`**:
   - Use `omh_loop` (`action=start`, `status`, `tick`, `feedback`, `pause`, `resume`, `complete`) to maintain durable loop state under `$OMH_HOME/runtime/loops/`.
   - Declare a permission profile before repository mutations, external dispatches, or publishing.
   - Keep `omh_todo` aligned with the active cycle's concrete step.
3. **Apply constraint discipline before each cycle**:
   - **Identify** the single binding constraint gating progress (blocked queue, missing verification, failing test, or open criterion).
   - **Exploit** work already paid for (observe prepared items or close the open criterion before queuing new work).
   - **Subordinate** other lanes to the binding constraint.
   - **Elevate** only when exploit/subordinate leave it binding, stating the cost.
   - **Repeat** at the next cycle boundary.
4. **Measured loops (when a metric exists)**:
   - Declare the evaluation contract before the first attempt: exact `terminal` command, metric name, direction, and rule that the loop never modifies the scoring harness.
   - Run as attempt → measure → keep or revert. On an equal metric, keep the simpler change.
   - If no metric command exists, state explicitly that the loop is unmeasured and gate on verification evidence.
5. **Failure-mode checks**: actively surface `verification_gap`, `comprehension_debt`, and `cognitive_surrender` before advancing. Use cheap inner-loop checks (`terminal`) frequently and expensive outer-loop checks sparingly.
6. **Stuck-loop recovery**: if a loop stalls, re-read scoped files (`read_file`), re-plan once from recorded failures rather than chat memory, and escalate to a named blocker if a second stall occurs.

## Safety & Evidence Boundaries

- Never treat loop persistence as permission to bypass the declared permission profile.
- Queued ticks are prepared state, never observed execution, verification, or completion evidence (`omh_completion`).
- External outcomes stay in `waiting` state until observed evidence is supplied.
- On context or budget exhaustion, checkpoint via `omh_loop` rather than claiming completion.

## Completion Checklist

- Goal was classified and tracked in `omh_loop` with an explicit permission profile and verification plan.
- Each cycle named its binding constraint and observed verification or metric outcome.
- Completion is backed by observed `omh_completion` evidence rather than tick count or narration.
