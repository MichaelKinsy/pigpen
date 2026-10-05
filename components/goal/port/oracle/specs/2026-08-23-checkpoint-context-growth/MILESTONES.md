# MILESTONES — checkpoint context growth (PR A, issue #30)

## 2026-08-23 — Preflight baseline (plan §6.3)

Starting state:

- `main` verified at `a2b6568b615c639c76a1924ec2731e9a933876ce` before any branch work.
- Safety tag `pre-maintenance-2026-08-23` pushed at that SHA.
- Plan doc committed to main as `79f448b docs(plan): maintainer-owned implementation and merge program`.
- Branch protection enabled: PR required, status check `validate`, strict up-to-date,
  0 approvals, conversations must resolve, force pushes/deletions disabled, admin enforced.
- PR #27 old head recorded: `ea2251b4d7b9b3bc84bb5b29df65794c1456c447` (CarmeloCampos:feat/config-global).
- PR #29 old head recorded: `d2166d4acf0a61f6e64e272ecf23662cef7ca3ba` (eettoreee:feat/hide-unfocused-banner).
- Maintainer implementation notices posted on both PRs before any history replacement.
- Issue #30 confirmed OPEN; issue #26 confirmed OPEN.

Baseline validation (clean checkout of main @ a2b6568, node_modules from npm ci):

- `npm ci` — ok (install-scripts warning only)
- `npm run check` — tsc --noEmit clean
- `npm run lint` — eslint clean
- `npm run test:all` — 818 tests, 818 pass, 0 fail (58 files)
- `npm run test:selfcheck` — OK: 56 unit + 1 integration + 1 e2e entries match manifest
- `npm run test:serial` — 786 tests, 786 pass, 0 fail
- `npm pack --dry-run` — pi-goal-x-0.27.4.tgz, 53 files
- `npm audit --omit=dev` — found 0 vulnerabilities
- `npm run bench:gate:naf` — PASS: no regressions, all claim-specific invariants hold (98 after rows vs 95 before rows)
- bench snapshot copied to /tmp/pi-goal-x-baseline-main-a2b6568.json; working tree restored to clean afterwards

## 2026-08-23 — PR A implementation (branch maint/pr-a-issue-30-checkpoints)

Commit sequence:

1. `docs(spec): define bounded checkpoint persistence and recovery`
2. `test(runtime): reproduce repeated full checkpoint growth` — reproducer red before the fix (missing bounded builder; legacy path persisted the full objective).
3. `fix(runtime): emit minimal v2 continuation checkpoints` — v2 marker is 68 chars for typical goal ids; runtime scheduling no longer loads settings or builds any prompt.
4. `fix(context): retain only the latest minimal checkpoint` — pure `compactGoalCheckpointContext` helper; provider context keeps ≤1 marker.
5. `feat(recovery): add checkpoint health report and offline session repair`
6. `bench(context): gate checkpoint growth and composed prompt duplication`
7. `docs(recovery): document repair, compatibility, and rollback`
8. `chore(release): 0.27.5`

Measured results (B10):

- persisted checkpoint content per turn: ~6.4K → 68 chars (v2 marker)
- persisted at 1,000 turns: 68,000 chars (gate ≤160,000); objective occurrences: 0
- provider-visible checkpoints after compaction (1,001-checkpoint fixture): 1 (gate ≤1)
- 850-entry legacy recovery: ~5,440,000 → 51,850 checkpoint chars (~0.96%, gate ≤5%)
- composed request full goal blocks: exactly 1; objective occurrences: exactly 1

Validation on the PR branch: test:all green (822 → 837 incl. new suites),
self-check manifest regenerated, check/lint clean, bench:gate:naf PASS,
test:checkpoint-recovery 19/19.
