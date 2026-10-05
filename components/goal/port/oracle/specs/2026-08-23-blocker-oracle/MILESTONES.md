# MILESTONES — blocker Oracle

## 2026-08-23 — implemented on maint/pr-g-blocker-oracle

- Settings layer + menu section (Blocker Oracle) with per-leaf inheritance.
- Ledger events oracle_started/result/failed/followup_attempted wired through
  validators, sanitizers, and reconstruction.
- goal-oracle.ts: fingerprinting, structured advice validation, isolated
  read-only session runner (injectable session factory), armed follow-up
  markers, rendering helpers.
- update_goal(blocked) state machine: default-off legacy path preserved
  (existing blocked-flow tests green unchanged apart from the deliberately
  newly-required reason); Oracle paths covered by tests/goal-oracle.test.ts.
- Both demonstration journeys (actionable-advice; needs-human) scripted in
  tests; live-agent journeys remain optional manual verification (the Oracle
  session factory is the only live dependency).
