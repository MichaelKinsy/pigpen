# Technical plan — GoalService ledger path simplification

The service previously duplicated the same batch-then-individual-fallback
logic in five mutation paths. Extract `appendLedgerEventsBestEffort` as a
private service helper. It attempts `appendGoalEvents` for two or more events;
on failure it retries each event with `appendGoalEvent` and routes each failure
to `onDiagnostic`. Single events continue through `appendGoalEvent`.

The helper is deliberately narrow: ledger factory exceptions remain owned by
their existing callers, and authoritative goal-file writes still happen before
ledger appends. This keeps the refactor low-risk while making batching uniform
for `apply` and `updateTask`, which previously appended their events one at a
time.

Validation:

- `npm run check`
- `npm test`
- targeted `goal-service` tests for multi-event ordering and failure behavior
