# Benchmark baseline — after

Generated 2026-08-05T11:47:04.695Z · agent-free (B8) · local machine numbers (p50/p95/max, ms unless noted).

Fixture sizes and storage classes are per row. The next run re-emits this file as `BENCH-AFTER.md` and the B6 gate diffs the two.

| id | label | modules | fixture | ops | n | p50 ms | p95 ms | max ms | latency | notes |
|---|---|---|---|---|---|---|---|---|---|---|
| B1.settings.present | settings load (file present) | goal-settings | 1 settings file | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B1.settings.present.lat25 | settings load (file present, +25ms/op) | goal-settings | 1 settings file | - | 100 | 31.3 | 35.1 | 35.1 | 25ms/op | mean 31.4ms |
| B1.settings.missing | settings load (file missing) | goal-settings | no file | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B1.pool.1g | pool scan (1 goal) | storage/goal-files | 1 active goal files | - | 30 | 0 | 0.1 | 0.1 | 0ms | mean 0ms |
| B1.pool.1g.lat25 | pool scan (1 goal, +25ms/op) | storage/goal-files | 1 active goal files | - | 10 | 66.3 | 70.3 | 70.3 | 25ms/op | mean 65.5ms |
| B1.pool.10g | pool scan (10 goals) | storage/goal-files | 10 active goal files | - | 30 | 0.2 | 0.3 | 0.5 | 0ms | mean 0.2ms |
| B1.pool.10g.lat25 | pool scan (10 goals, +25ms/op) | storage/goal-files | 10 active goal files | - | 10 | 350.2 | 381.4 | 381.4 | 25ms/op | mean 353.8ms |
| B1.pool.50g | pool scan (50 goals) | storage/goal-files | 50 active goal files | - | 30 | 0.6 | 1.1 | 1.1 | 0ms | mean 0.7ms |
| B1.pool.50g.lat25 | pool scan (50 goals, +25ms/op) | storage/goal-files | 50 active goal files | - | 10 | 1632.8 | 1666.6 | 1666.6 | 25ms/op | mean 1629.3ms |
| B1.ledger.1k | ledger full parse (1k events) | goal-ledger | 1000 ledger events | - | 20 | 0 | 0 | 0 | 0ms | mean 0ms |
| B1.ledger.1k.lat25 | ledger full parse (1k, +25ms/op) | goal-ledger | 1000 ledger events | - | 5 | 31.3 | 35.1 | 35.1 | 25ms/op | mean 30.3ms |
| B1.lock.uncontended | lock acquire+release (uncontended) | storage/goal-lock | fresh lock dir | - | 50 | 0.1 | 0.2 | 0.3 | 0ms | mean 0.1ms |
| B1.append.single | ledger append (single event) | goal-ledger | 1k-event ledger file | - | 50 | 0.2 | 0.2 | 0.2 | 0ms | mean 0.2ms |
| B1.append.x4 | ledger append x4 (current one-by-one) | goal-ledger | 1k-event ledger file | - | 20 | 0.6 | 0.7 | 0.7 | 0ms | mean 0.6ms; P1-8 batches to one op |
| B3.parse.1000 | ledger full parse (1000 events) | goal-ledger | 1000 events | - | 10 | 0 | 0 | 0 | 0ms | mean 0ms |
| B3.reconstruct.1000 | ledger reconstruction (1000 events) | goal-ledger | 1000 in-memory events | - | 10 | 0.6 | 0.8 | 0.8 | 0ms | mean 0.7ms |
| B3.parse.5000 | ledger full parse (5000 events) | goal-ledger | 5000 events | - | 10 | 0 | 0 | 0 | 0ms | mean 0ms |
| B3.reconstruct.5000 | ledger reconstruction (5000 events) | goal-ledger | 5000 in-memory events | - | 10 | 3.1 | 3.6 | 3.6 | 0ms | mean 3.2ms |
| B3.parse.10000 | ledger full parse (10000 events) | goal-ledger | 10000 events | - | 10 | 0 | 0 | 0 | 0ms | mean 0ms |
| B3.reconstruct.10000 | ledger reconstruction (10000 events) | goal-ledger | 10000 in-memory events | - | 10 | 6.4 | 7.8 | 7.8 | 0ms | mean 6.6ms |
| B2.readturn.1g | per-turn read pipeline (1 goal, 1k ledger) | goal-settings + storage/goal-files + goal-ledger + prompts/goal-prompts | 1 goals, 1000 events | 4 | 20 | 0 | 1.3 | 1.3 | 0ms | fs ops/turn 4 (p50), mean 0.1ms |
| B2.readturn.10g | per-turn read pipeline (10 goals, 1k ledger) | goal-settings + storage/goal-files + goal-ledger + prompts/goal-prompts | 10 goals, 1000 events | 13 | 20 | 0.1 | 1.3 | 1.3 | 0ms | fs ops/turn 13 (p50), mean 0.2ms |
| B2.mutationturn.task | per-turn mutation (one update_goal_task) | goal-task-tools + goal-service + storage/goal-files + goal-ledger + storage/goal-lock | 1 goal, 1 task | 24 | 1 | 1.2 | 1.2 | 1.2 | 0ms | fs ops for one task mutation: 24 (P1-3 batches to one lock+write+append) |
| B4.taskListBlock.10t | taskListBlock (10-task tree) | prompts/goal-prompts | 10 tasks (half complete, contracts+evidence) | 277 | 1 | - | - | - | 0ms | 1107 chars, ~277 tokens; est prefill ~0.28s @ 1000t/s (estimate, not live) |
| B4.continuationPrompt.10t | continuationPrompt (10-task tree) | prompts/goal-prompts | 10 tasks (half complete, contracts+evidence) | 973 | 1 | - | - | - | 0ms | 3889 chars, ~973 tokens; est prefill ~0.97s @ 1000t/s (estimate, not live) |
| B4.goalPrompt.10t | goalPrompt (10-task tree) | prompts/goal-prompts | 10 tasks (half complete, contracts+evidence) | 973 | 1 | - | - | - | 0ms | 3889 chars, ~973 tokens; est prefill ~0.97s @ 1000t/s (estimate, not live) |
| B4.taskListBlock.50t | taskListBlock (50-task tree) | prompts/goal-prompts | 50 tasks (half complete, contracts+evidence) | 387 | 1 | - | - | - | 0ms | 1548 chars, ~387 tokens; est prefill ~0.39s @ 1000t/s (estimate, not live) |
| B4.continuationPrompt.50t | continuationPrompt (50-task tree) | prompts/goal-prompts | 50 tasks (half complete, contracts+evidence) | 1083 | 1 | - | - | - | 0ms | 4330 chars, ~1083 tokens; est prefill ~1.08s @ 1000t/s (estimate, not live) |
| B4.goalPrompt.50t | goalPrompt (50-task tree) | prompts/goal-prompts | 50 tasks (half complete, contracts+evidence) | 1083 | 1 | - | - | - | 0ms | 4330 chars, ~1083 tokens; est prefill ~1.08s @ 1000t/s (estimate, not live) |
| B5.startup.1g | session startup loadState (1 goal, parallel reads) | goal-state + storage/goal-files + goal-settings | 1 open goals | - | 6 | 0.2 | 0.3 | 0.3 | 0ms | mean 0.2ms; P1-7 parallel + cached |
| B5.startup.1g.lat25 | session startup loadState (1 goal, +25ms/op) | goal-state + storage/goal-files + goal-settings | 1 open goals | - | 3 | 27.3 | 27.3 | 27.3 | 25ms/op | P1-7 parallel reads amortise the per-op latency |
| B5.startup.10g | session startup loadState (10 goals, parallel reads) | goal-state + storage/goal-files + goal-settings | 10 open goals | - | 6 | 0.5 | 0.7 | 0.7 | 0ms | mean 0.5ms; P1-7 parallel + cached |
| B5.startup.10g.lat25 | session startup loadState (10 goals, +25ms/op) | goal-state + storage/goal-files + goal-settings | 10 open goals | - | 3 | 27.7 | 27.7 | 27.7 | 25ms/op | P1-7 parallel reads amortise the per-op latency |
| B5.startup.50g | session startup loadState (50 goals, parallel reads) | goal-state + storage/goal-files + goal-settings | 50 open goals | - | 6 | 2 | 2.1 | 2.1 | 0ms | mean 1.9ms; P1-7 parallel + cached |
| B5.startup.50g.lat25 | session startup loadState (50 goals, +25ms/op) | goal-state + storage/goal-files + goal-settings | 50 open goals | - | 3 | 29.5 | 29.5 | 29.5 | 25ms/op | P1-7 parallel reads amortise the per-op latency |
| B5.lock.contended | lock acquire under two-process contention (child holds 3s, DEFAULT bounds) | storage/goal-lock | 2 processes, 1 goal lock | - | 1 | 251.6 | 251.6 | 251.6 | 0ms | fail-fast in 251.6ms; P1-5 bounded window ≈200ms (was ~2.8s frozen) |
| B5.auditor.dispatch | update_goal(complete) dispatch to pre-audit gate (auditor stubbed) | goal-core-tools + goal-completion + goal-state + goal-service + goal-ledger | 1 active goal each, 5 fixtures | - | 5 | 1.3 | 2.7 | 2.7 | 0ms | cold=1.2ms warm=2.7ms; P1-6 seeds the auditor session warm |
| B7.tool.create_goal | create_goal handler | goal-core-tools + goal-state + goal-service + goal-notifications | 1 focused fixture | 12 | 5 | 0.6 | 1.2 | 1.2 | 0ms | fs ops/case ~12; mean 0.7ms |
| B7.tool.get_goal | get_goal handler | goal-core-tools + goal-state + goal-format + goal-pool | 1 focused fixture | 4 | 5 | 0.1 | 0.2 | 0.2 | 0ms | fs ops/case ~4; mean 0.1ms |
| B7.tool.update_goal.paused | update_goal(paused) handler | goal-core-tools + goal-state + goal-service | 1 focused fixture | 20 | 5 | 0.8 | 0.9 | 0.9 | 0ms | fs ops/case ~20; mean 0.7ms |
| B7.tool.update_goal.blocked | update_goal(blocked) handler | goal-core-tools + goal-service + goal-ledger | 1 focused fixture | 20 | 5 | 0.8 | 1.7 | 1.7 | 0ms | fs ops/case ~20; mean 0.9ms |
| B7.tool.set_goal_tasks.50 | set_goal_tasks (50 tasks) handler | goal-task-tools + goal-task-confirmation + goal-policy + goal-service | 1 focused fixture | 20 | 5 | 1 | 1.2 | 1.2 | 0ms | fs ops/case ~20; mean 1ms |
| B7.tool.update_goal_task | update_goal_task(complete) handler | goal-task-tools + goal-service + storage/goal-lock + goal-ledger | 1 goal, 20 tasks | 24 | 5 | 0.9 | 1.9 | 1.9 | 0ms | fs ops/case ~24; mean 1.1ms |
| B7.evt.before_agent_start | before_agent_start event (1 goal, 1k ledger) | goal-events + goal-state + goal-service + goal-ledger + prompts/goal-prompts | 1 goal, 1000 events | - | 5 | 0 | 0.1 | 0.1 | 0ms | mean 0ms; P1-1/2 cut the reads here |
| B7.render.confirmationTasks | renderConfirmationTasks (20 tasks) | goal-task-confirmation | 20-task tree | - | 50 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.render.draftConfirmation | buildDraftConfirmationText | goal-draft | 1 goal, 20 tasks | - | 50 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.render.questionnaireAnswers | formatQuestionnaireAnswers (3 Q&A) | goal-questionnaire | 3 answers | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.render.widgetLines | renderGoalWidgetLines (20 tasks) | widgets/goal-widget | 1 goal, 20 tasks | - | 100 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.render.widgetComponent | GoalWidgetComponent.render (mock TUI) | widgets/goal-widget | 1 goal, 20 tasks | - | 100 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.render.taskOverlay | task-list overlay render (20 tasks) | widgets/task-list-overlay | 1 goal, 20 tasks | - | 100 | 0 | 0 | 0.1 | 0ms | mean 0ms |
| B7.render.escapeDialog | escape dialog render | widgets/goal-escape-dialog | objective text | - | 100 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.policy.taskSummary | buildTaskSummary (20 tasks) | goal-policy | 20-task tree | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.policy.validateTasks | validateTaskListProposal (50 flat tasks) | goal-policy + goal-task-tools | 50 flat tasks | - | 100 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.policy.completionReport | buildCompletionReport | goal-policy | 1 goal, 20 tasks | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.pool.goalList | buildGoalListText (10 goals) | goal-pool + goal-core + goal-format | 10 open goals | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.pool.selectorLabel | goalSelectorLabel | goal-pool + goal-core | 1 goal | - | 2000 | 0 | 0 | 0.3 | 0ms | mean 0ms |
| B7.accounting.charge | GoalAccounting begin+charge+end | goal-accounting | 1 active goal | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.accounting.budget | budgetLine + budgetRemaining | goal-accounting | goal with 100k budget | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.notifications.running | buildGoalRunningNotification | widgets/goal-notifications | 1 config | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.compaction.summary | buildCompactionSummary (500 events) | goal-compaction + goal-ledger | 1 goal, 500 events | - | 50 | 0.1 | 0.2 | 0.4 | 0ms | mean 0.1ms |
| B7.compaction.goalSummary | buildGoalCompactSummary (500 events) | goal-compaction | 1 goal, 500 events | - | 50 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.ledger.reconstruct | reconstructGoalLedger (500 events) | goal-ledger | 500 events | - | 50 | 0.1 | 0.1 | 0.2 | 0ms | mean 0.1ms |
| B7.ledger.latestEvents | latestEventsForGoal (500 events) | goal-ledger | 500 events | - | 500 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.contract.extract | extractVerificationContract | goal-contract + goal-draft | objective text | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms; P1-11 dedups the goal-draft copy |
| B7.contract.promptSafe | promptSafeObjective | goal-contract | objective text | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.record.normalize | normalizeGoalRecord | goal-record | raw record | - | 2000 | 0 | 0 | 0.1 | 0ms | mean 0ms |
| B7.files.serialize | serializeGoalFile | storage/goal-files + goal-core + goal-record | 1 goal | - | 1000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.files.parse | parseGoalFile | storage/goal-files + goal-record | 1 goal file | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms; P1-1 caches per-turn parse |
| B7.format.renderEvent | renderGoalEvent heading | goal-format | task_complete event | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.format.goalDetails | goalDetails | goal-format + goal-accounting | 1 goal, 20 tasks | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.runtime.stalePrompt | staleContinuationPrompt | prompts/goal-prompts + goal-core | 1 goal | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.runtime.unfocusedPrompt | unfocusedOpenGoalsPrompt | prompts/goal-prompts | 3 open goals | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
