---
name: pigpen-omh-ulw-qa
description: "[omh] Hostile scenario testing: generate adversarial QA scenarios from changed behavior and risk areas, observe pass/fail evidence separately from fixes, and enforce bounded fix-rerun loops with cleanup."
license: MIT
author: "oh-my-hermes maintainers"
metadata:
  hermes:
    tags: [workflow, oh-my-hermes, verification]
    category: verification
    phase: qa
    role: reviewer
    quality_tier: scenario-gated
    related_skills: [pigpen-omh-ulw-work, pigpen-omh-ulw-plan]
---

# OMH Adversarial QA (`ulw-qa`)

Hermes-native `ulw-qa` workflow skill adapted for `pig-hermes` + `oh-my-hermes-pig`.

## Why This Exists

`ulw-qa` attacks changed behavior under inputs, edge cases, and failure modes that happy-path unit tests miss. It records each hostile scenario's observed pass/fail evidence separately from proposed fixes and enforces cleanup of every temporary QA resource.

## Do Not Use When

- Do not auto-start `ulw-qa` from another skill's output without explicit user invocation or go-ahead.
- The user only wants a quick single-command verification of an already-agreed check; run that command via `terminal` directly.
- Purely aesthetic layout review with no behavioral failure scenarios to probe.

## Adversarial QA Protocol

1. **Inventory changed behavior & risk areas**: inspect diffs and affected files (`read_file`, `terminal`). List every resource the scenarios will create or mutate (temp dirs, files, ports, processes, test records) before running them.
2. **Design hostile scenarios**: cover boundary values, malformed/empty inputs, missing PATH/env/dependencies, permission failures, concurrency/races, stale state, and recovery paths. Each scenario states:
   - Setup
   - Action (exact `terminal` command or surface interaction)
   - Expected signal
   - Broken signal
3. **Execute & observe separately from fixes**:
   - Run scenarios via `terminal` (or isolated `delegate_task` probes routed with `omh_delegate_route` category `review`).
   - Record raw observed pass/fail evidence in `omh_completion` before proposing or applying any code fix.
   - If a scenario requires an unreachable external service or destructive live action without approval, mark it `not_observed` and provide a manual test guide (setup, do, expect, broken).
4. **Bounded fix-and-rerun loop**:
   - Route confirmed defects to the coding owner (`patch` + `terminal` in Hermes harness, or `omh_maestro` if an external coding owner was chosen).
   - Allow at most **2 fix-and-rerun rounds** per failing scenario. Never redefine a failing scenario to make it pass; if it still fails after 2 rounds, report it as an open finding with full evidence.
5. **Cleanup receipt**: tear down every file, process, port, or fixture created during QA and report the cleanup command and observed result (or explicitly list what remains).

## Safety & Evidence Boundaries

- Never execute destructive actions (submit, buy, send, delete) against live external targets without explicit user approval.
- Keep observed QA results separate from proposed fixes and unverified hypotheses.
- Track QA checklist states in `omh_todo` and record observed evidence in `omh_completion`.

## Completion Checklist

- Every hostile scenario names setup, action, expected behavior, observed result, and pass/fail basis.
- Proposed fixes are separated from observed QA evidence, and failed scenarios stayed within 2 fix-rerun rounds.
- Every temporary QA resource was cleaned up with an observed cleanup receipt (or remaining state explicitly named).
