---
name: pigpen-omh-ulw-plan
description: "[omh] High-stakes technical proposal needing approval: consensus planning with repo facts, options/tradeoffs, risk review, testable acceptance criteria, and verification commands."
license: MIT
author: "oh-my-hermes maintainers"
metadata:
  hermes:
    tags: [workflow, oh-my-hermes, planning]
    category: planning
    phase: reviewed-plan
    role: planner
    quality_tier: reviewed-plan-gated
    related_skills: [pigpen-omh-ulw-interview, pigpen-omh-ulw-research, pigpen-omh-ulw-work, pigpen-omh-dag-ulw]
---

# OMH Consensus Plan (Ralplan)

Hermes-native `ulw-plan` workflow skill adapted for `pig-hermes` + `oh-my-hermes-pig`.

## Why This Exists

`ulw-plan` makes risky or architectural changes reviewable before any code is edited: gather observed repository facts, compare options, expose risks, define testable acceptance criteria and exact verification commands, and record a plan artifact without pretending implementation already happened.

## First Steps

- Initialize the planning checklist in `omh_todo` (`action=set`) with English operator-legible stages:
  1. Repo facts and evidence check
  2. Target state and options/tradeoffs
  3. Risk and critic review
  4. Acceptance criteria and verification commands
  5. Plan artifact and acceptance gate
- Keep exactly one item active and advance states as stages complete. Treat `omh_todo` items as declarations, never execution evidence.

## Do Not Use When

- Requirements are still too ambiguous to name goals, non-goals, or acceptance criteria; use `pigpen-omh-ulw-interview` first.
- Repository terminology is still unresolved; use `pigpen-omh-ulw-context` first.
- The change is a tiny local edit with no architectural or regression risk; execute directly.

## Planning Protocol

1. **Gather observed facts**: inspect files via `read_file` and check behavior via read-only `terminal` commands. If external or contested facts are missing, run a bounded `pigpen-omh-ulw-research` stage first.
2. **State the target state**: name each consumer, their current workflow, and every difference between current and target state. Do not shrink scope silently.
3. **Compare options & run critic review**: record at least two options (or chosen option + rejected alternatives) with tradeoffs and a risk register. When delegating a planner or critic subagent, set `omh_delegate_route` with category `ultrabrain` before `delegate_task`, then clear it after dispatch.
4. **Define `Success criteria` table**: `Criterion | Task | Verification scenario` — one row per target-state difference, each with an outside judge (tests, CI, measurements, or reviewer) and exact verification commands. Size tasks as `XS`, `S`, `M`, `L`, or split `XL`.
5. **Write lanes in node-prompt shape**: for each task include `TASK`, `DELIVERABLE`, `SCOPE`, `VERIFY`, `STOP WHEN`, and `depends_on` so `pigpen-omh-dag-ulw` or `pigpen-omh-ulw-work` can execute directly without re-planning.
6. **Separate acceptance from execution**: plan acceptance approves the plan content only. After acceptance, recommend the best follow-on path (`pigpen-omh-dag-ulw`, `pigpen-omh-ulw-work`, or `omh_maestro`) with a one-line reason and wait for explicit go-ahead before editing code.

## Safety & Evidence Boundaries

- Do not implement code directly from the planning lane.
- Label missing evidence and unresolved tradeoffs explicitly.
- Keep rejected options and prepared handoffs (`prepared_not_observed`) distinct from execution evidence (`omh_completion`).

## Completion Checklist

- `omh_todo` stages were declared and reached terminal states.
- Observed repo facts, options/rejected alternatives, risks, acceptance criteria, and verification commands are recorded.
- Target-state coverage matches `Success criteria` rows (or names user-excluded non-goals).
- Follow-on execution starts only on explicit user go-ahead after plan acceptance.
