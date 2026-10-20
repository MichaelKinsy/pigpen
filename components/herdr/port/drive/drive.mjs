// Replays the event sequences of ../../extensions/herdr/testdata/drive-cases.json against the ORIGINAL
// (../oracle/herdr-agent-state.ts) in Node, the way the original's own test does: a stub `pi` that keeps the
// handlers by name, a ctx per event, and a fake herdr on a unix socket. It writes the normalized effects to
// ../../extensions/herdr/testdata/drive-golden.json, which the Go test (drive_test.go) must reproduce byte for
// byte through the fake PiG host.
//
//   node --experimental-strip-types drive.mjs [cases.json [golden.json]]
//
// Normalization (the only one): the request id keeps its `pig:<kind>:` prefix and shape, and each seq becomes its
// rank among the case's distinct seq values (a retry repeats the request, so the same rank); `seqFromClock` says
// every seq is at or above the clock in microseconds at the start of the case. Everything else, key order and
// escapes included, is the raw request line.
import { readFileSync, writeFileSync, mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url));
const MODULE = join(here, "../oracle/herdr-agent-state.ts");
const casesFile = process.argv[2] ?? join(here, "../../extensions/herdr/testdata/drive-cases.json");
const goldenFile = process.argv[3] ?? join(here, "../../extensions/herdr/testdata/drive-golden.json");
const cases = JSON.parse(readFileSync(casesFile, "utf8"));
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export function normalize(lines, startMs) {
  const seqs = lines.map((l) => Number(/"seq":(\d+)/.exec(l.line)?.[1] ?? NaN)).filter((n) => !Number.isNaN(n));
  const distinct = [...new Set(seqs)].sort((a, b) => a - b);
  return {
    requests: lines.map((l) => ({
      conn: l.conn,
      line: l.line
        .replace(/"id":"pig:(state|release):\d+:[0-9a-z]+"/, '"id":"pig:$1:<ms>:<rand>"')
        .replace(/"seq":(\d+)/, (_, n) => `"seq":${distinct.indexOf(Number(n))}`),
    })),
    seqFromClock: seqs.every((n) => n >= startMs * 1000),
  };
}

async function startHerdr(spec) {
  const lines = [];
  let connections = 0;
  const dir = mkdtempSync(join(tmpdir(), "hd"));
  const path = join(dir, "s");
  const server = createServer((socket) => {
    const conn = connections++;
    const behavior = spec?.behaviors?.[conn] ?? "ok";
    let buffer = "";
    socket.on("error", () => {});
    socket.on("data", (chunk) => {
      buffer += chunk.toString();
      let newline = buffer.indexOf("\n");
      while (newline >= 0) {
        lines.push({ conn, line: buffer.slice(0, newline) });
        buffer = buffer.slice(newline + 1);
        if (behavior === "close") socket.end();
        else if (behavior !== "silent") setTimeout(() => socket.write('{"result":{}}\n'), spec?.delayMs ?? 0);
        newline = buffer.indexOf("\n");
      }
    });
  });
  await new Promise((resolve) => server.listen(path, resolve));
  return { lines, path, close: async () => { server.closeAllConnections?.(); await new Promise((resolve) => server.close(resolve)); rmSync(dir, { recursive: true, force: true }); } };
}

const BASE_CTX = { mode: "tui", idle: true, file: "/home/user/.pig/agent/sessions/one.jsonl", id: "session-one" };

function makeCtx(spec) {
  const value = (v, name) => () => { if (v === "throws") throw new Error(`no ${name}`); return v ?? undefined; };
  return {
    mode: spec.mode,
    isIdle: value(spec.idle, "idle state"),
    sessionManager: { getSessionFile: value(spec.file, "session"), getSessionId: value(spec.id, "session") },
  };
}

const golden = [];
for (const [index, c] of cases.entries()) {
  const herdr = await startHerdr(c.server);
  const saved = { ...process.env };
  const env = { HERDR_ENV: "1", HERDR_SOCKET_PATH: herdr.path, HERDR_PANE_ID: "w1:p1", HERDR_BIN_PATH: "/unused", ...(c.env ?? {}) };
  for (const [k, v] of Object.entries(env)) (v === null ? delete process.env[k] : (process.env[k] = v));
  const startMs = Date.now();
  let handlers;
  let busHandlers;
  let instances = 0;
  // One extension instance: the module is imported fresh, as Pi's loader does for every session.
  const load = async () => {
    handlers = new Map();
    busHandlers = new Map();
    const pi = {
      on: (name, handler) => handlers.set(name, handler),
      events: { on: (name, handler) => busHandlers.set(name, handler) },
    };
    instances += 1;
    const { default: install } = await import(`${MODULE}?case=${index}&instance=${instances}`);
    install(pi);
  };
  await load();
  const steps = [];
  for (const step of c.steps) {
    const ctx = makeCtx({ ...BASE_CTX, ...(c.mode ? { mode: c.mode } : {}), ...(c.ctx ?? {}), ...(step.ctx ?? {}) });
    let result = "ok";
    try {
      switch (step.op) {
        case "start": if (step.fresh) await load(); await handlers.get("session_start")?.({ reason: "startup" }, ctx); break;
        case "agent_start": await handlers.get("agent_start")?.({}, ctx); break;
        case "agent_settled": await handlers.get("agent_settled")?.({}, ctx); break;
        case "shutdown": await handlers.get("session_shutdown")?.(step.reason === undefined ? {} : { reason: step.reason }); break;
        case "bus": await busHandlers.get("herdr:blocked")?.(step.data); break;
        case "sleep": await sleep(step.ms); break;
        default: throw new Error(`unknown op ${step.op}`);
      }
    } catch {
      result = "throws";
    }
    steps.push(result);
    await sleep(step.waitMs ?? 60);
  }
  await sleep(c.finalMs ?? 150);
  golden.push({
    name: c.name,
    handlers: [...handlers.keys()].sort(),
    bus: [...busHandlers.keys()].sort(),
    steps,
    ...normalize(herdr.lines, startMs),
  });
  await herdr.close();
  for (const k of Object.keys(process.env)) if (!(k in saved)) delete process.env[k];
  Object.assign(process.env, saved);
}
writeFileSync(goldenFile, `${JSON.stringify(golden, null, 1)}\n`);
console.log(`${golden.length} cases, ${golden.reduce((n, g) => n + g.requests.length, 0)} requests -> ${goldenFile}`);
