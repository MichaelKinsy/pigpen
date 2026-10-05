#!/usr/bin/env node
// Dump full screens from a zellij capture at given timestamps.
import xtermPkg from "@xterm/headless";
import fs from "node:fs";

const { Terminal } = xtermPkg;
const file = process.argv[2] ?? "/tmp/zj.bin";
const rows = Number(process.argv[3] ?? 24);
const cols = Number(process.argv[4] ?? 100);
const atTimes = process.argv.slice(5).map(Number);
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

let offset = 0;
let lastT = 0;
const shots = [];
for (const item of timeline) {
	if (item.resize !== undefined) { emu.resize(cols, item.resize); continue; }
	if (item.mark !== undefined) continue;
	await feed(raw.slice(offset, offset + item.len));
	offset += item.len;
	lastT = item.t;
	for (const want of atTimes) {
		if (!shots.includes(want) && lastT >= want) {
			shots.push(want);
			const b = emu.buffer.active;
			console.log(`\n===== screen at t=${want}s =====`);
			for (let y = 0; y < b.length; y++) {
				console.log(`${String(y).padStart(2)}: ${strip(b.getLine(y)?.translateToString(true) ?? "")}`);
			}
		}
	}
}
