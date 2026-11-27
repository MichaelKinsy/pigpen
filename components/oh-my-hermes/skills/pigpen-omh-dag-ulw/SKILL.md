---
name: pigpen-omh-dag-ulw
description: "[omh] Large multi-part job to run on its own: plan a validated dependency graph in omh_dag, run ready nodes in parallel via delegate_task, verify with a separate node, and repair failures before reporting."
license: MIT
author: "oh-my-hermes maintainers"
metadata:
  hermes:
    tags: [workflow, oh-my-hermes, agent-coordination]
    category: agent-coordination
    phase: dag-delivery
    role: tracker
    quality_tier: evidence-gated
    related_skills: [pigpen-omh-ulw-work, pigpen-omh-ulw-plan]
---

# OMH DAG Ultrawork (`dag-ulw`)

Hermes-native `dag-ulw` workflow skill adapted for `pig-hermes` + `oh-my-hermes-pig`.

## Why This Exists

`dag-ulw` runs multi-part engineering jobs end-to-end as a validated DAG: define the graph in `omh_dag`, dispatch ready nodes with disjoint write scopes via `omh_delegate_route` + `delegate_task`, settle completed nodes from their handoff evidence, verify the goal with a separate `verify` node, and repair failed nodes before reporting.

## First Steps

1. **Define the goal & criteria**: state the goal in one line plus separately checkable outcome criteria (what must be true, not merely that a command ran). If the conversation already has an accepted plan from `pigpen-omh-ulw-plan`, map each task directly into a DAG node.
2. **Check identities**: inspect available roles with `omh_roles` (`action=list`) and assign an optional identity when a built-in or custom role fits.
3. **Define the DAG before dispatching**: call `omh_dag` (`action=define`) with `goal` and nodes (`id`, `label`, `category`, `prompt`, `depends_on`, `write_scope`, `role` in `build|baseline|verify|review`). Fix any validation error or write-scope overlap before dispatching. Never call `delegate_task` before `omh_dag` defines the graph.
4. **Track layers in `omh_todo`**: keep one checklist item per graph layer (named for what it delivers) and advance it as layers settle.

## Do Not Use When

- One small change with one owner and one check; edit directly with `patch` and verify with `terminal`.
- The plan itself is undecided and needs a consensus decision record first; use `pigpen-omh-ulw-plan`.
- Workers must converse mid-run instead of handing off through files; use `omh_team` / `pigpen-omh-ulw-work`.

## Graph Execution Protocol

1. **Route and dispatch ready nodes**:
   - Query ready nodes via `omh_dag` (`action=ready` or `action=status`).
   - Climb the category ladder intentionally (`quick` → `deep` → `ultrabrain`, reserving `ultrabrain` for at most one pivotal decision node; `baseline` stays `quick`; `review` uses `review`).
   - For each ready node, set `omh_delegate_route` for its category, then invoke `delegate_task` with the self-contained node prompt (`TASK`, `DELIVERABLE`, `SCOPE`, `VERIFY`, `STOP WHEN`).
2. **Settle from observed handoffs**:
   - Read each completed node's output/files (`read_file`) and call `omh_dag` (`action=settle`) with `completed`, `failed`, or `blocked`.
   - A `verify` node depends on all producer nodes, runs the real verification command via `terminal`, and is never the same node that authored the code.
3. **Repair & extend loop**:
   - If a node fails, repair locally (up to two repair cycles per node); a failure blocks only its dependents.
   - If all nodes pass but goal criteria reveal missing work, extend the run rather than silently dropping requirements.
4. **Close with evidence**: record final verification in `omh_completion` and report observed `omh_run_summary` (or explicit `run-summary not_available`), naming any open items plainly.

## Safety & Evidence Boundaries

- Never run two concurrently runnable nodes with overlapping `write_scope`; shared files require a `depends_on` edge or a single owner.
- A child's self-report is a claim until checked against its files and `VERIFY` output.
- External coding CLIs run only when the user explicitly chose that coding owner (`omh_maestro`).

## Completion Checklist

- Graph was defined in `omh_dag` before any `delegate_task` dispatch and every dispatched node was settled.
- No two concurrent nodes shared a write scope without an ordering edge.
- A separate `verify` node ran the real verification command after all producers.
- Final report names what passed, what remains open, and includes `omh_run_summary`.
