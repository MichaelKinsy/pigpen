/**
 * Dashboard model tests (plan §19.1): pure derivation of the unified
 * dashboard view model from persisted goal state and the durable ledger.
 *
 * The model module must stay free of TUI imports; these tests exercise the
 * derivation rules only (status codes, top-level progress, tree flattening,
 * current-task resolution, subtask progress, budget, activity, formatting).
 */

import assert from "node:assert/strict";
import test from "node:test";

import type { GoalLedgerEvent } from "../extensions/goal-ledger.ts";
import type { GoalRecord, GoalTask } from "../extensions/goal-record.ts";
import {
	anchoredScrollOffset,
	clampScrollOffset,
	compactTaskViewportRows,
	deriveCurrentTask,
	deriveCurrentTaskSubtaskProgress,
	deriveGoalDashboardModel,
	deriveGoalStatus,
	deriveTaskListViewport,
	deriveTopLevelTaskProgress,
	flattenTaskTree,
	formatBudget,
	formatCompactTokens,
	formatDashboardDuration,
	latestCompletedNodeIndex,
	maxScrollOffset,
	taskViewportPageSize,
	type DashboardTaskNode,
} from "../extensions/widgets/goal-dashboard-model.ts";

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

function task(id: string, title: string, overrides: Partial<GoalTask> = {}): GoalTask {
	return { id, title, status: "pending", ...overrides };
}

function goal(overrides: Partial<GoalRecord> = {}): GoalRecord {
	return {
		id: "g1",
		objective: "Add CSV export to reports",
		status: "active",
		autoContinue: true,
		usage: { tokensUsed: 18200, activeSeconds: 767 },
		sisyphus: false,
		createdAt: "2026-01-01T00:00:00.000Z",
		updatedAt: "2026-01-01T00:00:00.000Z",
		...overrides,
	};
}

function model(
	overrides: Partial<GoalRecord> = {},
	opts: Partial<{ focused: boolean; otherOpenGoals: number; ledgerEvents: GoalLedgerEvent[]; activityLimit: number }> = {},
) {
	const m = deriveGoalDashboardModel(goal(overrides), { focused: true, otherOpenGoals: 0, ...opts });
	if (!m) throw new Error("deriveGoalDashboardModel returned null for a non-null goal");
	return m;
}

/** Standard five-top-level-task tree from the plan's examples. */
function fiveTaskList(): GoalTask[] {
	return [
		task("t1", "Review reports page and data source", { status: "complete", evidence: "Reviewed source" }),
		task("t2", "Implement filtered CSV export", { status: "complete" }),
		task("t3", "Add the download button", {
			verificationContract: "The button downloads a CSV using the active filters.",
			subtasks: [
				task("t3.1", "Add loading state", { status: "complete", evidence: "Loading state added" }),
				task("t3.2", "Generate timestamped filename", { status: "complete" }),
				task("t3.3", "Add error handling"),
			],
		}),
		task("t4", "Add documentation"),
		task("t5", "Add and run tests"),
	];
}

function withTasks(taskList: GoalTask[]): Partial<GoalRecord> {
	return { taskList: { tasks: taskList, blockCompletion: false, proposedAt: "2026-01-01T00:00:00.000Z" } };
}

function ev(type: string, at: string, extra: Record<string, unknown> = {}): GoalLedgerEvent {
	return { type, goalId: "g1", at, ...extra } as unknown as GoalLedgerEvent;
}

// ---------------------------------------------------------------------------
// Formatting helpers
// ---------------------------------------------------------------------------

test("formatDashboardDuration renders compact h/m/s labels", () => {
	assert.equal(formatDashboardDuration(0), "0s");
	assert.equal(formatDashboardDuration(767), "12m47s");
	assert.equal(formatDashboardDuration(3661), "1h01m01s");
});

test("formatCompactTokens renders compact token labels", () => {
	assert.equal(formatCompactTokens(0), "0");
	assert.equal(formatCompactTokens(999), "999");
	assert.equal(formatCompactTokens(1200), "1.2K");
	assert.equal(formatCompactTokens(18200), "18.2K");
	assert.equal(formatCompactTokens(2_500_000), "2.5M");
});

test("formatBudget summarizes used/total/percentage", () => {
	assert.equal(formatBudget(18200, 50000), "18.2K / 50K · 36%");
	assert.equal(formatBudget(0, 0), "0 / 0 · 0%");
});

// ---------------------------------------------------------------------------
// Status derivation
// ---------------------------------------------------------------------------

test("auditorEnabled derives from the goal's persisted skipAuditor (§auditor-toggle)", () => {
	// Unset/absent → on (the global default); skipAuditor: true → off.
	assert.equal(model().auditorEnabled, true, "no per-goal setting keeps the auditor on");
	assert.equal(model({ skipAuditor: true }).auditorEnabled, false, "skipAuditor: true turns the auditor off");
});

test("status maps lifecycle states to explicit display codes", () => {
	assert.deepEqual(deriveGoalStatus(goal()), { code: "running", label: "In progress", footerLabel: "running" });
	assert.deepEqual(deriveGoalStatus(goal({ autoContinue: false })), { code: "idle", label: "Idle", footerLabel: "active" });
	assert.deepEqual(deriveGoalStatus(goal({ status: "paused", stopReason: "agent" })), { code: "paused", label: "Paused (agent)", footerLabel: "paused (agent)" });
	assert.deepEqual(deriveGoalStatus(goal({ status: "paused", stopReason: "user" })), { code: "paused", label: "Paused (user)", footerLabel: "paused" });
	assert.deepEqual(
		deriveGoalStatus(goal({ status: "blocked", pauseReason: "Build fails", pauseSuggestedAction: "Run npm test" })),
		{ code: "blocked", label: "Blocked", footerLabel: "blocked", reason: "Build fails", suggestedAction: "Run npm test" },
	);
	assert.deepEqual(deriveGoalStatus(goal({ status: "budget_limited" })), { code: "budget_limited", label: "Budget limited", footerLabel: "budget limited" });
	assert.deepEqual(deriveGoalStatus(goal({ status: "complete" })), { code: "complete", label: "Complete", footerLabel: "complete" });
});

// ---------------------------------------------------------------------------
// Top-level progress (§9.1)
// ---------------------------------------------------------------------------

test("active goal without tasks has no progress sections", () => {
	const m = model();
	assert.equal(m.title, "Add CSV export to reports");
	assert.equal(m.status.code, "running");
	assert.equal(m.taskProgress, undefined);
	assert.deepEqual(m.taskTree, []);
	assert.equal(m.currentTask, undefined);
});

test("partial tasks derive 3/5 · 60% with skipped counted as done", () => {
	const tasks = fiveTaskList();
	tasks.push(task("t6", "Legacy fallback", { status: "skipped" }));
	const m = model(withTasks(tasks));
	assert.deepEqual(m.taskProgress, { completed: 3, total: 6, percentage: 50 });
	// skipped is tracked separately in the tree but counts toward progress
	const skipped = m.taskTree.find((n) => n.id === "t6");
	assert.equal(skipped?.status, "skipped");
});

test("all top-level tasks complete derive 100% and no current task", () => {
	const tasks = fiveTaskList().map((t) => ({
		...t,
		status: "complete" as const,
		subtasks: t.subtasks?.map((s) => ({ ...s, status: "complete" as const })),
	}));
	const m = model(withTasks(tasks));
	assert.deepEqual(m.taskProgress, { completed: 5, total: 5, percentage: 100 });
	assert.equal(m.currentTask, undefined);
});

test("top-level progress counts only top-level tasks", () => {
	const tasks = [task("a", "A", { status: "complete" }), task("b", "B", { subtasks: [task("b.1", "B1", { status: "complete" })] })];
	assert.deepEqual(deriveTopLevelTaskProgress(goal(withTasks(tasks))), { completed: 1, total: 2, percentage: 50 });
});

// ---------------------------------------------------------------------------
// Task tree flattening (§9.2)
// ---------------------------------------------------------------------------

test("flattenTaskTree walks the tree recursively with depth and current marker", () => {
	const nodes = flattenTaskTree(fiveTaskList(), "t3.3");
	const ids = nodes.map((n) => n.id);
	assert.deepEqual(ids, ["t1", "t2", "t3", "t3.1", "t3.2", "t3.3", "t4", "t5"]);
	assert.equal(nodes.find((n) => n.id === "t3.3")?.depth, 1);
	assert.equal(nodes.find((n) => n.id === "t3")?.depth, 0);
	assert.equal(nodes.find((n) => n.id === "t3.3")?.isCurrent, true);
	assert.equal(nodes.filter((n) => n.isCurrent).length, 1);
});

test("flattenTaskTree with no tasks returns an empty tree", () => {
	assert.deepEqual(flattenTaskTree(undefined), []);
	assert.deepEqual(flattenTaskTree([]), []);
});

test("tree nodes carry verification contracts and evidence", () => {
	const nodes = flattenTaskTree(fiveTaskList());
	const t1 = nodes.find((n) => n.id === "t1");
	const t3 = nodes.find((n) => n.id === "t3");
	assert.equal(t1?.evidence, "Reviewed source");
	assert.equal(t3?.verificationContract, "The button downloads a CSV using the active filters.");
});

test("tree nodes carry direct-child subtask counts for the compact marker", () => {
	const nodes = flattenTaskTree(fiveTaskList());
	const t3 = nodes.find((n) => n.id === "t3");
	// t3 has three direct children, two complete → 2/3; skipped counts as done.
	assert.equal(t3?.totalSubtasks, 3);
	assert.equal(t3?.completedSubtasks, 2);
	// Leaves carry zero counts.
	for (const id of ["t1", "t2", "t4", "t5"]) {
		assert.equal(nodes.find((n) => n.id === id)?.totalSubtasks, 0);
		assert.equal(nodes.find((n) => n.id === id)?.completedSubtasks, 0);
	}
	// Subtask nodes are leaves too.
	assert.equal(nodes.find((n) => n.id === "t3.1")?.totalSubtasks, 0);
});

test("tree node subtask counts count skipped children as done", () => {
	const tasks = [
		task("a", "A", {
			subtasks: [
				task("a.1", "A1", { status: "complete" }),
				task("a.2", "A2", { status: "skipped", skipReason: "Covered" }),
				task("a.3", "A3"),
			],
		}),
	];
	const node = flattenTaskTree(tasks)[0]!;
	assert.equal(node.totalSubtasks, 3);
	assert.equal(node.completedSubtasks, 2);
});

// ---------------------------------------------------------------------------
// Task-list viewport / scroll (§9.6)
// ---------------------------------------------------------------------------

function nodesWithCompletions(completed: Array<[string, string]>): DashboardTaskNode[] {
	const ids = ["t1", "t2", "t3", "t4", "t5", "t6", "t7"];
	return ids.map((id, i) => {
		const entry = completed.find(([tid]) => tid === id);
		return {
			id,
			title: `Task ${id}`,
			status: entry ? ("complete" as const) : ("pending" as const),
			depth: 0,
			isCurrent: false,
			totalSubtasks: 0,
			completedSubtasks: 0,
			...(entry ? { completedAt: entry[1] } : {}),
		};
	});
}

test("latestCompletedNodeIndex picks the max completedAt and ties resolve to the last in plan order", () => {
	const nodes = nodesWithCompletions([
		["t1", "2026-01-01T10:00:00.000Z"],
		["t2", "2026-01-01T11:00:00.000Z"],
		["t5", "2026-01-01T09:00:00.000Z"],
	]);
	assert.equal(latestCompletedNodeIndex(nodes), 1); // t2
	const tied = nodesWithCompletions([
		["t2", "2026-01-01T11:00:00.000Z"],
		["t5", "2026-01-01T11:00:00.000Z"],
	]);
	assert.equal(latestCompletedNodeIndex(tied), 4); // t5 wins the tie (last position)
});

test("latestCompletedNodeIndex returns -1 without completed/timestamped tasks", () => {
	assert.equal(latestCompletedNodeIndex([]), -1);
	assert.equal(latestCompletedNodeIndex(nodesWithCompletions([])), -1);
	// status complete but no completedAt (legacy) does not qualify
	const legacy = [{ id: "t1", title: "A", status: "complete" as const, depth: 0, isCurrent: false, totalSubtasks: 0, completedSubtasks: 0 }];
	assert.equal(latestCompletedNodeIndex(legacy), -1);
	// skipped tasks never qualify
	const skipped = [
		{ id: "t1", title: "A", status: "skipped" as const, depth: 0, isCurrent: false, completedAt: "2026-01-01T10:00:00.000Z", totalSubtasks: 0, completedSubtasks: 0 },
	];
	assert.equal(latestCompletedNodeIndex(skipped), -1);
});

test("anchoredScrollOffset bottom-anchors the latest completion as the last visible row", () => {
	// t5 completed last (index 4); a 3-row window ends at t5 → offset 2
	const nodes = nodesWithCompletions([
		["t1", "2026-01-01T10:00:00.000Z"],
		["t2", "2026-01-01T11:00:00.000Z"],
		["t5", "2026-01-01T12:00:00.000Z"],
	]);
	assert.equal(anchoredScrollOffset(nodes, 3), 2); // rows t3..t5, t5 at the bottom
	assert.equal(anchoredScrollOffset(nodes, 5), 0); // t5 already the bottom row of the initial window
	// anchor near the end clamps to the tail
	const tail = nodesWithCompletions([["t6", "2026-01-01T12:00:00.000Z"]]);
	assert.equal(anchoredScrollOffset(tail, 3), 3); // rows t4..t6, t6 at the bottom
	// early completion with everything else pending stays at the top
	const early = nodesWithCompletions([["t2", "2026-01-01T10:00:00.000Z"]]);
	assert.equal(anchoredScrollOffset(early, 5), 0);
	// no completions → top
	assert.equal(anchoredScrollOffset(nodesWithCompletions([]), 3), 0);
	// list fits entirely → top
	assert.equal(anchoredScrollOffset(nodesWithCompletions([["t3", "2026-01-01T10:00:00.000Z"]]), 7), 0);
	// long list: the most recent completion (t12) pulls the window to the tail,
	// hiding the earliest tasks — the core ask
	const longList: DashboardTaskNode[] = Array.from({ length: 12 }, (_, i) => ({
		id: `t${i + 1}`,
		title: `Task ${i + 1}`,
		status: "pending" as const,
		depth: 0,
		isCurrent: false,
		totalSubtasks: 0,
		completedSubtasks: 0,
	}));
	longList[1] = { ...longList[1]!, status: "complete", completedAt: "2026-01-01T10:00:00.000Z" };
	longList[11] = { ...longList[11]!, status: "complete", completedAt: "2026-01-01T11:00:00.000Z" };
	assert.equal(anchoredScrollOffset(longList, 5), 7); // rows t8..t12
});

test("clampScrollOffset and maxScrollOffset bound the window", () => {
	assert.equal(maxScrollOffset(7, 5), 2);
	assert.equal(maxScrollOffset(3, 5), 0);
	assert.equal(clampScrollOffset(-3, 7, 5), 0);
	assert.equal(clampScrollOffset(99, 7, 5), 2);
	assert.equal(clampScrollOffset(1.9, 7, 5), 1);
	assert.equal(clampScrollOffset(2, 3, 5), 0);
});

test("compactTaskViewportRows matches the §5.5 width buckets", () => {
	assert.equal(compactTaskViewportRows(140), 5);
	assert.equal(compactTaskViewportRows(100), 5);
	assert.equal(compactTaskViewportRows(99), 4);
	assert.equal(compactTaskViewportRows(70), 4);
	assert.equal(compactTaskViewportRows(69), 3);
	assert.equal(compactTaskViewportRows(50), 3);
	assert.equal(compactTaskViewportRows(49), 2);
	assert.equal(compactTaskViewportRows(40), 2);
});

test("taskViewportPageSize is one viewport of rows", () => {
	assert.equal(taskViewportPageSize(5), 5);
	assert.equal(taskViewportPageSize(1), 1);
	assert.equal(taskViewportPageSize(0), 1);
});

test("deriveTaskListViewport computes the window with hidden counts", () => {
	const v = deriveTaskListViewport(9, 5, 2);
	assert.deepEqual(v, { totalRows: 9, rows: 5, offset: 2, maxOffset: 4, hiddenAbove: 2, hiddenBelow: 2 });
	assert.deepEqual(deriveTaskListViewport(9, 5, 99), { totalRows: 9, rows: 5, offset: 4, maxOffset: 4, hiddenAbove: 4, hiddenBelow: 0 });
	assert.deepEqual(deriveTaskListViewport(4, 5, 3), { totalRows: 4, rows: 5, offset: 0, maxOffset: 0, hiddenAbove: 0, hiddenBelow: 0 });
	assert.deepEqual(deriveTaskListViewport(0, 5, 0), { totalRows: 0, rows: 5, offset: 0, maxOffset: 0, hiddenAbove: 0, hiddenBelow: 0 });
});

// ---------------------------------------------------------------------------
// Current task resolution (§7.2, §7.4, §9.3)
// ---------------------------------------------------------------------------

test("persisted current top-level task is resolved without inference", () => {
	const m = model({ ...withTasks(fiveTaskList()), currentTaskId: "t3" });
	assert.equal(m.currentTask?.id, "t3");
	assert.equal(m.currentTask?.title, "Add the download button");
	assert.equal(m.currentTask?.depth, 0);
	assert.equal(m.currentTask?.inferred, undefined);
	const t3 = m.taskTree.find((n) => n.id === "t3");
	assert.equal(t3?.isCurrent, true);
});

test("current nested subtask is resolved with its depth", () => {
	const m = model({ ...withTasks(fiveTaskList()), currentTaskId: "t3.3" });
	assert.equal(m.currentTask?.id, "t3.3");
	assert.equal(m.currentTask?.depth, 1);
	assert.equal(m.taskTree.find((n) => n.id === "t3.3")?.isCurrent, true);
});

test("current parent task shows direct-child subtask progress", () => {
	const m = model({ ...withTasks(fiveTaskList()), currentTaskId: "t3" });
	assert.deepEqual(
		{
			completed: m.currentTask?.completedSubtasks,
			total: m.currentTask?.totalSubtasks,
			pct: m.currentTask?.subtaskPercentage,
		},
		{ completed: 2, total: 3, pct: 67 },
	);
});

test("current leaf task omits subtask progress (all-zero totals)", () => {
	const m = model({ ...withTasks(fiveTaskList()), currentTaskId: "t4" });
	assert.equal(m.currentTask?.totalSubtasks, 0);
	assert.equal(m.currentTask?.subtaskPercentage, 0);
});

test("invalid currentTaskId falls back to the first pending task and is marked inferred", () => {
	const m = model({ ...withTasks(fiveTaskList()), currentTaskId: "t999" });
	assert.equal(m.currentTask?.inferred, true);
	assert.equal(m.currentTask?.id, "t3"); // first pending in tree order
	assert.equal(m.currentTask?.completedSubtasks, 2); // subtask progress still derives
});

test("removed current task falls back to first pending", () => {
	const tasks = [task("a", "A", { status: "complete" }), task("b", "B")];
	const m = model({ ...withTasks(tasks), currentTaskId: "vanished" });
	assert.equal(m.currentTask?.id, "b");
	assert.equal(m.currentTask?.inferred, true);
});

test("currentTaskId pointing at a completed task is not accepted", () => {
	const tasks = [task("a", "A", { status: "complete" }), task("b", "B")];
	const m = model({ ...withTasks(tasks), currentTaskId: "a" });
	assert.equal(m.currentTask?.id, "b");
});

test("deriveCurrentTask returns undefined when nothing is pending", () => {
	const tasks = [task("a", "A", { status: "complete" })];
	const nodes: DashboardTaskNode[] = flattenTaskTree(tasks);
	assert.equal(deriveCurrentTask(goal(withTasks(tasks)), nodes), undefined);
});

// ---------------------------------------------------------------------------
// Subtask progress rule (§9.3)
// ---------------------------------------------------------------------------

test("deriveCurrentTaskSubtaskProgress uses direct children of the parent", () => {
	const tasks = fiveTaskList();
	const progress = deriveCurrentTaskSubtaskProgress({ id: "t3" }, tasks);
	assert.deepEqual(progress, { completedSubtasks: 2, totalSubtasks: 3, subtaskPercentage: 67 });
});

test("deriveCurrentTaskSubtaskProgress omits the ratio for a leaf", () => {
	const tasks = fiveTaskList();
	assert.deepEqual(deriveCurrentTaskSubtaskProgress({ id: "t4" }, tasks), { completedSubtasks: 0, totalSubtasks: 0, subtaskPercentage: 0 });
});

// ---------------------------------------------------------------------------
// Verification visibility (§11)
// ---------------------------------------------------------------------------

test("goal-level and task-level verification contracts are surfaced", () => {
	const m = model({
		...withTasks(fiveTaskList()),
		verificationContract: "Run npm test with zero failures.",
		currentTaskId: "t3",
	});
	assert.equal(m.goalVerificationContract, "Run npm test with zero failures.");
	assert.equal(m.currentTask?.verificationContract, "The button downloads a CSV using the active filters.");
});

// ---------------------------------------------------------------------------
// Open goals / focus / path
// ---------------------------------------------------------------------------

test("other open goals and focus state are reflected", () => {
	const m = model(withTasks(fiveTaskList()), { focused: false, otherOpenGoals: 2 });
	assert.equal(m.focused, false);
	assert.equal(m.otherOpenGoals, 2);
});

test("filePath prefers the active path and falls back to the archive path", () => {
	const m = model({ activePath: ".pi/goals/active_goal_g1.md" });
	assert.equal(m.filePath, ".pi/goals/active_goal_g1.md");
	const archived = model({ status: "complete", activePath: undefined, archivedPath: ".pi/goals/archived/goal_g1.md" });
	assert.equal(archived.filePath, ".pi/goals/archived/goal_g1.md");
});

test("no goal record derives a null model", () => {
	assert.equal(deriveGoalDashboardModel(null, { focused: false, otherOpenGoals: 0 }), null);
});

// ---------------------------------------------------------------------------
// Token budget
// ---------------------------------------------------------------------------

test("token budget derives used/total/percentage/remaining", () => {
	const m = model({ tokenBudget: 50000 });
	assert.deepEqual(m.budget, { used: 18200, total: 50000, percentage: 36, remaining: 31800 });
});

test("budget percentage clamps to 100 and remaining to zero", () => {
	const m = model({ tokenBudget: 10000 });
	assert.deepEqual(m.budget, { used: 18200, total: 10000, percentage: 100, remaining: 0 });
});

test("no budget means no budget section", () => {
	assert.equal(model().budget, undefined);
});

// ---------------------------------------------------------------------------
// Usage labels
// ---------------------------------------------------------------------------

test("usage derives footer-status bits (compact duration + compact tokens)", () => {
	const m = model();
	assert.equal(m.usage.activeSeconds, 767);
	assert.equal(m.usage.tokens, 18200);
	assert.equal(m.usage.footerBits, "12m47s 18.2K");
});

// ---------------------------------------------------------------------------
// Activity (§12 via the model)
// ---------------------------------------------------------------------------

test("recent activity is derived from the durable ledger", () => {
	const events = [
		ev("goal_created", "2026-01-01T09:00:00.000Z", { objective: "x", sisyphus: false, autoContinue: true }),
		ev("task_complete", "2026-01-01T09:05:00.000Z", { taskId: "t2", evidence: "Done" }),
		ev("task_started", "2026-01-01T09:06:00.000Z", { taskId: "t3" }),
	];
	const m = model(withTasks(fiveTaskList()), { ledgerEvents: events });
	assert.deepEqual(
		m.recentActivity.map((a) => a.text),
		["Created and focused the goal.", "Completed “Implement filtered CSV export”. — Done", "Started “Add the download button”."],
	);
});

test("activity respects the configured limit and excludes other goals", () => {
	const events = [
		ev("goal_created", "2026-01-01T09:00:00.000Z", { objective: "x", sisyphus: false, autoContinue: true }),
		ev("task_complete", "2026-01-01T09:05:00.000Z", { taskId: "t1" }),
		ev("task_complete", "2026-01-01T09:06:00.000Z", { taskId: "t2" }),
		{ ...ev("goal_created", "2026-01-01T09:01:00.000Z", { objective: "other", sisyphus: false, autoContinue: true }), goalId: "g2" },
	];
	const m = model(withTasks(fiveTaskList()), { ledgerEvents: events, activityLimit: 2 });
	assert.deepEqual(
		m.recentActivity.map((a) => a.text),
		["Completed “Review reports page and data source”.", "Completed “Implement filtered CSV export”."],
	);
});
