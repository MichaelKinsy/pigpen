# Milestones — GoalService ledger path simplification

## 2026-08-09 — Implemented

- Extracted the repeated ledger batch/fallback/diagnostic path into
  `GoalService.appendLedgerEventsBestEffort`.
- Applied it to turn flushes, generic mutations, task updates, goal creation,
  and out-of-turn event appends.
- Added regression coverage proving multiple events preserve order and land as
  one mutation result.
- `npm run check` passed.
- `npm test` passed: 725 tests.
- `npm run test:integration` passed: 29 tests.
- `npm run test:all` passed: 756 tests.
- `npm pack --dry-run` and `git diff --check` passed.
- The existing `npm run bench:gate:naf` passed all 95 campaign rows.
