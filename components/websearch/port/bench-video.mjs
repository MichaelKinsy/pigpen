// Times the ORIGINAL's video routes with canned programs, to compare with BenchmarkVideoExtract
// (extensions/websearch/video_bench_test.go). `execFileSync` is replaced in-process, so only the
// module's own work is timed.
//
//   node port/bench-video.mjs [iterations]
//   node --expose-gc --min-semi-space-size=512 --max-semi-space-size=512 port/bench-video.mjs [iterations] alloc
//
// With `alloc` it also prints the heap growth per call: the semi space is large enough that no
// scavenge runs during the loop, so the growth of `heapUsed` is what the calls allocated.
//
// Run it from components/websearch with the oracle's dependencies installed (Node 24).
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import cp from "node:child_process";
import { syncBuiltinESMExports } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const iterations = Number(process.argv[2] ?? 200);
const measureAlloc = process.argv[3] === "alloc" && typeof globalThis.gc === "function";
const home = mkdtempSync(join(tmpdir(), "pi-bench-video-"));
const agent = join(home, "agent");
mkdirSync(agent, { recursive: true });
Object.assign(process.env, { HOME: home, USERPROFILE: home, PI_CODING_AGENT_DIR: agent, GEMINI_API_KEY: "synthetic-gemini-key" });
writeFileSync(join(agent, "web-search.json"), JSON.stringify({ image: { enabled: true } }));
const clip = join(agent, "clip.mp4");
writeFileSync(clip, "synthetic media");

cp.execFileSync = (command, _args, opts) => {
	const out = command === "yt-dlp" ? "125.5\nhttps://stream.example/video.mp4\n" : command === "ffprobe" ? "75.25\n" : "JPEGBYTES";
	return opts?.encoding ? out : Buffer.from(out);
};
syncBuiltinESMExports();

const oracle = join(dirname(fileURLToPath(import.meta.url)), "oracle");
const { extractContent } = await import(new URL(`file://${join(oracle, "extract.ts")}`).href);
const lookup = async () => [{ address: "93.184.216.34", family: 4 }];
const median = (xs) => xs.sort((a, b) => a - b)[Math.floor(xs.length / 2)];
async function time(label, url, options) {
	const run = async () => {
		const r = await extractContent(url, undefined, { lookup, ...options });
		if (r.error) throw new Error(r.error);
	};
	await run();
	const samples = [];
	for (let i = 0; i < iterations; i++) {
		const t = process.hrtime.bigint();
		await run();
		samples.push(Number(process.hrtime.bigint() - t) / 1e3);
	}
	console.log(`${label}: median ${median(samples).toFixed(1)} us over ${iterations} runs`);
	if (measureAlloc) {
		globalThis.gc();
		const before = process.memoryUsage().heapUsed;
		for (let i = 0; i < iterations; i++) await run();
		console.log(`${label}: alloc ${Math.round((process.memoryUsage().heapUsed - before) / iterations)} bytes/op over ${iterations} runs`);
	}
}
const yt = "https://www.youtube.com/watch?v=dQw4w9WgXcQ";
await time("youtube-frames", yt, { frames: 3 });
await time("youtube-frame", yt, { timestamp: "1:23" });
await time("local-frames", clip, { frames: 4 });
await time("local-range", clip, { timestamp: "0:10-1:00" });
