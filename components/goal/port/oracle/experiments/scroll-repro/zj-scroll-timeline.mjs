#!/usr/bin/env node
// Per-second SCROLL: 0/N indicator + pi full-render (2J/3J) correlation.
import xtermPkg from "@xterm/headless";
import fs from "node:fs";

const { Terminal } = xtermPkg;
const file = process.argv[2] ?? "/tmp/zj-mock2.bin";
const rows = Number(process.argv[3] ?? 24);
const cols = Number(process.argv[4] ?? 100);
const raw = fs.readFileSync(file);
const timeline = fs
	.readFileSync(file + ".timeline", "utf8")
	.split("\n")
	.filter(Boolean)
	.map((l) => {
		const [a, b] = l.split("\t");
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

const scrollAt = (t) => {
	const b = emu.buffer.active;
	const top = strip(b.getLine(1)?.translateToString(true) ?? "");
	const m = top.match(/SCROLL:\s*(\d+)\/(\d+)/);
	return m ? `${m[1]}/${m[2]}` : top.slice(-22);
};

let offset = 0;
let lastSampleSec = -1;
const rows2 = [];
let wipes = 0;
for (const item of timeline) {
	if (item.mark !== undefined) {
		rows2.push(`MARK@${item.mark}`);
		continue;
	}
	const slice = raw.slice(offset, offset + item.len);
	offset += item.len;
	const s = slice.toString("utf8");
	const n2 = (s.match(/\x1b\[2J/g) || []).length;
	const n3 = (s.match(/\x1b\[3J/g) || []).length;
	await feed(slice);
	const sec = Math.floor(item.t);
	if (n2 || n3) wipes++;
	if (sec !== lastSampleSec) {
		lastSampleSec = sec;
		const scr = scrollAt(item.t);
		const b = emu.buffer.active;
		// widget status line (2nd row of the widget box or the box area)
		rows2.push(`${String(sec).padStart(3)}s SCROLL:${scr}${n2 || n3 ? ` [2J=${n2} 3J=${n3}]` : ""}`);
	}
}
console.log("slices with 2J/3J:", wipes);
console.log(rows2.join("\n"));
