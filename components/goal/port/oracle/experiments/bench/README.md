# Agent-free benchmark harness (PLAN.md Part 4, B1–B9)

Deterministic wall-clock benchmarks over the **real extension functions** with
**no live agents, no network, and no unauthorized child processes** (B8).

## Run

```sh
# BEFORE baselines (captured on 2026-08-04 before any optimisation change)
npm run bench -- before

# AFTER re-run (once the optimisations land)
npm run bench -- after

# Regression gate (compares after vs before, fails on regressions)
npm run bench:gate
```

The orchestrator (`run-bench.mjs`) emits:

- `experiments/bench/baseline-<phase>.json` — raw rows,
- `specs/2026-08-04-extension-review-plan/BENCH-<PHASE>.md` — human table
  (committed in the spec dir per the plan).

Requires Node 22.15+ (`registerHooks`).

## What is measured (B1–B9)

| Bench | Covers | Key rows |
|---|---|---|
| B1 `b1-io.mjs` | I/O hot paths with slow-storage emulation (25ms/op latency injection) | settings load, pool scan (1/10/50 goals), ledger parse, lock acquire, ledger append (single + x4 one-by-one) |
| B3 `b3-ledger-scale.mjs` | Long-session scaling | ledger parse + reconstruction at 1k/5k/10k events (BEFORE is O(n): 0.5ms → 5.1ms; P1-2 must make it flat) |
| B2 `b2-turn-accounting.mjs` | Per-turn extension overhead | synthetic read turn (settings+pool+ledger+prompt) and one real `update_goal_task` mutation turn; fs ops/turn + ms/turn |
| B4 `b4-prompt-size.mjs` | Prompt/context size | `taskListBlock` / `continuationPrompt` / `goalPrompt` tokens at 10/50 tasks (chars/4 estimate; prefill est @ 1000 t/s — documented heuristic, not a live measurement) |
| B5 `b5-startup-contention-auditor.mjs` | Startup, contention, auditor | `loadState` at 1/10/50 goals; two-process lock wait (child holds 3s — BEFORE ≈ 2.8s frozen); `update_goal(complete)` dispatch to the pre-audit gate with a stubbed auditor |
| B7 `b7-feature-matrix.mjs` | Every extension feature (Part 0 module map = coverage map) | tool handlers (create/get/update_goal/set_goal_tasks/update_goal_task), `before_agent_start`, dialog + widget + overlay + escape renders (mock TUI), policy, accounting, compaction, ledger helpers, contract, format, notifications, records, files |

B6 `b6-gate.mjs` replays B1–B9 rows and fails on regressions
(`after.p50 > max(before.p50*1.5, before.p50+10)`) plus the claim-specific
invariants: B3 flatness (10k/1k < 2), B2 ops reduction, B4 token reduction,
B5 lock-wait collapse.

B9: `run-bench.mjs` is the baseline/budget emitter — the first run's table is
the spec; the after run's diff sets per-feature budgets for the next run.

## Agent-free guarantee (B8)

`bench-adapter-hooks.mjs` installs `registerHooks` that:

1. redirect `@earendil-works/pi-ai`, `-/pi-coding-agent`, `-/pi-tui` to the
   **same stubs as the test suite** — `createAgentSession` throws, so no live
   agent can ever start;
2. redirect `node:fs` to `node-fs.mjs` (sync-op counting + injected latency);
3. redirect `node:net`/`node:http`/`node:https`/`node:child_process` to
   throwing wrappers — any network use or spawn fails the run
   (`assertNoViolations`, `state.violations`).

The only permitted process use is B5's two-process lock contention child,
gated by `allowChildProcess(() => spawnContention(...))`.

## Numbers captured BEFORE (2026-08-04, this machine)

Headline rows from `BENCH-BEFORE.md` (full table in the spec dir):

- pool scan @ 25ms/op: **124ms (1 goal) / 688ms (10) / 3206ms (50)** vs
  0.1/0.5/1.6ms local — the P1-1/P1-7 order-of-magnitude lever on slow
  storage (a per-turn reconcile is 2–3 scans).
- contended lock acquire: **2832ms** main-thread block (P1-5 → tens of ms).
- ledger parse: 0.5ms @1k → 5.1ms @10k events (P1-2 → flat).
- per-turn read pipeline: 6 fs ops/turn @1 goal, 24 @10 goals (P1-1 → 1–2).
- one task mutation: 28 fs ops (P1-3 → one transaction).
- continuation prompt, 50-task tree: **2505 estimated tokens** (P1-4 → 5–10x
  smaller goal block).
- `before_agent_start` with 1 goal + 1k ledger: <1ms local.

These numbers are evidence on this machine, not machine-independent
performance promises (same caveat as the test runner).
