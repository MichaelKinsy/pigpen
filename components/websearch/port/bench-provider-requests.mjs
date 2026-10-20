// Times the ORIGINAL hosted providers on the inputs of extensions/websearch/testdata/provider-wire.json with `fetch` replaced by
// canned responses (the network is not part of the measurement), for the performance section of the
// port's PR: warm (one process, median and p90 of N calls) and cold (a fresh node process per run: module
// import plus the first call; median of COLD runs). Run it beside `go test -bench HostedWire` on the same host.
//
//   cd components/websearch/port && node bench-provider-requests.mjs [warmRuns=200] [coldRuns=10]
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const cases = JSON.parse(readFileSync(join(here, "..", "extensions", "websearch", "testdata", "provider-wire.json"), "utf8")).filter((c) => c.expect.error === null);
const warmRuns = Number(process.argv[2] ?? 200);
const coldRuns = Number(process.argv[3] ?? 10);
const modules = {
	searchWithTinyFish: "tinyfish.ts", extractWithTinyFish: "tinyfish.ts", searchWithSearch1API: "search1api.ts", extractWithSearch1API: "search1api.ts",
	searchWithSearchinfinity: "searchinfinity.ts", searchWithQuerit: "querit.ts", extractWithQuerit: "querit.ts", searchWithParallel: "parallel.ts",
	extractWithParallel: "parallel.ts", searchWithParallelMcp: "parallel-mcp.ts", extractWithParallelMcp: "parallel-mcp.ts", searchWithOllama: "ollama.ts",
	extractWithOllama: "ollama.ts", searchWithKagi: "kagi.ts", extractWithKagi: "kagi.ts", searchWithBrightData: "brightdata.ts", extractWithBrightDataUnlocker: "brightdata-unlocker.ts",
};
const lookup = `async () => [{ address: "93.184.216.34", family: 4 }]`;

function script(c, runs) {
	const search = c.kind === "search";
	const opts = JSON.stringify(c.args[c.args.length - 1]);
	const call = search
		? `mod.${c.fn}(${JSON.stringify(c.args[0])}, ${opts})`
		: `mod.${c.fn}(${JSON.stringify(c.args[0])}, undefined, { ...${opts}, lookup: ${lookup} })`;
	return `
		const responses = ${JSON.stringify(c.responses)};
		let i = 0;
		globalThis.fetch = async () => { const r = responses[Math.min(i++, responses.length - 1)]; return new Response(typeof r.body === "string" ? r.body : JSON.stringify(r.body), { status: r.status }); };
		const t0 = process.hrtime.bigint();
		const mod = await import(${JSON.stringify(join(here, "oracle", modules[c.fn]))});
		await ${call};
		const first = Number(process.hrtime.bigint() - t0) / 1e3;
		const times = [];
		for (let n = 0; n < ${runs}; n++) {
			i = 0;
			const s = process.hrtime.bigint();
			await ${call};
			times.push(Number(process.hrtime.bigint() - s) / 1e3);
		}
		times.sort((a, b) => a - b);
		console.log(JSON.stringify({ first, median: times[times.length >> 1] ?? first, p90: times[Math.floor(times.length * 0.9)] ?? first }));
	`;
}

function run(c, runs) {
	const home = mkdtempSync(join(tmpdir(), "bench-"));
	const agent = join(home, "agent");
	mkdirSync(agent, { recursive: true });
	writeFileSync(join(agent, "web-search.json"), "{}\n");
	const env = { PATH: process.env.PATH, HOME: home, USERPROFILE: home, PI_CODING_AGENT_DIR: agent, ...c.env };
	const r = spawnSync(process.execPath, ["--input-type=module"], { input: script(c, runs), encoding: "utf8", env, maxBuffer: 4 << 20 });
	if (r.status !== 0) throw new Error(`${c.name}: ${r.stderr}`);
	return JSON.parse(r.stdout.trim().split("\n").pop());
}

const med = (xs) => xs.slice().sort((a, b) => a - b)[xs.length >> 1];
console.log("case\twarm median us\twarm p90 us\tcold first call us (module import + call)");
for (const c of cases) {
	const warm = run(c, warmRuns);
	const cold = Array.from({ length: coldRuns }, () => run(c, 0).first);
	console.log(`${c.name}\t${warm.median.toFixed(0)}\t${warm.p90.toFixed(0)}\t${med(cold).toFixed(0)}`);
}
