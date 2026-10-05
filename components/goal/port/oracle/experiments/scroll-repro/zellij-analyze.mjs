#!/usr/bin/env node
// Zellij-focused analysis: replay the raw zellij output, and at each MARK +
// at 1s intervals afterward, snapshot the LAST 3 screen rows (zellij's status
// bar area) plus detect the goal widget box. Reports how the status bar
// (incl. any scroll indicator) changes over time while pi sits idle.
import xtermPkg from "@xterm/headless";
import fs from "node:fs";

const { Terminal } = xtermPkg;
const file = process.argv[2] ?? "/tmp/zj.bin";
const rows = Number(process.argv[3] ?? 24);
const cols = Number(process.argv[4] ?? 100);
const raw = fs.readFileSync(file);
const timeline = fs
	.readFileSync(file + ".timeline", "utf8")
	.split("\n")
	.filter(Boolean)
	.map((l) => {
		const [a, b] = l.split("\t");
		if (a === "RESIZE") return { resize: Number(b) };
		if (a === "MARK") return { mark: b };
		return { t: Number(a), len: Number(b) };
	});

const emu = new Terminal({ cols, rows, scrollback: 5000, allowProposedApi: true });
const drain = () => new Promise((res) => { emu.onWriteParsed(() => setTimeout(res, 2)); emu.write(""); });
async function feed(data) {
	emu.write(data);
	await drain();
}

const strip = (s) => s.replace(/\x1b\[[0-9;?]*[a-zA-Z]/g, "").replace(/\s+$/, "");
function screenShot(label) {
	const b = emu.buffer.active;
	const lines = [];
	for (let y = 0; y < b.length; y++) lines.push(strip(b.getLine(y)?.translateToString(true) ?? ""));
	const last3 = lines.slice(-3).join(" | ");
	const hasWidget = lines.some((l) => l.includes("pi-goal-x"));
	const hasScroll = last3.includes("SCROLL") || last3.includes("scroll") || /[0-9]+\/[0-9]+/.test(last3);
	console.log(`[${label}] widget=${hasWidget} scrollInd=${hasScroll}`);
	console.log(`   status: ${last3}`);
	return { hasWidget, hasScroll, last3 };
}

const snapshots = [];
let offset = 0;
const samples = [];
for (const item of timeline) {
	if (item.mark !== undefined) {
		console.log(`=== MARK: ${item.mark} ===`);
		screenShot(item.mark);
		samples.push(screenShot(item.mark).last3);
		continue;
	}
	if (item.resize !== undefined) {
		emu.resize(cols, item.resize);
		console.log(`=== RESIZE to ${item.resize} rows ===`);
		continue;
	}
	await feed(raw.slice(offset, offset + item.len));
	offset += item.len;
	// 1s-interval samples after 15s (post-resize steady state)
	if (item.t > 15 && (samples.length === 0 || item.t - samples[samples.length - 1].t >= 1)) {
		samples.push({ t: item.t, ...screenShot(`t=${item.t.toFixed(1)}s`) });
	}
}
// final screen
console.log("=== END ===");
screenShot("end");
