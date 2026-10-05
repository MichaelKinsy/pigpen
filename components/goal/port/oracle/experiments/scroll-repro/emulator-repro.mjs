// FULL emulator-level reproduction: pi's real frame (TuiMainScreen +
// ScrollView transcript + VStack dock + real GoalWidgetComponent) writing
// into a REAL terminal emulator (@xterm/headless — the same VT engine as
// xterm.js/ghostty-class emulators), with a user who has scrolled up.
//
// Scenarios:
//   A. Terminal BIGGER than the widget (widget fits) — the user's new
//      finding: goal updates keep yanking the viewport to the bottom.
//   B. Resize the terminal BELOW the widget's natural height (expanded),
//      then goal updates.
//   C. Resize below the widget (unexpanded), then goal updates.
//   D. Scroll-up hold: with the user scrolled up, a goal update must not
//      change the buffer's line count (the multiplexer/emulator yank
//      trigger) and must not emit 2J/3J.
//
// Reported per update: widget rendered height, frame length, xterm buffer
// length, baseY, viewportY, and whether the update (a) changed the buffer
// line count [YANK TRIGGER], (b) wiped the scrollback [2J/3J].
//
// Usage:
//   node experiments/scroll-repro/emulator-repro.mjs            # report
//   node experiments/scroll-repro/emulator-repro.mjs --expect   # assertions
//   node experiments/scroll-repro/emulator-repro.mjs --full     # dump streams

import xtermPkg from "@xterm/headless";
const { Terminal } = xtermPkg;
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

function goalStateSequence() {
	return [
		{ label: "task completes (feed +1)", mutate(g) { g.taskList.tasks = [task("t1", "complete"), ...Array.from({ length: 11 }, (_, i) => task(`t${i + 2}`, "pending"))]; }, events: [ledger("task_complete", "2026-08-10T00:01:00Z", { taskId: "t1" })] },
		{ label: "usage tick", mutate(g) { g.usage = { activeSeconds: 2000, tokensUsed: 2500 }; }, events: [] },
		{ label: "feed grows to 3", mutate(g) { g.usage = { activeSeconds: 4100, tokensUsed: 3800 }; }, events: [ledger("task_complete", "2026-08-10T00:01:00Z", { taskId: "t1" }), ledger("task_complete", "2026-08-10T00:02:00Z", { taskId: "t2" }), ledger("task_complete", "2026-08-10T00:03:00Z", { taskId: "t3" })] },
		{ label: "usage tick", mutate(g) { g.usage = { activeSeconds: 6100, tokensUsed: 5900 }; }, events: [] },
		{ label: "budget configured", mutate(g) { g.tokenBudget = 200000; }, events: [] },
		{ label: "usage tick", mutate(g) { g.usage = { activeSeconds: 8100, tokensUsed: 7400 }; }, events: [] },
		{ label: "contract+evidence wrap", mutate(g) { g.taskList.tasks = g.taskList.tasks.map((t) => t.id === "t2" ? { ...t, verificationContract: "Run the full test suite (0 failures)", evidence: "npm test: 781 tests, 0 failures; tsc clean" } : t); }, events: [] },
		{ label: "usage tick", mutate(g) { g.usage = { activeSeconds: 10100, tokensUsed: 9100 }; }, events: [] },
	];
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
function countWipes(stream) {
	let c2 = 0, c3 = 0, alt = 0, i = 0;
	while (i < stream.length) {
		if (stream.startsWith("\x1b[2J", i)) { c2++; i += 4; continue; }
		if (stream.startsWith("\x1b[3J", i)) { c3++; i += 4; continue; }
		if (stream.startsWith("\x1b[?1049", i)) { alt++; i += 7; continue; }
		i++;
	}
	return { c2, c3, alt };
}

let failures = 0;
function check(cond, label, detail) {
	if (!cond) { failures++; console.log(`  ✗ FAIL: ${label}${detail ? ` (${detail})` : ""}`); }
	else console.log(`  ✓ ${label}`);
}

// ── the emulator rig ────────────────────────────────────────────────────────

function makeRig(rows, chatLines, expanded, tasks) {
	const emu = new Terminal({ cols: COLS, rows, scrollback: 2000, allowProposedApi: true });
	const drain = () => new Promise((res) => { emu.onWriteParsed(() => setTimeout(res, 5)); emu.write(""); });
	const pending = { writes: [] };
	const terminal = {
		columns: COLS,
		get rows() { return emu.rows; },
		write(data) { pending.writes.push(String(data)); },
		hideCursor() {}, showCursor() {}, start() {}, stop() {}, setTitle() {}, moveBy() {},
	};
	const tui = new TuiMainScreen(terminal, false, "/tmp/tui-emulator-repro");
	const currentRef = { current: { ...structuredClone(baseGoal), taskList: { tasks } } };
	const eventsRef = { current: [] };
	const component = new GoalWidgetComponent({
		tui, theme,
		getGoal: () => currentRef.current,
		getOpenGoalCount: () => 1,
		getSettings: () => ({}),
		getExpanded: () => expanded.current,
		getLedgerEvents: () => eventsRef.current,
	});
	const frames = makeFrame(tui, { chatLines, widgetComponent: component });
	return {
		emu, drain, pending, tui, component, currentRef, eventsRef, expanded, frames,
		async flush() {
			const stream = pending.writes.join("");
			pending.writes.length = 0;
			if (stream) { emu.write(stream); await drain(); }
			return stream;
		},
	};
}

async function runScenario(name, rig, { steps, cap, widgetStart, expectStable }) {
	console.log(`\n── ${name} ──`);
	const { emu, tui } = rig;
	tui.doRender();
	await rig.flush();
	const b = emu.buffer.active;
	let frame = frameLines(tui);
	let widgetH = widgetSpan(frame, widgetStart);
	let bufLen = b.length, baseY = b.baseY;
	let yankCount = 0, wipeCount = 0, churnCount = 0;
	const rows = [];
	rows.push({ step: "initial", widgetH, frameLen: frame.length, bufLen, baseY });
	for (const step of steps) {
		step.mutate(rig.currentRef.current);
		rig.eventsRef.current = step.events;
		rig.component.invalidate();
		tui.doRender();
		const stream = await rig.flush();
		const w = countWipes(stream);
		if (w.c2 || w.c3 || w.alt) {
			wipeCount++;
			console.log(`  ✗ WIPE at "${step.label}": 2J=${w.c2} 3J=${w.c3}`);
		}
		frame = frameLines(tui);
		widgetH = widgetSpan(frame, widgetStart);
		bufLen = b.length; baseY = b.baseY;
		if (baseY !== rows[rows.length - 1].baseY) churnCount++;
		if (baseY !== rows[rows.length - 1].baseY) yankCount++; // new lines appended -> follow-to-bottom trigger
		rows.push({ step: step.label, widgetH, frameLen: frame.length, bufLen, baseY });
	}
	console.log(`  per-update: ${rows.map((r) => `${r.step.split(" ")[0]}:w${r.widgetH}/f${r.frameLen}/b${r.bufLen}/y${r.baseY}`).join("\n              ")}`);
	console.log(`  widget height constant: ${new Set(rows.map((r) => r.widgetH)).size === 1} (${[...new Set(rows.map((r) => r.widgetH))].join(",")})`);
	console.log(`  buffer baseY churn (yank triggers): ${churnCount}, 2J/3J wipes: ${wipeCount}`);
	check(churnCount === 0, `buffer line count CONSTANT across goal updates (no yank trigger)`, `churn=${churnCount}`);
	check(wipeCount === 0, `NO 2J/3J/1049 emissions`, `wipes=${wipeCount}`);
	check(rows.every((r) => r.widgetH <= cap), `widget height <= cap ${cap}`, rows.map((r) => r.widgetH).join(","));
	if (expectStable) {
		check(new Set(rows.map((r) => r.widgetH)).size === 1, `widget rendered height constant`, rows.map((r) => r.widgetH).join(","));
	}
	return { churnCount, wipeCount, rows };
}

// ── A: NEW FINDING — terminal BIGGER than the widget (fits) ────────────────

async function scenarioAFits() {
	console.log(`\n── A: TERMINAL BIGGER THAN THE WIDGET (expanded, 40-row terminal, cap 34, natural < cap) ──`);
	const expanded = { current: true };
	const tasks = Array.from({ length: 12 }, (_, i) => task(`t${i}`, i < 5 ? "complete" : "pending"));
	const rig = makeRig(40, 30, expanded, tasks);
	const cap = Math.max(1, 40 - DOCK_RESERVE);
	const steps = goalStateSequence();
	const widgetStart = 1 + 30 + 1;
	await runScenario("A: fits case", rig, { steps, cap, widgetStart, expectStable: true });
}

// ── B: resize below the widget height (expanded) ────────────────────────────

async function scenarioBResizeExpanded() {
	console.log(`\n── B: RESIZE BELOW THE WIDGET (expanded: 40 → 24 rows, cap 18) ──`);
	const expanded = { current: true };
	const tasks = Array.from({ length: 12 }, (_, i) => task(`t${i}`, i < 5 ? "complete" : "pending"));
	const rig = makeRig(40, 30, expanded, tasks);
	rig.tui.doRender();
	await rig.flush();
	rig.emu.resize(COLS, 24);
	rig.tui.doRender();
	const resizeStream = await rig.flush();
	const w = countWipes(resizeStream);
	console.log(`  resize write: bytes=${resizeStream.length} 2J=${w.c2} 3J=${w.c3}`);
	const cap = Math.max(1, 24 - DOCK_RESERVE);
	const widgetStart = 1 + 30 + 1;
	await runScenario("B: post-resize", rig, { steps: goalStateSequence(), cap, widgetStart, expectStable: true });
}

// ── C: resize below the widget height (unexpanded) ──────────────────────────

async function scenarioCResizeCompact() {
	console.log(`\n── C: RESIZE BELOW THE WIDGET (unexpanded: 30 → 14 rows, cap 8) ──`);
	const expanded = { current: false };
	const tasks = Array.from({ length: 6 }, (_, i) => task(`t${i}`, i < 3 ? "complete" : "pending"));
	const rig = makeRig(30, 20, expanded, tasks);
	rig.tui.doRender();
	await rig.flush();
	rig.emu.resize(COLS, 14);
	rig.tui.doRender();
	const resizeStream = await rig.flush();
	const w = countWipes(resizeStream);
	console.log(`  resize write: bytes=${resizeStream.length} 2J=${w.c2} 3J=${w.c3}`);
	const cap = Math.max(1, 14 - DOCK_RESERVE);
	const widgetStart = 1 + 20 + 1;
	await runScenario("C: post-resize", rig, { steps: goalStateSequence().slice(0, 5), cap, widgetStart, expectStable: true });
}

// ── D: scroll-up hold — the user's exact experience ─────────────────────────

async function scenarioDScrollHold() {
	console.log(`\n── D: USER SCROLLED UP — do goal updates yank the viewport? (expanded, 40-row terminal) ──`);
	const expanded = { current: true };
	const tasks = Array.from({ length: 12 }, (_, i) => task(`t${i}`, i < 5 ? "complete" : "pending"));
	const rig = makeRig(40, 30, expanded, tasks);
	const { emu, tui } = rig;
	tui.doRender();
	await rig.flush();
	const b = emu.buffer.active;
	console.log(`  initial: buffer=${b.length} baseY=${b.baseY} viewportY=${b.viewportY} (scrollback ${b.baseY} lines)`);
	// The user scrolls up into the chat (10 lines up).
	emu.scrollLines(-10);
	console.log(`  user scrolls up 10: viewportY=${b.viewportY} (${b.baseY - b.viewportY} lines up)`);
	const startBaseY = b.baseY;
	let yanked = false;
	for (const step of goalStateSequence().slice(0, 4)) {
		step.mutate(rig.currentRef.current);
		rig.eventsRef.current = step.events;
		rig.component.invalidate();
		tui.doRender();
		const stream = await rig.flush();
		const w = countWipes(stream);
		if (w.c2 || w.c3 || w.alt) {
			yanked = true;
			console.log(`  ✗ "${step.label}": 2J/3J WIPE (scrollback destroyed)`);
		}
		const baseYDelta = b.baseY - startBaseY;
		const viewportHeld = b.viewportY === Math.max(0, startBaseY - 10);
		console.log(`  "${step.label}": buffer=${b.length} baseY=${b.baseY} (Δ${baseYDelta}) viewportY=${b.viewportY} held=${viewportHeld}`);
		if (baseYDelta !== 0) {
			yanked = true;
			console.log(`    → buffer line count changed: multiplexer/emulator following the pane bottom would re-pin`);
		}
	}
	check(!yanked, `scrolled-up viewport holds across goal updates (no 2J/3J, no buffer growth)`, yanked ? "yank detected" : "clean");
}

// ── run ─────────────────────────────────────────────────────────────────────

await scenarioAFits();
await scenarioBResizeExpanded();
await scenarioCResizeCompact();
await scenarioDScrollHold();

if (expectMode) {
	console.log(`\n${failures === 0 ? "OK: all emulator assertions passed" : `FAIL: ${failures} assertion(s) failed`}`);
	process.exit(failures === 0 ? 0 : 1);
}
