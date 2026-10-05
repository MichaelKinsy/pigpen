// PROBE: when the frame overflows the terminal (the user: "only the widget
// is viewable"), does ANY widget content change trigger pi-tui's full render
// (2J+3J wipe)? pi's condition: firstChanged < previousBufferLength - height
// -> fullRender(true) -> the terminal clears scrollback + redraws -> the
// user is forced to the bottom and N churns.
//
// Uses the same real stack as emulator-repro: TuiMainScreen + ScrollView
// transcript + VStack dock (pending/status/widget/editor/footer) + real
// GoalWidgetComponent into @xterm/headless. Mutates the active goal's usage
// (the per-render "elapsed tick") and counts 2J/3J wipes per height.
import xtermPkg from "@xterm/headless";
const { Terminal } = xtermPkg;
import { ScrollView } from "../../node_modules/@earendil-works/pi-tui/dist/components/scroll-view.js";
import { VStack } from "../../node_modules/@earendil-works/pi-tui/dist/components/v-stack.js";
import { Container } from "../../node_modules/@earendil-works/pi-tui/dist/tui.js";
import { TuiMainScreen } from "../../node_modules/@earendil-works/pi-tui/dist/index.js";
import { GoalWidgetComponent } from "../../extensions/widgets/goal-widget.ts";

const COLS = 100;
const stripAnsi = (s) => s.replace(/\x1b\[[0-9;?]*[a-zA-Z]/g, "");
const baseGoal = {
	id: "probe-1", version: 3, objective: "Probe goal for overflow full-render. " + "x".repeat(50),
	status: "active", autoContinue: false, usage: { tokensUsed: 48213, activeSeconds: 1427 },
	createdAt: "2026-08-11T00:00:00.000Z", updatedAt: "2026-08-11T00:00:00.000Z",
	revision: 3, verificationContract: "Probe", activePath: ".pi/goals/active_goal_probe.md",
	taskList: {
		blockCompletion: false,
		tasks: [
			{ id: "t1", title: "Task one with a fairly long title that wraps on narrow widths possibly", status: "complete", completedAt: "2026-08-11T00:00:00.000Z" },
			{ id: "t2", title: "Task two", status: "pending" },
			{ id: "t3", title: "Task three with a long verification contract that wraps across lines", status: "pending", verificationContract: "Verify the widget holds a constant rendered height across every goal-state change in every regime" },
		],
	},
};

const theme = { fg: (_c, s) => s, bg: (_c, s) => s, bold: (s) => s, dim: (s) => s };

function makeFrame(tui, { chatLines, widgetComponent, editorLines, chatLinesRef, statusLine }) {
	const documentContainer = new Container();
	const header = new Container();
	header.render = () => ["pi • model • cwd"];
	const chat = new Container();
	chat.render = () => [...Array.from({ length: chatLines }, (_, i) => `chat line ${i} ${"x".repeat(30)}`), ...chatLinesRef.current];
	documentContainer.addChild(header);
	documentContainer.addChild(chat);
	const transcript = new ScrollView(documentContainer, { follow: "end", primary: true, overscroll: "chain" });
	const pending = new Container();
	pending.render = () => [];
	const status = new Container();
	status.render = () => [statusLine.current + " 51.6%/128k (auto)"];
	const widgetContainer = new Container();
	widgetContainer.addChild(widgetComponent);
	const editor = new Container();
	editor.render = () => editorLines.map((l) => `❯ ${l}`);
	const footer = new Container();
	footer.render = () => ["~/projects/pi-goal-x (main)", "↑1.4M ↓431k R59M 51.6%/128k (auto)  deepseek-v4-flash"];
	const dock = new VStack([pending, status, widgetContainer, editor, footer]);
	tui.addChild(transcript);
	tui.addChild(dock);
	return { transcript, dock };
}

function countWipes(s) {
	let c2 = 0, c3 = 0, i = 0;
	while (i < s.length) {
		if (s.startsWith("\x1b[2J", i)) { c2++; i += 4; continue; }
		if (s.startsWith("\x1b[3J", i)) { c3++; i += 4; continue; }
		i++;
	}
	return { c2, c3 };
}

function makeRig(rows, chatLines, editorLines) {
	const emu = new Terminal({ cols: COLS, rows, scrollback: 2000, allowProposedApi: true });
	const drain = () => new Promise((res) => { emu.onWriteParsed(() => setTimeout(res, 3)); emu.write(""); });
	const pending = { writes: [] };
	const terminal = {
		columns: COLS,
		get rows() { return emu.rows; },
		write(data) { pending.writes.push(String(data)); },
		hideCursor() {}, showCursor() {}, start() {}, stop() {}, setTitle() {}, moveBy() {},
	};
	const tui = new TuiMainScreen(terminal, false, "/tmp/tui-probe");
	const currentRef = { current: structuredClone(baseGoal) };
	const eventsRef = { current: [] };
	const expanded = { current: true };
	const chatLinesRef = { current: [] };
	const statusLine = { current: "status idle" };
	const component = new GoalWidgetComponent({
		tui, theme,
		getGoal: () => currentRef.current,
		getOpenGoalCount: () => 17,
		getSettings: () => ({}),
		getExpanded: () => expanded.current,
		getLedgerEvents: () => eventsRef.current,
	});
	makeFrame(tui, { chatLines, widgetComponent: component, editorLines, chatLinesRef, statusLine });
	return {
		emu, drain, pending, tui, component, currentRef, eventsRef, expanded, chatLinesRef, statusLine,
		async flush() {
			const stream = pending.writes.join("");
			pending.writes.length = 0;
			if (stream) { emu.write(stream); await drain(); }
			return stream;
		},
	};
}

async function probe(rows, chatLines, editorLines) {
	const rig = makeRig(rows, chatLines, editorLines);
	const { emu, tui, currentRef } = rig;
	tui.doRender();
	const stream1 = await rig.flush();
	const frame1 = (tui.previousLines ?? []).map(stripAnsi);
	const frameLen1 = frame1.length;
	const w0 = countWipes(stream1);
	// the "elapsed tick": active goal usage change, widget content changes
	currentRef.current = structuredClone(baseGoal);
	currentRef.current.usage = { tokensUsed: 48213, activeSeconds: 1428 };
	tui.doRender();
	const stream2 = await rig.flush();
	const w1 = countWipes(stream2);
	const frameLen2 = (tui.previousLines ?? []).length;
	const b = emu.buffer.active;
	const bufBefore = { len: b.length, baseY: b.baseY };
	// one more tick
	currentRef.current = structuredClone(baseGoal);
	currentRef.current.usage = { tokensUsed: 48213, activeSeconds: 1429 };
	tui.doRender();
	const stream3 = await rig.flush();
	const w2 = countWipes(stream3);
	// agent write: append a chat line (grow the document)
	rig.chatLinesRef.current.push(`chat line ${rig.chatLinesRef.current.length} new agent output ${"y".repeat(20)}`);
	tui.doRender();
	const stream4 = await rig.flush();
	const w3 = countWipes(stream4);
	// status tick (spinner)
	rig.statusLine.current = "⠹ Working... ↑0 ↓0";
	tui.doRender();
	const stream5 = await rig.flush();
	const w4 = countWipes(stream5);
	const bufAfter = { len: b.length, baseY: b.baseY };
	const editorIdx = frame1.findIndex((l) => l.startsWith("❯"));
	const widgetEnd = editorIdx >= 0 ? editorIdx : frame1.length;
	// widget span: from the box top to the editor
	const boxTop = frame1.findIndex((l) => l.includes("pi-goal-x"));
	const widgetH = boxTop >= 0 ? widgetEnd - boxTop : -1;
	const overflow = widgetH > 0 && rows < widgetH + (frame1.length - widgetEnd);
	const changed = w2.c2 - w0.c2;
	return {
		rows, chatLines, frameLen1, widgetH, below: frame1.length - (boxTop >= 0 ? boxTop : 0) - widgetH,
		overflow: overflow ? "OVERFLOW" : "fits",
		wipesTick1: w1.c2 - w0.c2, wipesTick2: w2.c2 - w0.c2,
		wipesChatAppend: w3.c2 - w0.c2, wipesStatus: w4.c2 - w0.c2,
		bufChurn: JSON.stringify(bufBefore) !== JSON.stringify(bufAfter),
	};
}

(async () => {
	console.log("rows | chat | frameLen | widgetH | below | geometry | tick1 | tick2 | chatAppend | statusTick | bufChurn");
	for (const rows of [16, 15, 14, 13, 12, 11]) {
		for (const chatLines of [40]) {
			for (const ed of [[""], ["l1", "l2", "l3", "l4", "l5"]]) {
				const r = await probe(rows, chatLines, ed);
				console.log(
					`${r.rows} | ${r.chatLines} | ${r.frameLen1} | ${r.widgetH} | ${r.below} | ${r.geometry} | ${r.wipesTick1} | ${r.wipesTick2} | ${r.wipesChatAppend} | ${r.wipesStatus} | ${r.bufChurn}`,
				);
			}
		}
	}
})();
