// Cross-check of the shared learning database: the original writes or reads holds.db with node:sqlite, the Go port with
// its embedded engine (extensions/warden TestLearningCrossCheck). Run from /work-like root with tsx installed:
//   node --import tsx port/perf/learning-cross.mts write <db>    # the original writes 6 rows, prints holdStats as JSON
//   node --import tsx port/perf/learning-cross.mts read  <db>    # the original reads what the Go port wrote
import { existsSync } from "node:fs";

const [mode, db] = process.argv.slice(2);
if (!mode || !db) throw new Error("usage: learning-cross.mts write|read <db>");
process.env.PI_WARDEN_DB = db;
const L = await import("../oracle/src/learning.ts");
const project = "/cross/project";

if (mode === "write") {
  await L.initSchema(365);
  const now = Date.now();
  const rows: Array<[boolean, string, number]> = [[true, "approved", 1], [true, "declined", 2], [true, "replanned", 3], [false, "accepted", 4], [false, "regretted", 5], [true, "approved", 6]];
  for (const [held, outcome, age] of rows) {
    const id = await L.recordHold({
      timestamp: now - age * 1000, projectRoot: project, tool: "bash", commandPreview: `cmd ${age}`, scores: { irreversible: 0.5, reasons: ["destructive: git force push"], largeOutput: 0.3 },
      level: "confirm", held, reasons: ["destructive: git force push"], agentReason: "irreversible action detected",
    });
    await L.recordOutcome(id, outcome);
  }
  console.log(JSON.stringify(await L.holdStats(project)));
} else {
  if (!existsSync(db)) throw new Error("no database");
  console.log(JSON.stringify(await L.holdStats(project)));
  const history = await L.querySmartHistory("bash", { irreversible: 0.5, reasons: ["destructive: git force push"] }, project);
  console.log(JSON.stringify({ exact: history.exact.length, similar: history.similar.length, sameReason: history.sameReason.length }));
  const recs = await L.generateRecommendations(project);
  const steer = await L.analyzeSteerEffectivenessReport(project);
  console.log(JSON.stringify({ recommendations: recs, overall: steer.overall, byType: steer.byType }));
}
