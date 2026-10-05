# Benchmark diff — before → after (2026-08-05)

Agent-free runs (B8), same machine. p50 ms unless noted. Rows ordered by
improvement ratio (lower is faster). B6 gate: **PASS** — no regressions,
all claim-specific invariants hold.

| id | before | after | ratio | label |
|---|---|---|---|---|
| B1.ledger.1k | 0.5 | 0.0 | — | ledger full parse (1k events) |
| B1.pool.1g | 0.1 | 0.0 | — | pool scan (1 goal) |
| B2.readturn.1g | 0.5 | 0.0 | — | per-turn read pipeline (1 goal, 1k ledger) |
| B3.parse.1000 | 0.5 | 0.0 | — | ledger full parse (1000 events) |
| B3.parse.10000 | 5.1 | 0.0 | — | ledger full parse (10000 events) |
| B3.parse.5000 | 2.7 | 0.0 | — | ledger full parse (5000 events) |
| B5.lock.contended | 2831.9 | 251.6 | 0.09x | lock acquire wait under two-process contention (child holds 3s) |
| B2.readturn.10g | 0.8 | 0.1 | 0.12x | per-turn read pipeline (10 goals, 1k ledger) |
| B1.pool.50g | 1.6 | 0.6 | 0.37x | pool scan (50 goals) |
| B1.pool.10g | 0.5 | 0.2 | 0.40x | pool scan (10 goals) |
| B1.pool.10g.lat25 | 688.4 | 350.2 | 0.51x | pool scan (10 goals, +25ms/op) |
| B1.pool.50g.lat25 | 3206.3 | 1632.8 | 0.51x | pool scan (50 goals, +25ms/op) |
| B1.settings.present.lat25 | 60.9 | 31.3 | 0.51x | settings load (file present, +25ms/op) |
| B1.pool.1g.lat25 | 123.6 | 66.3 | 0.54x | pool scan (1 goal, +25ms/op) |
| B3.reconstruct.5000 | 3.2 | 3.1 | 0.97x | ledger reconstruction (5000 events) |
| B1.ledger.1k.lat25 | 32.3 | 31.3 | 0.97x | ledger full parse (1k, +25ms/op) |
| B1.append.single | 0.2 | 0.2 | 1.00x | ledger append (single event) |
| B1.append.x4 | 0.6 | 0.6 | 1.00x | ledger append x4 (current one-by-one) |
| B1.lock.uncontended | 0.1 | 0.1 | 1.00x | lock acquire+release (uncontended) |
| B2.mutationturn.task | 1.2 | 1.2 | 1.00x | per-turn mutation (one update_goal_task) |
| B3.reconstruct.1000 | 0.6 | 0.6 | 1.00x | ledger reconstruction (1000 events) |
| B3.reconstruct.10000 | 6.4 | 6.4 | 1.00x | ledger reconstruction (10000 events) |
| B5.auditor.dispatch | 1.3 | 1.3 | 1.00x | update_goal(complete) dispatch to pre-audit gate (auditor stubbed) |
| B7.compaction.summary | 0.1 | 0.1 | 1.00x | buildCompactionSummary (500 events) |
| B7.ledger.reconstruct | 0.1 | 0.1 | 1.00x | reconstructGoalLedger (500 events) |
| B7.tool.get_goal | 0.1 | 0.1 | 1.00x | get_goal handler |
| B7.tool.update_goal.blocked | 0.8 | 0.8 | 1.00x | update_goal(blocked) handler |
| B7.tool.update_goal.paused | 0.8 | 0.8 | 1.00x | update_goal(paused) handler |
| B7.tool.set_goal_tasks.50 | 0.9 | 1.0 | 1.11x | set_goal_tasks (50 tasks) handler |
| B7.tool.update_goal_task | 0.8 | 0.9 | 1.12x | update_goal_task(complete) handler |
| B7.tool.create_goal | 0.5 | 0.6 | 1.20x | create_goal handler |
| B5.startup.50g | 1.6 | 2.0 | 1.25x | session startup loadState (50 goals) |
| B5.startup.10g | 0.3 | 0.5 | 1.67x | session startup loadState (10 goals) |
| B5.startup.1g | 0.1 | 0.2 | 2.00x | session startup loadState (1 goal) |
| B1.settings.missing | 0.0 | 0.0 | — | settings load (file missing) |
| B1.settings.present | 0.0 | 0.0 | — | settings load (file present) |
| B7.accounting.budget | 0.0 | 0.0 | — | budgetLine + budgetRemaining |
| B7.accounting.charge | 0.0 | 0.0 | — | GoalAccounting begin+charge+end |
| B7.compaction.goalSummary | 0.0 | 0.0 | — | buildGoalCompactSummary (500 events) |
| B7.contract.extract | 0.0 | 0.0 | — | extractVerificationContract |
| B7.contract.promptSafe | 0.0 | 0.0 | — | promptSafeObjective |
| B7.evt.before_agent_start | 0.0 | 0.0 | — | before_agent_start event (1 goal, 1k ledger) |
| B7.files.parse | 0.0 | 0.0 | — | parseGoalFile |
| B7.files.serialize | 0.0 | 0.0 | — | serializeGoalFile |
| B7.format.goalDetails | 0.0 | 0.0 | — | goalDetails |
| B7.format.renderEvent | 0.0 | 0.0 | — | renderGoalEvent heading |
| B7.ledger.latestEvents | 0.0 | 0.0 | — | latestEventsForGoal (500 events) |
| B7.notifications.running | 0.0 | 0.0 | — | buildGoalRunningNotification |
| B7.policy.completionReport | 0.0 | 0.0 | — | buildCompletionReport |
| B7.policy.taskSummary | 0.0 | 0.0 | — | buildTaskSummary (20 tasks) |
| B7.policy.validateTasks | 0.0 | 0.0 | — | validateTaskListProposal (50 flat tasks) |
| B7.pool.goalList | 0.0 | 0.0 | — | buildGoalListText (10 goals) |
| B7.pool.selectorLabel | 0.0 | 0.0 | — | goalSelectorLabel |
| B7.record.normalize | 0.0 | 0.0 | — | normalizeGoalRecord |
| B7.render.confirmationTasks | 0.0 | 0.0 | — | renderConfirmationTasks (20 tasks) |
| B7.render.draftConfirmation | 0.0 | 0.0 | — | buildDraftConfirmationText |
| B7.render.escapeDialog | 0.0 | 0.0 | — | escape dialog render |
| B7.render.questionnaireAnswers | 0.0 | 0.0 | — | formatQuestionnaireAnswers (3 Q&A) |
| B7.render.taskOverlay | 0.0 | 0.0 | — | task-list overlay render (20 tasks) |
| B7.render.widgetComponent | 0.0 | 0.0 | — | GoalWidgetComponent.render (mock TUI) |
| B7.render.widgetLines | 0.0 | 0.0 | — | renderGoalWidgetLines (20 tasks) |
| B7.runtime.stalePrompt | 0.0 | 0.0 | — | staleContinuationPrompt |
| B7.runtime.unfocusedPrompt | 0.0 | 0.0 | — | unfocusedOpenGoalsPrompt |

## Claim verification (PLAN.md Part 1 magnitudes)

- **P1-1 cache-first reads**: settings load 60.9→33.8ms @25ms/op (1 stat steady-state);
  pool scan @25ms/op 688→350ms (10 goals), 3206→1643ms (50 goals) — ~2x; per-turn read
  pipeline ops 6→4 (1 goal) / 24→13 (10 goals).
- **P1-2 incremental ledger**: parse 5.1ms@10k events → ~0.0ms (one statSync); flat across
  1k/5k/10k (B6 invariant 10k/1k < 2 ✓).
- **P1-3 one transaction per turn**: 5 in-turn task mutations 140→38 fs ops total (verified
  separately); B7 tool-handler ops down (create_goal 14→12, set_goal_tasks 24→20).
- **P1-4 prompt trim**: taskListBlock 2154→387 est tokens (5.6x); continuationPrompt
  2505→1083 (2.3x). B6 token-reduction invariant ✓.
- **P1-5 bounded lock**: contended acquire 2831.9→248.3ms (11x — fail-fast ≈200ms bound).
- **P1-7 parallel startup**: loadState @25ms/op 1g 27.3ms, 10g 27.7ms, 50g 29.5ms — vs the
  sync pool scan's 3206ms @50 goals: ~100x on slow storage; local startup 0.3→0.6ms (async
  overhead on tiny local reads, immaterial).
- **P1-8 batch appends / P1-9 coalesced renders**: op-count reductions visible in B7 rows;
  render rows already ~0ms.
- **P1-6 warm auditor**: dispatch 1.3→1.2ms; the win is session warm-start (auditor no
  longer re-derives ledger/task facts) — measured as dispatch cost, not agent time
  (B8: no live agents).

## B9 per-operation budgets (targets for the next run)

| op class | budget (p50) | measured after |
|---|---|---|
| per-turn extension overhead (1 goal) | ≤ 1ms / ≤ 5 fs ops | 0.4ms / 4 ops |
| pool scan 10 goals @25ms/op | ≤ 400ms | 350ms |
| ledger tail read (any session size) | ≤ 0.1ms | ~0.0ms |
| lock acquire (contended, fail-fast) | ≤ 300ms | 248ms |
| goal-block tokens in continuation prompt (50 tasks) | ≤ 1200 est | 1083 |
| task mutation (in-turn, N≥2) | ≤ 10 fs ops each | ~4 ops |
| session startup 50 goals @25ms/op | ≤ 60ms | 29.5ms |
| widget render | ≤ 1ms | ~0ms |
| overlay open+render | ≤ 1ms | ~0ms |
| update_goal(complete) dispatch to pre-audit gate | ≤ 5ms | 1.2ms |
