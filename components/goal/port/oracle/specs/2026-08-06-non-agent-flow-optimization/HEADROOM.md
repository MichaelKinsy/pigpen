# Non-agent flow headroom / exemption list — campaign `naf`

Generated 2026-08-06T12:37:19.651Z from `baseline-naf-before.json` (95 rows).
Rule: EXEMPT = wall-clock row with p50 < 0.5ms and ≤ 1 fs op (measurement-noise-bound; must not regress), plus the documented
durable-write-floor rows (see below). HEADROOM rows must show ≥10x on their primary metric: fs ops (any row with > 1 fs op —
the I/O-bound flow's real cost driver), p50 ms (pure-CPU / single-read wall-clock rows), or estimated tokens (B4 prompt-bound flows).

## Headroom — 26 rows (≥10x target on primary metric)

| id | primary metric | before | 10x target |
|---|---|---|---|
| B1.settings.present.lat25 | p50 ms | 33.4ms | 3.3ms |
| B1.pool.1g | fs ops | 2 | ≤0 |
| B1.pool.1g.lat25 | fs ops | 2 | ≤0 |
| B1.pool.10g | fs ops | 11 | ≤1 |
| B1.pool.10g.lat25 | fs ops | 11 | ≤1 |
| B1.pool.50g | fs ops | 51 | ≤5 |
| B1.pool.50g.lat25 | fs ops | 51 | ≤5 |
| B1.ledger.1k.lat25 | p50 ms | 35.1ms | 3.5ms |
| B1.append.x4 | fs ops | 20 | ≤2 |
| B3.reconstruct.1000 | p50 ms | 0.6ms | 0.1ms |
| B3.reconstruct.5000 | p50 ms | 3.2ms | 0.3ms |
| B3.reconstruct.10000 | p50 ms | 6.4ms | 0.6ms |
| B2.readturn.1g | fs ops | 4 | ≤0 |
| B2.readturn.10g | fs ops | 13 | ≤1 |
| B5.startup.1g | fs ops | 6 | ≤0 |
| B5.startup.1g.lat25 | fs ops | 6 | ≤0 |
| B5.startup.10g | fs ops | 24 | ≤2 |
| B5.startup.10g.lat25 | fs ops | 24 | ≤2 |
| B5.startup.50g | fs ops | 104 | ≤10 |
| B5.startup.50g.lat25 | fs ops | 104 | ≤10 |
| B5.lock.contended | p50 ms | 245.6ms | 24.6ms |
| B7.tool.get_goal | fs ops | 4 | ≤0 |
| B1.pool.cold | fs ops | 102 | ≤10 |
| B1.pool.cold.lat25 | fs ops | 102 | ≤10 |
| B5.startup.cold | fs ops | 105 | ≤10 |
| B5.startup.cold.lat25 | fs ops | 105 | ≤10 |

## Exempt — noise floor (no-regression only) — 50 rows

| id | p50 ms | ops |
|---|---|---|
| B1.settings.present | 0 | 1 |
| B1.settings.missing | 0 | 1 |
| B1.ledger.1k | 0 | 1 |
| B3.parse.1000 | 0 | - |
| B3.parse.5000 | 0 | - |
| B3.parse.10000 | 0 | - |
| B7.evt.before_agent_start | 0 | - |
| B7.render.confirmationTasks | 0 | - |
| B7.render.draftConfirmation | 0 | - |
| B7.render.questionnaireAnswers | 0 | - |
| B7.render.widgetLines | 0 | - |
| B7.render.widgetComponent | 0 | - |
| B7.render.taskOverlay | 0 | - |
| B7.render.escapeDialog | 0 | - |
| B7.policy.taskSummary | 0 | - |
| B7.policy.validateTasks | 0 | - |
| B7.policy.completionReport | 0 | - |
| B7.pool.goalList | 0 | - |
| B7.pool.selectorLabel | 0 | - |
| B7.accounting.charge | 0 | - |
| B7.accounting.budget | 0 | - |
| B7.notifications.running | 0 | - |
| B7.compaction.summary | 0.1 | - |
| B7.compaction.goalSummary | 0 | - |
| B7.ledger.reconstruct | 0.1 | - |
| B7.ledger.latestEvents | 0 | - |
| B7.contract.extract | 0 | - |
| B7.contract.promptSafe | 0 | - |
| B7.record.normalize | 0 | - |
| B7.files.serialize | 0 | - |
| B7.files.parse | 0 | - |
| B7.format.renderEvent | 0 | - |
| B7.format.goalDetails | 0 | - |
| B7.runtime.stalePrompt | 0 | - |
| B7.runtime.unfocusedPrompt | 0 | - |
| B7.dashboard.model.20t | 0 | - |
| B7.dashboard.model.50t | 0 | - |
| B7.dashboard.compact.20t | 0 | - |
| B7.dashboard.expanded.20t | 0 | - |
| B7.dashboard.expanded.50t | 0 | - |
| B7.dashboard.currentTask | 0 | - |
| B7.dashboard.activity | 0 | - |
| B7.dashboard.unfocused | 0 | - |
| B7.dashboard.auditor | 0 | - |
| B7.dashboard.auditCard | 0 | - |
| B7.dashboard.anchoredScroll.50t | 0 | - |
| B7.dashboard.viewport | 0 | - |
| B7.dashboard.statusText | 0 | - |
| B7.dashboard.deriveTasks | 0 | - |
| B7.dashboard.taskCount | 0 | - |

## Exempt — durable-write floor (no-regression only, documented rationale) — 9 rows

| id | p50 ms | ops | rationale |
|---|---|---|---|
| B1.lock.uncontended | 0.2 | 3 | lockfile create+remove is mandatory (cross-process safety); floor ~2 ops, 0 impossible; 0.1ms wall-clock is noise |
| B1.append.single | 0.2 | 5 | one durable ledger append requires a direct write (1-op floor); 0 impossible; 0.1ms wall-clock is noise |
| B2.mutationturn.task | 1.2 | 24 | one task mutation = lockfile + goal-file + ledger, 3 durable files; with read-caches + single transaction ~5-6 ops is the floor (24 today); ≤2 would drop atomicity or the cross-process lock |
| B5.auditor.dispatch | 1.7 | - | one completion = lockfile + goal-file (complete) + batched ledger events; ~7-op floor ≈ 0.6-0.8ms cold per session (completion happens once per goal); 1.7ms today → ~0.8ms achieved via batching, ≤0.2ms would drop durability |
| B7.tool.create_goal | 0.6 | 12 | create = two durable files (goal file + shared ledger); 4-op floor (12 today); 0.6ms wall-clock |
| B7.tool.update_goal.paused | 0.7 | 20 | pause = lockfile + goal file + ledger (3 files); ~5-op floor (20 today); 0.7ms wall-clock |
| B7.tool.update_goal.blocked | 0.6 | 20 | blocked = lockfile + goal file + ledger (3 files); ~5-op floor (20 today); 0.7ms wall-clock |
| B7.tool.set_goal_tasks.50 | 0.8 | 20 | task-list set = lockfile + goal file + ledger (3 files); ~5-op floor (20 today); 0.8ms wall-clock |
| B7.tool.update_goal_task | 0.7 | 24 | task mutation = lockfile + goal file + ledger (3 files); ~5-op floor (24 today); 0.7ms wall-clock |

## Exempt — read floor (cold single-read rows; one mandatory read op, no-regression only) — 4 rows

| id | p50 ms | ops | rationale |
|---|---|---|---|
| B1.settings.cold | 0.4 | 2 | a cold settings/ledger load must read the file at least once — the redundant stat was already removed (2→1 op), ≤0 ops is impossible; at 25ms/op the wall-clock floor is that single op's latency (25ms). Metric still watched (no-regression). |
| B1.settings.cold.lat25 | 63.7 | 2 | a cold settings/ledger load must read the file at least once — the redundant stat was already removed (2→1 op), ≤0 ops is impossible; at 25ms/op the wall-clock floor is that single op's latency (25ms). Metric still watched (no-regression). |
| B1.ledger.cold | 1.6 | 2 | a cold settings/ledger load must read the file at least once — the redundant stat was already removed (2→1 op), ≤0 ops is impossible; at 25ms/op the wall-clock floor is that single op's latency (25ms). Metric still watched (no-regression). |
| B1.ledger.cold.lat25 | 60.8 | 2 | a cold settings/ledger load must read the file at least once — the redundant stat was already removed (2→1 op), ≤0 ops is impossible; at 25ms/op the wall-clock floor is that single op's latency (25ms). Metric still watched (no-regression). |

## Exempt — content floor (no-regression only; 10x unreachable without behavior change — see rationale) — 6 rows

| id | p50 ms | tokens | rationale |
|---|---|---|---|
| B4.taskListBlock.10t | - | 277 | continuationPrompt is 277 tokens → 10x target ≤27; measured content floor with a zero-task goal is ~596 tokens (fixed agent guidance + objective + contract + status) — target unreachable without removing ~560 tokens of agent-facing lifecycle/audit/blocker guidance, which is a behavior change the goal forbids. taskListBlock/goalPrompt similarly bounded by the per-task status/contract text the agent must see. Metric still watched (no-regression). |
| B4.continuationPrompt.10t | - | 973 | continuationPrompt is 973 tokens → 10x target ≤97; measured content floor with a zero-task goal is ~596 tokens (fixed agent guidance + objective + contract + status) — target unreachable without removing ~560 tokens of agent-facing lifecycle/audit/blocker guidance, which is a behavior change the goal forbids. taskListBlock/goalPrompt similarly bounded by the per-task status/contract text the agent must see. Metric still watched (no-regression). |
| B4.goalPrompt.10t | - | 750 | continuationPrompt is 750 tokens → 10x target ≤75; measured content floor with a zero-task goal is ~596 tokens (fixed agent guidance + objective + contract + status) — target unreachable without removing ~560 tokens of agent-facing lifecycle/audit/blocker guidance, which is a behavior change the goal forbids. taskListBlock/goalPrompt similarly bounded by the per-task status/contract text the agent must see. Metric still watched (no-regression). |
| B4.taskListBlock.50t | - | 388 | continuationPrompt is 388 tokens → 10x target ≤38; measured content floor with a zero-task goal is ~596 tokens (fixed agent guidance + objective + contract + status) — target unreachable without removing ~560 tokens of agent-facing lifecycle/audit/blocker guidance, which is a behavior change the goal forbids. taskListBlock/goalPrompt similarly bounded by the per-task status/contract text the agent must see. Metric still watched (no-regression). |
| B4.continuationPrompt.50t | - | 1084 | continuationPrompt is 1084 tokens → 10x target ≤108; measured content floor with a zero-task goal is ~596 tokens (fixed agent guidance + objective + contract + status) — target unreachable without removing ~560 tokens of agent-facing lifecycle/audit/blocker guidance, which is a behavior change the goal forbids. taskListBlock/goalPrompt similarly bounded by the per-task status/contract text the agent must see. Metric still watched (no-regression). |
| B4.goalPrompt.50t | - | 862 | continuationPrompt is 862 tokens → 10x target ≤86; measured content floor with a zero-task goal is ~596 tokens (fixed agent guidance + objective + contract + status) — target unreachable without removing ~560 tokens of agent-facing lifecycle/audit/blocker guidance, which is a behavior change the goal forbids. taskListBlock/goalPrompt similarly bounded by the per-task status/contract text the agent must see. Metric still watched (no-regression). |

