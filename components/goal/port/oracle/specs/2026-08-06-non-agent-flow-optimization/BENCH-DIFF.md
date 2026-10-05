# Benchmark diff — before → after (naf, 2026-08-06T12:37:19.722Z)

Agent-free runs (B8), same machine. p50 ms unless noted (ops/tokens rows show their count). Rows ordered by
improvement ratio (lower is faster). The after value is the per-feature budget for the next campaign.

| id | before | after | ratio | label |
|---|---|---|---|---|
| B1.settings.present.lat25 | 33.4ms | 0ms | 0.00x | settings load (file present, +25ms/op) |
| B1.pool.1g.lat25 | 62.9ms | 0ms | 0.00x | pool scan (1 goal, +25ms/op) |
| B1.pool.10g | 0.3ms | 0ms | 0.00x | pool scan (10 goals) |
| B1.pool.10g.lat25 | 338.9ms | 0ms | 0.00x | pool scan (10 goals, +25ms/op) |
| B1.pool.50g | 0.7ms | 0ms | 0.00x | pool scan (50 goals) |
| B1.pool.50g.lat25 | 1600.5ms | 0ms | 0.00x | pool scan (50 goals, +25ms/op) |
| B1.ledger.1k.lat25 | 35.1ms | 0ms | 0.00x | ledger full parse (1k, +25ms/op) |
| B1.append.single | 0.2ms | 0ms | 0.00x | ledger append (single event) |
| B1.append.x4 | 0.7ms | 0ms | 0.00x | ledger append x4 (current one-by-one) |
| B3.reconstruct.1000 | 0.6ms | 0ms | 0.00x | ledger reconstruction (1000 events) |
| B2.readturn.10g | 0.1ms | 0ms | 0.00x | per-turn read pipeline (10 goals, 1k ledger) |
| B5.startup.1g | 0.2ms | 0ms | 0.00x | session startup loadState (1 goal, parallel reads) |
| B5.startup.1g.lat25 | 27.3ms | 0ms | 0.00x | session startup loadState (1 goal, +25ms/op) |
| B5.startup.10g | 0.5ms | 0ms | 0.00x | session startup loadState (10 goals, parallel reads) |
| B5.startup.10g.lat25 | 27.9ms | 0ms | 0.00x | session startup loadState (10 goals, +25ms/op) |
| B5.startup.50g | 2.4ms | 0ms | 0.00x | session startup loadState (50 goals, parallel reads) |
| B5.startup.50g.lat25 | 29.7ms | 0ms | 0.00x | session startup loadState (50 goals, +25ms/op) |
| B7.tool.get_goal | 0.1ms | 0ms | 0.00x | get_goal handler |
| B7.compaction.summary | 0.1ms | 0ms | 0.00x | buildCompactionSummary (500 events) |
| B7.ledger.reconstruct | 0.1ms | 0ms | 0.00x | reconstructGoalLedger (500 events) |
| B1.pool.cold.lat25 | 3199.2ms | 98.6ms | 0.03x | cold sync pool read (50 goals, +25ms/op) |
| B5.lock.contended | 245.6ms | 12.8ms | 0.05x | lock acquire under two-process contention (child holds 3s, DEFAULT bounds) |
| B3.reconstruct.5000 | 3.2ms | 0.3ms | 0.09x | ledger reconstruction (5000 events) |
| B3.reconstruct.10000 | 6.4ms | 0.6ms | 0.09x | ledger reconstruction (10000 events) |
| B1.pool.cold | 5.3ms | 0.9ms | 0.17x | cold sync pool read (50 goals, fresh process) |
| B5.startup.cold | 6.5ms | 2ms | 0.31x | cold session startup loadState (50 goals, fresh process) |
| B1.lock.uncontended | 0.2ms | 0.1ms | 0.50x | lock acquire+release (uncontended) |
| B5.auditor.dispatch | 1.7ms | 0.9ms | 0.53x | update_goal(complete) dispatch to pre-audit gate (auditor stubbed) |
| B1.settings.cold.lat25 | 63.7ms | 35.5ms | 0.56x | cold settings load (+25ms/op) |
| B1.ledger.cold.lat25 | 60.8ms | 36.5ms | 0.60x | cold ledger read (+25ms/op) |
| B5.startup.cold.lat25 | 184ms | 112.3ms | 0.61x | cold session startup loadState (50 goals, +25ms/op) |
| B1.settings.cold | 0.4ms | 0.3ms | 0.75x | cold settings load (fresh process) |
| B1.ledger.cold | 1.6ms | 1.4ms | 0.87x | cold ledger read (1k events, fresh process) |
| B4.goalPrompt.50t | 862 | 861 | 1.00x | goalPrompt (50-task tree) |
| B4.continuationPrompt.10t | 973 | 972 | 1.00x | continuationPrompt (10-task tree) |
| B4.continuationPrompt.50t | 1084 | 1083 | 1.00x | continuationPrompt (50-task tree) |
| B1.settings.present | 0ms | 0ms | 1.00x | settings load (file present) |
| B1.settings.missing | 0ms | 0ms | 1.00x | settings load (file missing) |
| B1.pool.1g | 0ms | 0ms | 1.00x | pool scan (1 goal) |
| B1.ledger.1k | 0ms | 0ms | 1.00x | ledger full parse (1k events) |
| B3.parse.1000 | 0ms | 0ms | 1.00x | ledger full parse (1000 events) |
| B3.parse.5000 | 0ms | 0ms | 1.00x | ledger full parse (5000 events) |
| B3.parse.10000 | 0ms | 0ms | 1.00x | ledger full parse (10000 events) |
| B2.readturn.1g | 0ms | 0ms | 1.00x | per-turn read pipeline (1 goal, 1k ledger) |
| B4.taskListBlock.10t | 277 | 277 | 1.00x | taskListBlock (10-task tree) |
| B4.goalPrompt.10t | 750 | 750 | 1.00x | goalPrompt (10-task tree) |
| B4.taskListBlock.50t | 388 | 388 | 1.00x | taskListBlock (50-task tree) |
| B7.tool.create_goal | 0.6ms | 0.6ms | 1.00x | create_goal handler |
| B7.evt.before_agent_start | 0ms | 0ms | 1.00x | before_agent_start event (1 goal, 1k ledger) |
| B7.render.confirmationTasks | 0ms | 0ms | 1.00x | renderConfirmationTasks (20 tasks) |
| B7.render.draftConfirmation | 0ms | 0ms | 1.00x | buildDraftConfirmationText |
| B7.render.questionnaireAnswers | 0ms | 0ms | 1.00x | formatQuestionnaireAnswers (3 Q&A) |
| B7.render.widgetLines | 0ms | 0ms | 1.00x | renderGoalWidgetLines (20 tasks) |
| B7.render.widgetComponent | 0ms | 0ms | 1.00x | GoalWidgetComponent.render (mock TUI) |
| B7.render.taskOverlay | 0ms | 0ms | 1.00x | task-list overlay render (20 tasks) |
| B7.render.escapeDialog | 0ms | 0ms | 1.00x | escape dialog render |
| B7.policy.taskSummary | 0ms | 0ms | 1.00x | buildTaskSummary (20 tasks) |
| B7.policy.validateTasks | 0ms | 0ms | 1.00x | validateTaskListProposal (50 flat tasks) |
| B7.policy.completionReport | 0ms | 0ms | 1.00x | buildCompletionReport |
| B7.pool.goalList | 0ms | 0ms | 1.00x | buildGoalListText (10 goals) |
| B7.pool.selectorLabel | 0ms | 0ms | 1.00x | goalSelectorLabel |
| B7.accounting.charge | 0ms | 0ms | 1.00x | GoalAccounting begin+charge+end |
| B7.accounting.budget | 0ms | 0ms | 1.00x | budgetLine + budgetRemaining |
| B7.notifications.running | 0ms | 0ms | 1.00x | buildGoalRunningNotification |
| B7.compaction.goalSummary | 0ms | 0ms | 1.00x | buildGoalCompactSummary (500 events) |
| B7.ledger.latestEvents | 0ms | 0ms | 1.00x | latestEventsForGoal (500 events) |
| B7.contract.extract | 0ms | 0ms | 1.00x | extractVerificationContract |
| B7.contract.promptSafe | 0ms | 0ms | 1.00x | promptSafeObjective |
| B7.record.normalize | 0ms | 0ms | 1.00x | normalizeGoalRecord |
| B7.files.serialize | 0ms | 0ms | 1.00x | serializeGoalFile |
| B7.files.parse | 0ms | 0ms | 1.00x | parseGoalFile |
| B7.format.renderEvent | 0ms | 0ms | 1.00x | renderGoalEvent heading |
| B7.format.goalDetails | 0ms | 0ms | 1.00x | goalDetails |
| B7.runtime.stalePrompt | 0ms | 0ms | 1.00x | staleContinuationPrompt |
| B7.runtime.unfocusedPrompt | 0ms | 0ms | 1.00x | unfocusedOpenGoalsPrompt |
| B7.dashboard.model.20t | 0ms | 0ms | 1.00x | deriveGoalDashboardModel (20 tasks, 12 events) |
| B7.dashboard.model.50t | 0ms | 0ms | 1.00x | deriveGoalDashboardModel (50 tasks, 12 events) |
| B7.dashboard.compact.20t | 0ms | 0ms | 1.00x | renderCompactDashboard (20 tasks) |
| B7.dashboard.expanded.20t | 0ms | 0ms | 1.00x | renderExpandedDashboard (20 tasks, 24 rows) |
| B7.dashboard.expanded.50t | 0ms | 0ms | 1.00x | renderExpandedDashboard (50 tasks, 24 rows) |
| B7.dashboard.currentTask | 0ms | 0ms | 1.00x | renderCurrentTaskBlock (current task) |
| B7.dashboard.activity | 0ms | 0ms | 1.00x | renderActivityBlock (8 items) |
| B7.dashboard.unfocused | 0ms | 0ms | 1.00x | renderUnfocusedDashboard (3 open goals) |
| B7.dashboard.auditor | 0ms | 0ms | 1.00x | deriveAuditorDashboardModel + renderAuditorDashboard (running) |
| B7.dashboard.auditCard | 0ms | 0ms | 1.00x | deriveAuditResultCard + renderAuditResultCard |
| B7.dashboard.anchoredScroll.50t | 0ms | 0ms | 1.00x | flatten + anchoredScrollOffset (50 tasks) |
| B7.dashboard.viewport | 0ms | 0ms | 1.00x | deriveTaskListViewport (50 rows, 24 visible, offset 7) |
| B7.dashboard.statusText | 0ms | 0ms | 1.00x | buildGoalStatusText standard (20 tasks) |
| B7.dashboard.deriveTasks | 0ms | 0ms | 1.00x | deriveTasksFromObjective (5 markers) |
| B7.dashboard.taskCount | 0ms | 0ms | 1.00x | countAllTasks (50 tasks) |
| B2.mutationturn.task | 1.2ms | 1.3ms | 1.08x | per-turn mutation (one update_goal_task) |
| B7.tool.set_goal_tasks.50 | 0.8ms | 0.9ms | 1.13x | set_goal_tasks (50 tasks) handler |
| B7.tool.update_goal.paused | 0.7ms | 0.8ms | 1.14x | update_goal(paused) handler |
| B7.tool.update_goal_task | 0.7ms | 0.8ms | 1.14x | update_goal_task(complete) handler |
| B7.tool.update_goal.blocked | 0.6ms | 0.7ms | 1.17x | update_goal(blocked) handler |

## Missing from either run

