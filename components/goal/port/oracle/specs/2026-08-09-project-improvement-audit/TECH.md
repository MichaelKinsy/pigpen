# Technical plan — broad project improvement pass

1. Centralize `GoalService` ledger batching and diagnostics in a private helper.
2. Keep `GoalService`'s turn buffer active when lock acquisition fails; retry it
   at the next flush boundary. Do not change the lock's bounded wait policy.
3. Extend the pure `goal-status` model with a health mode. Pass the ledger
   malformed count from the command handler, derive task/budget/file checks from
   the existing goal record, and render a bounded plain-text report.
4. Add tests for event ordering, lock-contention retry, health severity and
   status-mode compatibility.
5. Run type checking, unit/integration tests, package validation, and the NAF
   benchmark gate.

Follow-up hot-path cleanup: reuse the single ledger snapshot inside
`before_agent_start`, load tweak settings once, and remove an unused budget
state read. These are behavior-preserving and keep the zero-I/O cache contract.
