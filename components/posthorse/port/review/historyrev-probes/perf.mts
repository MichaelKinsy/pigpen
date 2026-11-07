// Head-to-head of history search (all=true) on identical archives: node perf.mts gen | run.
// gen writes /home/kinsy/ph-work/scratch/historyrev/perf/{a200,s1000}; run times the oracle's historyMatches.
import { mkdirSync, writeFileSync, rmSync, openSync, writeSync, closeSync } from "node:fs";
import { historyMatches } from "../../oracle/src/history-search.ts";

const root = "/home/kinsy/ph-work/scratch/historyrev/perf";
let seed = 7;
const rnd = (n: number) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n; };
const words = "lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua".split(" ");
const sentence = (n: number) => Array.from({ length: n }, () => words[rnd(words.length)]).join(" ");
function writeSession(file: string, cwd: string, entries: number, words: number, marker: number) {
	const fd = openSync(file, "w");
	let buf = JSON.stringify({ type: "session", version: 3, id: `s${rnd(1e9)}`, timestamp: "2026-01-01T00:00:00.000Z", cwd }) + "\n";
	let parent: string | null = null;
	for (let i = 0; i < entries; i++) {
		const id = (i + 1e8).toString(16).slice(-8);
		const role = i % 2 ? "assistant" : "user";
		const text = sentence(words) + (marker > 0 && i % marker === 0 ? ` zebra-marker-77 entry ${i}` : "");
		buf += JSON.stringify({ type: "message", id, parentId: parent, timestamp: "2026-01-01T00:00:00.000Z", message: { role, content: [{ type: "text", text }] } }) + "\n";
		parent = id;
		if (buf.length > 1 << 20) { writeSync(fd, buf); buf = ""; }
	}
	writeSync(fd, buf);
	closeSync(fd);
}
const mode = process.argv[2];
if (mode === "gen") {
	rmSync(root, { recursive: true, force: true });
	mkdirSync(`${root}/a200/--proj--`, { recursive: true });
	writeSession(`${root}/a200/--proj--/big.jsonl`, "/proj", 700_000, 40, 7000);
	mkdirSync(`${root}/s1000/--proj--`, { recursive: true });
	for (let i = 0; i < 1000; i++) {
		const name = `${root}/s1000/--proj--/s${String(i).padStart(4, "0")}.jsonl`;
		writeSession(name, i % 10 === 9 ? "/other" : "/proj", 200, 40, i % 100 === 0 ? 50 : 0);
		if (i % 50 === 0) {
			mkdirSync(name.slice(0, -6), { recursive: true });
			writeSession(`${name.slice(0, -6)}/sub.jsonl`, "/elsewhere", 100, 40, 20);
		}
	}
	console.log("generated");
} else {
	const rg = process.argv[3] !== "norg";
	if (!rg) process.env.PATH = "/nonexistent";
	const fail = (m: string): never => { throw new Error(m); };
	for (const shape of ["a200", "s1000"]) {
		for (const query of ["zebra-marker-77", "lorem", "qqqxyz"]) {
			if (process.env.HISTORYREV_ONLY && process.env.HISTORYREV_ONLY !== `${shape}/${query}`) continue;
			const source = { dir: `${root}/${shape}`, cwd: "/proj", currentFile: undefined, branch: () => [], projection: () => [] };
			const times: number[] = [];
			let hits = 0;
			let ids = "";
			for (let r = 0; r < 5; r++) {
				const t0 = performance.now();
				const m = await historyMatches(source, { op: "search", query, all: true }, undefined, fail);
				times.push(performance.now() - t0);
				hits = m.hits.length;
				ids = m.hits.map((h) => `${h.id}:${h.priority}:${h.text.length}`).join(",");
			}
			times.sort((a, b) => a - b);
			console.log(`ts ${rg ? "rg" : "norg"} ${shape} ${query}: median ${times[2]!.toFixed(1)} ms, hits ${hits}, rss ${(process.memoryUsage().rss / 2 ** 20).toFixed(0)} MiB`);
			if (process.env.HISTORYREV_IDS) console.log(`ids ${shape} ${query} ${ids}`);
		}
	}
}
