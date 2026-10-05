# Product — reliability and maintainability contract

Date: 2026-08-09

Roadmap owner for the engineering work beyond runtime features. Phases in this
spec dir: **0** (stabilize current worktree as a standalone release),
**1** (CI/release automation + declared compatibility bounds), **3**
(persistence scaling: ledger checkpoint/index, `/goal-refresh`, pool-snapshot
validation, recovery tooling). Phases 2 (module refactor), 4 (completion UX),
and 5 (test-shape expansion) are deliberately out of scope for this goal.

## Invariants

- The zero-I/O steady-state read path is preserved: caches remain the hot
  path; checkpoint/replay must not reintroduce per-read I/O.
- Ledger event format stays backward compatible; existing `.pi` data keeps
  loading.
- Public commands, tool names, markdown files, and event format do not change;
  phase 3 only *adds* `/goal-refresh` and read-only recovery reports.
- Baselines recorded in phase 0 gate every later change: no regression beyond
  documented tolerance (`max(before*1.5, before+10)ms`, the B6 rule) without
  explicit sign-off.

## Success criteria

1. Current ledger-dedup/health-report worktree released standalone (spec
   `2026-08-09-goal-service-ledger-dedup`) with green check/tests/pack/audit/
   bench gate and recorded baselines.
2. GitHub Actions CI on Node 22.15+ reproduces the full validation sequence on
   main and PRs.
3. `package.json` declares Node + pi SDK ranges; Dependabot/Renovate enabled;
   safe `typebox` patch taken; TypeScript 7 evaluated (documented verdict);
   lint/format gate + incremental stricter tsconfig; superseded docs excluded
   from the npm tarball.
4. Versioned atomic ledger checkpoint/index bounds startup cost to checkpoint
   + recent tail.
5. `/goal-refresh` invalidates pool/ledger/settings caches and reports what
   changed; no watchers or per-turn polling.
6. Pool-snapshot validation corrected or benchmarked/documented; cold-start
   fast path verified after snapshot writes.
7. Read-only recovery report (malformed goal files/ledger lines, stale locks,
   orphaned snapshot data); repair ops require confirmation + backup.
8. Crash/interruption + multi-process tests green for atomic write, append,
   lock expiry, ledger-tail recovery, refresh.

## Baseline contract

Figures recorded at the phase-0 release point (2026-08-09, local SSD, NAF
campaign, agent-free B8 harness) in `MILESTONES.md`. Later phases re-run the
same bench scripts and must not regress beyond the B6 tolerance; the
`bench:gate:naf` gate enforces this in CI from phase 1 onward.
