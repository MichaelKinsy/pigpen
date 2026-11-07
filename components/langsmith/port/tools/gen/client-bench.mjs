// Throughput / allocation baseline of the real langsmith 0.10.5 Client for the same workload as
// BenchmarkEndToEnd in client_bench_test.go: 1000 runs (create + update) to a loopback server that discards them.
//   node client-bench.mjs
import http from "node:http";
import { load } from "./wire-common.mjs";

const { Client } = await load("client.js");
const server = http.createServer((req, res) => {
  req.resume();
  req.on("end", () => {
    res.setHeader("content-type", "application/json");
    res.end(req.url === "/info" ? JSON.stringify({ batch_ingest_config: { use_multipart_endpoint: true, size_limit: 100, size_limit_bytes: 20971520 }, instance_flags: { gzip_body_enabled: false } }) : "{}");
  });
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const base = `http://127.0.0.1:${server.address().port}`;
const client = new Client({ apiKey: "k", apiUrl: base });
client.autoBatchAggregationDelayMs = 5;

const id = (n) => `0195b2a0-7e41-7c3d-9f00-${(100000000000 + n).toString()}`;
const run = (n) => ({
  id: id(n), name: "turn", start_time: "2024-03-01T10:00:00.010000", end_time: "2024-03-01T10:00:09.010000", run_type: "llm", reference_example_id: undefined,
  extra: { metadata: { model: "m", provider: "p", thinking: "high", turn: n }, runtime: { library: "langsmith" } }, serialized: undefined, error: undefined,
  inputs: { messages: Array.from({ length: 6 }, (_, i) => ({ role: "user", content: "message body é ".repeat(20) + i, timestamp: 1700000000000 + i })), tools: [] },
  outputs: { output: { role: "assistant", content: "answer ".repeat(60) } }, session_name: "proj", child_runs: [], parent_run_id: undefined,
  trace_id: id(n), dotted_order: `20240301T100000010000Z${id(n)}`, tags: ["pi", "t"], attachments: undefined, events: undefined,
});

const N = Number(process.env.N ?? 20);
await client.createRun(run(-1));
await client.awaitPendingTraceBatches();
global.gc?.();
const rss0 = process.memoryUsage().rss;
let peak = rss0;
const t0 = process.hrtime.bigint();
for (let k = 0; k < N; k++) {
  for (let i = 0; i < 1000; i++) {
    const r = run(i);
    await client.createRun(r);
    await client.updateRun(id(i), { outputs: { o: "x" }, end_time: "2024-03-01T10:00:09.010000", trace_id: r.trace_id, dotted_order: r.dotted_order });
  }
  await client.awaitPendingTraceBatches();
  peak = Math.max(peak, process.memoryUsage().rss);
}
const ns = Number(process.hrtime.bigint() - t0);
console.log(JSON.stringify({ iterations: N, ms_per_1000_runs: ns / 1e6 / N, rss_mb_before: rss0 / 1048576, rss_mb_peak: peak / 1048576 }));
server.close();
process.exit(0);
