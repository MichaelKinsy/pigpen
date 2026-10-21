// Times the original's context-saver hot paths on the inputs of extensions/warden/context_bench_test.go, so the Go
// port can be compared with the original under Node. Run from port/oracle: `node --import tsx ../bench/bench-original.ts`.
import { compressOutput, outputKey } from "../oracle/src/output.js";
import { SeenText } from "../oracle/src/dedupe.js";
import { buildFilterBatches, chunkLines } from "../oracle/src/filter.js";
import { buildRequests, buildUnits, renderSummary } from "../oracle/src/relevance.js";

const benchLog = (lines: number) => {
  const out = Array.from({ length: lines }, (_, i) => `progress ${i} complete: compiled module ${i} without warnings`);
  out[Math.floor(lines / 2)] = "ERROR: parser.test.ts keeps the header: expected 3 to equal 4";
  return `${out.join("\n")}\nsummary: 1 failed, 3999 passed\nexit code 1\n`;
};
const benchTsc = (lines: number) => {
  const out: string[] = [];
  for (let i = 0; i < lines; i++) out.push(i % 40 === 0 ? `src/f${i}.ts(${i % 90 + 1},5): error TS2322: Type 'string' is not assignable to type 'number'.` : `progress ${i} complete: compiled module ${i}`);
  return `${out.join("\n")}\nFound ${lines / 40} errors in ${lines / 40} files.`;
};
const report = (n: number, tag: string) => Array.from({ length: n }, (_, i) => `${tag} line ${i}: the build of module ${i} passed with no warnings at all`).join("\n");
const fresh = (chars: number) => Array.from({ length: Math.ceil(chars / 50) }, (_, i) => `new status line ${i} ${"n".repeat(28)}`).join("\n");

// BENCH_ONLY (a regular expression on the case name) limits the run; BENCH_ITERS replaces the iteration count and
// makes the warm-up as long as the timed run (default: 3 warm-up runs and 25 timed ones, as in the first measurements).
function median(name: string, run: () => unknown, iterations = 25): void {
  if (process.env.BENCH_ONLY && !new RegExp(process.env.BENCH_ONLY).test(name)) return;
  if (process.env.BENCH_ITERS) iterations = Number(process.env.BENCH_ITERS);
  for (let i = 0; i < (process.env.BENCH_ITERS ? iterations : 3); i++) run();
  const times: number[] = [];
  for (let i = 0; i < iterations; i++) { const start = process.hrtime.bigint(); run(); times.push(Number(process.hrtime.bigint() - start) / 1e6); }
  times.sort((a, b) => a - b);
  console.log(`${name}\t${times[Math.floor(times.length / 2)]!.toFixed(3)} ms`);
}

const log = benchLog(3500);
const tsc = benchTsc(2000);
median("CompressOutputGeneric200KB", () => compressOutput(log, "errors_and_summary"));
median("CompressOutputParserTsc100KB", () => compressOutput(tsc, "errors_and_summary", "tsc"));
median("OutputKey200KB", () => outputKey(log));
const rep = report(5000, "report");
const text = `Turn 4\n${rep}\n${fresh(4000)}`;
median("SeenTextAddFind5000Lines", () => { const seen = new SeenText(); seen.add(rep, "an earlier bash result"); return seen.find(text); });
const input = { tool: "bash", command: "npm test", task: "Fix the failing parser test", plan: "Run the suite" };
median("FilterChunksAndBatches200KB", () => buildFilterBatches(chunkLines(log, 2000), input), 10);
const span: any[] = [{ role: "user", content: "u".repeat(3000) }];
for (let i = 0; i < 120; i++) {
  span.push({ role: "assistant", content: [{ type: "text", text: `step ${i} ${"t".repeat(600)}` }, { type: "toolCall", id: `c${i}`, name: "bash", arguments: { command: `run ${i} ${"a".repeat(900)}` } }] });
  span.push({ role: "toolResult", toolCallId: `c${i}`, toolName: "bash", content: [{ type: "text", text: "o".repeat(5000) }], isError: false });
}
median("RelevanceBuildRequests120Calls", () => { const { units } = buildUnits({ messages: span }); return buildRequests(units, { task: "t" }); }, 10);
const built = buildUnits({ messages: span });
const keepAll = new Set(built.units.flatMap(u => "id" in u ? [u.id] : []));
median("RelevanceRenderSummary120Calls", () => renderSummary(built.units, built.files, keepAll), 10);
