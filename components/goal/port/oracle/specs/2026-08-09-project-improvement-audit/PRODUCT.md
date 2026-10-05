# Product plan — broad project improvement pass

Date: 2026-08-09

## Audit ideas and refinement

The project already contains substantial performance work, so the improvement
pass is focused on changes that improve the shipped runtime rather than adding
parallel abstractions:

| Idea | Decision | Reason |
| --- | --- | --- |
| Deduplicate ledger append/failure handling | Ship | Five copies had drift risk; the existing batch API was not used by every mutation path. |
| Preserve turn buffers across lock contention | Ship | A failed lock acquisition could close a buffered transaction without persisting it. |
| Add a goal health report | Ship | Existing read-only `/goal-status` can expose actionable storage, ledger, task, budget, and focus diagnostics without another lifecycle command. |
| Rewrite the large state module | Defer | A broad split needs characterization by ownership and would add risk without a specific measured defect in this pass. |
| Replace session caches with always-stat external-edit detection | Defer | It conflicts with the documented zero-I/O hot path; session boundaries and explicit writes remain the current freshness contract. |
| Add new goal storage formats or import/export | Defer | No user need or compatibility contract is present; it would expand persistence complexity. |

## Health-report behavior

`/goal-status health` is read-only and concise. It reports:

- focus and lifecycle state;
- active-file presence/path status;
- malformed ledger entries;
- task completion and verification-contract coverage;
- token-budget usage when configured;
- an overall `OK`, `WARN`, or `ERROR` summary.

It must never claim that the goal's work is complete. The existing standard and
verbose status modes remain unchanged.

## Success criteria

- All three selected improvements are implemented with focused regression tests.
- Existing public commands/tools and persistence formats remain compatible.
- `npm run check`, the complete test suite, package dry-run, and the benchmark
  gate pass.
