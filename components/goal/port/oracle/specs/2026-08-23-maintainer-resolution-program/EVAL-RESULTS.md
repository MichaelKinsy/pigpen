# Eval results — outcome-quality gating

## What ran

Deterministic protocol gate (plan §80.1) for EVERY optimization PR (E and F):

- full state-machine test suite green (test:all + test:serial);
- required semantic markers present and single-source (context:gate +
  dedicated occurrence tests);
- no new unsafe tool exposure (tool-profile tests unchanged/green);
- stale checkpoints cannot execute work (existing golden tests green);
- evidence-gated completion and contracted-task gates intact;
- Sisyphus order retention intact.

Result: PASS on both PRs; no baseline-pass/candidate-fail regressions were
possible under these invariants without failing CI.

## What did NOT run (explicit follow-up)

The paired live-agent non-inferiority evaluation (§80.2) was DEFERRED by user
decision at goal confirmation: it requires real provider API calls across a
cheap executor model, a stronger general model, and one different-provider
model over identical seeded journey snapshots. The journey scaffolding
(experiments/context fixtures + injectable session factories) is in place;
the remaining work is wiring live model runtimes, grading artifacts against
rubrics, independent adjudication, and storing redacted aggregates here.

**Gate rule that still applies to any future token-reduction PR:**
`if (baseline.passed && !candidate.passed) failMerge("baseline-pass/candidate-fail")`
