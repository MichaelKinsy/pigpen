# Milestones — broad project improvement pass

## 2026-08-09 — Audit and scope refined

- Reviewed the storage caches, lock behavior, service mutation paths, status
  model, command surface, tests, and existing benchmark campaign.
- Selected three improvements with direct evidence and kept larger speculative
  rewrites deferred.
- Ledger simplification, transaction retry preservation, and the read-only
  health report are implemented.

## 2026-08-09 — Implemented and validated

- `GoalService` now retries buffered turn writes after temporary lock
  contention, preserving the in-memory transaction until it can flush.
- Generic status output gained `/goal-status health`; the command is read-only
  and has pure rendering plus handler-level integration coverage.
- Updated README, architecture, flow design, and CHANGELOG documentation.
- `npm run check` passed.
- `npm test` passed: 725 tests.
- `npm run test:integration` passed: 29 tests.
- `npm run test:all` passed: 756 tests.
- `npm run test:selfcheck` passed with 51 unit, 1 integration, and 1 e2e
  discovered entries.
- `npm pack --dry-run`, `git diff --check`, and `npm run bench:gate:naf`
  passed; the benchmark gate reported all 95 campaign rows green.

## 2026-08-09 — Follow-up hot-path cleanup

- Reused one ledger snapshot for overlapping prompt-steering branches.
- Removed one duplicate settings-cache lookup and one dead budget-state read.
- The cleanup preserves the existing zero-I/O cache contract and public
  behavior.
- Final validation passed: `npm run check`, `npm run test:all` (756 tests),
  `npm run test:selfcheck`, `npm pack --dry-run`, `git diff --check`, and
  `npm run bench:gate:naf` (95 campaign rows, no regressions).
