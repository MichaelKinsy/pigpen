// Headless validation for the goal widget terminal-height bound
// (spec 2026-08-10-widget-height-bound-scrollback-fix) and the stable
// rendered height (spec 2026-08-11-stable-widget-height).
//
// Drives the REAL pi-tui main-screen renderer (TuiMainScreen — pi's default
// renderer) with pi's REAL frame layout (ScrollView transcript + VStack dock:
// header, chat, status, widget, editor, footer — exactly as
// interactive-mode.js mounts them in regular mode) and the REAL
// GoalWidgetComponent, at the user's repro: terminal height == the goal
// UI's natural height.
//
// Reports per scenario:
//   - widget rendered lines (post-fix must be <= terminalRows - DOCK_RESERVE)
//   - frame length + viewportTop (lines above the viewport live in terminal
//     scrollback and are reachable by scrolling up)
//   - whether the chat and the editor/footer are on screen or reachable
//   - \x1b[2J / \x1b[3J emissions on a widget state update (a 3J wipes
//     terminal scrollback — the bug)
//   - sticky-cap scenarios (spec 2026-08-11): widget rendered height +
//     buffer line count stay CONSTANT across goal-state changes once the
//     widget is at the cap, the fits case is byte-identical, resizes adapt,
//     and regime changes reset the latch.
//
// Usage:
//   node widget-height-bound.mjs           # report mode
//   node widget-height-bound.mjs --expect  # assertion mode (exit 1 on failure)

import { ScrollView } from "../../node_modules/@earendil-works/pi-tui/dist/components/scroll-view.js";
import { VStack } from "../../node_modules/@earendil-works/pi-tui/dist/components/v-stack.js";
import { Container } from "../../node_modules/@earendil-works/pi-tui/dist/tui.js";
import { TuiMainScreen } from "../../node_modules/@earendil-works/pi-tui/dist/index.js";
import { GoalWidgetComponent } from "../../extensions/widgets/goal-widget.ts";
// The rig's dock chrome (status 1 + editor 2 + footer 1) + 1 slack: the widget
// sizes itself against the MEASURED chrome (spec 2026-08-11), so the expected
// cap is terminalRows - (measuredChrome + 1).
const DOCK_RESERVE = 4 + 1;

const COLS = 120;
const ROWS = 24; // == expanded dashboard natural height (the primary repro)

const expectMode = process.argv.includes("--expect");
const theme = { fg: (_c, s) => s, bg: (_c, s) => s, bold: (s) => s, dim: (s) => s };
const stripAnsi = (s) => s.replace(/\x1b\[[0-9;?]*[a-zA-Z]/g, "").replace(/\x1b\][^\x07]*\x07/g, "");

function makeTerminal(rows) {
	const writes = [];
	return {
		terminal: {
			columns: COLS,
			rows,
			write(data) { writes.push(String(data)); },
			hideCursor() {}, showCursor() {}, start() {}, stop() {}, setTitle() {},
		},
		writes,
	};
}

function analyzeForWipes(stream) {
	let clear2J = 0;
	let clear3J = 0;
	let alt1049 = 0;
	let i = 0;
	while (i < stream.length) {
		if (stream.startsWith("\x1b[2J", i)) { clear2J++; i += 4; continue; }
		if (stream.startsWith("\x1b[3J", i)) { clear3J++; i += 4; continue; }
		if (stream.startsWith("\x1b[?1049", i)) { alt1049++; i += 7; continue; }
		i++;
	}
	return { clear2J, clear3J, alt1049 };
}

const T = "2026-08-10T00:00:00Z";
function task(id, status, extra = {}) {
	return { id, title: `Task ${id} with a reasonably long title for wrapping tests`, status, createdAt: T, updatedAt: T, completedAt: status === "complete" ? T : undefined, ...extra };
}
function ledger(type, at, extra = {}) { return { type, at, goalId: "g1", ...extra }; }

const baseGoal = {
	id: "g1",
	createdAt: T,
	updatedAt: T,
	objective: "=== Goal ===\nObjective: Fix the scrollback issue so the user can scroll up",
	status: "active",
	autoContinue: true,
	usage: { activeSeconds: 65, tokensUsed: 2500 },
	sisyphus: true,
	activePath: ".pi/goals/active_goal.md",
	taskList: { tasks: [] },
	verificationContract: undefined,
	tokenBudget: undefined,
};

/** A realistic goal-state progression while a goal runs (spec 2026-08-11). */
function goalStateSequence() {
	return [
		{ label: "3 pending tasks", mutate(g) { g.taskList.tasks = [task("t1", "pending"), task("t2", "pending"), task("t3", "pending")]; }, events: [] },
		{ label: "+1 complete (activity feed grows)", mutate(g) { g.taskList.tasks = [task("t1", "complete"), task("t2", "pending"), task("t3", "pending")]; }, events: [ledger("task_complete", "2026-08-10T00:01:00Z", { taskId: "t1" })] },
		{ label: "6 tasks, feed 3", mutate(g) { g.taskList.tasks = [task("t1", "complete"), task("t2", "complete"), task("t3", "complete"), task("t4", "pending"), task("t5", "pending"), task("t6", "pending")]; }, events: [ledger("task_complete", "2026-08-10T00:01:00Z", { taskId: "t1" }), ledger("task_complete", "2026-08-10T00:02:00Z", { taskId: "t2" }), ledger("task_complete", "2026-08-10T00:03:00Z", { taskId: "t3" })] },
		{ label: "12 tasks (crosses the cap)", mutate(g) { g.taskList.tasks = Array.from({ length: 12 }, (_, i) => task(`t${i}`, i < 5 ? "complete" : "pending")); }, events: [ledger("task_complete", "2026-08-10T00:01:00Z", { taskId: "t1" }), ledger("task_complete", "2026-08-10T00:02:00Z", { taskId: "t2" }), ledger("task_complete", "2026-08-10T00:03:00Z", { taskId: "t3" }), ledger("task_complete", "2026-08-10T00:04:00Z", { taskId: "t4" }), ledger("task_complete", "2026-08-10T00:05:00Z", { taskId: "t5" })] },
		{ label: "verification contract added", mutate(g) { g.verificationContract = "Run npm test (0 failures) and re-read every requirement before finishing"; }, events: [] },
		{ label: "budget configured", mutate(g) { g.tokenBudget = 200000; }, events: [] },
		{ label: "usage tick (content only)", mutate(g) { g.usage = { activeSeconds: 2000, tokensUsed: 2500 }; }, events: [] },
		{ label: "current task gains contract + evidence", mutate(g) { g.taskList.tasks = g.taskList.tasks.map((t) => t.id === "t6" ? { ...t, verificationContract: "Run the full test suite", evidence: "npm test: 773 tests, 0 failures; tsc clean" } : t); }, events: [] },
	];
}

/**
 * Real pi frame: ScrollView transcript (documentContainer = header + chat)
 * above a VStack dock [pending, status, widgetContainerAbove, editor,
 * footer] — the exact interactive-mode regular-mode geometry.
 */
function makeFrame(tui, { chatLines, widgetComponent }) {
	const documentContainer = new Container();
	const header = new Container();
	header.render = () => ["pi • model • cwd"];
	const chat = new Container();
	chat.render = () => Array.from({ length: chatLines }, (_, i) => `chat line ${i} ${"x".repeat(30)}`);
	documentContainer.addChild(header);
	documentContainer.addChild(chat);
	const transcript = new ScrollView(documentContainer, { follow: "end", primary: true, overscroll: "chain" });

	const pending = new Container();
	pending.render = () => [];
	const status = new Container();
	status.render = () => ["⠋ Working..."];
	const widgetContainer = new Container();
	widgetContainer.addChild(widgetComponent);
	const editor = new Container();
	editor.render = () => ["❯ ", ""];
	const footer = new Container();
	footer.render = () => ["─ footer ─"];
	const dock = new VStack([pending, status, widgetContainer, editor, footer]);

	tui.addChild(transcript);
	tui.addChild(dock);
	return { transcript, dock };
}

function makeWidget(tui, { expanded, currentRef, eventsRef, auditorRef, resultRef }) {
	return new GoalWidgetComponent({
		tui,
		theme,
		getGoal: () => currentRef.current,
		getOpenGoalCount: () => 1,
		getSettings: () => ({}),
		getExpanded: () => expanded.current,
		getLedgerEvents: () => eventsRef.current,
		getAuditorProgress: () => (auditorRef ? auditorRef.current : null),
		getAuditResult: () => (resultRef ? resultRef.current : null),
	});
}

let failures = 0;
let demoFailures = 0;
function check(cond, label, detail) {
	if (!cond) {
		failures++;
		console.log(`  ✗ FAIL: ${label}${detail ? ` (${detail})` : ""}`);
	} else {
		console.log(`  ✓ ${label}`);
	}
}
function demoCheck(cond, label, detail) {
	if (!cond) {
		demoFailures++;
		console.log(`  ✗ (expected pre-fix) ${label}${detail ? ` (${detail})` : ""}`);
	} else {
		console.log(`  ✓ ${label}`);
	}
}

function setup(rows) {
	const { terminal, writes } = makeTerminal(rows);
	const tui = new TuiMainScreen(terminal, false, "/tmp/tui-widget-bound");
	return { terminal, writes, tui };
}

function frameLines(tui) {
	return (tui.previousLines ?? []).map(stripAnsi);
}

/** Widget rendered height = rows reserved in the frame between the status
 * line and the editor ("❯ "). Sticky-cap blank padding counts as widget rows
 * (the widget component rendered them). */
function widgetSpan(frame, widgetStart) {
	const editorIdx = frame.findIndex((line, i) => i >= widgetStart && line.startsWith("❯"));
	return editorIdx >= 0 ? editorIdx - widgetStart : Math.max(0, frame.length - widgetStart);
}

// ── Scenario: sticky-cap steady-state stability (spec 2026-08-11) ───────────

function runStickySteadyState() {
	console.log(`\n── sticky cap: steady-state stability across goal-state changes (24-row terminal, expanded) ──`);
	const { terminal, writes, tui } = setup(24);
	const currentRef = { current: structuredClone(baseGoal) };
	const eventsRef = { current: [] };
	const expanded = { current: true };
	const component = makeWidget(tui, { expanded, currentRef, eventsRef });
	makeFrame(tui, { chatLines: 10, widgetComponent: component });
	tui.doRender();
	writes.length = 0;

	const seq = goalStateSequence();
	const cap = Math.max(1, 24 - DOCK_RESERVE);
	let prevHeight = undefined;
	let prevFrameLen = undefined;
	let heights = [];
	let frameLens = [];
	let stable = true;
	for (const step of seq) {
		step.mutate(currentRef.current);
		eventsRef.current = step.events;
		component.invalidate();
		tui.doRender();
		writes.length = 0;
		const frame = frameLines(tui);
		const widgetStart = 1 + 10 + 1; // header + chat + status
		const h = widgetSpan(frame, widgetStart);
		heights.push(h);
		frameLens.push(frame.length);
		if (h > cap) {
			console.log(`  ✗ FAIL: widget rendered ${h} lines > cap ${cap} at "${step.label}"`);
			failures++;
		}
		// The latch engages at the FIRST render of the regime: the widget
		// rendered height and the buffer line count are constant across every
		// goal-state change (fits or capped).
		if (prevHeight !== undefined && h !== prevHeight) stable = false;
		if (prevFrameLen !== undefined && frame.length !== prevFrameLen) stable = false;
		prevHeight = h;
		prevFrameLen = frame.length;
		const upd = analyzeForWipes(writes.join(""));
		if (upd.clear2J !== 0 || upd.clear3J !== 0 || upd.alt1049 !== 0) {
			console.log(`  ✗ FAIL: wipe at "${step.label}" (2J=${upd.clear2J}, 3J=${upd.clear3J})`);
			failures++;
		}
	}
	check(stable, `widget rendered height + buffer line count CONSTANT across all goal-state changes`, `heights=${heights.join(",")}`);
	check(heights.every((h) => h <= cap), `every rendered height <= cap ${cap}`);
	console.log(`  heights per state: ${heights.join(" → ")}`);
}

// ── Scenario: fits case — first render byte-identical, then latched ────────

function runFitsByteIdentical() {
	console.log(`\n── fits case: widget never exceeds the cap; first render byte-identical, then the height latches (30-row terminal, expanded, 3 tasks) ──`);
	const { terminal, writes, tui } = setup(30);
	const currentRef = { current: {
		...structuredClone(baseGoal),
		taskList: { tasks: [task("t1", "pending"), task("t2", "pending"), task("t3", "pending")] },
	} };
	const eventsRef = { current: [] };
	const expanded = { current: true };
	const component = makeWidget(tui, { expanded, currentRef, eventsRef });
	makeFrame(tui, { chatLines: 4, widgetComponent: component });
	tui.doRender();
	writes.length = 0;

	const natural = component.render(COLS);
	const cap = Math.max(1, 30 - DOCK_RESERVE);
	check(natural.length <= cap, `natural height ${natural.length} fits (cap ${cap})`);
	const frame = frameLines(tui);
	const widgetStart = 1 + 4 + 1;
	const widgetLines = frame.slice(widgetStart, widgetStart + natural.length);
	check(widgetLines.join("\n") === natural.join("\n"), "rendered widget == natural (byte-identical)");
	// state update stays differential, height unchanged
	currentRef.current.usage = { activeSeconds: 2000, tokensUsed: 2500 };
	component.invalidate();
	tui.doRender();
	const upd = analyzeForWipes(writes.join(""));
	writes.length = 0;
	check(upd.clear2J === 0 && upd.clear3J === 0, `fits-case update emits no 2J/3J (2J=${upd.clear2J}, 3J=${upd.clear3J})`);
	const frame2 = frameLines(tui);
	const h2 = widgetSpan(frame2, widgetStart);
	check(h2 === natural.length, `fits-case height unchanged after update (${h2} == ${natural.length})`);

	// growth (activity feed + progress) must NOT change the rendered height —
	// the fits case is latched at the first-render natural (spec revision)
	eventsRef.current = [{ type: "task_complete", at: "2026-08-10T00:01:00Z", goalId: baseGoal.id, taskId: "t1" }];
	currentRef.current = { ...currentRef.current, taskList: { tasks: [task("t1", "complete"), task("t2", "pending"), task("t3", "pending")] } };
	component.invalidate();
	tui.doRender();
	const upd2 = analyzeForWipes(writes.join(""));
	writes.length = 0;
	const frame3 = frameLines(tui);
	const h3 = widgetSpan(frame3, widgetStart);
	check(upd2.clear2J === 0 && upd2.clear3J === 0, `fits-case growth update emits no 2J/3J (2J=${upd2.clear2J}, 3J=${upd2.clear3J})`);
	check(h3 === natural.length, `fits-case growth keeps the latched height (${h3} == ${natural.length})`);
}

// ── Scenario: resize adaptation ─────────────────────────────────────────────

function runResizeAdaptation() {
	console.log(`\n── sticky cap: terminal resize adapts (grow shows more widget, shrink re-latches) ──`);
	const { terminal, writes, tui } = setup(24);
	const currentRef = { current: {
		...structuredClone(baseGoal),
		taskList: { tasks: Array.from({ length: 12 }, (_, i) => task(`t${i}`, i < 5 ? "complete" : "pending")) },
	} };
	const eventsRef = { current: [] };
	const expanded = { current: true };
	const component = makeWidget(tui, { expanded, currentRef, eventsRef });
	makeFrame(tui, { chatLines: 4, widgetComponent: component });
	tui.doRender();
	writes.length = 0;

	const widgetStart = 1 + 4 + 1;
	const cap24 = Math.max(1, 24 - DOCK_RESERVE);
	const h24 = widgetSpan(frameLines(tui), widgetStart);
	check(h24 === cap24, `latched at cap ${cap24} on a 24-row terminal (rendered ${h24})`);

	terminal.rows = 30;
	tui.doRender();
	writes.length = 0;
	const cap30 = Math.max(1, 30 - DOCK_RESERVE);
	const natural30 = component.render(COLS).length;
	const h30 = widgetSpan(frameLines(tui), widgetStart);
	check(h30 === Math.min(natural30, cap30), `grow to 30 rows: rendered ${h30} == min(natural ${natural30}, new cap ${cap30}) (more widget revealed)`);

	terminal.rows = 24;
	tui.doRender();
	writes.length = 0;
	const hBack = widgetSpan(frameLines(tui), widgetStart);
	check(hBack === cap24, `shrink to 24 rows: rendered ${hBack} == cap ${cap24} again`);
	currentRef.current.usage = { activeSeconds: 5000, tokensUsed: 3000 };
	component.invalidate();
	tui.doRender();
	const upd = analyzeForWipes(writes.join(""));
	writes.length = 0;
	check(upd.clear2J === 0 && upd.clear3J === 0, `post-resize update emits no 2J/3J (2J=${upd.clear2J}, 3J=${upd.clear3J})`);
	const hFinal = widgetSpan(frameLines(tui), widgetStart);
	check(hFinal === cap24, `height stable after resize + update (${hFinal})`);
}

// ── Scenario: regime reset ──────────────────────────────────────────────────

function runRegimeReset() {
	console.log(`\n── sticky cap: regime change (compact↔expanded) re-evaluates from natural ──`);
	const { terminal, writes, tui } = setup(24);
	const currentRef = { current: {
		...structuredClone(baseGoal),
		taskList: { tasks: Array.from({ length: 12 }, (_, i) => task(`t${i}`, i < 5 ? "complete" : "pending")) },
	} };
	const eventsRef = { current: [] };
	const expanded = { current: true };
	const component = makeWidget(tui, { expanded, currentRef, eventsRef });
	makeFrame(tui, { chatLines: 4, widgetComponent: component });
	tui.doRender();
	writes.length = 0;
	const widgetStart = 1 + 4 + 1;
	const cap = Math.max(1, 24 - DOCK_RESERVE);
	const hExpanded = widgetSpan(frameLines(tui), widgetStart);
	check(hExpanded === cap, `expanded latched at cap ${cap} (rendered ${hExpanded})`);

	expanded.current = false;
	component.invalidate();
	tui.doRender();
	writes.length = 0;
	const compactNatural = component.render(COLS).length;
	const hCompact = widgetSpan(frameLines(tui), widgetStart);
	check(hCompact === compactNatural && hCompact < cap, `compact re-evaluated from natural (${hCompact} == natural ${compactNatural} < cap ${cap})`);

	expanded.current = true;
	component.invalidate();
	tui.doRender();
	writes.length = 0;
	const hAgain = widgetSpan(frameLines(tui), widgetStart);
	check(hAgain === cap, `expanded re-latched at cap ${cap} (rendered ${hAgain})`);
}

// ── Scenario: audit dashboard at a small terminal ───────────────────────────

function runAuditSticky() {
	const cap = Math.max(1, 13 - DOCK_RESERVE);
	console.log(`\n── sticky cap: audit dashboard (13-row terminal, cap ${cap}) ──`);
	const { terminal, writes, tui } = setup(13);
	const currentRef = { current: structuredClone(baseGoal) };
	const eventsRef = { current: [] };
	const expanded = { current: true };
	const auditorRef = { current: null };
	const component = makeWidget(tui, { expanded, currentRef, eventsRef, auditorRef });
	makeFrame(tui, { chatLines: 2, widgetComponent: component });
	tui.doRender();
	writes.length = 0;
	const widgetStart = 1 + 2 + 1;
	let heights = [];
	let frameLens = [];
	const phases = [
		{ phase: "running", elapsedMs: 0, recentOutput: [], currentTool: undefined, percentage: 20 },
		{ phase: "tool_executing", elapsedMs: 8000, recentOutput: ["output line 1"], currentTool: "npm test", percentage: 60 },
		{ phase: "producing_report", elapsedMs: 30000, recentOutput: ["o1", "o2", "o3"], currentTool: "tsc", percentage: 90 },
		{ phase: "done", elapsedMs: 42000, recentOutput: [], currentTool: undefined, percentage: 100 },
	];
	let stable = true;
	let prev = undefined;
	for (const p of phases) {
		auditorRef.current = { ...p, auditorLabel: "anthropic/claude-sonnet-4", phase: p.phase };
		component.invalidate();
		tui.doRender();
		writes.length = 0;
		const h = widgetSpan(frameLines(tui), widgetStart);
		heights.push(h);
		if (h > cap) { console.log(`  ✗ FAIL: audit rendered ${h} > cap ${cap}`); failures++; }
		if (prev !== undefined && h !== prev) stable = false;
		prev = h;
		const upd = analyzeForWipes(writes.join(""));
		if (upd.clear2J !== 0 || upd.clear3J !== 0) { console.log(`  ✗ FAIL: audit wipe at "${p.phase}"`); failures++; }
	}
	check(stable, `audit rendered height constant across phases`, `heights=${heights.join(",")}`);
	check(heights.every((h) => h <= cap), `audit rendered heights <= cap ${cap}`);
}

// ── Existing scenarios (2026-08-10 invariants) ──────────────────────────────

function runScenario({ label, chatLines, expanded, preFix, rows, demo }) {
	console.log(`\n── ${label}${demo ? " (demonstration only)" : ""} ──`);
	const { terminal, writes, tui } = setup(rows);
	const currentRef = { current: { ...structuredClone(baseGoal), taskList: { tasks: Array.from({ length: 12 }, (_, i) => task(`t${i}`, i < 5 ? "complete" : "pending")) } } };
	const eventsRef = { current: [] };
	const expandedRef = { current: expanded };
	// preFix: hide `terminal` from the widget so it renders unbounded (the
	// pre-bound behavior); everything else delegates to the real TUI.
	const widgetTui = preFix
		? new Proxy(tui, {
			get: (t, p) => {
				if (p === "terminal") return undefined;
				const v = Reflect.get(t, p);
				return typeof v === "function" ? v.bind(t) : v;
			},
		})
		: tui;
	const component = makeWidget(widgetTui, { expanded: expandedRef, currentRef, eventsRef });
	makeFrame(tui, { chatLines, widgetComponent: component });
	tui.doRender();
	writes.length = 0;

	const frame = frameLines(tui);
	const widgetStart = 1 + chatLines + 1; // header + chat + status
	const widgetNatural = component.render(COLS).length; // widget's own render (unbounded comparison)
	const frameLen = frame.length;
	const viewportTop = Math.max(0, frameLen - rows);
	const cap = Math.max(1, rows - DOCK_RESERVE);
	const renderedWidget = Math.min(widgetNatural, Math.max(0, frameLen - widgetStart));
	const chatAboveViewport = chatLines > 0 ? frameLen > rows : false;
	const footerIdx = frameLen - 1;
	const footerVisible = footerIdx >= viewportTop;
	const editorVisible = frameLen - 2 >= viewportTop;

	const maybeCheck = demo ? demoCheck : check;
	maybeCheck(renderedWidget <= cap, `widget rendered ${renderedWidget} lines <= cap ${cap}`, `frame=${frameLen}`);
	maybeCheck(chatAboveViewport || chatLines === 0, `chat reachable above viewport (chat=${chatLines}, viewportTop=${viewportTop}, frame=${frameLen})`);
	maybeCheck(editorVisible && footerVisible, `editor+footer visible (editorAt=${frameLen - 2}, footerAt=${footerIdx}, viewportTop=${viewportTop})`);
	maybeCheck(frame[widgetStart]?.startsWith("╭─ pi-goal-x"), "widget header preserved");
	maybeCheck(frame[widgetStart + 1]?.includes("goal:"), "widget status line preserved");

	const next = { ...currentRef.current, updatedAt: "2026-08-10T00:02:00Z", usage: { activeSeconds: 66, tokensUsed: 2600 } };
	currentRef.current = next;
	component.invalidate();
	tui.doRender();
	const updateWrites = writes.join("");
	writes.length = 0;
	const upd = analyzeForWipes(updateWrites);
	maybeCheck(upd.clear2J === 0 && upd.clear3J === 0 && upd.alt1049 === 0,
		`widget update emits no 2J/3J/1049 (2J=${upd.clear2J}, 3J=${upd.clear3J})`,
		`bytes=${updateWrites.length}`);

	return { frameLen, viewportTop, renderedWidget, cap, natural: widgetNatural };
}

// Scenarios
runStickySteadyState();
runFitsByteIdentical();
runResizeAdaptation();
runRegimeReset();
runAuditSticky();
runScenario({ label: "equal-height, chat present (24-row terminal, expanded dashboard)", chatLines: 10, expanded: true, preFix: false, rows: 24 });
runScenario({ label: "equal-height, chat present — PRE-FIX comparison (unbounded widget)", chatLines: 10, expanded: true, preFix: true, rows: 24, demo: true });
runScenario({ label: "equal-height, empty chat (24-row terminal, expanded dashboard)", chatLines: 0, expanded: true, preFix: false, rows: 24 });
runScenario({ label: "equal-height, compact dashboard (13-row terminal)", chatLines: 6, expanded: false, preFix: false, rows: 13 });
runScenario({ label: "normal terminal, expanded dashboard (30 rows — fits, must be unchanged)", chatLines: 4, expanded: true, preFix: false, rows: 30 });

if (expectMode) {
	if (failures > 0) {
		console.log(`\nFAIL: ${failures} assertion(s) failed`);
		process.exit(1);
	}
	console.log("\nOK: all assertions passed");
	process.exit(0);
} else {
	console.log(`\nreport mode — ${failures} failures`);
}
