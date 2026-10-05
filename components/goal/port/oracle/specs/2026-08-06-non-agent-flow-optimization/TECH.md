# Non-agent flow optimisation (2026-08-06) — TECH.md

Technical plan for the naf campaign. Numbers refer to
`HEADROOM.md` (fresh BEFORE baseline, 2026-08-06, this machine).

## 0. Tooling (task-1, done)

- `experiments/bench/campaigns.mjs` — campaign registry; each campaign has
  its own spec dir + JSON prefix so baselines never clobber each other.
- `run-bench.mjs [phase] [campaign]`, `b6-gate.mjs [campaign]` — campaign-
  parameterised orchestrator and gate (`npm run bench:naf -- before|after`,
  `npm run bench:gate:naf`).
- B1 now records fs op counts per row; B5 startup rows too (honest primary
  metrics for I/O-bound rows).
- B7 gained section D: unified-dashboard render/model rows plus post-bench
  non-agent flows (`goal-task-derive`, `goal-task-count`, `goal-status`).
  All 15 new rows measure at the noise floor (p50 <0.5ms) — the dashboard
  surface is already fast; it is covered for regression protection.
- `experiments/bench/classify.mjs [campaign] [--md path]` — programmatic
  headroom/exemption classification (emits HEADROOM.md).

## 1. Target structure (from HEADROOM.md, 29 headroom rows)

| Class | Rows | Primary metric | Mechanism |
|---|---|---|---|
| Read-path I/O | settings.present.lat25 (p50), pool.1g/10g/50g (+lat25) (fs ops), ledger.1k.lat25 (p50), B2.readturn.1g/10g (fs ops), get_goal (fs ops), B5.startup.* (fs ops) | fs ops / p50 | write-through session caches (below) |
| Write-path | append.x4 (fs ops 20→≤2) | fs ops | batched atomic append (one temp+rename for N events) |
| Lock | B5.lock.contended (p50 245.6→≤24.6) | p50 | non-blocking / bounded fail-fast acquire (was ~2.8s frozen pre-P1-5, 248ms now) |
| CPU | B3.reconstruct.1000/5000/10000 (p50 0.6/3.2/6.4→0.1/0.3/0.6) | p50 | O(1)-ish reconstruction / pre-indexed parse; avoid full replay per call |
| Auditor dispatch | B5.auditor.dispatch (p50 1.7→0.2) | p50 | warm dispatch: don't rebuild sessions/state per call |
| Prompts | B4.* (tokens 277–1084 → ≤27–108) | tokens | compact task/contract/objective fragments; content validated by e2e tests, flagged to user if semantics change |

## 2. Write-through session cache layer (core of the campaign)

New module `extensions/storage/read-cache.ts` (or equivalent inside
goal-files/goal-ledger/goal-settings):

- per-cwd memo of `{ settings, pool, ledger }` with a generation counter;
- invalidation: any extension write (persist, appendGoalEvent, saveSettings,
  createGoal, task transactions) bumps the generation / clears the entry;
- cheap signature check (dir/file mtime) where external-edit visibility must
  be preserved; ledger cache is append-invalidated (ledger only grows through
  `appendGoalEvent`);
- B8 constraint: the cache must not leak across fixtures in the bench (fresh
  cwd = fresh entry), and must never do I/O in the hot path (0 ops warm).

Targets this unlocks: pool scans 51 ops → 0–1, settings 1 → 0, ledger parse
1 → 0, read turns 13 → 0–1, startup warm p50 → ~0, get_goal 4 → 0.

## 3. Write-path batching

- `appendGoalEvent` → batched `appendGoalEvents(ctx, events)` doing one
  atomic temp+rename (2 ops) for N events; single append drops the pre-read
  (5 ops → 2). `append.x4` 20 → 2 (10x ✓); write-floor rows (append.single,
  lock.uncontended, tool mutations) are exempt-with-rationale but still get
  the op-count wins recorded in MILESTONES.

## 4. Lock

`B5.lock.contended` 245.6ms → ≤24.6ms. Direction: bounded fail-fast with a
non-blocking path so the main thread never spins the full window; caller
(goal-service persist) retries via the async turn machinery when the lock is
busy. Must keep cross-process exclusion semantics (tests pin the lock
behavior).

## 5. Gate extension (task-3)

`b6-gate.mjs` gains a per-campaign 10x invariant: for each headroom row in
the before baseline, assert after ≤ before/10 on the row's primary metric
(from classify.mjs); exempt rows (noise floor, durable-write floor) are
no-regression-only (existing tolerance). Gate must PASS before completion.

## 6. Validation

- `npm test` 0 failures; `npm run check` clean (extension changes are
  type-checked; bench files are .mjs).
- E2E: prompts' information content exercised by the existing e2e/integration
  tests; any prompt-token win that trims content is diffed and flagged.
- Dialogs + tool headings byte-identical to current main.

## 5. Cold start (task-2 extension, 2026-08-06)

Harness: `b5b-cold-start.mjs` (rows) + `bench-cold-child.mjs` (child that
runs ONE flow in a fresh process, reports ops + wall; hooks installed via
`--import bench-adapter-hooks.mjs`; spawn via the B8-authorized
`spawnContention`). Rows run in the naf campaign only, after B5.

Optimization: persistent pool snapshot in `storage/goal-files.ts` —
`.goals-pool-snapshot.json` in the goals dir:

- read path (`readPoolWithSnapshotSync/Async`): `lstat(root)` + snapshot read;
  on dir-mtime mismatch, one `readdir` verifies the `active_goal_*.md`
  filename set (ledger churn lives in the same dir and must not force a
  rescan); otherwise full scan + rewrite.
- write path: `writeActiveGoalFile` merges the written record in (read +
  temp+rename); `safeUnlinkGoalFile` removes by id (parsed from the active
  filename); archive flows through the unlink hook. Best-effort — a
  missing/corrupt snapshot falls back to a full scan.
- hydrate filters `status === "complete"` exactly like `scanActiveGoalFiles`.
- Symlink safety: the fast path still refuses symlinked roots; goal-file
  symlinks are only reachable through the full scan (unchanged behavior).
- Settings cold load dropped its redundant stat (readFile + ENOENT catch),
  2 → 1 op.

Cold rows (before = worktree of the pre-change commit ae4e562, same harness;
after = current tree):

| row | before | after | ratio |
|---|---|---|---|
| B1.pool.cold | 102 ops / 5.3ms | 3 ops / 0.9ms | 34x ops |
| B1.pool.cold.lat25 | 102 ops / 3199ms | 3 ops / 98.6ms | 32x |
| B5.startup.cold | 105 ops / 6.5ms | 4 ops / 2ms | 26x ops |
| B5.startup.cold.lat25 | 105 ops / 184ms | 4 ops / 112ms | 26x ops |
| B1.settings.cold | 2 ops | 1 op (read floor) | — |
| B1.ledger.cold | 2 ops | 1 op (read floor) | — |

Classification: pool/startup cold rows are HEADROOM (ops metric, targets
≤10/≤10) and met; settings/ledger cold rows are read-floor EXEMPT (one
mandatory read op; the redundant stat was removed).
