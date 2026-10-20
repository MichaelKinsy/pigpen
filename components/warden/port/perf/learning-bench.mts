// The original's learning module on the same history as extensions/warden/learning_bench_test.go: 500 rows of one
// project, half held, labelled round-robin. Run from port/oracle under Node 24 (node:sqlite):
//   cd port/oracle && node --import tsx ../perf/learning-bench.mts
// Prints the median of 21 runs of each operation, in microseconds.
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { performance } from "node:perf_hooks";

process.env.PI_WARDEN_DB = join(mkdtempSync(join(tmpdir(), "learn-bench-")), "holds.db");
const L = await import("../oracle/src/learning.ts");

await L.initSchema(365);
const now = Date.now();
const outcomes = ["approved", "declined", "replanned", "accepted"];
for (let i = 0; i < 500; i++) {
  const reasons = ["destructive: git force push", "irreversible 0.5"];
  const id = await L.recordHold({
    timestamp: now - i * 1000, projectRoot: "/bench/project", tool: "bash", commandPreview: `npm test ${i}`,
    scores: { irreversible: 0.5, reasons }, level: "confirm", held: i % 2 === 0, reasons, agentReason: "irreversible action detected",
  });
  await L.recordOutcome(id, outcomes[i % 4]!);
}

async function median(name: string, fn: () => Promise<unknown>, runs = 21) {
  await fn();
  const samples: number[] = [];
  for (let i = 0; i < runs; i++) {
    const start = performance.now();
    await fn();
    samples.push((performance.now() - start) * 1000);
  }
  samples.sort((a, b) => a - b);
  console.log(`${name}: ${samples[Math.floor(runs / 2)]!.toFixed(0)} us`);
}

await median("recordHold", () => L.recordHold({ timestamp: Date.now(), projectRoot: "/bench/project", tool: "bash", commandPreview: "npm test", scores: { irreversible: 0.5, reasons: ["irreversible 0.5"] }, level: "allow", held: true, reasons: ["irreversible 0.5"] }));
await median("generateRecommendations", () => L.generateRecommendations("/bench/project"));
await median("analyzeSteerEffectivenessReport", () => L.analyzeSteerEffectivenessReport("/bench/project"));
await median("holdStats", () => L.holdStats("/bench/project"));
