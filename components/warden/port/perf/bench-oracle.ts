// Timing of the original's supervision functions on the same inputs as supervision_bench_test.go, under the Node
// runtime PiG ships. Run from components/warden/port with the jiti of the Pi checkout:
//   node <pi>/node_modules/jiti/lib/jiti-cli.mjs perf/bench-oracle.ts
import { WasteTracker } from "../oracle/src/waste.ts";
import { buildStuckEvidence, parseFailure } from "../oracle/src/evidence.ts";
import { makeAttempt } from "../oracle/src/stuck.ts";
import { triageReport } from "../oracle/src/subagent.ts";
import { defaultConfig } from "../oracle/src/config.ts";

const text = (value: string) => [{ type: "text", text: value }];
const lines = (n: number) => `${Array.from({ length: n }, (_, i) => `line ${i + 1}`).join("\n")}\n`;
const call = (tool: string, input: Record<string, unknown>, result: string) => ({ tool, input, cwd: "/work/project", failed: false, result });
const calls = [
  call("bash", { command: "go test ./... -count=1" }, "ok  \tpkg\t0.2s\n"),
  call("read", { path: "src/a.go", offset: 1, limit: 40 }, lines(40)),
  call("bash", { command: "grep -n foo src/a.go" }, "12:foo\n"),
  call("bash", { command: "git status --short" }, " M a.go\n"),
  call("bash", { command: "sleep 1; echo running" }, "running\n"),
  call("edit", { path: "src/a.go", edits: [] }, "ok"),
];
const cfg = { enabled: true, tip: true, every: 20, sleep: true, paging: true, search: true, recheck: true };

const attempts = [];
for (let i = 0; i < 12; i++) {
  attempts.push(makeAttempt("bash", { command: "go test ./..." }, text(`--- FAIL: TestX${i} (0.00s)\n    x_test.go:${40 + i}: got 1, want 2\nFAIL\nFAIL\texample.com/x\t0.01s\n`), true));
  if (i % 3 === 0) attempts.push(makeAttempt("edit", { path: "x.go", edits: [{ oldText: "a\nb\nc", newText: "a\nB\nc" }] }, text("ok"), false));
}
const tap = `TAP version 13\n# Subtest: week start\nnot ok 1 - weekStart maps Sunday to the previous Monday\n  ---\n  duration_ms: 3.1\n  location: '/home/dev/proj/src/week.test.js:8:3'\n  error: |-\n    expected 2024-03-03 to equal 2024-03-04\n  code: 'ERR_ASSERTION'\n  ...\n1..1\n# tests 1\n# pass 0\n# fail 1\n${"ok 2 - fine\n".repeat(200)}`;
const completion = { id: "e1", customType: "subagent-notify", incremental: false, text: "Background tasks completed (1): **explorer**\n\n1. explorer\nResult: rewrote the parser and ran the test suite; all 12 tests pass" };
const subagent = defaultConfig().subagent;

async function time(name: string, n: number, f: () => unknown | Promise<unknown>) {
  const runs: number[] = [];
  for (let r = 0; r < 12; r++) {
    const start = process.hrtime.bigint();
    for (let i = 0; i < n; i++) await f();
    runs.push(Number(process.hrtime.bigint() - start) / n);
  }
  runs.sort((a, b) => a - b);
  console.log(`PERF ${name} ns/op=${Math.round(runs[6]!)}`);
}

const tracker = new WasteTracker();
let i = 0;
await time("WasteRecord", 20000, () => tracker.record(calls[i++ % calls.length]!, cfg));
await time("BuildStuckEvidence", 2000, () => buildStuckEvidence(attempts));
await time("ParseFailureTAP", 2000, () => parseFailure(tap));
await time("TriageReportOffline", 20000, () => triageReport(completion, { config: subagent, timeoutMs: 1000 }));
