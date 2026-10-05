/**
 * context:measure — capture and measure every fixture's composed request and
 * write experiments/context/baseline-main.json (PR D).
 *
 * Deterministic: fixed fixture ids/timestamps; the breakdown is byte-stable
 * across runs on the same code state. No network, no live model.
 */

import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import * as path from "node:path";

import { captureOne } from "./capture-context.mjs";
import { FIXTURES } from "./fixtures.mjs";
import { measureContext, semanticCounts } from "./measure-context.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));

const rows = [];
for (const fixtureId of Object.keys(FIXTURES).sort()) {
	const scenario = FIXTURES[fixtureId]();
	const captured = await captureOne(fixtureId);
	const withGoal = { ...captured, goal: scenario.goal };
	rows.push({
		fixture: fixtureId,
		breakdown: measureContext(captured),
		semantic: semanticCounts(withGoal),
	});
}

const baseline = {
	generatedAtCommit: process.env.CONTEXT_BASELINE_LABEL ?? "main",
	measuredAt: new Date().toISOString().slice(0, 10),
	fixtures: rows,
	totals: {
		totalSerializedChars: rows.reduce((sum, r) => sum + r.breakdown.totalSerializedChars, 0),
		estimatedTokens: rows.reduce((sum, r) => sum + r.breakdown.estimatedTokens, 0),
		childRequestChars: rows.reduce((sum, r) => sum + r.breakdown.childRequestChars, 0),
		combinedSerializedChars: rows.reduce((sum, r) => sum + r.breakdown.totalSerializedChars + r.breakdown.childRequestChars, 0),
		extensionAttributableChars: rows.reduce((sum, r) => sum + r.breakdown.extensionAttributableChars, 0),
	},
};

const outPath = process.env.CONTEXT_OUTPUT ?? path.join(here, "baseline-main.json");
writeFileSync(outPath, `${JSON.stringify(baseline, null, 2)}\n`, "utf8");

console.log(`[context:measure] ${rows.length} fixtures measured -> ${outPath}`);
for (const row of rows) {
	const b = row.breakdown;
	console.log(
		`  ${row.fixture.padEnd(34)} total=${String(b.totalSerializedChars).padStart(7)} sys=${String(b.extensionSystemChars).padStart(6)} tools=${String(b.toolSchemaChars).padStart(6)} msgs=${String(b.messageChars).padStart(6)} histCp=${b.historicalCheckpointChars}`,
	);
}
