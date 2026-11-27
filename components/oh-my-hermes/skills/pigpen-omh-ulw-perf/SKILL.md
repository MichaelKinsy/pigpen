---
name: pigpen-omh-ulw-perf
description: "[omh] Software slowness, memory leaks, or cost spikes: measure a baseline first, rank hot-path hypotheses, apply one reversible fix at a time behind a regression budget, and prove deltas with observed re-measurement."
license: MIT
author: "oh-my-hermes maintainers"
metadata:
  hermes:
    tags: [workflow, oh-my-hermes, optimization]
    category: optimization
    phase: measured-optimization-loop
    role: tracker
    quality_tier: measurement-gated
    related_skills: [pigpen-omh-ulw-loop, pigpen-omh-ulw-work]
---

# OMH Measured Performance Optimization (`ulw-perf`)

Hermes-native `ulw-perf` workflow skill adapted for `pig-hermes` + `oh-my-hermes-pig`.

## Why This Exists

`ulw-perf` prevents speculative micro-optimization by enforcing measurement before edits, one ranked hot-path hypothesis at a time, smallest reversible fixes, observed re-measurement with spread awareness, and an explicit regression budget.

## Do Not Use When

- The request is about correctness, code style, or documentation rather than measured cost (latency, memory, throughput, token cost, storage, CI, or query time).
- A single trivial, already-localized edit needs no baseline or optimization loop.

## Optimization Protocol

1. **Establish the evaluator & baseline first**:
   - Confirm the symptom, workload/reproduction, environment, and acceptable tolerance.
   - Define or produce a runnable evaluator command (`terminal` benchmark, profiler, or query/memory harness). Never edit code before a working evaluator exists.
   - Run repeated baseline measurements (stating run count and warm-up) and record **median and spread**.
2. **Profile & rank hot-path hypotheses**:
   - Localize the actual hotspot from profile/measurement output (`read_file`, `terminal`). Never micro-optimize unmeasured code.
   - Rank candidate hypotheses by expected impact and reversibility.
3. **One smallest reversible fix at a time**:
   - Apply the change in the Hermes harness (`patch` + `terminal`, or a `delegate_task` lane routed with `omh_delegate_route`), or via `omh_maestro` if the user explicitly chose an external coding owner.
   - Never present a restart, cache flush, or resource bump as a memory-leak fix; prove causation via revert-verify.
4. **Re-measure & judge honestly**:
   - Re-run the exact same evaluator command. Treat a delta smaller than the baseline spread as **no result**.
   - If the metric does not improve (or regresses correctness/tests), revert the change and re-rank hypotheses instead of stacking unverified edits.
5. **Declare the regression budget & gate**:
   - Lower-is-better metrics (latency, memory, cost): `baseline * (1 + tolerance)`.
   - Higher-is-better metrics (throughput): `baseline * (1 - tolerance)`.
   - Tolerance must be at least the observed spread. Name the CI or test command that enforces the budget.

## Safety & Evidence Boundaries

- Never claim a profile, benchmark, delta, or CI gate ran without observed `terminal` output (`omh_completion`).
- Keep unmeasured steps labeled `not_observed`.
- Track optimization stages in `omh_todo` and report observed before/after numbers with units and spread.

## Completion Checklist

- Baseline (median, spread, run count, warm-up), workload, environment, and evaluator command were recorded before any edit.
- Each accepted fix names the measured hot path, reversible diff, and observed re-measured delta.
- Regression budget, tolerance, and enforcing gate command are stated explicitly.
