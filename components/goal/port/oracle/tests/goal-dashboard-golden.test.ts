/**
 * Dashboard golden tests (plan §19.4): render compact and expanded dashboards
 * at 40/50/60/80/100/140 columns across every §5.5 layout mode and the §19.4
 * state list. For every rendered line: visibleWidth(line) <= width.
 *
 * Rendering data always flows through the shared pure model
 * (deriveGoalDashboardModel), so these tests also pin the model pipeline.
 */

import assert from "node:assert/strict";
import test from "node:test";

import { visibleWidth } from "@earendil-works/pi-tui";
import type { GoalLedgerEvent } from "../extensions/goal-ledger.ts";
import type { GoalRecord, GoalTask } from "../extensions/goal-record.ts";
import { deriveGoalDashboardModel } from "../extensions/widgets/goal-dashboard-model.ts";
import {
	renderCompactDashboard,
	renderExpandedDashboard,
	renderUnfocusedDashboard,
} from "../extensions/widgets/goal-dashboard-renderer.ts";
import { renderAuditorWidgetLines, type AuditorWidgetProgress } from "../extensions/widgets/goal-widget.ts";

const WIDTHS = [40, 50, 60, 80, 100, 140];

const theme = {
	fg: (_color: string, value: string) => value,
	bold: (value: string) => value,
} as never;

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

function task(id: string, title: string, overrides: Partial<GoalTask> = {}): GoalTask {
	return { id, title, status: "pending", ...overrides };
}

function record(overrides: Partial<GoalRecord> = {}): GoalRecord {
	return {
		id: "g1",
		objective: "Add CSV export to reports",
		status: "active",
		autoContinue: true,
		usage: { tokensUsed: 18200, activeSeconds: 767 },
		sisyphus: false,
		createdAt: "2026-01-01T00:00:00.000Z",
		updatedAt: "2026-01-01T00:00:00.000Z",
		activePath: ".pi/goals/active_goal_g1.md",
		...overrides,
	};
}

function withTasks(taskList: GoalTask[], overrides: Partial<GoalRecord> = {}): GoalRecord {
	return record({ ...overrides, taskList: { tasks: taskList, blockCompletion: false, proposedAt: "2026-01-01T00:00:00.000Z" } });
}

function fiveTaskTree(): GoalTask[] {
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
		task("t5", "Add and run tests", { status: "skipped", skipReason: "Covered by t2" }),
	];
}

/** 30 top-level tasks; t5 and t20 completed (t20 latest) — the anchored
 * viewport lands mid-list so both compact and expanded windows scroll in
 * both directions. */
function manyTaskTree(): GoalTask[] {
	const tasks: GoalTask[] = Array.from({ length: 30 }, (_, i) => ({
		id: `t${i + 1}`,
		title: `Task number ${i + 1}`,
		status: "pending" as const,
	}));
	tasks[4] = { ...tasks[4]!, status: "complete", completedAt: "2026-01-01T10:00:00.000Z" };
	tasks[19] = { ...tasks[19]!, status: "complete", completedAt: "2026-01-01T11:00:00.000Z" };
	return tasks;
}

function modelFor(goal: GoalRecord, opts: { focused?: boolean; otherOpenGoals?: number; ledgerEvents?: GoalLedgerEvent[] } = {}): ReturnType<typeof deriveGoalDashboardModel> {
	return deriveGoalDashboardModel(goal, {
		focused: opts.focused ?? true,
		otherOpenGoals: opts.otherOpenGoals ?? 2,
		ledgerEvents: opts.ledgerEvents ?? [],
	});
}

function ledgerEventsFor(goalId: string): GoalLedgerEvent[] {
	return [
		{ type: "goal_created", goalId, objective: "Add CSV export to reports", sisyphus: false, autoContinue: true, at: "2026-01-01T09:00:00.000Z" },
		{ type: "task_complete", goalId, taskId: "t2", evidence: "Done", at: "2026-01-01T09:05:00.000Z" },
		{ type: "task_started", goalId, taskId: "t3.3", at: "2026-01-01T09:06:00.000Z" },
	] as unknown as GoalLedgerEvent[];
}

function auditorProgress(): AuditorWidgetProgress {
	return {
		phase: "tool_executing",
		elapsedMs: 138000,
		currentTool: "read",
		currentToolArgs: '{"path":"src/parser.ts"}',
		recentOutput: ["checking file exists...", "confirming test coverage..."],
		percentage: 72,
		label: "Workspace inspection",
	};
}

// ---------------------------------------------------------------------------
// Width-safety helper
// ---------------------------------------------------------------------------

function assertWidthSafe(lines: string[], width: number): void {
	for (let i = 0; i < lines.length; i++) {
		assert.ok(
			visibleWidth(lines[i]!) <= width,
			`line ${i} at width ${width} has visible width ${visibleWidth(lines[i]!)}: ${JSON.stringify(lines[i]!.slice(0, 60))}`,
		);
	}
}

for (const width of WIDTHS) {
	test(`width safety at ${width} cols: compact and expanded over all dashboard states`, () => {
		const states: GoalRecord[] = [
			// Running without tasks.
			record(),
			// Running with tasks.
			withTasks(fiveTaskTree(), { currentTaskId: "t3", verificationContract: "Run npm test with zero failures." }),
			// Paused.
			record({ status: "paused", stopReason: "user", pauseReason: "User is away", pauseSuggestedAction: "Run /goal-resume when back." }),
			// Blocked.
			record({ status: "blocked", pauseReason: "Missing API key", pauseSuggestedAction: "Set the key and run /goal-resume." }),
			// Budget limited.
			record({ status: "budget_limited", tokenBudget: 20000 }),
			// Complete (pre-archive).
			withTasks(fiveTaskTree().map((t) => ({ ...t, status: "complete" as const, subtasks: t.subtasks?.map((s) => ({ ...s, status: "complete" as const, skipReason: undefined })) })), {
				status: "complete",
				activePath: undefined,
				archivedPath: ".pi/goals/archived/goal_g1.md",
			}),
			// Long objective.
			record({ objective: "x".repeat(600) }),
			// Long path.
			record({ activePath: `.pi/goals/${"a".repeat(200)}.md` }),
			// Long contract.
			record({ verificationContract: "Run ".repeat(60) }),
			// Unicode content.
			withTasks([
				task("u1", "中文任务：处理報告数据", { status: "complete" }),
				task("u2", "日本語のタスクと📊チャート"),
				task("u3", "Combiné avec des caractères accentués"),
			], { currentTaskId: "u2" }),
		];

		for (const goal of states) {
			const model = modelFor(goal, { ledgerEvents: ledgerEventsFor(goal.id) });
			if (!model) continue;
			assertWidthSafe(renderCompactDashboard(model, theme, width), width);
			assertWidthSafe(renderExpandedDashboard(model, theme, width), width);
		}

		// Scrolled viewports: a long list with completion timestamps at
		// several window positions (anchored default, top, middle, off-scale)
		// must stay width-safe — the ↑/↓ indicator rows count toward width.
		const scrolled = withTasks(manyTaskTree(), { currentTaskId: "t21" });
		const scrolledModel = modelFor(scrolled);
		if (scrolledModel) {
			assertWidthSafe(renderCompactDashboard(scrolledModel, theme, width), width);
			assertWidthSafe(renderExpandedDashboard(scrolledModel, theme, width, { rows: 10 }), width);
			for (const offset of [0, 3, 7, 99]) {
				assertWidthSafe(renderCompactDashboard(scrolledModel, theme, width, { scrollOffset: offset }), width);
				assertWidthSafe(renderExpandedDashboard(scrolledModel, theme, width, { rows: 10, scrollOffset: offset }), width);
			}
		}
	});
}

test("unfocused panel is width-safe at every width", () => {
	for (const width of WIDTHS) {
		assertWidthSafe(renderUnfocusedDashboard(3, theme, width), width);
	}
});

test("palette: neutral-gray chrome, colour-coded content (§5)", () => {
	const captured: Array<[string, string]> = [];
	const colorTheme = {
		fg: (color: string, value: string) => { captured.push([color, value]); return value; },
		bold: (value: string) => value,
	} as never;
	const model = modelFor(withTasks(fiveTaskTree(), { currentTaskId: "t3" }));
	assert.ok(model);
	renderCompactDashboard(model, colorTheme, 100);
	renderExpandedDashboard(model, colorTheme, 100);
	const byColor = (c: string) => captured.filter(([col]) => col === c).map(([, v]) => v);
	const muted = byColor("muted");
	// The chrome is ONE neutral-gray tone (muted, #808080 — no blue tinge):
	// corners, top/bottom dashes, left/right edges and interior rules all
	// share it. The blue-gray mdLink token is banned from the dashboard.
	assert.equal(byColor("mdLink").length, 0, "no blue-tinged mdLink anywhere — chrome is neutral gray");
	assert.equal(byColor("borderMuted").length, 0, "the dark borderMuted token is not used");
	assert.ok(muted.some((v) => /^─+$/.test(v)), "top/bottom frame dashes are neutral gray");
	assert.ok(muted.includes("│"), "vertical edges are neutral gray");
	for (const glyph of ["╭", "╮", "├", "┤", "╰", "╯"]) {
		assert.ok(muted.includes(glyph), `${glyph} corner is neutral gray`);
	}
	assert.ok(muted.some((v) => v.startsWith("─ Tasks")), "section-rule label is part of the frame tone");
	// Frame lines are one tone: the gray must appear on BOTH the left and the
	// right of every frame line (regression: it used to read muted on one
	// side and mdLink on the other).
	assert.ok(muted.some((v) => v === "─"), "header leading dash is frame-colored (LHS)");
	assert.ok(muted.some((v) => v.includes("Ctrl+Shift+T: expand tasks")), "footer hint is part of the frame tone");
	assert.ok(muted.some((v) => v.includes("12m47s")), "header usage meta is part of the frame tone");
	assert.ok(muted.some((v) => v.includes("Add CSV export to reports")), "header title is part of the frame tone");
	// Task markers are colour-coded pastel: pending amber, complete green, skipped gray.
	assert.ok(byColor("mdHeading").includes("·"), "pending marker is pastel amber");
	assert.ok(byColor("success").includes("✓"), "complete marker is muted green");
	assert.ok(muted.includes("~"), "skipped marker is gray");
	// Task ids share the marker color; titles stay pastel amber; the current
	// task is fully accent (marker, id, title).
	assert.ok(byColor("success").some((v) => v.includes("t1") || v.includes("t2")), "completed ids are muted green");
	assert.ok(byColor("mdHeading").some((v) => v.includes("Review reports page")), "task titles are pastel amber");
	assert.ok(byColor("accent").some((v) => v.includes("t3")), "current task id stays accent (teal)");
	assert.ok(byColor("accent").some((v) => v.includes("Add the download button")), "current task title is accent too");
	// The compact header brand is teal; the progress bar is fully muted —
	// neutral-gray fill with dim empty cells, matching the muted percentage.
	assert.ok(byColor("accent").some((v) => v.includes("pi-goal-x")), "brand is accent");
	assert.ok(muted.some((v) => /^█+$/.test(v)), "progress bar fill is neutral gray");
	assert.ok(byColor("dim").some((v) => /^░+$/.test(v)), "progress bar empty cells are dim");
	// No accent progress numbers remain: the tasks counts, the header bar
	// fraction and the current task's subtask fraction are all neutral gray.
	assert.ok(muted.some((v) => /✓\d+ done · \d+ open/.test(v)), "task counts live in the muted header row");
	assert.ok(muted.some((v) => /^\d+\/\d+$/.test(v)), "subtask fraction is neutral gray");
	assert.ok(muted.some((v) => /^ ▸ \d+\/\d+$/.test(v)), "per-row subtask marker is neutral gray");
	assert.equal(byColor("accent").some((v) => /^\d+\/\d+/.test(v)), false, "no accent progress fractions remain");
	// No blinding #ffff00-style warning anywhere.
	assert.equal(byColor("warning").length, 0, "the bright warning color is not used in the dashboard");
});

test("audit-running widget is width-safe at every width", () => {
	for (const width of WIDTHS) {
		assertWidthSafe(renderAuditorWidgetLines(auditorProgress(), theme, width), width);
	}
});

// ---------------------------------------------------------------------------
// Compact layout goldens (spot checks at fixed widths)
// ---------------------------------------------------------------------------

test("compact: top-level task list is shown by default with '+N more' overflow", () => {
	const model = modelFor(withTasks(fiveTaskTree(), { currentTaskId: "t3" }));
	assert.ok(model);
	// Medium (80): four rows + overflow for the fifth.
	const medium = renderCompactDashboard(model, theme, 80).join("\n");
	assert.match(medium, /├─ Tasks /);
	assert.match(medium, /✓ t1  Review reports page and data source/);
	assert.match(medium, /… \+1 more task/);
	// Wide (100): all five top-level rows fit, no overflow.
	const wide = renderCompactDashboard(model, theme, 100).join("\n");
	assert.match(wide, /~ t5  Add and run tests/);
	assert.doesNotMatch(wide, /\+[0-9]+ more task/);
	// Minimal (40): only the first two rows fit, the rest overflow.
	const minimal = renderCompactDashboard(model, theme, 40).join("\n");
	assert.match(minimal, /\+3 more tasks/);
	assert.match(minimal, /✓ t1  /);
	// No tasks → no task section at all.
	const emptyModel = modelFor(record());
	assert.ok(emptyModel);
	const empty = renderCompactDashboard(emptyModel, theme, 100).join("\n");
	assert.equal(empty.includes("├─ Tasks"), false);
});

test("compact: the auditor indicator is a bottom-right border dot, green on / gray off (§auditor-toggle)", () => {
	const model = modelFor(withTasks(fiveTaskTree(), { currentTaskId: "t3" }));
	assert.ok(model);
	// The standalone muted auditor content line is gone; the status lives in
	// the border footer as a single dot before the corner.
	const wide = renderCompactDashboard(model, theme, 100).join("\n");
	assert.doesNotMatch(wide, /Auditor  on/, "no standalone auditor content line");
	assert.doesNotMatch(wide, /│ Auditor/, "no Auditor label row");
	assert.match(wide, /toggle auditor ● ─╯/, "dot + right-aligned note sit at the bottom-right border");
	// Wide/medium right-align a note explaining that Ctrl+Shift+A turns the
	// auditor on and off; it sits directly left of the dot at the right edge.
	// Narrow/minimal keep just the dot.
	assert.match(wide, /Ctrl\+Shift\+A: toggle auditor/, "wide footer carries the toggle note");
	assert.match(wide, /Ctrl\+Shift\+A: toggle auditor ●/, "note sits directly left of the border dot");
	assert.match(wide, /expand tasks─+ Ctrl\+Shift\+A: toggle auditor/, "note is right-aligned: only fill between hint and note");
	assert.doesNotMatch(wide, /· Ctrl\+Shift\+A/, "chord is no longer glued to the left hint");
	assert.match(renderCompactDashboard(model, theme, 80).join("\n"), /Ctrl\+Shift\+A: toggle auditor ●/, "medium footer carries the right-aligned note + dot");
	for (const width of [60, 40]) {
		const out = renderCompactDashboard(model, theme, width).join("\n");
		assert.doesNotMatch(out, /Ctrl\+Shift\+A/, "narrow/minimal keep the dot without the note");
		assert.doesNotMatch(out, /toggle auditor/, "no toggle note in narrow/minimal");
		assert.match(out, /─ ● ─╯/, "the border dot survives every layout");
	}

	// Green when the auditor is on, gray (muted) when off — assert the actual
	// theme colors via a color-capturing theme.
	const seen: Array<[string, string]> = [];
	const trackingTheme = {
		fg: (color: string, value: string) => {
			seen.push([color, value]);
			return value;
		},
		bold: (value: string) => value,
	} as never;
	seen.length = 0;
	renderCompactDashboard(model, trackingTheme, 100);
	assert.ok(seen.some(([c, v]) => c === "success" && v === "●"), "auditor on renders a green dot");

	const offGoal = withTasks(fiveTaskTree(), { currentTaskId: "t3", skipAuditor: true });
	const offModel = modelFor(offGoal);
	assert.ok(offModel);
	seen.length = 0;
	renderCompactDashboard(offModel, trackingTheme, 100);
	assert.ok(seen.some(([c, v]) => c === "muted" && v === "●"), "auditor off renders a muted gray dot");
	assert.equal(seen.some(([c, v]) => c === "success" && v === "●"), false, "off state never renders the green dot");

	// The expanded dashboard is byte-identical with the auditor indicator: it
	// never renders it (compact-only per spec).
	const expanded = renderExpandedDashboard(model, theme, 100).join("\n");
	assert.doesNotMatch(expanded, /Auditor/, "expanded dashboard does not render the compact auditor indicator");
	for (const width of WIDTHS) {
		assertWidthSafe(renderCompactDashboard(model, theme, width), width);
		assertWidthSafe(renderCompactDashboard(offModel, theme, width), width);
	}
});

test("compact: the default viewport anchors to the most recently completed tasks (§9.6)", () => {
	const model = modelFor(withTasks(manyTaskTree(), { currentTaskId: "t21" }));
	assert.ok(model);
	// Wide (100): 5 rows; anchor t20 → window t16..t20, indicator on both sides.
	const wide = renderCompactDashboard(model, theme, 100).join("\n");
	assert.match(wide, /↑ 15 more tasks/);
	assert.match(wide, /Task number 20/);
	assert.doesNotMatch(wide, /[✓▸·~] t1\s/, "the earliest task row is hidden");
	assert.match(wide, /… \+10 more tasks/);
	// Minimal (40): 2 rows; the anchor stays the last visible row.
	const minimal = renderCompactDashboard(model, theme, 40).join("\n");
	assert.match(minimal, /↑ 18 more tasks/);
	assert.match(minimal, /Task number 20/);
});

test("compact: an explicit scrollOffset windows the list and clamps at both ends", () => {
	const model = modelFor(withTasks(manyTaskTree(), { currentTaskId: "t21" }));
	assert.ok(model);
	const top = renderCompactDashboard(model, theme, 100, { scrollOffset: 0 }).join("\n");
	assert.doesNotMatch(top, /↑ \d+ more tasks/);
	assert.match(top, /Task number 1/);
	assert.match(top, /… \+25 more tasks/);
	const tail = renderCompactDashboard(model, theme, 100, { scrollOffset: 99 }).join("\n");
	assert.match(tail, /↑ 25 more tasks/);
	assert.doesNotMatch(tail, /… \+\d+ more tasks/);
});

test("expanded: rows + scrollOffset window the tree with indicators; full tree by default", () => {
	const model = modelFor(withTasks(manyTaskTree()));
	assert.ok(model);
	const mid = renderExpandedDashboard(model, theme, 100, { rows: 10, scrollOffset: 5 }).join("\n");
	assert.match(mid, /↑ 5 more tasks/);
	assert.match(mid, /Task number 6/);
	assert.match(mid, /… \+15 more tasks/);
	// No rows option → the whole tree is rendered (backward compatible).
	const full = renderExpandedDashboard(model, theme, 100).join("\n");
	assert.doesNotMatch(full, /↑ \d+ more tasks/);
	assert.match(full, /Task number 30/);
});

test("compact: running with tasks at 100 shows status, header counts + bar, current, contract, file", () => {
	const model = modelFor(withTasks(fiveTaskTree(), { currentTaskId: "t3", verificationContract: "Run npm test with zero failures." }));
	assert.ok(model);
	const lines = renderCompactDashboard(model, theme, 100);
	const text = lines.join("\n");
	assert.match(lines[0]!, /^╭─ pi-goal-x ─ Add CSV export to reports/);
	assert.doesNotMatch(lines[0]!, /12m47s/, "usage moved from the header into the status line");
	assert.match(lines[1]!, /goal: running \[12m47s 18\.2K\] \(\+2 open\)/);
	// Header row: counts first (skipped counted as done → ✓3), task bar, then
	// the current task's subtask bar beside it (`· Sub 2/3`); the standalone
	// compact progress lines are gone.
	assert.match(text, /├─ Tasks · ✓3 done · 2 open ─+ \[.*\] · Sub 2\/3 \[.*\] ┤/);
	assert.doesNotMatch(text, /Tasks  \[.*\] \d\/\d · \d+%/, "standalone compact progress line is gone");
	assert.doesNotMatch(text, /Subtasks \[.*\] 2\/3 · 67%/, "standalone compact subtask line is gone from compact");
	assert.match(text, /✓ t1  Review reports page and data source/);
	assert.match(text, /▸ t3  Add the download button ☑ ▸ 2\/3/);
	assert.match(text, /· t4  Add documentation/);
	assert.match(text, /~ t5  Add and run tests/);
	assert.doesNotMatch(text, /· t4  Add documentation ▸/, "leaf tasks show no subtask marker");
	assert.doesNotMatch(text, /\+[0-9]+ more task/, "all five top-level tasks fit at 100 cols");
	assert.match(text, /Current  t3 · Add the download button/);
	assert.match(text, /· Sub 2\/3 \[.*\]/, "subtask bar sits beside the task bar in the header");
	assert.match(text, /Verify   Run npm test with zero failures/);
	assert.match(text, /File     \.pi\/goals\/active_goal_g1\.md/);
	assert.match(lines.at(-1) ?? "", /Ctrl\+Shift\+T: expand tasks/);
});

test("compact: minimal mode at 40 keeps the essential summary", () => {
	const model = modelFor(withTasks(fiveTaskTree(), { currentTaskId: "t3", verificationContract: "Run npm test with zero failures." }));
	assert.ok(model);
	const lines = renderCompactDashboard(model, theme, 40);
	const text = lines.join("\n");
	assert.match(text, /goal: running/);
	assert.match(text, /Tasks · ✓3 done · 2 open/);
	assert.match(text, /▸ Add the download button/);
	assert.doesNotMatch(text, /Other goals/);
	assert.doesNotMatch(text, /File/);
});

test("compact: blocked state surfaces blocker and suggested action", () => {
	const model = modelFor(record({ status: "blocked", pauseReason: "Build fails", pauseSuggestedAction: "Run npm test" }));
	assert.ok(model);
	const lines = renderCompactDashboard(model, theme, 100);
	const text = lines.join("\n");
	assert.match(text, /goal: blocked/);
	assert.match(text, /Blocker  Build fails/);
	assert.match(text, /Action   Run npm test/);
});

test("compact: paused state shows who paused and the reason", () => {
	const model = modelFor(record({ status: "paused", stopReason: "agent", pauseReason: "Waiting for input" }));
	assert.ok(model);
	const text = renderCompactDashboard(model, theme, 100).join("\n");
	assert.match(text, /goal: paused \(agent\)/);
	assert.match(text, /Reason   Waiting for input/);
});

test("compact: budget-limited state shows the budget summary", () => {
	const model = modelFor(record({ status: "budget_limited", tokenBudget: 20000 }));
	assert.ok(model);
	const text = renderCompactDashboard(model, theme, 100).join("\n");
	assert.match(text, /⛽ Budget 18\.2K \/ 20K · 91%/);
	assert.match(text, /goal: budget limited/);
});

test("compact: complete state shows the §4.7 message", () => {
	const model = modelFor(withTasks(fiveTaskTree().map((t) => ({ ...t, status: "complete" as const, subtasks: t.subtasks?.map((s) => ({ ...s, status: "complete" as const })) })), {
		status: "complete",
		activePath: undefined,
		archivedPath: ".pi/goals/archived/goal_g1.md",
	}));
	assert.ok(model);
	const text = renderCompactDashboard(model, theme, 100).join("\n");
	assert.match(text, /All required work is complete/);
	assert.match(text, /✓5 done · 0 open/);
});

// ---------------------------------------------------------------------------
// Expanded layout goldens
// ---------------------------------------------------------------------------

test("expanded: full dashboard at 100 shows sections and complete task tree", () => {
	const model = modelFor(withTasks(fiveTaskTree(), { currentTaskId: "t3", verificationContract: "Run npm test with zero failures." }), {
		ledgerEvents: ledgerEventsFor("g1"),
	});
	assert.ok(model);
	const lines = renderExpandedDashboard(model, theme, 100);
	const text = lines.join("\n");
	assert.match(lines[1]!, /goal: running \[12m47s 18\.2K\] \(\+2 open\)/);
	assert.match(text, /├─ Progress /);
	assert.match(text, /3\/5 tasks · 60%/);
	assert.match(text, /├─ Tasks /);
	assert.match(text, /✓ t1  Review reports page and data source/);
	assert.match(text, /▸ t3  Add the download button/);
	assert.match(text, /✓ t3\.1  Add loading state/);
	assert.match(text, /· t4  Add documentation/);
	assert.match(text, /├─ Current task /);
	assert.match(text, /Subtasks \[.*\] 2\/3 · 67%/);
	assert.match(text, /Contract: The button downloads a CSV using the active filters/);
	assert.match(text, /├─ Verification /);
	assert.match(text, /Run npm test with zero failures/);
	assert.match(text, /├─ Recent activity /);
	assert.match(text, /Started “Add error handling”/);
	assert.match(lines.at(-1) ?? "", /Esc\/Ctrl\+Shift\+T: collapse/);
});

test("expanded: inferred current task is labelled", () => {
	const model = modelFor(withTasks(fiveTaskTree()));
	assert.ok(model);
	const text = renderExpandedDashboard(model, theme, 100).join("\n");
	assert.match(text, /Inferred from the first pending task/);
});

test("expanded: current nested subtask shows its depth in the tree", () => {
	const model = modelFor(withTasks(fiveTaskTree(), { currentTaskId: "t3.3" }));
	assert.ok(model);
	const text = renderExpandedDashboard(model, theme, 100).join("\n");
	assert.match(text, /▸ t3\.3  Add error handling/);
	assert.match(text, /t3\.3 · Add error handling/);
});

test("expanded: no task sections when tasks are disabled (§9.5)", () => {
	const model = modelFor(withTasks(fiveTaskTree(), { verificationContract: "Run the suite." }), {});
	assert.ok(model);
	// Re-derive with tasksDisabled (settings).
	const disabled = deriveGoalDashboardModel(withTasks(fiveTaskTree(), { verificationContract: "Run the suite." }), {
		focused: true,
		otherOpenGoals: 1,
		tasksDisabled: true,
	});
	assert.ok(disabled);
	const text = renderExpandedDashboard(disabled, theme, 100).join("\n");
	assert.doesNotMatch(text, /Tasks/);
	assert.doesNotMatch(text, /Current task/);
	assert.match(text, /Verification/);
});

test("compact: custom dashboard keybindings render in the footer hints", () => {
	const model = modelFor(withTasks(fiveTaskTree(), { currentTaskId: "t3" }));
	assert.ok(model);
	const custom = { toggleExpand: "ctrl+alt+t", scrollUp: "ctrl+alt+u", scrollDown: "ctrl+alt+d" } as const;
	// Wide (100): no overflow — the expand hint shows the configured toggle key.
	const wide = renderCompactDashboard(model, theme, 100, { keybindings: custom }).join("\n");
	assert.match(wide, /Ctrl\+Alt\+T: expand tasks/);
	assert.doesNotMatch(wide, /Ctrl\+Shift\+T/, "the default toggle key is replaced when configured");
	// Wide (100) with a six-task tree: overflow — both configured scroll keys
	// appear in the hint (a sixth task exceeds the 5-row viewport at 100).
	const six = modelFor(withTasks([...fiveTaskTree(), task("t6", "Extra row for overflow")], { currentTaskId: "t3" }));
	assert.ok(six);
	const wideOverflow = renderCompactDashboard(six, theme, 100, { keybindings: custom }).join("\n");
	assert.match(wideOverflow, /Ctrl\+Alt\+T: expand · Ctrl\+Alt\+U\/Ctrl\+Alt\+D: scroll/);
	assert.match(wideOverflow, /\+1 more task/);
	// Defaults render identically when keybindings are omitted (width 100 so
	// the hint fits; at 80 the auditor note truncates the hint the same way
	// on current main — pre-existing, not a regression).
	const defaults = renderCompactDashboard(six, theme, 100).join("\n");
	assert.match(defaults, /Ctrl\+Shift\+T: expand · Ctrl\+Shift\+↑↓: scroll/);
	// Long custom key names are tolerated: the hint ellipsizes inside the box
	// instead of widening it (width safety preserved).
	const longKeys = { toggleExpand: "ctrl+alt+shift+super+t", scrollUp: "ctrl+alt+pageUp", scrollDown: "ctrl+alt+pageDown" } as const;
	const longLines = renderCompactDashboard(model, theme, 80, { keybindings: longKeys });
	for (const line of longLines) assert.ok(visibleWidth(line) <= 80, `custom long-key footer stays within width: ${JSON.stringify(line)}`);
});

test("expanded: the collapse footer shows the configured toggle key", () => {
	const model = modelFor(withTasks(fiveTaskTree(), { currentTaskId: "t3" }));
	assert.ok(model);
	const custom = { toggleExpand: "ctrl+alt+t", scrollUp: "ctrl+alt+up", scrollDown: "ctrl+alt+down" } as const;
	assert.match(renderExpandedDashboard(model, theme, 100, { keybindings: custom }).join("\n"), /Esc\/Ctrl\+Alt\+T: collapse/);
	assert.match(renderExpandedDashboard(model, theme, 100).join("\n"), /Esc\/Ctrl\+Shift\+T: collapse/);
});
