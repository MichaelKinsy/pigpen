# Non-agent flow optimisation (2026-08-06) — PRODUCT.md

Campaign: **naf** · Goal: benchmark every non-agent flow of pi-goal-x on
current main with the existing agent-free B1–B9 harness (extended to cover
the 0.24.0 unified-dashboard surface), then make every flow with meaningful
headroom measure ≥10x faster than a fresh BEFORE baseline, with zero
regressions elsewhere.

## Behavior stance

This campaign is **performance-only**. User-visible behavior (dialogs, tool
headings, command surface, prompts' *information content*) is preserved; unit
and integration tests must stay green (`npm test`, `npm run check`).

The one deliberate semantics nuance: **session-level read caching**.
Today `loadGoalSettings`, `readActiveGoalPool`, and `readGoalLedger` re-read
disk on every call (a per-turn reconcile does multiple scans). The campaign
introduces write-through caches keyed by cwd:

- reads return the cached value without touching disk;
- every extension-mediated write (goal create/update, ledger append, settings
  save) invalidates or bumps the cache, so **all extension-visible changes
  are always observed**;
- the only path that can go stale mid-session is an *external* hand-edit of a
  goal/settings/ledger file by another tool or process; those are picked up
  on the next full reconcile that detects a signature change (dir/file
  mtime), and at session restart. Cross-process extension writers are still
  mutually excluded by the per-goal lock and the revision check (which reads
  through the cache but is invalidated by any lock-guarded write).

If any optimization turns out to require an actual behavior change, it is
flagged to the user before landing — never silently folded in.

## Out of scope

- Anything requiring live agent sessions, model calls, or network (LLM
  latency cannot be guaranteed 10x).
- Auditor agent-session logic (the auditor *dispatch* path is measured, not
  the auditor itself).
- Feature changes, new slash commands.
- The four goal dialogs and goal tool-call headings stay byte-identical.
- The blocked 2026-08-04 review-plan goal's signoff (separate concern; its
  work is already merged to main).

## Cold session start (2026-08-06, user-requested extension)

The warm rows measure steady-state per-call cost; the campaign additionally
benchmarks the **cold** path (fresh process, all caches empty) via
`b5b-cold-start.mjs` (each sample runs the flow once in a fresh child
process): `B1.pool.cold`, `B1.settings.cold`, `B1.ledger.cold`,
`B5.startup.cold` + `.lat25` variants.

Cold read optimization — **persistent pool snapshot**:
`.pi/goals/.goals-pool-snapshot.json` caches the parsed active pool as one
file. A cold pool read is `lstat(root) + readFile(snapshot)` (2-3 ops) instead
of a per-goal scan (2 ops × goal count); every extension write
(`writeActiveGoalFile` / `safeUnlinkGoalFile` / archive) keeps it current via
a read-modify-write delta (+4 ops per mutation, mutation rows are
write-floor-exempt). Freshness:

- if the goals-dir mtime is unchanged → serve the snapshot (2 ops);
- if it changed but the `active_goal_*.md` filename set is identical (ledger
  appends live in the same dir and churn the dir mtime) → one extra readdir
  verifies the names and the snapshot is served (3 ops);
- if goal files were added/removed (including external edits) → full scan +
  snapshot rewrite (correctness preserved).

Semantics nuance vs before: an *in-place content edit* of a goal file by an
external tool (same filename, dir mtime unchanged) may be served stale from
the snapshot until the next extension write or mtime-changing change — the
same staleness class already documented for the in-memory pool cache
(mid-session). The safety-critical persist path is unaffected: it re-reads
the goal file directly (`parseGoalFile`, mtime-keyed) under the lock, so
cross-process revision conflicts are still detected. Tests pin all of this
(655 green).

Measured cold wins (this machine, agent-free): pool scan 102→3 fs ops
(34x), p50 5.3→0.9ms; lat25 3.2s→99ms (32x); session startup 105→4 fs ops
(26x); settings/ledger cold reads 2→1 op (the remaining op is the mandatory
read floor).
