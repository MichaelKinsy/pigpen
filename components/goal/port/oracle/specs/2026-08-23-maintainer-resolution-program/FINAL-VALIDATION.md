# FINAL VALIDATION — maintainer resolution program (2026-08-23)

Baseline: main @ `a2b6568b615c639c76a1924ec2731e9a933876ce`
Safety tag: `pre-maintenance-2026-08-23` (pushed)
Branch protection: enabled before any PR merge (PR required, status check
`validate`, strict up-to-date, 0 approvals, conversation resolution, no force
pushes/deletions, admin enforced).

## Merged PRs (all via expected_head_sha after green validate CI; main CI green after each)

| PR | Title | Head SHA | Merge SHA |
|---|---|---|---|
| #32 (PR A, issue #30) | bounded v2 checkpoints + recovery | 53c8f4e | 2557e50 |
| #27 (PR B rewrite) | layered global settings | 6ceba52 | 03ff28f |
| #29 (PR C rewrite) | layered hideUnfocusedBanner | 4bf271f | b18f071 |
| #33 | release 0.28.0 | ef73311 | 34dd84e |
| #34 (PR D) | model-context accounting | 2c6cec3 | 6bfbba4 |
| #35 (PR E) | single-source prompt dedup | 1a5fb95 | 850a3f7 |
| #37 (PR F) | UI/model payload separation | e369092 | 8335398 |
| #38 (PR G, issue #26) | blocker Oracle | ddabff4 | 246786e |
| #39 | release 0.29.0 | 2aa1aba | bf26d16 |

## Provenance

- Old contributor heads recorded and verified BEFORE replacement:
  - PR #27 fork head `ea2251b4d7b9b3bc84bb5b29df65794c1456c447` — replaced on
    CarmeloCampos:feat/config-global under
    `--force-with-lease=<ref>:ea2251b4…`; lease held.
  - PR #29 fork head `d2166d4acf0a61f6e64e272ecf23662cef7ca3ba` — replaced on
    eettoreee:feat/hide-unfocused-banner under lease; lease held.
- `scripts/verify-pr-head.mjs --forbid-ancestor <old heads> --forbid-author
  'CarmeloCampos' --forbid-author 'eettoreee'` PASS on every implementation
  branch. No cherry-pick/rebase/patch-apply/merge of contributor commits; no
  Co-authored-by trailers.
- Maintainer notices posted on #27/#29 before each replacement.

## Releases

| Version | Tag | GitHub release | npm | Contents |
|---|---|---|---|---|
| 0.27.5 | v0.27.5 | yes | pi-goal-x@0.27.5 | PR A (#30 fix, health report, pi-goal-x-recover CLI, B10 gates) |
| 0.28.0 | v0.28.0 | yes | pi-goal-x@0.28.0 | PR B + PR C |
| 0.29.0 | v0.29.0 | yes | pi-goal-x@0.29.0 | PRs D/E/F/G |

Release checks (§84): package tarball installs clean (`npm pack --dry-run`),
100-turn synthetic goal smoke test passed (100 checkpoints = 7,300 chars,
max marker 73 chars, zero objective leakage), legacy-fixture dry-run recovery
verified (19,869 → 177 chars), bin executable shipped.

## Test counts at final state (main @ bf26d16)

- `npm run check` / `npm run lint`: clean
- `npm run test:all`: **877 tests, 877 pass, 0 fail** (66 files)
- `npm run test:selfcheck`: OK (64 unit + 1 integration + 1 e2e manifest match)
- `npm run test:serial`: 844 pass, 0 fail
- `npm pack --dry-run`: ok; `npm audit --omit=dev`: 0 vulnerabilities
- `npm run bench:gate:naf`: PASS every PR cycle
- `npm run context:gate`: PASS every PR cycle since PR D
- `npm run test:checkpoint-recovery`: 19/19
- `npm run test:settings-race`: PASS (two-writer lost-update coverage)
- `npm run test:oracle`: 7/7

## Issue closures with evidence

- **#30** CLOSED COMPLETED after PR #32 merged + 0.27.5 released. Measured:
  full checkpoint copies per turn 1→0 (68–73 char markers); provider-visible
  history N→≤1; 1,000-turn persisted content ~6.4MB→68,000 chars; 850-entry
  legacy recovery 5,440,000→51,850 chars (~0.96%).
- **#26** CLOSED COMPLETED after PR #38 merged + 0.29.0 released. Default-off
  verified; actionable-advice and needs-human journeys demonstrated in tests;
  context cost measured (+136 tool-schema bytes/request).

## Benchmark highlights

- B10 checkpoint growth: v2 marker ≤160 chars enforced; persisted@1000 turns
  ≤160,000 gate (actual 68,000); recovery ≤5% gate (actual ~0.96%).
- Context baseline (composed requests): task fixtures reduced −97..−277 chars
  by PR E despite +136 schema bytes added later by PR G's attempted_actions.

## Follow-up work explicitly left out

1. **Live paired outcome non-inferiority evaluation (§80.2)** — DEFERRED by
   user decision during goal confirmation. The deterministic protocol gate
   (§80.1: full state-machine suite, semantic-marker presence, no unsafe tool
   exposure, stale-checkpoint work prohibition, evidence-gated completion,
   Sisyphus order retention) gated every optimization PR instead. Before
   shipping further token reductions, run §80.2 across ≥3 models with the
   journey harness seeded from identical fixture snapshots.
2. Live Oracle journeys against a real stronger model (the session factory is
   injectable; scripted journeys cover the protocol).
3. Settings `.bak` optional backup file (§83.3 left as optional future hardening).
