// The original's functions behind the Go benchmarks of the supervision, rules and guard-core slices, run on the same
// inputs under Node (the cases carry the Go benchmark's name without "Benchmark"). Run from port/oracle with
// port/bench/perf-run.sh, or by hand:
//   node --no-warnings --experimental-transform-types --import ../bench/ts-register.mjs ../bench/perf-original.ts time [Case]
//   node --no-warnings --experimental-transform-types --expose-gc --min-semi-space-size=128 --max-semi-space-size=128 \
//     --import ../bench/ts-register.mjs ../bench/perf-original.ts alloc [Case]
// Output, one line per case: `<name> <number>`: ns/op in "time" mode (a default Node, GC included, as in a Go benchmark), and
// bytes/op in "alloc" mode (the heap growth over a batch with no scavenge between the two readings, which the large
// semi-space of that mode allows; it would distort the timing, so the two are separate runs). Node does not count
// allocations, so there is no allocs/op.
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PerformanceObserver } from "node:perf_hooks";
import { WasteTracker } from "../oracle/src/waste.ts";
import { buildStuckEvidence, parseFailure } from "../oracle/src/evidence.ts";
import { makeAttempt } from "../oracle/src/stuck.ts";
import { triageReport } from "../oracle/src/subagent.ts";
import { defaultConfig } from "../oracle/src/config.ts";
import type { PathRule, ArmingRule } from "../oracle/src/config.ts";
import { buildRulesRequest, describeTarget, evaluateRules, parseRules, RuleStore, scoreRuleAnswers, LOCATOR_QUESTION } from "../oracle/src/rules.ts";
import { shellWrites } from "../oracle/src/shell-writes.ts";
import { evaluateAction, matchPatterns, matchPathRules, sqlTarget } from "../oracle/src/guard.ts";
import { ArmingTracker } from "../oracle/src/arming.ts";

type Case = { name: string; run: () => unknown };
const cases: Case[] = [];
const add = (name: string, run: () => unknown) => { cases.push({ name, run }); };

// ---- supervision (inputs of supervision_bench_test.go) ----
const call = (tool: string, input: Record<string, unknown>, result: string) => ({ tool, input, cwd: "/work/project", failed: false, result });
const wasteCalls = [
  call("bash", { command: "go test ./... -count=1" }, "ok  \tpkg\t0.2s\n"),
  call("read", { path: "src/a.go", offset: 1, limit: 40 }, `${Array.from({ length: 40 }, (_, i) => `line ${i + 1}`).join("\n")}\n`),
  call("bash", { command: "grep -n foo src/a.go" }, "12:foo\n"),
  call("bash", { command: "git status --short" }, " M a.go\n"),
  call("bash", { command: "sleep 1; echo running" }, "running\n"),
  call("edit", { path: "src/a.go", edits: [] }, "ok"),
];
const wasteCfg = { enabled: true, tip: true, every: 20, sleep: true, paging: true, search: true, recheck: true };
const attempts = [];
for (let i = 0; i < 12; i++) {
  attempts.push(makeAttempt("bash", { command: "go test ./..." }, [{ type: "text", text: `--- FAIL: TestX${i} (0.00s)\n    x_test.go:${40 + i}: got 1, want 2\nFAIL\nFAIL\texample.com/x\t0.01s\n` }], true));
  if (i % 3 === 0) attempts.push(makeAttempt("edit", { path: "x.go", edits: [{ oldText: "a\nb\nc", newText: "a\nB\nc" }] }, [{ type: "text", text: "ok" }], false));
}
const tap = `TAP version 13\n# Subtest: week start\nnot ok 1 - weekStart maps Sunday to the previous Monday\n  ---\n  duration_ms: 3.1\n  location: '/home/dev/proj/src/week.test.js:8:3'\n  error: |-\n    expected 2024-03-03 to equal 2024-03-04\n  code: 'ERR_ASSERTION'\n  ...\n1..1\n# tests 1\n# pass 0\n# fail 1\n${"ok 2 - fine\n".repeat(200)}`;
const completion = { id: "e1", customType: "subagent-notify", incremental: false, text: "Background tasks completed (1): **explorer**\n\n1. explorer\nResult: rewrote the parser and ran the test suite; all 12 tests pass" };
const tracker = new WasteTracker();
let wasteAt = 0;
add("WasteRecord", () => tracker.record(wasteCalls[wasteAt++ % wasteCalls.length]!, wasteCfg));
add("BuildStuckEvidence", () => buildStuckEvidence(attempts));
add("ParseFailureTAP", () => parseFailure(tap));
add("TriageReportOffline", () => triageReport(completion, { config: defaultConfig().subagent, timeoutMs: 1000 }));

// ---- rules (rules_bench_test.go and rules_cmp_bench_test.go) ----
const rulesDoc = (n: number) => Array.from({ length: n }, (_, i) => `# Rule ${i + 1}\npaths: src/**/*.ts\nseverity: high\nThe code must follow rule number ${i + 1} in every file it writes, with no exception.`).join("\n\n");
const project = mkdtempSync(join(tmpdir(), "warden-perf-"));
mkdirSync(join(project, "src"), { recursive: true });
writeFileSync(join(project, "pi-warden.md"), rulesDoc(40));
const sourceLines = Array.from({ length: 80 }, (_, i) => `export function f${i}(a: number): number {\n  return a + ${i};\n}`).join("\n");
writeFileSync(join(project, "src", "a.ts"), sourceLines);
const rulesCfg = defaultConfig().rules;
const doc40 = rulesDoc(40);
const rules40 = parseRules(doc40);
const set40 = { sources: ["pi-warden.md"], rules: rules40, alwaysDropped: 0 };
const store = new RuleStore();
const writeInput = (path: string, content: string) => ({ path, content });
const target300 = describeTarget("write", writeInput("src/a.ts", "export const a = 1;\n".repeat(300)), project)!;
const edits3 = { path: "src/a.ts", edits: [
  { oldText: "return a + 3;", newText: "return a + 3 + 1;" },
  { oldText: "return a + 20;", newText: "console.log(a);\n  return a + 20;" },
  { oldText: "return a + 41;", newText: "return a * 41;" },
] };
add("RulesParse40", () => parseRules(doc40));
add("RulesStoreLoadCached", () => store.load(project, rulesCfg));
add("RulesBuildRequest40", () => buildRulesRequest(target300, set40));
add("RulesDescribeWriteTarget", () => describeTarget("write", writeInput("src/a.ts", "export const a = 1;\n".repeat(600)), project));
add("RulesDescribeEditTarget", () => describeTarget("edit", edits3, project));
const heredoc = "mkdir -p src && cat > src/a.ts <<'EOF'\n" + "export const a = 1;\n".repeat(100) + "EOF\necho b >> b.ts";
add("ShellWritesHeredoc", () => shellWrites(heredoc, {}));
const answers: Record<string, unknown> = {};
for (const r of rules40) answers[`rule_${r.id}`] = { type: "choice", choice: "compliant", probabilities: { compliant: 0.9, violation: 0.05 } };
add("RulesScore40", () => scoreRuleAnswers(rules40, answers as never, rulesCfg));
const rulesOptions = { cwd: project, config: rulesCfg, set: set40, timeoutMs: 1000 };
const stub = {
  async evaluate(request: unknown) {
    // The judge is handed the request rules.ts built itself, so the shape is known.
    const body = request as { questions: Record<string, { criteria: Record<string, unknown> }> };
    const out: Record<string, unknown> = {};
    for (const [id, question] of Object.entries(body.questions)) {
      if (id === LOCATOR_QUESTION) {
        const keys = Object.keys(question.criteria);
        out[id] = { type: "choice", choice: keys[0], confidence: 0.7, probabilities: Object.fromEntries(keys.map((key, i) => [key, i === 0 ? 0.7 : 0.3 / (keys.length - 1)])) };
        continue;
      }
      const violation = id === "rule_rule-7" ? 0.85 : 0.05;
      out[id] = { type: "choice", choice: violation >= 0.5 ? "violation" : "compliant", confidence: 0.9, probabilities: { compliant: 1 - violation - 0.02, violation, not_applicable: 0.01, insufficient_context: 0.01 } };
    }
    return { model: "jev-test", elapsedMs: 7, usage: { input_tokens: 10, output_tokens: 0 }, answers: out } as never;
  },
};
const writeCall = writeInput("src/a.ts", "export const a = 1;\n".repeat(300));
add("RulesEvaluateOffline", () => evaluateRules("write", writeCall, rulesOptions));
add("RulesEvaluateStubJudge40", () => evaluateRules("write", writeCall, { ...rulesOptions, judge: stub as never }));

// ---- guard core (guardcore_bench_test.go and rules_cmp_bench_test.go) ----
const actionConfig = defaultConfig().action;
const bashAction = (command: string) => ({ tool: "bash", input: { command }, cwd: "/work/project", task: "run the checks" });
const pathRules: PathRule[] = [
  { id: "env", paths: ["**/.env"], access: "none", tools: ["*"], action: "warn" },
  { id: "keys", paths: ["~/.ssh/id_*"], access: "read", tools: ["*"], action: "block" },
  { id: "audit", paths: ["/var/audit/*.log"], access: "write", tools: ["*"], action: "confirm" },
];
const ordinary = bashAction("go test ./... && git status");
const withPathRules = bashAction("go test ./... && cat notes.txt | tee out.log");
const sqlCommand = `psql -h 127.0.0.1 -U postgres -d app -c "DELETE FROM users WHERE email LIKE 'crew-%';"`;
const sqlAction = bashAction(sqlCommand);
add("EvaluateActionOrdinaryCommand", () => evaluateAction(ordinary, { config: actionConfig }));
add("EvaluateActionWithPathRules", () => evaluateAction(withPathRules, { config: { ...actionConfig, pathRules } }));
add("EvaluateActionSQLCommand", () => evaluateAction(sqlAction, { config: actionConfig }));
const armingRule: ArmingRule = { id: "r", when: { edited: ["**/*.yaml"] }, arms: { command: "\\bapply\\b", for: "10m" }, action: "confirm" };
const armed = new ArmingTracker([armingRule]);
armed.arm("write", "/p/a.yaml", "/p");
const idle = new ArmingTracker([armingRule]);
add("ArmingCheckWhenArmed", () => armed.checkArmed("kubectl apply -f a.yaml"));
add("ArmingCheckNotArmed", () => idle.checkArmed("kubectl apply -f a.yaml"));
const arming = new ArmingTracker([armingRule]);
add("ArmingArmMatching", () => arming.arm("write", "/p/a.yaml", "/p"));
add("ArmingArmNotMatching", () => arming.arm("edit", "/p/src/a.go", "/p"));
add("SQLTargetLocalDelete", () => sqlTarget(sqlCommand));
const hostedSql = `psql postgres://app:secret@db.example.com:5432/app -c "UPDATE users SET plan = 'pro' WHERE id = 7"`;
add("SQLTargetHostedWrite", () => sqlTarget(hostedSql));
const commands = [
  "ls -la",
  "go test ./... -run TestFoo -count=1",
  "git status --short && git diff --stat",
  "rm -rf ./build && mkdir build && cp -r src build/ && cd build && make all",
  "cat > /tmp/x <<'EOF'\nline one\nline two\nEOF\nsed -i 's/a/b/' /tmp/x",
];
let commandAt = 0;
add("MatchPatternsFiveCommands", () => matchPatterns("bash", { command: commands[commandAt++ % commands.length]! }, "/work/repo", {}));
const userRules = [
  { id: "no-curl-post", pattern: "curl\\s+.*-X\\s*POST", message: "POST" },
  { id: "no-scp", pattern: "\\bscp\\b", message: "scp" },
  { id: "no-psql", pattern: "psql\\s+.*prod", message: "prod db" },
];
add("MatchPatternsFiveCommandsUserRules", () => matchPatterns("bash", { command: commands[commandAt++ % commands.length]! }, "/work/repo", { commandRules: userRules as never, commandDenyRules: userRules.slice(0, 1) as never }));
add("MatchPatternsWritePath", () => matchPatterns("write", { path: "/home/user/project/src/internal/module/file_name.go", content: "x" }, "/home/user/project", {}));
add("MatchPathRulesCommand", () => matchPathRules("bash", { command: "go test ./... && cat notes.txt | tee out.log" }, "/work/project", pathRules, new Set()));
add("MatchPathRulesWrite", () => matchPathRules("write", { path: "/work/project/.env", content: "x" }, "/work/project", pathRules, new Set()));

// ---- the measurement ----
const gcSeen = new PerformanceObserver(() => {});
gcSeen.observe({ entryTypes: ["gc"] });
// node --expose-gc puts `gc` on the global object; the cast only names it, nothing external is read through it.
const collect = (globalThis as unknown as { gc?: () => void }).gc;
const isThenable = (v: unknown): v is PromiseLike<unknown> => typeof v === "object" && v !== null && "then" in v && typeof v.then === "function";

async function batch(run: () => unknown, n: number): Promise<void> {
  for (let i = 0; i < n; i++) { const r = run(); if (isThenable(r)) await r; }
}

// ns/op: warm up for 250 ms (the optimising compiler needs thousands of calls), then the median of five 100 ms windows.
async function nsPerOp(c: Case): Promise<number> {
  const warm = process.hrtime.bigint();
  while (process.hrtime.bigint() - warm < 250_000_000n) await batch(c.run, 10);
  const windows: number[] = [];
  for (let w = 0; w < 5; w++) {
    let n = 0;
    const started = process.hrtime.bigint();
    while (process.hrtime.bigint() - started < 100_000_000n) { await batch(c.run, 10); n += 10; }
    windows.push(Number(process.hrtime.bigint() - started) / n);
  }
  windows.sort((a, b) => a - b);
  return windows[2]!;
}

// bytes/op: the heap growth of a batch that fits one young generation (run with a large semi-space so that no scavenge
// happens between the two readings; a batch in which one was observed is retried smaller). Run with --min-semi-space-size.
async function bytesPerOp(c: Case): Promise<number> {
  await batch(c.run, 300);
  let size = 400;
  for (let attempt = 0; attempt < 8; attempt++, size = Math.max(5, size >> 1)) {
    collect?.();
    gcSeen.takeRecords();
    const before = process.memoryUsage().heapUsed;
    await batch(c.run, size);
    const after = process.memoryUsage().heapUsed;
    if (gcSeen.takeRecords().length === 0 && after > before) return (after - before) / size;
  }
  return 0;
}

const mode = process.argv[2] === "alloc" ? "alloc" : "time";
const only = process.argv[3];
for (const c of cases) {
  if (only && only !== c.name) continue;
  console.log(`${c.name} ${(mode === "time" ? await nsPerOp(c) : await bytesPerOp(c)).toFixed(0)}`);
}
