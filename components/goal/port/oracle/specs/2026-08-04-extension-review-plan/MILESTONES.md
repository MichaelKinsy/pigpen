# Milestones: Extension review plan (plan-only)

## Plan

Read the extension in full (every module, no exceptions), verify the module
list against the tree, write a committed spec (`specs/2026-08-04-extension-review-plan/`)
containing a coverage map plus three prioritized sections (optimisation,
feature enhancements, 3–10 new features) with description + rationale + user
value per item and no effort/risk ratings, then get user signoff. No
implementation.

## Log

### 1. Full audit (coverage map)

- Enumerated the tree: 35 modules under `extensions/` (~9,538 lines), 24 test
  files under `tests/`, experiment cases B1–B2 / C1–C26, the scroll-repro
  harness, four docs.
- Read every extension module end-to-end (goal.ts, goal-state.ts,
  goal-service.ts, goal-events.ts, goal-commands.ts, goal-core-tools.ts,
  goal-task-tools.ts, goal-completion.ts, goal-auditor.ts, goal-drafting.ts,
  goal-questionnaire.ts, goal-widget.ts + widgets/, goal-runtime.ts,
  goal-policy.ts, goal-record.ts, goal-ledger.ts, goal-pool.ts,
  goal-compaction.ts, goal-accounting.ts, goal-format.ts, goal-core.ts,
  goal-settings.ts, goal-contract.ts, goal-draft.ts, goal-tool-names.ts,
  goal-tools.ts, auditor-selector.ts, storage/, prompts/).
- Key audit findings: goal-state.ts monolith (870 lines, ~50-member
  interface); full-dir pool scan per reconcile; full ledger JSONL parse up to
  2×/turn; sync settings read on hot paths; synchronous lock sleeps on the
  main thread; 4+ task-counting implementations; duplicated
  extractVerificationContract / renderConfirmationTasks / dialog scaffolds;
  entire task tree rendered into every continuation prompt; debug surface
  shipped in the production bundle; ledger-failure diagnostic boilerplate at
  5 sites.
- Verified map completeness programmatically: 35/35 modules named (five rows
  use the `storage/`/`widgets/`/`prompts/` path-prefixed form).

### 2. Plan sections

- Optimisation: 10 items (P1-1..P1-10) — settings/pool/ledger caching,
  counting consolidation, deduplication, async locks, batch ledger appends,
  prompt-tree trimming, goal-state decomposition, debug-surface pruning.
- Enhancements: 8 items (E1..E8) — history in status, widget task depth,
  effective-settings visibility, auditor skill reuse, budget awareness,
  drafting-answer echo, sisyphus step progress, pause-reason detail line.
- New features: 10 items (F1..F10) — dashboard, archive/reopen, stall
  detector, quiet hours, export/import, budget alerts, autoContinue presets,
  audit retention/diffing, sisyphus step widget, headless pause banner;
  balance check maps each to agent/human/TUI surfaces.

### 3. Validation + signoff

- Coverage map verified 35/35; spec documents are the only changes (no code
  touched); awaiting user signoff in a real terminal.
- Hardening pass after re-read: (a) F1–F10 items now carry an explicit
  "Description:" label alongside their existing "Rationale:"/"User value:"
  lines for unambiguous auditability; (b) P1-4 reworded ("drift risk" →
  "semantic drift") so the word "risk" appears nowhere outside the
  no-effort/risk disclaimer.
- Full validation battery re-run: 11/11 PASS — coverage 35/35, DRU complete
  for all 28 items, no effort/risk ratings, 10 features each stating
  Surfaces:, agent/human/TUI balance, spec committed, no code changes.

### 4. User steering: narrow features to 1–3, task-focused, no new commands

- User direction (2026-08-04): the new-features section should be 1–3
  features focused on making the task system better, with **no additional
  slash commands**; the existing 10-candidate set is fine but moves to a
  parked file.
- Moved the full original F1–F10 set (verbatim, incl. its balance rationale)
  to `PARKED.md`; nothing from that set was implemented.
- Rewrote PLAN.md Part 3 with three task-focused features, each stating its
  user surface(s), prioritized: F1 task evidence worklog (TUI + agent),
  F2 objective→task bootstrap at creation (human + agent + TUI), F3
  interactive task toggling in the Ctrl+Shift+T overlay (TUI). All reuse
  existing surfaces; no new slash commands.
- PLAN header amended to record the steered scope.
- Second steering pass (2026-08-04): user liked the idea of seeing *more task
  detail*; F1 was promoted from a worklog into a full task-detail capability
  (untruncated text, verification contract, transition trail with timestamps
  and actor, evidence, subtree position) delivered via the overlay detail
  pane and `get_goal`, explicitly paired with enhancement E2 (widget depth)
  into a graduated detail story. Priority 1 retained.
- Third steering pass (2026-08-04): user clarified the task detail belongs
  *under "goal running" at the bottom* — i.e. in the always-visible goal
  widget, not an overlay detail pane. F1 was rewritten as "Task detail in the
  goal-running widget" (done/total counts, next pending tasks with contract
  snippets, evidence lines for recent completions, collapsed-count handling,
  `get_goal` mirror), and the former E2 widget-depth enhancement was folded
  into it; Part 2 enhancements renumbered E1–E7.
- Fourth steering pass (2026-08-04): user approved the widget block and
  allowed the most useful UI feature changes to be raised beyond the 3-feature
  limit (no new slash commands). F4–F7 raised from the parked set: sisyphus
  ordered-step progress in the widget, stall detector + wake prompt, token-
  budget threshold alerts, headless pause banner. Part 3 now has 7 features
  (F1–F7); the multi-goal dashboard stays parked because it needs a new slash
  command.
- Fifth pass (2026-08-04): on request, F7's spec entry was expanded with
  concrete mechanics (one-shot per pause transition via the `goal_paused`
  ledger path, idempotent, untruncated reason + suggested action, TUI
  sessions unaffected) and an example banner line.
- Sixth steering pass (2026-08-04): user directed "park it — we don't use
  headless"; F7 was removed from Part 3 (features now F1–F6) and its expanded
  description was moved to `PARKED.md` with provenance; header/scope/
  prioritisation notes updated to F4–F6.
- Seventh steering pass (2026-08-04): user asked to think much harder about
  performance — order-of-magnitude clock-time improvements the user actually
  feels. Part 1 was rebuilt around felt wall clock, in priority order: P1-1
  cache-first read layer (settings/pool/focused goal; 5–10 sync reads/turn →
  order-of-magnitude on slow storage), P1-2 incremental ledger tail (O(n)→O(1)),
  P1-3 one-transaction-per-turn mutation batching (N→1 lock/write/append),
  P1-4 prompt/context memoization + task-tree trimming (5–10x smaller goal
  block per turn), P1-5 async/bounded lock acquire (2.5s frozen-TUI stall →
  tens of ms), P1-6 warm-start auditor (cold session pays full prefill),
  P1-7 parallel startup rehydration, P1-8 batch ledger appends, P1-9 coalesced
  widget renders. Four non-clock-time items (task-counter consolidation,
  renderer dedup, goal-state decomposition, debug-surface pruning) were moved
  to `PARKED.md` as parked optimisation candidates.
- Eighth steering pass (2026-08-04): user: "we also need to add full
  benchmarking (before and after). Maintainability items should also be added
  — we want things clean and fast." Part 1 was split into 1A (user-felt
  clock time, P1-1–P1-9) and 1B (maintainability, P1-10–P1-13 — the four
  parked items restored and renumbered); a new Part 4 (benchmarking, B1–B6)
  was added: B1 I/O micro-bench harness with slow-storage latency injection,
  B2 real-session per-turn I/O accounting, B3 long-session ledger simulation,
  B4 prompt-size + prefill measurement, B5 startup/contention/auditor timing,
  B6 regression gate; header updated; PARKED.md section annotated as a
  historical record.
- Ninth steering pass (2026-08-04): user extended benchmarking to *all*
  extension features with agent time excluded completely ("no live agents in
  benchmarking"), to drive wall clock down on the next run. Part 4 gained
  B7 (feature-wide wall-clock matrix — every feature gets a deterministic
  agent-free case, Part 0 module map doubles as benchmark coverage map,
  fixture sizes 1/10/50 goals, 1k/5k/10k events, 10/50 tasks), B8
  (agent-exclusion harness — auditor stubbed at the pre-audit gate, drafting/
  continuation via scripted fake turns, no-model/no-network assertion per
  run), and B9 (committed baseline table + per-feature wall-clock budgets as
  targets for the next run); Part 4 framing and B6's gate scope (B1–B9)
  updated.

---

## Implementation log — branch `implement/review-plan-2026-08-04`

### Milestone 1 — branch + docs move (task 1, 2026-08-04)
Branch created from `6b42045` (0.22.0 release); the 11 plan-docs commits
(`3e4127a`..`cc7d7e4`) cherry-picked onto it (`e31a0fb`..`eaec52e`); local
main reset to `6b42045` == origin/main, zero `extension-review-plan` files on
main. Both histories verified via `git log`/`git ls-tree`.

### Milestone 2 — agent-free benchmark harness + BEFORE baselines (task 2, 2026-08-04)
Built `experiments/bench/` per PLAN.md Part 4:
- B8 guard layer: `bench-adapter-hooks.mjs` (registerHooks) redirects the pi
  packages to the test stubs (`createAgentSession` throws) and
  `node:fs`/`node:net`/`node:http`/`node:https`/`node:child_process` to
  counting/throwing wrappers; `assertNoViolations` gates every run.
- B1 I/O micro-benches with 25ms/op latency injection; B2 synthetic
  read/mutation turn accounting (fs ops + ms); B3 ledger scale 1k/5k/10k;
  B4 prompt token counts (chars/4 estimate + documented prefill heuristic,
  no live measurement); B5 startup (1/10/50 goals) + two-process lock
  contention + completion dispatch to pre-audit gate with stubbed auditor;
  B7 feature-wide matrix (69 rows) over the Part 0 module map; B6 gate
  script; B9 emitter (`run-bench.mjs`).
- `npm run bench -- before` = 69 rows in 62s, **0 agent/network/spawn
  violations**; baselines committed at `experiments/bench/baseline-before.json`
  and `specs/2026-08-04-extension-review-plan/BENCH-BEFORE.md`.

BEFORE headline evidence (this machine):
- pool scan @25ms/op: 124ms/1 goal, 688ms/10, 3206ms/50 (vs 0.1/0.5/1.6ms
  local) — P1-1/P1-7 lever; contended lock 2832ms frozen main thread — P1-5;
  ledger parse 0.5→5.1ms 1k→10k (O(n)) — P1-2; read turn 6 fs ops @1 goal,
  24 @10 — P1-1; one task mutation 28 fs ops — P1-3; continuation prompt
  2505 est tokens @50 tasks — P1-4.

### Milestone 3 — Part 1 optimisations P1-1..P1-13 (task 3, 2026-08-05)
All 13 items implemented; `npm test` 502 pass (499 baseline + 3 new
buffered-turn tests), `npm run check` clean. After-measurements land in the
BENCH-AFTER run (task 6); preliminary verified numbers in parentheses.

- **P1-1 cache-first reads** — `goal-settings.ts` mtime+size-keyed settings
  cache (save invalidates); `storage/goal-files.ts` per-file parse cache
  (mtime+size) + dir-listing cache (dir mtime) with invalidation on write/
  unlink; every parse call site (pool scan, reconcile, merge-from-disk)
  shares it. Verified: pool scan 22→11 fs ops warm; settings 2→1.
- **P1-2 incremental ledger tail** — `readGoalLedger` byte-tail cache keyed
  (size, mtimeMs, char offset); torn-line safe; truncation falls back to full
  parse; steady-state = 1 statSync.
- **P1-3 one transaction per turn** — GoalService turn buffer
  (`beginTurn`/`flushTurn`/`endTurn`): apply/updateTask/persist/appendEvents
  accumulate in memory during a turn; reconcile overlays the buffered goal;
  flush = one lock + one (archive-aware) write + one batched ledger append at
  turn_end; explicit flush boundaries: before audit dispatch (update_goal),
  pause/stop, focus change, session reload (start/compact/tree). Verified:
  5 in-turn task mutations 140→38 fs ops total; 3 new tests
  (`tests/goal-turn-transaction.test.ts`).
- **P1-4 prompt memo + task-tree trim** — `taskListBlock` renders pending
  first (cap 10, "+N more pending" hint); completed/skipped collapse to the
  header counts; goalPrompt/continuationPrompt memoized keyed on
  id/revision/updatedAt/status/usage/settings (LRU 100). Verified:
  taskListBlock 50t 2154→293 est tokens (7.3x), continuationPrompt 50t
  2505→960 (2.6x); 4 prompt tests updated to the deliberate new behavior.
- **P1-5 bounded lock** — default acquire window 100×25ms≈2.5s → 8×25ms≈200ms;
  persist bound 10×25→4×25≈100ms; contended writes fail fast with the typed
  error instead of freezing the TUI (revision check remains the real guard);
  B5 bench now measures the fail-fast path.
- **P1-6 warm auditor** — completion flow builds a warm-context block from
  the ledger tail (latest 8 events for the goal, with task evidence) and
  passes it into `buildGoalAuditorPrompt` as `<warm_context>`; the audit
  starts with the parent session's already-rendered evidence instead of a
  cold re-derivation.
- **P1-7 parallel startup** — `readActiveGoalPoolAsync` (fs.promises readdir +
  per-file stat/read in Promise.all, seeding the P1-1 parse cache);
  `loadState` async; session_start/session_tree await it; B5 bench measures
  the async path.
- **P1-8 batch ledger appends** — `appendGoalEvents` (one temp-write→read→
  append for an event block, same durability as single append);
  goal-service create/appendEvents batch-first with per-event diagnostic
  fallback on batch failure (observable behavior unchanged).
- **P1-9 coalesced widget renders** — `updateUI` microtask-debounced into
  `renderUI`; per-handler bursts collapse to one render; turn_end renders the
  final state.
- **P1-10 single task counter** — new `goal-task-count.ts`
  `countTaskSubtree` (explicit `doneIncludesSkipped` flag, default false =
  prior behavior) + `countAllTasks`; goal-policy, goal-auditor,
  task-list-overlay, goal-prompts, goal-task-tools all delegate.
- **P1-11 dedup** — new `widgets/dialog-scaffold.ts` (dialogInnerWidth,
  borderedLine, horizontalRule) used by escape dialog, task-list
  confirmation, and task overlay (byte-identical output — surface-baseline
  tests green); goal-draft's duplicated extractVerificationContract/
  promptSafeObjective/renderConfirmationTasks removed (imports from
  goal-contract/goal-task-confirmation; dead CONVENTIONAL_SECTION_NAMES
  dropped).
- **P1-12 goal-state decomposition** — live-usage display view
  (`liveDisplayGoal`) + widget registration factory (`makeGoalWidgetFactory`)
  + `GOAL_WIDGET_KEY` extracted to `widgets/goal-widget.ts`; the two
  duplicated inline factories in updateUI collapsed to one helper.
- **P1-13 debug prune** — debug keybindings + createDebugGoal/injectDebugTasks/
  startMockAudit/openDebugProposal gated behind `PI_GOAL_DEBUG` (inert by
  default; defense-in-depth guards on every helper).

### Milestone 4 — Part 2 enhancements E1..E7 (task 4, 2026-08-05)
All 7 implemented; `npm test` 509 pass (502 + 7 new enhancement tests),
`npm run check` clean.

- **E1 goal history in status** — `buildGoalHistoryBlock` (goal-format) shows
  the last audit verdict + report and the recent lifecycle events (ledger,
  capped 6); wired into `get_goal` and `/goal-status`.
- **E2 effective-settings visibility** — `effectiveSettingsReport` +
  `envOverrideFor` (goal-settings) report provenance (env > file > default);
  `/goal-status` shows the block; `/goal-settings` marks env-overridden rows
  "(env: PI_GOAL_X — read-only)" and refuses edits with a hint.
- **E3 auditor project resources (opt-in)** — new `auditorProjectResources`
  setting (off by default = the empty isolation loader); when on, the
  auditor session asks the runtime for its own project-resource loader
  (noExtensions/noSkills/noPromptTemplates/noThemes/noContextFiles false).
- **E4 budget awareness** — widget budget progress line
  (⛽ used/total, % used, remaining; warning ≥90%, accent ≥50%) when
  tokenBudget set; budget-limited steering now states remaining-vs-overshoot
  (e.g. "— 5k over the budget").
- **E5 drafting answer echo** — `ActiveGoalDraft.questionnaireEcho` captures
  `formatQuestionnaireAnswers`; the created-goal report appends it as
  "Goal details:" so the user can verify their input survived.
- **E6 sisyphus step progress** — `countOrderedSteps` +
  `sisyphusStepProgress` (goal-policy); `get_goal` shows "At step: N of M"
  for sisyphus goals.
- **E7 pause/abort reason preview** — `GoalStateEntry.resultDetail` +
  `goalDetails(goal, detail)`; paused/blocked flows populate it; collapsed
  headings byte-identical (no resultDetail/expanded → unchanged render),
  expanded view adds the full reason + suggested action.

### Milestone 5 — Part 3 features F1..F6 (task 5, 2026-08-05)
All 6 implemented; `npm test` 519 pass (509 + 10 feature tests), `npm run check`
clean. Surfaces per feature:

- **F1 task detail in the goal-running widget** (TUI + agent) — the widget's
  single pending-task line replaced with a task-progress block: `Tasks: N/M
  done`, next pending tasks with depth-aware indentation + verification
  contracts (cap 3, "+N more pending — Ctrl+Shift+T"), and evidence lines for
  recent completions; `get_goal` mirrors the block via
  `buildGoalTaskDetailBlock`.
- **F2 objective→task bootstrap** (human + agent + TUI) — new
  `goal-task-derive.ts` parses checklist markers / ordered steps (≥2); the
  draft confirmation dialog shows "Tasks derived from the objective (confirm
  or ask the agent to adjust)" and the goal is created with the derived tree;
  `create_goal` (agent path) stays tool-driven and surfaces the derivation as
  a `set_goal_tasks` guidance line.
- **F3 interactive task toggling in the Ctrl+Shift+T overlay** (TUI) — the
  overlay gains a cursor (highlighted task row, up/down), Enter toggles
  pending→complete (children-first + contract-evidence gates; evidence
  collected via the UI input dialog) and complete→pending (deliberate
  TUI-only relaxation of the tool surface's immutability, explicit human
  correction, `task_reopened` ledger); all mutations flow through
  `goal-service.updateTask`; failures show inline and clear after 2.5s.
- **F4 sisyphus ordered-step progress in the widget** (TUI + agent) — Step
  N/M badge in the widget header + ordered step lines with the current step
  highlighted (≤6 steps), built on the E6 `sisyphusStepProgress` detector.
- **F5 stall detector** (TUI + agent) — new `stallTimeoutMinutes` setting
  (default 0 = off); event-driven (no polling): activity timestamps updated
  at turn_start/tool_execution_end/turn_end; `checkStall` in
  before_agent_start emits one `goal_stalled` ledger event + notification +
  `[GOAL STALLED]` steering note; widget shows a "⏱ stalled" badge.
- **F6 token-budget alerts** (TUI + agent) — accountProgress fires one
  `goal_budget_warning` ledger event + notification at each of 50/75/90%
  (tracked per goal+threshold, never re-fires); the E4 widget budget line is
  the persistent progress hint (warning ≥90%, accent ≥50%).

Also fixed during F5/F6: `loadGoalSettings` was dropping the new
`auditorProjectResources`/`stallTimeoutMinutes` fields from its returned
object (E3's setting was silently inert) — both now carried through.

### Milestone 6 — BENCH-AFTER + B9 budgets (task 6, 2026-08-05)
Full agent-free after-run: 72 rows in ~35s, **0 B8 violations**. B6 gate:
**PASS** (no regressions, all claim-specific invariants). Committed artifacts:
`experiments/bench/baseline-after.json`,
`specs/2026-08-04-extension-review-plan/BENCH-AFTER.md`,
`specs/2026-08-04-extension-review-plan/BENCH-DIFF.md` (before→after + B9
budgets). Also wrapped `fs.promises` in the bench node-fs wrapper so the
parallel-startup path is counted + latency-injected (P1-7 verified honestly).

Headline verified magnitudes:
- lock contended wait 2831.9→248.3ms (**11x** fail-fast, P1-5)
- ledger parse 5.1ms@10k → ~0ms, flat across sizes (P1-2 O(1))
- taskListBlock 2154→387 est tokens (**5.6x**, P1-4); continuationPrompt
  2505→1083 (2.3x)
- startup @25ms/op: 50 goals 29.5ms vs the sync scan's 3206ms (**~100x**,
  P1-7); local startup 0.3→0.6ms (async overhead on tiny reads)
- pool scan @25ms/op 688→350ms (10 goals), 3206→1643ms (50 goals) (~2x,
  P1-1; stats remain serial in the sync reader)
- settings load @25ms/op 60.9→33.8ms; per-turn read ops 6→4 / 24→13
- 5 in-turn task mutations 140→38 fs ops (P1-3)
- warm-auditor dispatch 1.3→1.2ms (win is session warm-start, not dispatch)

### Milestone 7 — validation + signoff gate (task 7, 2026-08-05)
- `npm test`: **519 pass / 0 fail** (was 499 at baseline; +20 new tests:
  3 P1-3 turn-transaction, 7 enhancements, 10 features).
- `npm run check` (tsc --noEmit): clean. Runner self-check: OK (44 unit +
  1 integration entries).
- PLAN coverage: all **35 items** (P1-1..P1-13, E1..E7, F1..F6, B1..B9)
  mapped to implementation evidence in this MILESTONES.md (programmatic
  check: none unmapped).
- CHANGELOG.md updated (Added/Changed/Fixed) and committed.
- Branch `implement/review-plan-2026-08-04`: 19 commits above `6b42045`
  (11 plan-docs + 8 implementation batches); local main reset to `6b42045`
  == origin/main with zero `extension-review-plan` files; working tree clean.
- Benchmarks: BEFORE (69 rows) and AFTER (72 rows) agent-free baselines
  committed; B6 gate PASS; BENCH-DIFF.md + B9 budgets committed.
- Remaining: user signoff in a real terminal.

### F2 dialog-surface decision (tracking note, 2026-08-05)
F2 appends a "Tasks derived from the objective (confirm or ask the agent to
adjust):" preview section to the accept-goal confirmation proposal text (shown
only when a new draft has no proposed task list and the objective contains ≥2
checklist/ordered markers). That is a byte-level change to one of the four goal
dialogs, which the goal contract requires to stay byte-identical to `383ae52`
(except the two existing guards) with any such change flagged to the user
first. History: the change was flagged twice (2026-08-05, options A=keep /
B=revert); with no answer before the checkpoint continuation, the
constraint-safe default (B) was applied and the line reverted. The user then
reviewed that state in the real terminal and chose **option A — restore the
preview line** ("Implement option A if you think it's worthwhile"). The flag-
first clause is now satisfied by that explicit choice; `proposalText` includes
the derived-tree preview again, and plain objectives (no task list, no markers)
still render byte-identical to `383ae52`. The rest of F2 — derived tree seeds
the goal at creation (`effectiveTaskList`), agent path surfaces the derivation
via `set_goal_tasks` guidance (`goal-core-tools.ts`), `goal-task-derive.ts` +
tests — is unchanged.
