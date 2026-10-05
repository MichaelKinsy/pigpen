// Full reproduction: resize the terminal below the widget's height
// (both unexpanded and expanded), then keep driving goal-state updates and
// periodic re-renders (spinner-style), capturing the REAL byte stream from
// the real TuiMainScreen and counting everything that could re-pin or wipe
// the terminal:
//   - \x1b[2J / \x1b[3J (full-render scrollback wipe)
//   - frame length changes (buffer line-count churn -> emulator re-pin)
//   - widget rendered height
//   - "\r\n" sequences emitted while the cursor sits on the bottom row
//     (emulator scroll-to-bottom)
//   - pi's internal fullRender triggers (firstChanged < viewportTop, etc.)
//
// Usage:
//   node experiments/scroll-repro/resize-repro.mjs            # report
//   node experiments/scroll-repro/resize-repro.mjs --expect   # assertions
//   node experiments/scroll-repro/resize-repro.mjs --full     # dump all streams

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
const expectMode = process.argv.includes("--expect");
const fullDump = process.argv.includes("--full");
const theme = { fg: (_c, s) => s, bg: (_c, s) => s, bold: (s) => s, dim: (s) => s };
const stripAnsi = (s) => s.replace(/\x1b\[[0-9;?]*[a-zA-Z]/g, "").replace(/\x1b\][^\x07]*\x07/g, "");
const T = "2026-08-10T00:00:00Z";

function task(id, status, extra = {}) {
	return { id, title: `Task ${id} with a reasonably long title for wrapping tests`, status, createdAt: T, updatedAt: T, completedAt: status === "complete" ? T : undefined, ...extra };
}
function ledger(type, at, extra = {}) { return { type, at, goalId: "g1", ...extra }; }

const baseGoal = {
	id: "g1", createdAt: T, updatedAt: T,
	objective: "=== Goal ===\nObjective: Fix the scrollback issue so the user can scroll up",
	status: "active", autoContinue: true, sisyphus: true,
	activePath: ".pi/goals/active_goal.md",
	usage: { activeSeconds: 65, tokensUsed: 2500 },
	taskList: { tasks: [] }, verificationContract: undefined, tokenBudget: undefined,
};

function makeTerminal(rows) {
	const writes = [];
	const terminal = {
		columns: COLS, rows,
		write(data) { writes.push(String(data)); },
		hideCursor() {}, showCursor() {}, start() {}, stop() {}, setTitle() {},
	};
	return {
		terminal, writes,
		tui: new TuiMainScreen(terminal, false, "/tmp/tui-resize-repro"),
	};
}

function analyze(stream) {
	let clear2J = 0, clear3J = 0, alt1049 = 0, bottomNewlines = 0;
	const s = String(stream);
	let i = 0;
	while (i < s.length) {
		if (s.startsWith("\x1b[2J", i)) { clear2J++; i += 4; continue; }
		if (s.startsWith("\x1b[3J", i)) { clear3J++; i += 4; continue; }
		if (s.startsWith("\x1b[?1049", i)) { alt1049++; i += 7; continue; }
		i++;
	}
	// A "\r\n" pair that lands on the terminal's bottom row pushes a line and
	// scrolls the viewport (the emulator's scroll-to-bottom trigger). We can't
	// know the emulator's cursor position from bytes alone, but any
	// differential write that ENDS a "\r\n" run on the frame's last line is a
	// candidate; count every \r\n for now and flag streams that contain them
	// after a widget-only update.
	const rn = (s.match(/\r\n/g) ?? []).length;
	return { clear2J, clear3J, alt1049, crlf: rn };
}

let failures = 0;
function check(cond, label, detail) {
	if (!cond) { failures++; console.log(`  ✗ FAIL: ${label}${detail ? ` (${detail})` : ""}`); }
	else console.log(`  ✓ ${label}`);
}

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

function frameLines(tui) { return (tui.previousLines ?? []).map(stripAnsi); }
function widgetSpan(frame, widgetStart) {
	const editorIdx = frame.findIndex((l, i) => i >= widgetStart && l.startsWith("❯"));
	return editorIdx >= 0 ? editorIdx - widgetStart : Math.max(0, frame.length - widgetStart);
}

function goalStateSequence(expanded) {
	const steps = [
		{ label: "task completes (activity feed +1)", mutate(g) { g.taskList.tasks = [task("t1", "complete"), ...Array.from({ length: 11 }, (_, i) => task(`t${i + 2}`, "pending"))]; }, events: [ledger("task_complete", "2026-08-10T00:01:00Z", { taskId: "t1" })] },
		{ label: "usage tick (content only)", mutate(g) { g.usage = { activeSeconds: 2000, tokensUsed: 2500 }; }, events: [] },
		{ label: "activity feed grows to 3", mutate(g) { g.usage = { activeSeconds: 4100, tokensUsed: 3800 }; }, events: [ledger("task_complete", "2026-08-10T00:01:00Z", { taskId: "t1" }), ledger("task_complete", "2026-08-10T00:02:00Z", { taskId: "t2" }), ledger("task_complete", "2026-08-10T00:03:00Z", { taskId: "t3" })] },
		{ label: "usage tick 2", mutate(g) { g.usage = { activeSeconds: 6100, tokensUsed: 5900 }; }, events: [] },
		{ label: "budget configured", mutate(g) { g.tokenBudget = 200000; }, events: [] },
		{ label: "usage tick 3", mutate(g) { g.usage = { activeSeconds: 8100, tokensUsed: 7400 }; }, events: [] },
		{ label: "current task gains contract + evidence (wrap)", mutate(g) { g.taskList.tasks = g.taskList.tasks.map((t) => t.id === "t2" ? { ...t, verificationContract: "Run the full test suite (0 failures)", evidence: "npm test: 781 tests, 0 failures; tsc clean" } : t); }, events: [] },
		{ label: "usage tick 4", mutate(g) { g.usage = { activeSeconds: 10100, tokensUsed: 9100 }; }, events: [] },
	];
	return steps;
}

// ── Scenario A: EXPANDED widget — resize 40 -> 24 (below natural height) ────

function runExpandedResize() {
	console.log(`\n── EXPANDED: terminal 40 -> 24 rows (below the widget's natural height), then goal updates ──`);
	const cap24 = Math.max(1, 24 - DOCK_RESERVE);
	const { terminal, writes, tui } = makeTerminal(40);
	const currentRef = { current: { ...structuredClone(baseGoal), taskList: { tasks: Array.from({ length: 12 }, (_, i) => task(`t${i}`, i < 5 ? "complete" : "pending")) } } };
	const eventsRef = { current: [] };
	const expanded = { current: true };
	const component = new GoalWidgetComponent({
		tui, theme,
		getGoal: () => currentRef.current,
		getOpenGoalCount: () => 1,
		getSettings: () => ({}),
		getExpanded: () => expanded.current,
		getLedgerEvents: () => eventsRef.current,
	});
	makeFrame(tui, { chatLines: 10, widgetComponent: component });

	// Initial render at 40 rows (widget fits).
	tui.doRender();
	writes.length = 0;
	let frame = frameLines(tui);
	const start = 1 + 10 + 1;
	const naturalAt40 = widgetSpan(frame, start);
	console.log(`  at 40 rows: widget rendered ${naturalAt40} lines (cap ${Math.max(1, 40 - DOCK_RESERVE)}), frame ${frame.length} lines`);

	// RESIZE to 24 rows.
	terminal.rows = 24;
	tui.doRender();
	const resizeStream = writes.join("");
	writes.length = 0;
	const resizeAn = analyze(resizeStream);
	console.log(`  resize write: bytes=${resizeStream.length} 2J=${resizeAn.clear2J} 3J=${resizeAn.clear3J} crlf=${resizeAn.crlf}`);
	frame = frameLines(tui);
	let widgetH = widgetSpan(frame, start);
	console.log(`  after resize: widget rendered ${widgetH} lines (cap ${cap24}), frame ${frame.length} lines, viewportTop=${tui.previousViewportTop}`);
	check(widgetH <= cap24, `widget capped at ${cap24} after the resize`, `rendered ${widgetH}`);

	// Drive goal-state updates + spinner-style re-renders.
	const seq = goalStateSequence(true);
	let wipes = 0, churn = 0, prevLen = frame.length, prevWidgetH = widgetH;
	const updateReports = [];
	for (const step of seq) {
		step.mutate(currentRef.current);
		eventsRef.current = step.events;
		component.invalidate();
		tui.doRender();
		const stream = writes.join("");
		writes.length = 0;
		const an = analyze(stream);
		if (an.clear2J || an.clear3J || an.alt1049) wipes++;
		frame = frameLines(tui);
		widgetH = widgetSpan(frame, start);
		if (frame.length !== prevLen) churn++;
		const update = {
			label: step.label, bytes: stream.length, widgetH, frameLen: frame.length,
			wipes: an.clear2J + an.clear3J + an.alt1049, crlf: an.crlf,
			firstChanged: tui.previousViewportTop,
		};
		updateReports.push(update);
		prevLen = frame.length;
		if (widgetH !== prevWidgetH) console.log(`  ✗ widget height changed ${prevWidgetH} -> ${widgetH} at "${step.label}"`);
		prevWidgetH = widgetH;
	}
	check(wipes === 0, `goal updates after resize emit NO 2J/3J/1049`, `wipes=${wipes}`);
	check(churn === 0, `frame length CONSTANT across updates after resize`, `churn=${churn}`);
	check(updateReports.every((u) => u.widgetH === cap24), `widget rendered height constant at ${cap24}`, updateReports.map((u) => u.widgetH).join(","));
	console.log(`  per-update: ${updateReports.map((u) => `${u.label.split(" (")[0]}:h${u.widgetH}/f${u.frameLen}/b${u.bytes}/w${u.wipes}`).join("\n              ")}`);
	if (fullDump) {
		console.log("── full write stream after resize (first update) ──");
		// Re-run first update to capture its stream for inspection.
		const step = seq[0];
		step.mutate(currentRef.current);
		eventsRef.current = step.events;
		component.invalidate();
		tui.doRender();
		const s = writes.join("").replace(/\x1b/g, "␛");
		writes.length = 0;
		console.log(s.length > 2000 ? s.slice(0, 2000) + "\n…(truncated)" : s);
	}
}

// ── Scenario B: UNEXPANDED widget — resize below its natural height ─────────

function runCompactResize() {
	console.log(`\n── UNEXPANDED: terminal 30 -> 14 rows (below the widget's natural height), then goal updates ──`);
	const cap14 = Math.max(1, 14 - DOCK_RESERVE); // 8
	const { terminal, writes, tui } = makeTerminal(30);
	const currentRef = { current: { ...structuredClone(baseGoal), taskList: { tasks: Array.from({ length: 6 }, (_, i) => task(`t${i}`, i < 3 ? "complete" : "pending")) } } };
	const eventsRef = { current: [] };
	const expanded = { current: false };
	const component = new GoalWidgetComponent({
		tui, theme,
		getGoal: () => currentRef.current,
		getOpenGoalCount: () => 1,
		getSettings: () => ({}),
		getExpanded: () => expanded.current,
		getLedgerEvents: () => eventsRef.current,
	});
	makeFrame(tui, { chatLines: 6, widgetComponent: component });

	tui.doRender();
	writes.length = 0;
	let frame = frameLines(tui);
	const start = 1 + 6 + 1;
	const naturalAt30 = widgetSpan(frame, start);
	console.log(`  at 30 rows: widget rendered ${naturalAt30} lines (cap ${Math.max(1, 30 - DOCK_RESERVE)})`);

	terminal.rows = 14;
	tui.doRender();
	const resizeStream = writes.join("");
	writes.length = 0;
	const resizeAn = analyze(resizeStream);
	console.log(`  resize write: bytes=${resizeStream.length} 2J=${resizeAn.clear2J} 3J=${resizeAn.clear3J} crlf=${resizeAn.crlf}`);
	frame = frameLines(tui);
	let widgetH = widgetSpan(frame, start);
	console.log(`  after resize: widget rendered ${widgetH} lines (cap ${cap14}), frame ${frame.length} lines, viewportTop=${tui.previousViewportTop}`);
	check(widgetH <= cap14, `widget capped at ${cap14} after the resize`, `rendered ${widgetH}`);

	const seq = goalStateSequence(false).slice(0, 5);
	let wipes = 0, churn = 0, prevLen = frame.length, prevWidgetH = widgetH;
	for (const step of seq) {
		step.mutate(currentRef.current);
		eventsRef.current = step.events;
		component.invalidate();
		tui.doRender();
		const an = analyze(writes.join(""));
		writes.length = 0;
		if (an.clear2J || an.clear3J || an.alt1049) wipes++;
		frame = frameLines(tui);
		widgetH = widgetSpan(frame, start);
		if (frame.length !== prevLen) churn++;
		if (widgetH !== prevWidgetH) console.log(`  ✗ widget height changed ${prevWidgetH} -> ${widgetH} at "${step.label}"`);
		prevLen = frame.length;
		prevWidgetH = widgetH;
	}
	check(wipes === 0, `goal updates after resize emit NO 2J/3J/1049`, `wipes=${wipes}`);
	check(churn === 0, `frame length CONSTANT across updates after resize`, `churn=${churn}`);
}

// ── Scenario C: periodic spinner-style re-renders after the resize ──────────

function runSpinnerAfterResize() {
	console.log(`\n── EXPANDED: spinner-style periodic re-renders (status line + usage tick) after resize to 24 ──`);
	const cap24 = Math.max(1, 24 - DOCK_RESERVE);
	const { terminal, writes, tui } = makeTerminal(40);
	const currentRef = { current: { ...structuredClone(baseGoal), taskList: { tasks: Array.from({ length: 12 }, (_, i) => task(`t${i}`, i < 5 ? "complete" : "pending")) } } };
	const eventsRef = { current: [] };
	const expanded = { current: true };
	const component = new GoalWidgetComponent({
		tui, theme,
		getGoal: () => currentRef.current,
		getOpenGoalCount: () => 1,
		getSettings: () => ({}),
		getExpanded: () => expanded.current,
		getLedgerEvents: () => eventsRef.current,
	});
	const frames = makeFrame(tui, { chatLines: 10, widgetComponent: component });
	tui.doRender();
	writes.length = 0;
	terminal.rows = 24;
	tui.doRender();
	writes.length = 0;
	const start = 1 + 10 + 1;

	// Every tick: the status line changes (spinner frame) AND the widget's
	// usage content changes (live display seconds) — a full tree re-render.
	let wipes = 0, churn = 0, prevLen = (frameLines(tui)).length;
	const spins = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];
	let usage = 65;
	for (let tick = 0; tick < 12; tick++) {
		const status = frames.dock.children[1];
		status.render = () => [`${spins[tick % spins.length]} Working...`];
		usage += 40;
		currentRef.current = { ...currentRef.current, usage: { activeSeconds: usage, tokensUsed: usage * 3 } };
		component.invalidate();
		tui.doRender();
		const an = analyze(writes.join(""));
		writes.length = 0;
		if (an.clear2J || an.clear3J || an.alt1049) wipes++;
		const frame = frameLines(tui);
		if (frame.length !== prevLen) churn++;
		prevLen = frame.length;
		const w = widgetSpan(frame, start);
		if (w !== cap24) console.log(`  ✗ tick ${tick}: widget height ${w} (expected ${cap24})`);
	}
	check(wipes === 0, `12 spinner ticks after resize emit NO 2J/3J/1049`, `wipes=${wipes}`);
	check(churn === 0, `frame length CONSTANT across 12 spinner ticks`, `churn=${churn}`);
}

runExpandedResize();
runCompactResize();
runSpinnerAfterResize();

if (expectMode) {
	console.log(`\n${failures === 0 ? "OK: all resize assertions passed" : `FAIL: ${failures} assertion(s) failed`}`);
	process.exit(failures === 0 ? 0 : 1);
}
