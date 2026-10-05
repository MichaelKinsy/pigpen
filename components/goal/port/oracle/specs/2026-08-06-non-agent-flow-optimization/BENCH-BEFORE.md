# Benchmark baseline — before

Generated 2026-08-06T11:53:50.941Z · agent-free (B8) · local machine numbers (p50/p95/max, ms unless noted).

Fixture sizes and storage classes are per row. The next run re-emits this file as `BENCH-AFTER.md` and the B6 gate diffs the two.

| id | label | modules | fixture | ops | n | p50 ms | p95 ms | max ms | latency | notes |
|---|---|---|---|---|---|---|---|---|---|---|
| B1.settings.present | settings load (file present) | goal-settings | 1 settings file | 1 | 200 | 0 | 0 | 0.4 | 0ms | mean 0ms |
| B1.settings.present.lat25 | settings load (file present, +25ms/op) | goal-settings | 1 settings file | 1 | 100 | 33.4 | 35.1 | 35.1 | 25ms/op | mean 31.8ms |
| B1.settings.missing | settings load (file missing) | goal-settings | no file | 1 | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B1.pool.1g | pool scan (1 goal) | storage/goal-files | 1 active goal files | 2 | 30 | 0 | 0.1 | 0.1 | 0ms | mean 0ms |
| B1.pool.1g.lat25 | pool scan (1 goal, +25ms/op) | storage/goal-files | 1 active goal files | 2 | 10 | 62.9 | 70.2 | 70.2 | 25ms/op | mean 62.5ms |
| B1.pool.10g | pool scan (10 goals) | storage/goal-files | 10 active goal files | 11 | 30 | 0.3 | 0.6 | 1.1 | 0ms | mean 0.3ms |
| B1.pool.10g.lat25 | pool scan (10 goals, +25ms/op) | storage/goal-files | 10 active goal files | 11 | 10 | 338.9 | 358.6 | 358.6 | 25ms/op | mean 338.4ms |
| B1.pool.50g | pool scan (50 goals) | storage/goal-files | 50 active goal files | 51 | 30 | 0.7 | 1.2 | 1.2 | 0ms | mean 0.8ms |
| B1.pool.50g.lat25 | pool scan (50 goals, +25ms/op) | storage/goal-files | 50 active goal files | 51 | 10 | 1600.5 | 1654.8 | 1654.8 | 25ms/op | mean 1603.1ms |
| B1.ledger.1k | ledger full parse (1k events) | goal-ledger | 1000 ledger events | 1 | 20 | 0 | 0 | 0 | 0ms | mean 0ms |
| B1.ledger.1k.lat25 | ledger full parse (1k, +25ms/op) | goal-ledger | 1000 ledger events | 1 | 5 | 35.1 | 35.1 | 35.1 | 25ms/op | mean 33.3ms |
| B1.lock.uncontended | lock acquire+release (uncontended) | storage/goal-lock | fresh lock dir | 3 | 50 | 0.2 | 0.2 | 0.3 | 0ms | mean 0.2ms |
| B1.append.single | ledger append (single event) | goal-ledger | 1k-event ledger file | 5 | 50 | 0.2 | 0.2 | 0.3 | 0ms | mean 0.2ms |
| B1.append.x4 | ledger append x4 (current one-by-one) | goal-ledger | 1k-event ledger file | 20 | 20 | 0.7 | 0.8 | 0.9 | 0ms | mean 0.7ms; P1-8 batches to one op |
| B3.parse.1000 | ledger full parse (1000 events) | goal-ledger | 1000 events | - | 10 | 0 | 0 | 0 | 0ms | mean 0ms |
| B3.reconstruct.1000 | ledger reconstruction (1000 events) | goal-ledger | 1000 in-memory events | - | 10 | 0.6 | 0.8 | 0.8 | 0ms | mean 0.6ms |
| B3.parse.5000 | ledger full parse (5000 events) | goal-ledger | 5000 events | - | 10 | 0 | 0 | 0 | 0ms | mean 0ms |
| B3.reconstruct.5000 | ledger reconstruction (5000 events) | goal-ledger | 5000 in-memory events | - | 10 | 3.2 | 3.5 | 3.5 | 0ms | mean 3.2ms |
| B3.parse.10000 | ledger full parse (10000 events) | goal-ledger | 10000 events | - | 10 | 0 | 0 | 0 | 0ms | mean 0ms |
| B3.reconstruct.10000 | ledger reconstruction (10000 events) | goal-ledger | 10000 in-memory events | - | 10 | 6.4 | 11.2 | 11.2 | 0ms | mean 7.2ms |
| B2.readturn.1g | per-turn read pipeline (1 goal, 1k ledger) | goal-settings + storage/goal-files + goal-ledger + prompts/goal-prompts | 1 goals, 1000 events | 4 | 20 | 0 | 1.1 | 1.1 | 0ms | fs ops/turn 4 (p50), mean 0.1ms |
| B2.readturn.10g | per-turn read pipeline (10 goals, 1k ledger) | goal-settings + storage/goal-files + goal-ledger + prompts/goal-prompts | 10 goals, 1000 events | 13 | 20 | 0.1 | 1.2 | 1.2 | 0ms | fs ops/turn 13 (p50), mean 0.2ms |
| B2.mutationturn.task | per-turn mutation (one update_goal_task) | goal-task-tools + goal-service + storage/goal-files + goal-ledger + storage/goal-lock | 1 goal, 1 task | 24 | 1 | 1.2 | 1.2 | 1.2 | 0ms | fs ops for one task mutation: 24 (P1-3 batches to one lock+write+append) |
| B4.taskListBlock.10t | taskListBlock (10-task tree) | prompts/goal-prompts | 10 tasks (half complete, contracts+evidence) | 277 | 1 | - | - | - | 0ms | 1107 chars, ~277 tokens; est prefill ~0.28s @ 1000t/s (estimate, not live) |
| B4.continuationPrompt.10t | continuationPrompt (10-task tree) | prompts/goal-prompts | 10 tasks (half complete, contracts+evidence) | 973 | 1 | - | - | - | 0ms | 3889 chars, ~973 tokens; est prefill ~0.97s @ 1000t/s (estimate, not live) |
| B4.goalPrompt.10t | goalPrompt (10-task tree) | prompts/goal-prompts | 10 tasks (half complete, contracts+evidence) | 750 | 1 | - | - | - | 0ms | 3000 chars, ~750 tokens; est prefill ~0.75s @ 1000t/s (estimate, not live) |
| B4.taskListBlock.50t | taskListBlock (50-task tree) | prompts/goal-prompts | 50 tasks (half complete, contracts+evidence) | 388 | 1 | - | - | - | 0ms | 1552 chars, ~388 tokens; est prefill ~0.39s @ 1000t/s (estimate, not live) |
| B4.continuationPrompt.50t | continuationPrompt (50-task tree) | prompts/goal-prompts | 50 tasks (half complete, contracts+evidence) | 1084 | 1 | - | - | - | 0ms | 4334 chars, ~1084 tokens; est prefill ~1.08s @ 1000t/s (estimate, not live) |
| B4.goalPrompt.50t | goalPrompt (50-task tree) | prompts/goal-prompts | 50 tasks (half complete, contracts+evidence) | 862 | 1 | - | - | - | 0ms | 3445 chars, ~862 tokens; est prefill ~0.86s @ 1000t/s (estimate, not live) |
| B5.startup.1g | session startup loadState (1 goal, parallel reads) | goal-state + storage/goal-files + goal-settings | 1 open goals | 6 | 6 | 0.2 | 0.4 | 0.4 | 0ms | mean 0.2ms; P1-7 parallel + cached |
| B5.startup.1g.lat25 | session startup loadState (1 goal, +25ms/op) | goal-state + storage/goal-files + goal-settings | 1 open goals | 6 | 3 | 27.3 | 27.3 | 27.3 | 25ms/op | P1-7 parallel reads amortise the per-op latency |
| B5.startup.10g | session startup loadState (10 goals, parallel reads) | goal-state + storage/goal-files + goal-settings | 10 open goals | 24 | 6 | 0.5 | 0.6 | 0.6 | 0ms | mean 0.5ms; P1-7 parallel + cached |
| B5.startup.10g.lat25 | session startup loadState (10 goals, +25ms/op) | goal-state + storage/goal-files + goal-settings | 10 open goals | 24 | 3 | 27.9 | 27.9 | 27.9 | 25ms/op | P1-7 parallel reads amortise the per-op latency |
| B5.startup.50g | session startup loadState (50 goals, parallel reads) | goal-state + storage/goal-files + goal-settings | 50 open goals | 104 | 6 | 2.4 | 2.6 | 2.6 | 0ms | mean 2.3ms; P1-7 parallel + cached |
| B5.startup.50g.lat25 | session startup loadState (50 goals, +25ms/op) | goal-state + storage/goal-files + goal-settings | 50 open goals | 104 | 3 | 29.7 | 29.7 | 29.7 | 25ms/op | P1-7 parallel reads amortise the per-op latency |
| B5.lock.contended | lock acquire under two-process contention (child holds 3s, DEFAULT bounds) | storage/goal-lock | 2 processes, 1 goal lock | - | 1 | 245.6 | 245.6 | 245.6 | 0ms | fail-fast in 245.6ms; P1-5 bounded window ≈200ms (was ~2.8s frozen) |
| B5.auditor.dispatch | update_goal(complete) dispatch to pre-audit gate (auditor stubbed) | goal-core-tools + goal-completion + goal-state + goal-service + goal-ledger | 1 active goal each, 5 fixtures | - | 5 | 1.7 | 4.2 | 4.2 | 0ms | cold=1.5ms warm=4.2ms; P1-6 seeds the auditor session warm |
| B7.tool.create_goal | create_goal handler | goal-core-tools + goal-state + goal-service + goal-notifications | 1 focused fixture | 12 | 5 | 0.6 | 1.6 | 1.6 | 0ms | fs ops/case ~12; mean 0.8ms |
| B7.tool.get_goal | get_goal handler | goal-core-tools + goal-state + goal-format + goal-pool | 1 focused fixture | 4 | 5 | 0.1 | 0.2 | 0.2 | 0ms | fs ops/case ~4; mean 0.1ms |
| B7.tool.update_goal.paused | update_goal(paused) handler | goal-core-tools + goal-state + goal-service | 1 focused fixture | 20 | 5 | 0.7 | 0.8 | 0.8 | 0ms | fs ops/case ~20; mean 0.7ms |
| B7.tool.update_goal.blocked | update_goal(blocked) handler | goal-core-tools + goal-service + goal-ledger | 1 focused fixture | 20 | 5 | 0.6 | 0.8 | 0.8 | 0ms | fs ops/case ~20; mean 0.7ms |
| B7.tool.set_goal_tasks.50 | set_goal_tasks (50 tasks) handler | goal-task-tools + goal-task-confirmation + goal-policy + goal-service | 1 focused fixture | 20 | 5 | 0.8 | 1 | 1 | 0ms | fs ops/case ~20; mean 0.8ms |
| B7.tool.update_goal_task | update_goal_task(complete) handler | goal-task-tools + goal-service + storage/goal-lock + goal-ledger | 1 goal, 20 tasks | 24 | 5 | 0.7 | 0.8 | 0.8 | 0ms | fs ops/case ~24; mean 0.7ms |
| B7.evt.before_agent_start | before_agent_start event (1 goal, 1k ledger) | goal-events + goal-state + goal-service + goal-ledger + prompts/goal-prompts | 1 goal, 1000 events | - | 5 | 0 | 0.1 | 0.1 | 0ms | mean 0ms; P1-1/2 cut the reads here |
| B7.render.confirmationTasks | renderConfirmationTasks (20 tasks) | goal-task-confirmation | 20-task tree | - | 50 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.render.draftConfirmation | buildDraftConfirmationText | goal-draft | 1 goal, 20 tasks | - | 50 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.render.questionnaireAnswers | formatQuestionnaireAnswers (3 Q&A) | goal-questionnaire | 3 answers | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.render.widgetLines | renderGoalWidgetLines (20 tasks) | widgets/goal-widget | 1 goal, 20 tasks | - | 100 | 0 | 0.1 | 0.1 | 0ms | mean 0ms |
| B7.render.widgetComponent | GoalWidgetComponent.render (mock TUI) | widgets/goal-widget | 1 goal, 20 tasks | - | 100 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.render.taskOverlay | task-list overlay render (20 tasks) | widgets/task-list-overlay | 1 goal, 20 tasks | - | 100 | 0 | 0.1 | 0.1 | 0ms | mean 0ms |
| B7.render.escapeDialog | escape dialog render | widgets/goal-escape-dialog | objective text | - | 100 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.policy.taskSummary | buildTaskSummary (20 tasks) | goal-policy | 20-task tree | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.policy.validateTasks | validateTaskListProposal (50 flat tasks) | goal-policy + goal-task-tools | 50 flat tasks | - | 100 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.policy.completionReport | buildCompletionReport | goal-policy | 1 goal, 20 tasks | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.pool.goalList | buildGoalListText (10 goals) | goal-pool + goal-core + goal-format | 10 open goals | - | 200 | 0 | 0 | 0.2 | 0ms | mean 0ms |
| B7.pool.selectorLabel | goalSelectorLabel | goal-pool + goal-core | 1 goal | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.accounting.charge | GoalAccounting begin+charge+end | goal-accounting | 1 active goal | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.accounting.budget | budgetLine + budgetRemaining | goal-accounting | goal with 100k budget | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.notifications.running | buildGoalRunningNotification | widgets/goal-notifications | 1 config | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.compaction.summary | buildCompactionSummary (500 events) | goal-compaction + goal-ledger | 1 goal, 500 events | - | 50 | 0.1 | 0.2 | 0.4 | 0ms | mean 0.1ms |
| B7.compaction.goalSummary | buildGoalCompactSummary (500 events) | goal-compaction | 1 goal, 500 events | - | 50 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.ledger.reconstruct | reconstructGoalLedger (500 events) | goal-ledger | 500 events | - | 50 | 0.1 | 0.1 | 0.2 | 0ms | mean 0.1ms |
| B7.ledger.latestEvents | latestEventsForGoal (500 events) | goal-ledger | 500 events | - | 500 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.contract.extract | extractVerificationContract | goal-contract + goal-draft | objective text | - | 2000 | 0 | 0 | 0.1 | 0ms | mean 0ms; P1-11 dedups the goal-draft copy |
| B7.contract.promptSafe | promptSafeObjective | goal-contract | objective text | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.record.normalize | normalizeGoalRecord | goal-record | raw record | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.files.serialize | serializeGoalFile | storage/goal-files + goal-core + goal-record | 1 goal | - | 1000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.files.parse | parseGoalFile | storage/goal-files + goal-record | 1 goal file | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms; P1-1 caches per-turn parse |
| B7.format.renderEvent | renderGoalEvent heading | goal-format | task_complete event | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.format.goalDetails | goalDetails | goal-format + goal-accounting | 1 goal, 20 tasks | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.runtime.stalePrompt | staleContinuationPrompt | prompts/goal-prompts + goal-core | 1 goal | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.runtime.unfocusedPrompt | unfocusedOpenGoalsPrompt | prompts/goal-prompts | 3 open goals | - | 2000 | 0 | 0 | 0.1 | 0ms | mean 0ms |
| B7.dashboard.model.20t | deriveGoalDashboardModel (20 tasks, 12 events) | widgets/goal-dashboard-model + goal-activity + goal-core | 1 goal, 20 tasks, 12 events | - | 100 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.dashboard.model.50t | deriveGoalDashboardModel (50 tasks, 12 events) | widgets/goal-dashboard-model + goal-activity + goal-core | 1 goal, 50 tasks, 12 events | - | 100 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.dashboard.compact.20t | renderCompactDashboard (20 tasks) | widgets/goal-dashboard-renderer | 1 goal, 20 tasks | - | 100 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.dashboard.expanded.20t | renderExpandedDashboard (20 tasks, 24 rows) | widgets/goal-dashboard-renderer | 1 goal, 20 tasks | - | 100 | 0 | 0.1 | 0.1 | 0ms | mean 0ms |
| B7.dashboard.expanded.50t | renderExpandedDashboard (50 tasks, 24 rows) | widgets/goal-dashboard-renderer | 1 goal, 50 tasks | - | 100 | 0 | 0.1 | 0.2 | 0ms | mean 0ms |
| B7.dashboard.currentTask | renderCurrentTaskBlock (current task) | widgets/goal-dashboard-renderer | 1 goal, current task | - | 100 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.dashboard.activity | renderActivityBlock (8 items) | widgets/goal-dashboard-renderer | 8 activity items | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.dashboard.unfocused | renderUnfocusedDashboard (3 open goals) | widgets/goal-dashboard-renderer | 3 open goals | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.dashboard.auditor | deriveAuditorDashboardModel + renderAuditorDashboard (running) | widgets/auditor-dashboard-model + widgets/goal-dashboard-renderer | running audit | - | 100 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.dashboard.auditCard | deriveAuditResultCard + renderAuditResultCard | widgets/auditor-dashboard-model + widgets/goal-dashboard-renderer | disapproved verdict | - | 200 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.dashboard.anchoredScroll.50t | flatten + anchoredScrollOffset (50 tasks) | widgets/goal-dashboard-model | 50-task tree | - | 500 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.dashboard.viewport | deriveTaskListViewport (50 rows, 24 visible, offset 7) | widgets/goal-dashboard-model | viewport math | - | 2000 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.dashboard.statusText | buildGoalStatusText standard (20 tasks) | goal-status + widgets/goal-dashboard-* | 1 goal, 20 tasks, 12 events | - | 100 | 0 | 0.1 | 0.1 | 0ms | mean 0ms |
| B7.dashboard.deriveTasks | deriveTasksFromObjective (5 markers) | goal-task-derive | objective with 5 checklist markers | - | 500 | 0 | 0 | 0 | 0ms | mean 0ms |
| B7.dashboard.taskCount | countAllTasks (50 tasks) | goal-task-count | 50-task tree | - | 500 | 0 | 0 | 0 | 0ms | mean 0ms |
