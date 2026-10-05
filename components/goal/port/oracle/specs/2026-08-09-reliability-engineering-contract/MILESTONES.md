# Milestones — reliability and maintainability contract

## 2026-08-09 — Phase 0: stabilize the worktree as a standalone release

The in-flight ledger-dedup/health-report changes were validated at the release
point and released as **pi-goal-x 0.26.3** (separate from any refactor work).

### Release-point validation (all green)

- `npm run check` (`tsc --noEmit`): clean.
- `node scripts/run-unit-tests.mjs all`: **756 tests pass** (51 unit files +
  integration + e2e), 0 failures.
- `test:selfcheck`: runner self-check OK, manifest matches.
- `npm pack --dry-run`: OK, 53 files.
- `npm audit --omit=dev`: 0 vulnerabilities.
- `git diff --check`: clean.
- `bench:gate:naf`: fresh release-point after-baseline gated against the
  committed before-baseline — **PASS, 95/95 rows, no regressions** (refreshed
  `experiments/bench/baseline-naf-after.json` +
  `specs/2026-08-06-non-agent-flow-optimization/BENCH-AFTER.md`).

### Baseline figures (fresh, release point, local SSD, NAF campaign, B8)

| Category | Row | p50 | fs ops | Notes |
| --- | --- | --- | --- | --- |
| Cold start | `B5.startup.cold` | 1.9 ms | 4 | wall 1.8–1.9ms ×5 |
| Cold start (slow disk) | `B5.startup.cold.lat25` | 118.6 ms | 4 | +25ms/op emulation |
| Cold start | `B1.pool.cold` | 0.8 ms | 3 | pool read |
| Cold start | `B1.settings.cold` | 0.3 ms | 1 | settings load |
| Cold start | `B1.ledger.cold` | 1.2 ms | 1 | full ledger parse |
| Contention | `B5.lock.contended` | 13.1 ms | — | fail-fast, bounded ≈200ms window |
| Mutation write | `B2.mutationturn.task` | 1.2 ms | 19 | one task mutation, batched append |
| Mutation write | `B1.append.single` | 0 ms | 1 | single event append |
| Mutation write | `B1.append.x4` | 0 ms | 1 | one appendFileSync for 4 events |
| Ledger read | `B1.ledger.1k` | 0 ms | 0 | cache-served steady state |
| Ledger read | `B2.readturn.1g` / `10g` | 0 ms | 0 | read turn = zero fs ops |
| Ledger read | `B3.parse.1000` / `10000` | 0 ms | — | flat parse at scale |
| Ledger read | `B1.ledger.cold` | 1.2 ms | 1 | cold full-parse bound |

Key structural numbers for later phases: steady-state read turns cost **0 fs
ops** (cache); a cold session startup reads 4 files in ~2ms; a full ledger
cold parse is ~1.2ms at current sizes; a contended lock resolves by
fail-fast in ~13ms inside the ≈200ms bound.

### Release

- `package.json` 0.26.2 → **0.26.3**; CHANGELOG Unreleased folded into
  `## [0.26.3] — 2026-08-09`.
- Feature commit: ledger-dedup + health report + specs (`goal-service-ledger-
  dedup`, `project-improvement-audit`) + refreshed NAF baselines.
- `chore(release): 0.26.3`; tag `v0.26.3`; `npm publish`; GitHub release;
  pushed `main` + tag.

## 2026-08-09 — Phase 1a: GitHub Actions CI

- Added `.github/workflows/ci.yml`: on push to `main` and every PR, matrix
  `node: [22, 24]` runs `npm ci` → `npm run check` → `test:all` →
  `test:selfcheck` → `npm pack --dry-run` → `npm audit --omit=dev` →
  `bench:gate:naf`.
- **Found + fixed a Node-22 incompatibility**: `scripts/run-unit-tests.mjs`
  passed `--test-isolation=none` unconditionally, which Node 22 rejects (the
  flag only exists since 23.4). The runner now probes the running binary
  (`--test-isolation=none --test --help` exit code) and omits the flag on
  older releases. Verified both paths:
  - Node 26: 756/756 pass with `--test-isolation=none`.
  - Node 22.23 (official binary): 756/756 pass via the process-isolation
    fallback; `tsc --noEmit` clean; selfcheck OK; `bench:gate:naf` PASS.
  - Full CI-equivalent sequence also green on Node 26 from a clean `npm ci`.
- The real Node 22.15+ floor is exercised on the GitHub runner on push.

## 2026-08-09 — Phase 1b: engineering contract

### package.json

- `engines`: `{"node": ">=22.15.0"}` (verified: full suite + tsc + gate green on
  Node 22.23 and Node 26; the runner probe already handles the
  `--test-isolation` flag difference across the range).
- **Peer ranges replace wildcard `"*"` after compatibility testing**: pi
  SDK floor verified by running the full suite (756/756) in a sandbox pinned
  to `@earendil-works/pi-*@0.83.0` + typebox 1.3.11, and against 0.84.1 in
  the real project. Declared `>=0.83.0 <0.85.0` for all three pi packages;
  `typebox: ^1.3.11`.
- **Safe typebox patch taken**: `^1.0.58` → `^1.3.11` (installed 1.3.11;
  the one available patch per `npm outdated`).
- Superseded `docs/goal-ts-refactor-test-strategy.md` removed from the
  `files` list — `npm pack --dry-run` now ships only current docs.

### Dependabot

- `.github/dependabot.yml`: weekly updates for `github-actions` and `npm`
  ecosystems, versioning-strategy auto.

### Lint gate (small, high-signal)

- `eslint.config.mjs` (flat config): eslint:recommended + TS parser;
  `no-undef`/`no-unused-vars` off for TS (tsc owns those; the core rule also
  misfires on interface-conformance params and re-exported symbols),
  `no-regex-spaces` off (deliberate multi-space matches in tests),
  `no-empty` with `allowEmptyCatch`, `@typescript-eslint/no-explicit-any`
  scoped to `extensions/**` (test scaffolding may use `as any`).
- Fixes applied: 9 `no-useless-escape` (unnecessary `\"` in regex literals),
  5 `no-control-regex` documented as deliberate ANSI-SGR matching via
  eslint-disable comments, 6 `any` in extensions (3 narrow structural casts
  over SDK stream events; 3 documented disables where the pi SDK itself
  types `Model<any>`, sdk.d.ts:18; 1 typed tool-input map in goal-drafting).
- Removed dangling `n/`-rule disable comments (the globals package's own
  config pattern leaked into two test files).
- `npm run lint` wired into CI after `check`. `eslint .` exit 0.

### Stricter TypeScript: `noUncheckedIndexedAccess` (incremental, now on)

- 161 errors when enabled: 36 in extensions/, 125 in tests — all mechanical
  `possibly undefined` index accesses, fixed with behavior-preserving `!`
  assertions (provably in-bounds loop accesses, guarded matches) or local
  narrowing (the width-safety loop). Fixed extensions first, then test
  files; `tsc` kept green throughout. Flag now enabled in `tsconfig.json`;
  `npm run check` clean; 756/756 tests pass (type-only changes).

### TypeScript 7 evaluation (isolated, documented verdict)

- Method: installed `typescript@7.0.2` (the native 7.0.2 tsc) into a temp
  copy + swapped into `node_modules` temporarily (package.json/lock
  untouched), ran the project `tsc --noEmit`.
- Result: **TypeScript 7.0.2 compiles the project with zero errors** — no
  code changes required for the type-check gate. Notes: `-p` works when the
  binary is invoked directly; TS7's `--version` prints "TypeScript: No errors
  found" (cosmetic output change); Node's runtime type stripping is
  unaffected.
- Verdict: adoption is viable; do it as an isolated PR (typescript 5.x →
  7.0.2 devDep bump) with CI + lint + full suite confirmation, kept separate
  from feature work. Not merged in this goal — evaluation only.

## 2026-08-09 — Phase 3a: versioned atomic ledger checkpoint + tail replay

### Design

- `reconstructGoalLedger` split into an accumulator (`ReconstructAccumulator`
  with goals/terminalGoals/focusedGoalId/focusGeneration/focusGenByGoal) +
  `applyLedgerEvents` (inline batch switch) + `finalizeLedgerState` —
  behavior-identical (existing reconstruction tests unchanged, plus the
  equivalence holds by construction).
- Checkpoint file `.pi/goals/.goal-ledger-checkpoint.json` (versioned,
  format-tagged, atomic temp-write + rename): reconstructed accumulator,
  per-goal recent-event tails (cap 12), `coveredBytes`/`coveredEvents`.
  The JSONL ledger is untouched and authoritative; the checkpoint is a
  best-effort optimization.
- `loadLedgerState(ctx)` cold path, in priority order: warm cache (0 fs ops)
  → fresh checkpoint (2 ops: checkpoint read + stat) → checkpoint + grown
  ledger (positioned tail read + incremental replay, checkpoint refreshed)
  → full parse + reconstruct + fresh checkpoint write (missing/corrupt/
  version-mismatch/truncated-below-coverage fallback).
- Mutation path maintains the in-memory mirror on every append (0 ops) and
  writes the disk checkpoint atomically, throttled to once per 32 appends or
  2s — preserving the NAF append headroom (`B1.append.x4` ≤ 2 ops: measured
  1) while bounding cold-start tails for long sessions. Staleness is always
  safe: coveredBytes mismatch only costs a tail replay.
- Adoption: `buildCompactionSummary` accepts an optional `LedgerStateReadResult`
  (reconstructed state + per-goal tails + state-carried latest auditor
  result); the post-compaction resync prompt in goal-events uses
  `loadLedgerState` instead of the full-events read.

### Bench evidence (fresh after-baseline, gate PASS)

| Row | ops | Notes |
| --- | --- | --- |
| `B1.ledgerstate.cold` | 2 | no checkpoint: stat + full parse + write |
| `B1.ledgerstate.cp.hit` | 2 | checkpoint read + stat, no parse |
| `B1.ledgerstate.cp.tail` | 2 | checkpoint + 50-event external tail |
| `B1.append.x4` (unchanged) | 1 | throttle keeps append headroom |
| `B3.reconstruct.5000` | 0.3ms | inlined accumulator + n=30 (was flaky at n=10) |

### Tests (9 new, in tests/goal-ledger-checkpoint.test.ts)

Full fallback + checkpoint write, warm-cache zero-op, tail replay (external
growth), version mismatch → full, malformed body → full, truncation below
coverage → full, external growth replay, mutation-path maintenance across the
throttle boundary, full-events API untouched. All pass; suite 765/765;
`tsc --noEmit` clean; lint clean; gate PASS 3/3 consecutive runs.

## 2026-08-09 — Phase 3b: /goal-refresh command

- Registered `goal-refresh` (goal-commands.ts): invalidates the pool, ledger
  (incl. checkpoint mirror), and settings caches, then re-reads cold and
  reports a diff — the explicit user-owned path for external `.pi` edits; no
  watchers, no per-turn polling.
- Pure `diffGoalRefreshState(before, after)` (exported, unit-testable): pool
  goal additions/removals, ledger event-count + malformed-entry changes,
  settings fingerprint changes (cache-served before vs cold after — a live
  stat can never see a pre-command change, so the parsed-settings fingerprint
  is the comparison key).
- README: `/goal-refresh` added to the quick-reference + command table;
  curated command list tests updated 14 → 15 (pinned order).
- Tests: 5 unit (unchanged / pool add+remove / ledger growth / malformed /
  settings fingerprint / combined) + 1 integration flow (unchanged → external
  goal file + ledger line + settings rewrite via raw fs → 3 changes reported
  → again no changes). Suite 771/771; tsc + lint clean; gate PASS.

## 2026-08-09 — Phase 3c: pool-snapshot fast-path fix

- **Root cause (verified)**: the pool snapshot lived at
  `.pi/goals/.goals-pool-snapshot.json` — INSIDE the goals directory whose
  mtime it records as its freshness key. Every snapshot write (temp + rename
  in that dir) bumped the dir mtime, so the recorded key never matched again:
  every cold read paid lstat + snapshot read + a readdir name-check fallback
  (`activeGoalNamesMatchSync`) = 3 ops instead of the claimed 2. Measured in
  the bench: `B1.pool.cold` ops=3.
- **Fix**: the snapshot now lives at `.pi/.goals-pool-snapshot.json` (outside
  the watched goals dir); writes never perturb the validity key. Legacy
  in-dir snapshots are still read as a one-time fallback and removed on the
  first new-location write (best-effort).
- **Verified**: `B1.pool.cold` now ops=2 (lstat + snapshot read, mtime key
  holds); the after-baseline records it; gate PASS. 4 new unit tests in
  tests/goal-pool-snapshot.test.ts: snapshot outside the dir + mtime key
  matches after write; delta updates keep the key fresh; legacy fallback is
  served (hydration marker proves snapshot origin); legacy file removed on
  write. Suite 775/775; tsc + lint clean.

## 2026-08-09 — Phase 3d: read-only recovery report + guarded repair

- New `extensions/goal-recovery.ts` + `/goal-recovery` command (read-only by
  default): reports malformed goal files (active_goal_*.md that fail to
  parse), malformed ledger lines (from the ledger reader), stale locks
  (`.locks/*.lock` with a dead pid or age > 30s TTL), and orphaned pool-
  snapshot entries (no matching file). `formatRecoveryReport` renders the
  report; never appends ledger events or rewrites goal files.
- `/goal-recovery repair`: removes stale locks and refreshes the pool
  snapshot — only with `ctx.ui.confirm` approval, and every touched file is
  copied to `.pi/goals/.recovery-backup/<timestamp>/` first. Malformed goal
  files and ledger lines are reported but never rewritten automatically
  (rewriting user-owned data is out of scope; automatic ledger rewriting is
  a documented non-goal).
- README + curated command list tests updated (16 commands).
- Tests (5, tests/goal-recovery.test.ts): healthy report; all four failure
  classes identified; confirmation rejection touches nothing; confirmed
  repair backs up + removes lock + refreshes snapshot (orphan gone);
  read-only invariance (no ledger appends, no goal-file rewrites).
  Suite 780/780; tsc + lint clean; gate PASS.

## 2026-08-09 — Phase 3e: crash/interruption + multi-process fault tests

- New tests/fault-injection.test.ts (5 tests, real child processes):
  - torn goal-file write (partial temp + truncated goal file) is detected as
    malformed, temp files are never parsed, a re-write atomically restores;
  - torn ledger tail (partial JSONL line) is counted malformed, valid events
    before it are intact, and the checkpoint tail replay tolerates it;
  - stale lock (dead pid, old mtime) is reclaimed promptly; a live child
    holder makes acquisition fail fast (bounded, <2s — no TUI freeze);
  - two child processes write the same goal concurrently via the extension's
    atomic writer — every surviving file parses cleanly and is one complete
    writer's version, never a hybrid;
  - a child process appends a goal file + ledger event while the parent's
    caches are warm — the refresh diff reports both (cross-process
    invalidation pickup).
  Suite 785/785; tsc + lint clean; gate PASS; diff-check clean.

## 2026-08-09 (post-goal steer) — CI slimmed

- Single **Node 24** job (the node 22 matrix leg dropped; Node 24 covers the
  `>=22.15.0` floor), halving runner time.
- `concurrency` group with `cancel-in-progress: true`: a new push/PR update
  cancels the previous run instead of queueing duplicates.
- All gates kept (check, lint, test:all, selfcheck, pack dry-run, audit,
  bench gate) — every remaining step is seconds-level (~30s wall-clock job).
