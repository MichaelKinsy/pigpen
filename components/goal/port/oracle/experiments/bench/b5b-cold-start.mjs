/**
 * B5b — Cold-session-start rows (NAF 2026-08-06).
 *
 * The B1/B5 warm rows measure steady-state per-call cost (min ops / p50 over
 * repeated in-process calls). A fresh process has no caches, so the honest
 * cold cost is measured here: each sample runs the flow ONCE in a fresh child
 * process (bench-cold-child.mjs) and reports the fs ops + wall time of that
 * single cold call. Children are node processes with the bench hooks only —
 * no network, no live agents (B8); child spawn is explicitly allowed like the
 * B5 contention harness.
 *
 * Rows (all with a 50-goal fixture + 1 settings file + 1k-event ledger):
 *   B1.pool.cold           cold sync pool read (readActiveGoalPool)
 *   B1.settings.cold       cold settings load (loadGoalSettings)
 *   B1.ledger.cold         cold ledger read (readGoalLedger)
 *   B5.startup.cold        cold session startup (createGoalCore + loadState)
 * plus .lat25 variants (25ms injected per fs op — slow-storage emulation).
 *
 * The same module runs against pre- and post-optimization code (it only
 * imports APIs that existed before the naf campaign), so the pre-optimization
 * cold numbers are captured from a worktree of the pre-change commit.
 *
 * Direct run prints the markdown table:
 *   node --import experiments/bench/bench-adapter-hooks.mjs \
 *        --experimental-strip-types experiments/bench/b5b-cold-start.mjs
 */

import path from "node:path";
import { fileURLToPath } from "node:url";
import { mkdirSync, writeFileSync, appendFileSync } from "node:fs";

import {
	makeFixtureCwd,
	makeGoalFiles,
	makeLedger,
	cleanupFixture,
} from "./bench-common.mjs";
import { loadLedgerState } from "../../extensions/goal-ledger.ts";
import { spawnContention } from "./node-child-process.mjs";
import { allowChildProcess } from "./guard-state.mjs";

const benchDir = fileURLToPath(new URL(".", import.meta.url));
const childPath = path.join(benchDir, "bench-cold-child.mjs");
const HOOKS_IMPORT = path.join(benchDir, "bench-adapter-hooks.mjs");

const GOAL_COUNT = 50;

/** One cold sample: spawn a fresh process, run the flow once, return {ops, wall}. */
function coldSample(cwd, flow, lat25) {
	return new Promise((resolve, reject) => {
		const args = [
			"--import", HOOKS_IMPORT,
			"--experimental-strip-types",
			childPath, cwd, flow, ...(lat25 ? ["lat25"] : []),
		];
		allowChildProcess(() => {
			const child = spawnContention(process.execPath, args, { stdio: ["ignore", "pipe", "inherit"] });
			let out = "";
			child.stdout.on("data", (d) => { out += d.toString(); });
			child.on("error", reject);
			child.on("exit", (code) => {
				if (code !== 0) return reject(new Error(`cold child exit ${code} for ${flow}`));
				try {
					resolve(JSON.parse(out));
				} catch {
					reject(new Error(`bad child output for ${flow}: ${out}`));
				}
			});
		});
	});
}

async function measureCold(cwd, flow, lat25, n = 5) {
	const samples = [];
	let minOps = Infinity;
	for (let i = 0; i < n; i++) {
		const s = await coldSample(cwd, flow, lat25);
		samples.push(s.wall);
		minOps = Math.min(minOps, s.ops);
	}
	samples.sort((a, b) => a - b);
	const p50 = Math.round(samples[Math.floor(samples.length / 2)] * 10) / 10;
	const p95 = Math.round(samples[Math.min(samples.length - 1, Math.ceil(samples.length * 0.95))] * 10) / 10;
	const max = Math.round(samples[samples.length - 1] * 10) / 10;
	return { ops: minOps, p50, p95, max, samples };
}

export async function run(baseline) {
	const cwd = makeFixtureCwd("b5b-cold-");
	// Separate fixture for checkpoint rows: a checkpoint must EXIST before the
	// cold child runs (written by this parent process), so the base rows keep a
	// checkpoint-free fixture.
	const cwdCp = makeFixtureCwd("b5b-cold-cp-");
	try {
		makeGoalFiles(cwd, GOAL_COUNT);
		mkdirSync(path.join(cwd, ".pi"), { recursive: true });
		writeFileSync(path.join(cwd, ".pi", "pi-goal-x-settings.json"), JSON.stringify({ subtaskDepth: 2, provider: "anthropic", model: "m", thinking_level: "medium" }));
		makeLedger(cwd, 1000);

		// Checkpoint fixture: same 50 goals + 1k-event ledger, plus a full
		// checkpoint written by this process (children are fresh, so they see a
		// genuinely cold checkpoint hit / tail replay).
		makeGoalFiles(cwdCp, GOAL_COUNT);
		mkdirSync(path.join(cwdCp, ".pi"), { recursive: true });
		writeFileSync(path.join(cwdCp, ".pi", "pi-goal-x-settings.json"), JSON.stringify({ subtaskDepth: 2, provider: "anthropic", model: "m", thinking_level: "medium" }));
		makeLedger(cwdCp, 1000);
		loadLedgerState({ cwd: cwdCp }); // full fallback in the parent writes the checkpoint
		// Tail fixture: checkpoint covers the ledger, then 50 external events grow it.
		const tailLedger = path.join(cwdCp, ".pi", "goals", "goal_events.jsonl");
		for (let i = 0; i < 50; i++) {
			appendFileSync(tailLedger, JSON.stringify({ type: "task_complete", goalId: `g${i % 10}`, taskId: `t-tail-${i}`, evidence: "checkpoint tail", at: new Date(Date.UTC(2026, 9, 1) + i * 1000).toISOString() }) + "\n", "utf8");
		}

		const rows = [
			["B1.pool.cold", "pool", false, `cold sync pool read (${GOAL_COUNT} goals, fresh process)`, "storage/goal-files"],
			["B1.pool.cold.lat25", "pool", true, `cold sync pool read (${GOAL_COUNT} goals, +25ms/op)`, "storage/goal-files"],
			["B1.settings.cold", "settings", false, "cold settings load (fresh process)", "goal-settings"],
			["B1.settings.cold.lat25", "settings", true, "cold settings load (+25ms/op)", "goal-settings"],
			["B1.ledger.cold", "ledger", false, "cold ledger read (1k events, fresh process)", "goal-ledger"],
			["B1.ledger.cold.lat25", "ledger", true, "cold ledger read (+25ms/op)", "goal-ledger"],
			["B5.startup.cold", "startup", false, `cold session startup loadState (${GOAL_COUNT} goals, fresh process)`, "goal-state + storage/goal-files + goal-settings"],
			["B5.startup.cold.lat25", "startup", true, `cold session startup loadState (${GOAL_COUNT} goals, +25ms/op)`, "goal-state + storage/goal-files + goal-settings"],
			["B1.ledgerstate.cold", "ledgerstate", false, "cold checkpoint-aware read, no checkpoint (full parse + write)", "goal-ledger"],
			["B1.ledgerstate.cp.hit", "ledgerstate", false, "cold checkpoint-aware read, checkpoint covers the ledger", "goal-ledger"],
			["B1.ledgerstate.cp.tail", "ledgerstate", false, "cold checkpoint-aware read, 50-event external tail", "goal-ledger"],
		];
		for (const [id, flow, lat25, label, modules] of rows) {
			const fixtureCwd = flow === "ledgerstate" && (id === "B1.ledgerstate.cp.hit" || id === "B1.ledgerstate.cp.tail")
				? cwdCp
				: cwd;
			const r = await measureCold(fixtureCwd, flow, lat25);
			baseline.add({
				id, label, modules,
				fixture: id.startsWith("B1.ledgerstate")
					? `${GOAL_COUNT} goals + settings + 1k-event ledger${id.endsWith(".hit") || id.endsWith(".tail") ? " + checkpoint" : ""}`
					: `${GOAL_COUNT} goals + settings + 1k-event ledger`,
				n: r.samples.length, p50: r.p50, p95: r.p95, max: r.max,
				ops: r.ops,
				...(lat25 ? { latency: "25ms/op" } : {}),
				notes: `fresh-process cold read; wall samples ${r.samples.join("/")}ms; ops = min over samples (deterministic per code state)`,
			});
		}
	} finally {
		cleanupFixture(cwd);
	}
	return baseline;
}

if (import.meta.url === `file://${process.argv[1]}`) {
	const { Baseline } = await import("./bench-common.mjs");
	const baseline = new Baseline();
	await run(baseline);
	process.stdout.write(baseline.markdown());
}
