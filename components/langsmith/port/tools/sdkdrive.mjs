#!/usr/bin/env node
// sdkdrive.mjs: drive the real vendored langsmith JS SDK from an op script against mockls and keep the request log.
//
//   node sdkdrive.mjs --ops ops.json --mockls BIN --log requests.jsonl [--sdk DIR] [--console console.json]
//   node sdkdrive.mjs --ops ops.json --endpoint http://127.0.0.1:PORT      (a mockls somebody else started)
//
// --sdk is the langsmith package directory (default /home/kinsy/ls-work/oracle-run/node_modules/langsmith, or $LS_SDK).
//
// The op script (JSON):
//   { "name": ..., "env": {NAME: value}, "files": {relPath: content}, "readFiles": [relPath], "mockls": {mockls config},
//     "ops": [ ... ] }
// The process environment is wiped of LANGSMITH_*, LANGCHAIN_*, OTEL_* and HOME/USERPROFILE (HOME becomes an empty
// temp dir) before the SDK is imported, then `env` is applied. "{{endpoint}}" in any string of the script (env values
// included) is the mockls base URL.
//
// Ops (names mirror the SDK calls; fixed ids and times make the wire deterministic):
//   {"op":"client",   "as":"c",  "config":{apiUrl, apiKey, autoBatchTracing, ...}}   new Client(config)
//   {"op":"runTree",  "as":"r",  "client":"c", "config":{id, name, run_type, start_time(ms), project_name, inputs,
//                                  metadata, tags, replicas, extra, ...}}              new RunTree(config)
//   {"op":"createChild","parent":"r","as":"k","config":{...}}                           r.createChild(config)
//   {"op":"end",      "run":"r", "outputs":{}, "error":"msg", "endTime":ms, "metadata":{}}  r.end(...)
//   {"op":"addEvent", "run":"r", "event":{name, kwargs}}                                r.addEvent(event)
//   {"op":"postRun",  "run":"r"}   {"op":"patchRun","run":"r"}                          awaited
//   {"op":"createRun","client":"c","run":{...},"options":{...}}  {"op":"updateRun","client":"c","id":"..","run":{..}}
//   {"op":"awaitPendingTraceBatches","client":"c"}   {"op":"sleep","ms":300}
//
// The result (stdout, JSON): { name, sdkVersion, endpoint, console: [{level, text}], errors: [...], filesAfter }. console holds
// what the SDK wrote with console.warn/console.error (the messages the Go port must also print).
import { spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";

function parseArgs(argv) {
  const a = {};
  for (let i = 0; i < argv.length; i++) {
    if (!argv[i].startsWith("--")) throw new Error(`unexpected argument ${argv[i]}`);
    a[argv[i].slice(2)] = argv[++i];
  }
  return a;
}

const args = parseArgs(process.argv.slice(2));
if (!args.ops || (!args.mockls && !args.endpoint)) {
  console.error("usage: sdkdrive.mjs --ops FILE (--mockls BIN --log FILE | --endpoint URL) [--sdk DIR] [--console FILE]");
  process.exit(2);
}
const sdkDir = resolve(args.sdk ?? process.env.LS_SDK ?? "/home/kinsy/ls-work/oracle-run/node_modules/langsmith");
const script = JSON.parse(readFileSync(args.ops, "utf8"));

// ---- mockls ---------------------------------------------------------------------------------------------------
let mock = null;
let endpoint = args.endpoint;
const scratch = mkdtempSync(join(tmpdir(), "sdkdrive-"));
if (args.mockls) {
  const portFile = join(scratch, "port");
  const cfgFile = join(scratch, "mockls.json");
  writeFileSync(cfgFile, JSON.stringify(script.mockls ?? {}));
  const margs = ["-addr", "127.0.0.1:0", "-config", cfgFile, "-port-file", portFile];
  if (args.log) margs.push("-log", args.log);
  mock = spawn(args.mockls, margs, { stdio: ["ignore", "ignore", "inherit"] });
  for (let i = 0; i < 200 && !(existsSync(portFile) && readFileSync(portFile, "utf8").endsWith("\n")); i++) await delay(25);
  if (!existsSync(portFile)) throw new Error("mockls did not start");
  endpoint = `http://127.0.0.1:${readFileSync(portFile, "utf8").trim()}`;
}
const expand = (v) => {
  if (typeof v === "string") return v.replaceAll("{{endpoint}}", endpoint);
  if (Array.isArray(v)) return v.map(expand);
  if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, expand(x)]));
  return v;
};

// ---- environment ----------------------------------------------------------------------------------------------
for (const k of Object.keys(process.env)) {
  if (/^(LANGSMITH_|LANGCHAIN_|OTEL_|HOME$|USERPROFILE$)/.test(k)) delete process.env[k];
}
// HOME is an empty directory and the working directory: `files` (relative paths) are written there before the SDK
// loads, and a relative LANGSMITH_CONFIG_FILE points at one of them without putting a temp path on the wire.
const home = join(scratch, "home");
mkdirSync(home, { recursive: true });
process.env.HOME = home;
process.chdir(home);
for (const [rel, content] of Object.entries(expand(script.files ?? {}))) {
  mkdirSync(dirname(join(home, rel)), { recursive: true });
  writeFileSync(join(home, rel), typeof content === "string" ? content : JSON.stringify(content, null, 2));
}
for (const [k, v] of Object.entries(expand(script.env ?? {}))) process.env[k] = String(v);

// ---- console capture --------------------------------------------------------------------------------------------
const consoleLog = [];
for (const level of ["warn", "error"]) {
  const orig = console[level].bind(console);
  console[level] = (...a) => {
    consoleLog.push({ level, text: a.map((x) => (typeof x === "string" ? x : x instanceof Error ? `${x.name}: ${x.message}` : JSON.stringify(x))).join(" ") });
    orig(...a);
  };
}

const { Client } = await import(pathToFileURL(join(sdkDir, "dist", "client.js")).href);
const { RunTree } = await import(pathToFileURL(join(sdkDir, "dist", "run_trees.js")).href);
const sdkVersion = JSON.parse(readFileSync(join(sdkDir, "package.json"), "utf8")).version;

// ---- ops ------------------------------------------------------------------------------------------------------
const clients = new Map();
const runs = new Map();
const errors = [];
const need = (map, name, what) => {
  if (!map.has(name)) throw new Error(`op refers to unknown ${what} ${JSON.stringify(name)}`);
  return map.get(name);
};
// A run config's `client` is a name in the script, not an object.
const runConfig = (cfg, clientName) => {
  const c = expand(cfg ?? {});
  if (clientName) c.client = need(clients, clientName, "client");
  return c;
};

try {
  for (const [i, raw] of script.ops.entries()) {
    const op = expand(raw);
    try {
      switch (op.op) {
        case "client":
          clients.set(op.as, new Client(op.config ?? {}));
          break;
        case "runTree":
          runs.set(op.as, new RunTree(runConfig(op.config, op.client)));
          break;
        case "createChild":
          runs.set(op.as, need(runs, op.parent, "run").createChild(runConfig(op.config)));
          break;
        case "end":
          await need(runs, op.run, "run").end(op.outputs, op.error, op.endTime, op.metadata);
          break;
        case "addEvent":
          need(runs, op.run, "run").addEvent(op.event);
          break;
        case "postRun":
          await need(runs, op.run, "run").postRun(op.excludeChildRuns);
          break;
        case "patchRun":
          await need(runs, op.run, "run").patchRun();
          break;
        case "createRun":
          await need(clients, op.client, "client").createRun(op.run, op.options);
          break;
        case "updateRun":
          await need(clients, op.client, "client").updateRun(op.id, op.run, op.options);
          break;
        case "awaitPendingTraceBatches":
          await need(clients, op.client, "client").awaitPendingTraceBatches();
          break;
        case "sleep":
          await delay(op.ms);
          break;
        default:
          throw new Error(`unknown op ${JSON.stringify(op.op)}`);
      }
    } catch (e) {
      // An op that throws is data (the SDK's own error text), recorded, not fatal: the script may expect it.
      errors.push({ op: i, name: op.op, error: `${e?.name}: ${e?.message}` });
    }
  }
  // Whatever the script left queued is flushed the way a process exit would: nothing is added to the wire.
  for (const c of clients.values()) await c.awaitPendingTraceBatches();
} finally {
  if (mock) {
    mock.kill("SIGTERM");
    await new Promise((r) => mock.once("exit", r));
  }
}

// Files the script asks to see afterwards (a profile the SDK rewrote after an OAuth refresh).
const filesAfter = {};
for (const rel of script.readFiles ?? []) {
  filesAfter[rel] = existsSync(join(home, rel)) ? readFileSync(join(home, rel), "utf8") : null;
}
const result = { name: script.name, sdkVersion, endpoint, console: consoleLog, errors, filesAfter };
if (args.console) writeFileSync(args.console, JSON.stringify(result, null, 2) + "\n");
process.stdout.write(JSON.stringify(result) + "\n");
