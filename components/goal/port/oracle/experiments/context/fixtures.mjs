/**
 * Deterministic fixtures for the model-context accounting harness (PR D).
 *
 * Every fixture produces a complete scenario: goal record(s), ledger events,
 * the trigger prompt, and the raw pre-hook message list. Fixed timestamps,
 * fixed ids — no Date.now() enters captured output.
 */

import { createGoal, safeIdPart } from "../../extensions/goal-record.ts";

const T0 = Date.UTC(2026, 7, 23, 12, 0, 0);

function iso(offsetSeconds) {
	return new Date(T0 + offsetSeconds * 1000).toISOString();
}

/** Deterministic goal: fixed id derived from a seed. */
function stableGoal(seed, config) {
	const goal = createGoal(config, T0);
	goal.id = `ctx-${safeIdPart(seed)}`;
	return goal;
}

function taskTree(count, { contracted = false } = {}) {
	const tasks = [];
	for (let i = 0; i < count; i += 1) {
		const task = {
			id: `t${i}`,
			title: `Task ${i}: implement the sub-feature with a reasonably descriptive title that exercises wrapping`,
			status: i < count / 2 ? "complete" : "pending",
		};
		if (i < count / 2) task.evidence = `verified by test run ${i} with assertions passing`;
		else if (contracted && i === count / 2) task.verificationContract = "Run the suite and confirm green with a grep check.";
		if (count >= 10 && i % 10 === 9 && i !== count - 1) {
			task.subtasks = [
				{ id: `${task.id}-a`, title: `Subtask A of ${task.id}`, status: "complete", evidence: "done" },
				{ id: `${task.id}-b`, title: `Subtask B of ${task.id}`, status: "pending" },
			];
		}
		tasks.push(task);
	}
	return tasks;
}

function list(tasks) {
	return { tasks, blockCompletion: true, proposedAt: iso(60) };
}

function goalWith(id, overrides) {
	const goal = stableGoal(id, { objective: overrides.objective ?? "Base objective", autoContinue: true, sisyphus: false });
	for (const [key, value] of Object.entries(overrides)) {
		if (key !== "objective") goal[key] = value;
	}
	return goal;
}

export const FIXTURES = {
	"active-regular-no-tasks": () => ({
		goal: stableGoal("no-tasks", { objective: "Write the migration guide page.", autoContinue: true, sisyphus: false }),
		trigger: "continue",
	}),
	"active-regular-10-tasks": () => ({
		goal: goalWith("tasks-10", { objective: "Ship the ten-part refactor.", taskList: list(taskTree(10)) }),
		trigger: "continue",
	}),
	"active-regular-50-tasks": () => ({
		goal: goalWith("tasks-50", { objective: "Ship the fifty-task program.", taskList: list(taskTree(50)) }),
		trigger: "continue",
	}),
	"current-contracted-task": () => ({
		goal: goalWith("contracted-task", {
			objective: "Finish the contracted migration.",
			taskList: list(taskTree(6, { contracted: true })),
			currentTaskId: "t3",
			verificationContract: "npm test passes with zero failures; grep shows no legacy imports.",
		}),
		trigger: "continue",
	}),
	"nested-tasks": () => ({
		goal: goalWith("nested-tasks", { objective: "Orchestrate nested work.", taskList: list(taskTree(20)) }),
		trigger: "continue",
	}),
	"half-complete-tree": () => ({
		goal: goalWith("half-complete", { objective: "Half-done checklist execution.", taskList: list(taskTree(8)) }),
		trigger: "continue",
	}),
	"long-objective": () => ({
		// Unique numbered clauses: realistic long objective without periodic
		// substrings (which make occurrence-counting ambiguous).
		goal: stableGoal("long-objective", { objective: `${Array.from({ length: 120 }, (_, i) => `Requirement paragraph ${i}: satisfy specific acceptance clause ${i}.`).join(" ")} End of requirements.`, autoContinue: true, sisyphus: false }),
		trigger: "continue",
	}),
	"sisyphus-ordered-objective": () => ({
		goal: stableGoal("sisyphus", { objective: "1. Draft spec\n2. Implement\n3. Validate", autoContinue: true, sisyphus: true }),
		trigger: "continue",
	}),
	"token-budget-zero-percent": () => budgetGoal("budget-zero", 100_000, 0),
	"token-budget-seventy-five-percent": () => budgetGoal("budget-75pct", 100_000, 75_000),
	"token-budget-at-limit": () => budgetGoal("budget-limit", 10_000, 10_000),
	"paused-goal": () => pausedGoal(),
	"blocked-goal": () => blockedGoal(),
	"unfocused-multiple-goals": () => {
		const extraOpenGoals = [
			stableGoal("unfocused-a", { objective: "First open but unfocused goal awaiting the user.", autoContinue: false, sisyphus: false }),
			stableGoal("unfocused-b", { objective: "Second open but unfocused goal awaiting the user.", autoContinue: false, sisyphus: false }),
		];
		for (const g of extraOpenGoals) g.status = "paused";
		return { goal: null, focusNull: true, extraOpenGoals, trigger: "what goals exist?" };
	},
	"latest-auditor-rejection": () => auditorRejectionFixture(),
	"post-compaction-turn": () => postCompactionFixture(),
	"stale-checkpoint": () => staleCheckpointFixture(),
	"guided-drafting-question": () => draftingFixture("drafting-question", "question"),
	"guided-proposal": () => draftingFixture("drafting-proposal", "proposal"),
	"completion-audit": () => auditFixture("completion-audit", "running"),
	"audit-rejection-and-rework": () => auditFixture("audit-rework", "rejected"),
	"oracle-consultation": () => ({goal: goalWith("oracle", {objective: "Resolve the missing dependency."}), trigger: "continue"}),
 "tasks-disabled": () => ({goal: goalWith("disabled", {objective: "Work without tracked tasks."}), settings: {disableTasks: true}, trigger: "continue"}),
 "get-goal-default-and-verbose": () => getGoalFixture(),
};

// ── scenario helpers ───────────────────────────────────────────────────────

function budgetGoal(seed, budget, used) {
	const goal = stableGoal(seed, { objective: `Stay within a ${budget}-token budget.`, autoContinue: true, sisyphus: false });
	goal.tokenBudget = budget;
	goal.usage.tokensUsed = used;
	goal.status = used >= budget ? "budget_limited" : "active";
	return { goal, trigger: "continue" };
}

function pausedGoal() {
	const goal = stableGoal("paused", { objective: "Paused mid-implementation by the user.", autoContinue: true, sisyphus: false });
	goal.status = "paused";
	goal.stopReason = "user";
	return { goal, trigger: "status?" };
}

function blockedGoal() {
	const goal = stableGoal("blocked", { objective: "Blocked waiting on external credentials.", autoContinue: false, sisyphus: false });
	goal.status = "blocked";
	goal.pauseReason = "missing API credentials after three attempts";
	return { goal, trigger: "status?" };
}

function auditorRejectionFixture() {
	const goal = stableGoal("auditor-rejection", { objective: "Deliver audited feature work.", autoContinue: true, sisyphus: false });
	goal.status = "paused";
	goal.stopReason = "agent";
	goal.pauseReason = "auditor rejected completion";
	const ledgerEvents = [{
		type: "audit_result",
		goalId: goal.id,
		verdict: "disapproved",
		report: "Completion rejected: the verification contract requires green tests; two suites fail.",
		at: iso(120),
	}];
	return { goal, ledgerEvents, trigger: "continue" };
}

function postCompactionFixture() {
	const goal = stableGoal("post-compaction", { objective: "Long-running effort resumed after compaction.", autoContinue: true, sisyphus: false });
	goal.taskList = list(taskTree(4));
	return {
		goal,
		messages: [
			userMessage("start work"),
			assistantMessage("working"),
			{ role: "compactionSummary", timestamp: Date.parse(iso(90)), summary: "Earlier turns summarized.", tokensBefore: 9000 },
		],
		trigger: `<pi_goal_continuation goal_id="${goal.id}" kind="checkpoint" v="2"/>`,
	};
}

function staleCheckpointFixture() {
	const current = stableGoal("stale-current", { objective: "The currently focused goal.", autoContinue: true, sisyphus: false });
	return {
		goal: current,
		messages: [
			{
				role: "custom",
				customType: "pi-goal-event",
				content: '<pi_goal_continuation goal_id="ghost-goal" kind="checkpoint">',
				details: { kind: "checkpoint", goalId: "ghost-goal", objective: "An older cleared goal objective" },
				display: false,
			},
		],
		trigger: '<pi_goal_continuation goal_id="ghost-goal" kind="checkpoint">',
	};
}

function draftingFixture(seed, stage) {
	return {
		goal: null,
		draftPrompt:
			stage === "question"
				? `[PI GOAL DRAFTING ${seed}] Clarify material ambiguity with one focused question.`
				: `[PI GOAL DRAFTING ${seed}] Present the structured proposal via propose_goal_draft.`,
		trigger: "/goal build the thing",
	};
}

function auditFixture(seed, verdict) {
	const goal = stableGoal(seed, { objective: "Complete the audited deliverable.", autoContinue: true, sisyphus: false });
	goal.status = "complete";
	goal.taskList = list(taskTree(2));
	const ledgerEvents = verdict === "rejected"
		? [{ type: "audit_result", goalId: goal.id, verdict: "disapproved", report: "Missing evidence for t1.", at: iso(150) }]
		: [];
	return { goal, ledgerEvents, trigger: "finish" };
}

function getGoalFixture() {
	const goal = stableGoal("get-goal", { objective: "Inspectable objective for get_goal sizing.", autoContinue: true, sisyphus: false });
	goal.taskList = list(taskTree(5));
	goal.currentTaskId = "t2";
	goal.verificationContract = "Show measurable evidence.";
	return { goal, trigger: "continue" };
}

// ── message constructors ───────────────────────────────────────────────────

export function userMessage(text) {
	return { role: "user", content: [{ type: "text", text }] };
}

export function assistantMessage(text) {
	return { role: "assistant", content: [{ type: "text", text }], stopReason: "end_turn" };
}

/** A legacy FULL checkpoint message (~6.4K chars), pre-#30 shape. */
export function legacyCheckpointMessage(goalId, index) {
	return {
		role: "custom",
		customType: "pi-goal-event",
		display: false,
		content: `[GOAL CHECKPOINT goalId=${goalId}]\nContinue working toward the active pi goal.\n${"x".repeat(6400)}`,
		details: { kind: "checkpoint", goalId, objective: `Objective ${goalId}: ${"y".repeat(2000)}` },
	};
}

/** The bounded v2 checkpoint marker. */
export function v2CheckpointMessage(goalId, seq = 1) {
	return {
		role: "custom",
		customType: "pi-goal-event",
		display: false,
		content: `<pi_goal_continuation goal_id="${goalId}" kind="checkpoint" v="2"/>`,
		details: { version: 2, kind: "checkpoint", goalId, status: "active", revision: 0, checkpointSeq: seq, timestamp: T0 },
	};
}

export const FIXTURE_IDS = Object.keys(FIXTURES).sort();
