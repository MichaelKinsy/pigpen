Less model context and faster dashboards, with regular goals, Sisyphus, drafting, tasks, verification, independent auditing, Oracle, budgets, persistence and recovery preserved.

### Improvements since v0.30.5

| Measurement | v0.30.5 | v0.31.0 | Improvement |
|---|---:|---:|---:|
| Extension-added model context, 14 active workflows | 145,134 characters | 89,993 characters | **38% smaller** |
| Expanded dashboard rendering, 50 long tasks | 1.71 ms | 0.28 ms | **6× faster** |
| Latest activity display, 100,000 history events | 11.9 ms | 0.003 ms | **Over 99.9% less time** |

Smaller context reduces estimated input-token overhead. These are serialized character measurements, not a promise of 38% fewer total billed tokens. Runtime figures are local warm-path benchmarks using identical fixtures and real SDK 0.84.1; they measure the named operations, not whole-application speed or cold startup. Full history remains stored losslessly.

### What changed

- Compact goal prompts and applicable tool profiles send less repeated material. Full objectives, contracts, tasks and evidence remain available through paginated `get_goal` retrieval.
- `update_goal_task` accepts ordered atomic batches, preserving existing single-task calls and individual ledger events.
- Incremental history indexes and bounded caches reduce repeated scanning, copying, settings resolution and dashboard layout work.
- Persistence rejects conflicting writers; old or corrupt derived checkpoints rebuild from the authoritative ledger, including Unicode history and Oracle state.

### Validation and compatibility

923 tests pass, with TypeScript, lint, context and performance gates passing. Both supported SDK lines, 0.83.0 and 0.84.1, pass 888 serial compatibility tests and outgoing provider-payload checks. Existing tool names, single-task calls, saved goals and session history remain compatible.

DeepSeek V4 Flash smoke runs produced correct artifacts; several task/Sisyphus runs did not reach audited completion within the request limit. This small sample does not establish statistical equivalence or reliable live token savings. Cumulative evaluation cost, including conservative charges for interrupted requests, stayed at **US$1.033 of the US$5 cap**.

[Measurements and reproduction](https://github.com/tmonk/pi-goal-x/tree/v0.31.0/specs/2026-09-07-release-0-31-0) · [Full comparison](https://github.com/tmonk/pi-goal-x/compare/v0.30.5...v0.31.0)
