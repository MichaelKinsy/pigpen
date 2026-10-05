#!/usr/bin/env node
// Post-analyze a raw pi PTY capture with the real @xterm/headless emulator:
// replay the byte stream, sampling the emulator's buffer length / baseY /
// viewport over time, and report buffer-line-count churn (the thing that
// drives a multiplexer's "0/XXXX" scrollback indicator to change) plus
// 2J/3J/1049 wipes.
//
// Usage: node experiments/scroll-repro/pi-pty-analyze.mjs /tmp/pi-run.bin [rows]
import xtermPkg from "@xterm/headless";
import fs from "node:fs";

const { Terminal } = xtermPkg;
const file = process.argv[2] ?? "/tmp/pi-run.bin";
const rows = Number(process.argv[3] ?? 30);
const cols = Number(process.argv[4] ?? 110);
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
const b = emu.buffer.active;

// replay with drains
const drain = () => new Promise((res) => { emu.onWriteParsed(() => setTimeout(res, 2)); emu.write(""); });
async function feed(data) {
	emu.write(data);
	await drain();
}
await feed(raw);

// sample the buffer over time by replaying in slices aligned to the timeline
const samples = [];
let offset = 0;
for (const item of timeline) {
	if (item.mark !== undefined) {
		samples.push({ t: item.mark, len: b.length, baseY: b.baseY, vp: b.viewportY });
		continue;
	}
	if (item.resize !== undefined) {
		emu.resize(cols, item.resize);
		samples.push({ t: "RESIZE", len: b.length, baseY: b.baseY, vp: b.viewportY });
		continue;
	}
	await feed(raw.slice(offset, offset + item.len));
	offset += item.len;
	samples.push({ t: Math.round(item.t * 10) / 10, len: b.length, baseY: b.baseY, vp: b.viewportY });
}

const wipes = countWipes(raw);
const lens = samples.map((s) => s.len);
const unique = [...new Set(lens)];
const churnCount = samples.filter((s, i) => i > 0 && s.len !== samples[i - 1].len).length;
console.log(`bytes: ${raw.length}  rows: ${rows}x${cols}`);
console.log(`wipes: 2J=${wipes.c2}  3J=${wipes.c3}  alt1049=${wipes.alt}`);
console.log(`buffer length timeline: ${samples.map((s) => `${s.t}s:${s.len}`).join("  ")}`);
console.log(`distinct buffer lengths: ${unique.join(",")}`);
console.log(`buffer length CHANGES across samples: ${churnCount}`);
console.log(`final buffer len=${b.length} baseY=${b.baseY} viewportY=${b.viewportY}`);
// tail of the screen to confirm what's showing
const lines = [];
for (let y = Math.max(0, b.length - Math.min(rows, 24)); y < b.length; y++) {
	lines.push(`${y}: ${b.getLine(y)?.translateToString(true) ?? ""}`);
}
console.log("--- tail of the pane ---");
console.log(lines.join("\n").replace(/\x1b\[[0-9;?]*[a-zA-Z]/g, ""));

function countWipes(stream) {
	if (Buffer.isBuffer(stream)) stream = stream.toString("utf8");
	let c2 = 0, c3 = 0, alt = 0;
	for (let i = 0; i < stream.length; i++) {
		if (stream.startsWith("\x1b[2J", i)) { c2++; i += 3; continue; }
		if (stream.startsWith("\x1b[3J", i)) { c3++; i += 3; continue; }
		if (stream.startsWith("\x1b[?1049", i)) { alt++; i += 6; continue; }
	}
	return { c2, c3, alt };
}
