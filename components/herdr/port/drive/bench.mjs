// Latency of the ORIGINAL's handlers in Node, the counterpart of extensions/herdr/perf_test.go (TestPerf): the same
// event sequence, the same medians in microseconds, printed as one `PERF {json}` line.
//
//   node --experimental-strip-types bench.mjs [samples]
//
// "register" is the import of the module plus the call of its factory (Node's own start-up is not in it). "handler" is the
// time the handler takes to return, sync part included (session_shutdown awaits its release); "reaches herdr" is the time
// from the call until the fake herdr socket received the request.
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url));
const MODULE = join(here, "../oracle/herdr-agent-state.ts");
const samples = Number(process.argv[2] ?? 30);
const us = (ns) => Number(ns) / 1000;
const median = (xs) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)];
const now = () => process.hrtime.bigint();

const ctx = { mode: "tui", isIdle: () => true, sessionManager: { getSessionFile: () => "/home/user/.pig/agent/sessions/one.jsonl", getSessionId: () => "session-one" } };
const register = [];
const handler = {};
const reach = {};
for (let i = 0; i < samples; i++) {
  const dir = mkdtempSync(join(tmpdir(), "hd"));
  const path = join(dir, "s");
  let received = 0;
  const sockets = new Set();
  const server = createServer((socket) => {
    sockets.add(socket);
    socket.on("error", () => {});
    socket.on("data", () => { received += 1; socket.write('{"result":{}}\n'); });
  });
  await new Promise((resolve) => server.listen(path, resolve));
  process.env.HERDR_ENV = "1";
  process.env.HERDR_SOCKET_PATH = path;
  process.env.HERDR_PANE_ID = "w1:p1";
  const handlers = new Map();
  const bus = new Map();
  const pi = { on: (n, h) => handlers.set(n, h), events: { on: (n, h) => bus.set(n, h) } };
  const begin = now();
  const { default: install } = await import(`${MODULE}?sample=${i}`);
  install(pi);
  register.push(us(now() - begin));

  const step = async (name, run, expectRequest = true) => {
    const before = received;
    const t0 = now();
    await run();
    (handler[name] ??= []).push(us(now() - t0));
    if (!expectRequest) return;
    while (received === before) await new Promise((resolve) => setImmediate(resolve));
    (reach[name] ??= []).push(us(now() - t0));
  };
  await step("session_start", () => handlers.get("session_start")({ reason: "startup" }, ctx));
  await step("agent_start", () => handlers.get("agent_start")({}, ctx));
  await step("herdr:blocked", () => bus.get("herdr:blocked")({ active: true, label: "approve command" }));
  await step("herdr:unblocked", () => bus.get("herdr:blocked")({ active: false }));
  await step("agent_settled", () => handlers.get("agent_settled")({}, ctx));
  await step("shutdown-resume", () => handlers.get("session_shutdown")({ reason: "resume" }), false);
  await step("quit", () => handlers.get("session_shutdown")({ reason: "quit" }));
  for (const socket of sockets) socket.destroy();
  await new Promise((resolve) => server.close(resolve));
  rmSync(dir, { recursive: true, force: true });
}
const out = { impl: "node-original", register_us: median(register) };
for (const [k, v] of Object.entries(handler)) out[`handler_${k}_us`] = median(v);
for (const [k, v] of Object.entries(reach)) out[`reaches_herdr_${k}_us`] = median(v);
out.rss_mb = Math.round(process.memoryUsage().rss / 1e5) / 10;
console.log("PERF", JSON.stringify(out));
