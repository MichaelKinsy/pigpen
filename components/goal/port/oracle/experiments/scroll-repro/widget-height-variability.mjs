// Root-cause evidence (spec 2026-08-11-stable-widget-height): measure the
// goal widget's NATURAL (unbounded) rendered height across goal-state
// mutations and show how the terminal-cap rendering (min(natural, cap))
// changes over time — the buffer line-count churn that makes the terminal
// jump to the bottom and defeats scroll-up.
//
// Usage:
//   node widget-height-variability.mjs [--compact] [--rows N]
//
// Pure renderer calls only (renderExpandedDashboard / renderCompactDashboard
// / renderAuditorWidgetLines); no TUI. Each "state" is a goal mutation that
// happens naturally while a goal runs: activity-feed growth, current-task
// contract/evidence appearing, verification text, budget, task growth.

import { renderExpandedDashboard, renderCompactDashboard } from "../../extensions/widgets/goal-dashboard-renderer.ts";
import { renderAuditorWidgetLines } from "../../extensions/widgets/goal-widget.ts";
import { deriveGoalDashboardModel } from "../../extensions/widgets/goal-dashboard-model.ts";
import { boundWidgetRenderLines, WIDGET_HEIGHT_RESERVE } from "../../extensions/widgets/goal-widget.ts";

const WIDTH = 120;
const ROWS = Number(process.argv[process.argv.indexOf("--rows") + 1]) || 24;
const theme = { fg: (_c, s) => s, bg: (_c, s) => s, bold: (s) => s, dim: (s) => s };
const cap = Math.max(1, ROWS - WIDGET_HEIGHT_RESERVE);

const T = "2026-08-10T00:00:00Z";
function task(id, status, { title = `Task ${id} with a reasonably long title for wrapping tests`, contract, evidence, completedAt } = {}) {
	return { id, title, status, createdAt: T, updatedAt: T, completedAt, verificationContract: contract, evidence };
}
function ledger(type, at, extra = {}) {
	return { type, at, goalId: "g1", ...extra };
}

const baseGoal = {
	id: "g1",
	createdAt: T,
	updatedAt: T,
	objective: "=== Goal ===\nObjective: Make pi scrollable when the goal widget is taller than the terminal",
	status: "active",
	autoContinue: true,
	usage: { activeSeconds: 65, tokensUsed: 2500 },
	activePath: ".pi/goals/active_goal.md",
	taskList: { tasks: [] },
	verificationContract: undefined,
	tokenBudget: undefined,
};

// A realistic progression of goal states while a goal runs.
function states() {
	const goal = structuredClone(baseGoal);
	const events = [];
	const out = [];
	const push = (label) =>
		out.push({ label, goal: structuredClone(goal), events: structuredClone(events) });

	push("A: goal created, 3 pending tasks");
	goal.taskList.tasks = [task("t1", "pending"), task("t2", "pending"), task("t3", "pending")];
	push("B: +1 task complete (activity feed grows)");
	goal.taskList.tasks[0] = task("t1", "complete", { completedAt: "2026-08-10T00:01:00Z" });
	events.push(ledger("task_complete", "2026-08-10T00:01:00Z", { taskId: "t1" }));
	push("C: +2 tasks complete, activity feed 3 items");
	goal.taskList.tasks[1] = task("t2", "complete", { completedAt: "2026-08-10T00:02:00Z" });
	goal.taskList.tasks[2] = task("t3", "complete", { completedAt: "2026-08-10T00:03:00Z" });
	events.push(ledger("task_complete", "2026-08-10T00:02:00Z", { taskId: "t2" }));
	events.push(ledger("task_complete", "2026-08-10T00:03:00Z", { taskId: "t3" }));
	push("D: current task gains contract + evidence (wraps)");
	goal.taskList.tasks = [
		task("t1", "complete", { completedAt: "2026-08-10T00:01:00Z" }),
		task("t2", "complete", { completedAt: "2026-08-10T00:02:00Z" }),
		task("t3", "pending", { contract: "Run npm test (0 failures) and re-read every requirement in the spec before finishing", evidence: "npm test passes: 128 tests, 0 failures; tsc clean; verified by re-reading PRODUCT.md" }),
		task("t4", "pending"),
		task("t5", "pending"),
	];
	push("E: goal verification contract added");
	goal.verificationContract = "Run npm test (0 failures), re-read PRODUCT/TECH/MILESTONES, and confirm the terminal scroll-up experience with the user";
	push("F: token budget configured");
	goal.tokenBudget = 200000;
	push("G: 12 tasks (primary repro shape), 5 complete");
	goal.taskList.tasks = Array.from({ length: 12 }, (_, i) =>
		task(`t${i}`, i < 5 ? "complete" : "pending", {
			title: `Task number ${i} with a reasonably long title for wrapping`,
			completedAt: i < 5 ? `2026-08-10T00:0${i + 1}:00Z` : undefined,
		}),
	);
	events.push(...Array.from({ length: 2 }, (_, i) => ledger("task_complete", `2026-08-10T00:0${i + 4}:00Z`, { taskId: `t${i + 3}` })));
	push("H: activity feed capped at 5 (steady state)");
	goal.taskList.tasks[5] = task("t5", "complete", { completedAt: "2026-08-10T00:06:00Z" });
	events.push(ledger("task_complete", "2026-08-10T00:06:00Z", { taskId: "t5" }));
	return out;
}

function height(label, renderFn) {
	const model = deriveGoalDashboardModel(states()[0].goal, { focused: true, otherOpenGoals: 0, ledgerEvents: [] });
	void model;
	const lines = renderFn();
	return lines.length;
}

let maxSpan = 0;
const rows = [];
let prevRendered;
for (const { label, goal, events } of states()) {
	const model = deriveGoalDashboardModel(goal, { focused: true, otherOpenGoals: 0, ledgerEvents: events });
	const expanded = renderExpandedDashboard(model, theme, WIDTH, { rows: 20 }).length;
	const compact = renderCompactDashboard(model, theme, WIDTH).length;
	const renderedExpanded = boundWidgetRenderLines(renderExpandedDashboard(model, theme, WIDTH, { rows: 20 }), ROWS).length;
	const renderedCompact = boundWidgetRenderLines(renderCompactDashboard(model, theme, WIDTH), ROWS).length;
	const span = Math.max(expanded, compact) - Math.min(expanded, compact);
	maxSpan = Math.max(maxSpan, span);
	const churn = prevRendered !== undefined && renderedExpanded !== prevRendered ? "  <-- rendered height CHANGED" : "";
	prevRendered = renderedExpanded;
	rows.push({ label, expanded, compact, renderedExpanded, renderedCompact, churn });
}

console.log(`width=${WIDTH}  terminal rows=${ROWS}  cap=terminalRows-WIDGET_HEIGHT_RESERVE=${cap}`);
console.log("");
console.log("natural (unbounded) heights vs. rendered (capped) heights per goal state:");
console.log("state | natural exp | natural cmp | rendered exp | rendered cmp");
let changed = 0;
for (const r of rows) {
	const changedCount = r.renderedExpanded === r.renderedCompact && r.renderedExpanded === r.expanded && r.renderedCompact === r.compact ? 0 : 1;
	if (r.churn) changed++;
	console.log(`${r.label.padEnd(46)} ${String(r.expanded).padStart(3)} ${String(r.compact).padStart(3)} ${String(r.renderedExpanded).padStart(5)} ${String(r.renderedCompact).padStart(5)}${r.churn}`);
}
console.log("");
console.log(`natural-height span across states: expanded ${Math.min(...rows.map((r) => r.expanded))}..${Math.max(...rows.map((r) => r.expanded))}, compact ${Math.min(...rows.map((r) => r.compact))}..${Math.max(...rows.map((r) => r.compact))}`);
console.log(`rendered-height changes across states (expanded): ${changed} of ${rows.length} transitions change the buffer line count`);

// Audit dashboard: height while the audit animation runs.
const auditStates = [
	{ label: "audit: just started", phase: "running", elapsedMs: 0, checks: 0, tool: false, output: 0 },
	{ label: "audit: tool executing, output 1", phase: "tool_executing", elapsedMs: 8000, checks: 2, tool: true, output: 1 },
	{ label: "audit: producing report, output 3", phase: "producing_report", elapsedMs: 30000, checks: 4, tool: true, output: 3 },
	{ label: "audit: done", phase: "done", elapsedMs: 42000, checks: 5, tool: false, output: 0 },
];
console.log("");
console.log("audit dashboard natural heights (expanded/debug audit shows tool details):");
let prevAudit;
for (const s of auditStates) {
	const progress = {
		phase: s.phase,
		currentTool: s.tool ? "npm test" : undefined,
		recentOutput: Array.from({ length: s.output }, (_, i) => `output line ${i} from the tool`),
		elapsedMs: s.elapsedMs,
		percentage: s.phase === "done" ? 100 : Math.min(99, s.checks * 20),
		auditorLabel: "anthropic/claude-sonnet-4",
	};
	const h = renderAuditorWidgetLines(progress, theme, WIDTH, { showToolDetails: true }).length;
	const delta = prevAudit !== undefined ? ` (Δ ${h - prevAudit} from prev)` : "";
	console.log(`${s.label.padEnd(36)} ${h}${delta}`);
	prevAudit = h;
}

if (process.argv.includes("--expect")) {
	if (changed < 3) {
		console.log("\nFAIL: expected the capped rendered height to change across goal states");
		process.exit(1);
	}
	console.log("\nOK: capped rendered height churns across goal states (root cause reproduced)");
}
