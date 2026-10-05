/**
 * Campaign diff emitter: before → after table (ratio) + per-feature budgets.
 *
 * Writes specs/<campaign-spec-dir>/BENCH-DIFF.md for a campaign. Rows ordered
 * by improvement ratio (lower is faster); rows missing from either run are
 * listed separately. Also emits the per-feature "budget" line for each row
 * (the after value is the budget for the next campaign).
 *
 * Usage:
 *   node --experimental-strip-types experiments/bench/diff-bench.mjs [campaign]
 */

import { readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { campaignConfig } from "./campaigns.mjs";

const benchDir = fileURLToPath(new URL(".", import.meta.url));
const campaign = process.argv[2] ?? "naf";
const cfg = campaignConfig(campaign);
const specDir = path.join(benchDir, "..", "..", "specs", cfg.specDir);
const before = JSON.parse(readFileSync(path.join(benchDir, `${cfg.jsonPrefix}before.json`), "utf8"));
const after = JSON.parse(readFileSync(path.join(benchDir, `${cfg.jsonPrefix}after.json`), "utf8"));

const beforeMap = new Map(before.rows.map((r) => [r.id, r]));
const afterMap = new Map(after.rows.map((r) => [r.id, r]));

function valueOf(row) {
	if (typeof row.p50 === "number") return { kind: "p50", v: row.p50, label: `${row.p50}ms` };
	if (typeof row.ops === "number") return { kind: "ops", v: row.ops, label: String(row.ops) };
	return { kind: "none", v: NaN, label: "-" };
}

const rows = [];
for (const [id, bRow] of beforeMap) {
	const aRow = afterMap.get(id);
	if (!aRow) continue;
	const b = valueOf(bRow);
	const a = valueOf(aRow);
	if (b.kind === "none" || a.kind === "none") continue;
	const ratio = b.v > 0 ? a.v / b.v : a.v === 0 ? 1 : 0;
	rows.push({ id, label: bRow.label, before: b.label, after: a.label, ratio, kind: b.kind });
}
rows.sort((x, y) => x.ratio - y.ratio);

const lines = [
	`# Benchmark diff — before → after (${campaign}, ${new Date().toISOString()})`,
	``,
	`Agent-free runs (B8), same machine. p50 ms unless noted (ops/tokens rows show their count). Rows ordered by`,
	`improvement ratio (lower is faster). The after value is the per-feature budget for the next campaign.`,
	``,
	`| id | before | after | ratio | label |`,
	`|---|---|---|---|---|`,
	...rows.map((r) => `| ${r.id} | ${r.before} | ${r.after} | ${r.ratio < 1 ? `${(r.ratio).toFixed(2)}x` : r.ratio === 1 ? "1.00x" : `${r.ratio.toFixed(2)}x`} | ${r.label} |`),
	``,
	`## Missing from either run`,
	``,
	...Array.from(new Set([...beforeMap.keys(), ...afterMap.keys()])).filter((id) => !beforeMap.has(id) || !afterMap.has(id))
		.map((id) => `- ${id}: ${beforeMap.has(id) ? "" : "not in before"} ${afterMap.has(id) ? "" : "not in after"}`),
	``,
];

const outPath = path.join(specDir, "BENCH-DIFF.md");
writeFileSync(outPath, lines.join("\n"));
console.log(`[diff] wrote ${outPath} (${rows.length} comparable rows)`);
