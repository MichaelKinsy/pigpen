// Times the original's loops, lesson and recall paths on the same inputs as extensions/warden/tools_bench_test.go.
// Copy into port/oracle and run: node --import tsx --import ./tests/setup-env.ts ./_bench.ts
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { applyLoopAction, updateLoops } from "./src/loops.ts";
import { buildCompactSnapshot, recallText } from "./src/compact.ts";
import { emptyPrefsStore, emptyWordCounts, extractPreferences, recordLesson } from "./src/prefs.ts";

const time = async (name: string, n: number, f: (i: number) => unknown | Promise<unknown>) => {
  const runs: number[] = [];
  for (let r = 0; r < 11; r++) {
    const start = performance.now();
    for (let i = 0; i < n; i++) await f(i);
    runs.push((performance.now() - start) * 1000 / n);
  }
  runs.sort((a, b) => a - b);
  console.log(`${name}: median ${runs[5]!.toFixed(2)} us/op over 11 runs of ${n}`);
};
const dir = mkdtempSync(join(tmpdir(), "bench-"));
const path = join(dir, "loops.json");
await time("loops add+list (file)", 200, async i => { await updateLoops(path, s => applyLoopAction(s, { action: "add", text: `Follow up ${i}` }, 1)); await updateLoops(path, s => applyLoopAction(s, { action: "list" }, 1)); });
const scan = { prefs: [], candidates: [], counts: emptyWordCounts() };
await time("remember (recordLesson, pure)", 2000, () => recordLesson({ lesson: "Never edit the generated client by hand", session: "s", now: 1, scan, store: emptyPrefsStore() }));
await time("extractPreferences", 2000, () => extractPreferences("ok, never force-push the release branch. always squash merge feature branches"));
const snap = buildCompactSnapshot({ savedOutputs: [], checks: [{ command: "npm test", passed: true, runIndex: 1, indexInRun: 0 }], holds: [], attempts: [{ key: "a", call: "npm run build", failed: true, output: "src/a.ts: error TS2322: bad" }], evidence: { mutations: 1, checks: [{ call: "npm test", passed: true }] as never, checksBeforeMutation: 0 }, activeTask: "Fix the build", runs: 1 });
await time("recall (recallText)", 5000, () => recallText(snap));
rmSync(dir, { recursive: true, force: true });
