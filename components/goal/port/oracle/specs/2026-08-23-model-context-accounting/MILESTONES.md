# MILESTONES — complete model-context accounting

## 2026-08-23 — Harness built on maint/pr-d-context-measurement

- 22 deterministic fixtures implemented (plan §46 list; ids prefixed `ctx-`).
- Capture drives the REAL extension handlers (session_start → before_agent_start
  → context) over materialized goal files + ledger events; no network path.
- Baseline artifact committed: experiments/context/baseline-main.json.
- Determinism verified: two consecutive `context:measure` runs produce
  byte-identical artifacts.
- Gate (`npm run context:gate`) re-measures in-process and enforces:
  deterministic equality, tool-schema bytes > 0 per fixture, semantic fields
  classified, historical checkpoint payload = 0, objective/[PI GOAL ACTIVE]/
  verification-contract exactly once on active-block fixtures.

Measured headline: active goal tools cost ~7,262 serialized chars per request
(previously invisible to B4); historical checkpoint payload is 0 everywhere.

Fixtures with empty extension system blocks (completion-audit,
audit-rejection-and-rework, drafting) reflect real early-return paths in
before_agent_start; they still exercise message/tool measurement.
