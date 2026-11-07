#!/usr/bin/env node
// materialise.mjs SCENARIO.json OUT_SCENARIO.json OUT_MOCKLS.json BASE_URL
//
// Turns a pigeq scenario that talks to its `ls` fake server into one that talks to a mockls instance at BASE_URL:
// every {{server:ls}} becomes BASE_URL and the `ls` server entry is dropped (the mock records the requests, not the
// harness). The `ls` server's routes are translated into the equivalent mockls config, so one source describes
// both: GET /info -> info, a route with a status/body/delay/drop and `times: n` -> a fault on calls
// consumed+1..consumed+n of that mockls route (a route without `times` runs to the end).
import { readFileSync, writeFileSync } from "node:fs";

const [scenarioPath, outScenario, outCfg, base] = process.argv.slice(2);
if (!base) {
  console.error("usage: materialise.mjs SCENARIO OUT_SCENARIO OUT_MOCKLS BASE_URL");
  process.exit(2);
}
const text = readFileSync(scenarioPath, "utf8");
const sc = JSON.parse(text);
const routes = sc.servers?.ls?.routes ?? [];

function routeName(r) {
  const m = (r.method ?? "").toUpperCase();
  const p = r.path;
  if (m === "GET" && p === "/info") return "info";
  if (m === "POST" && p === "/runs/multipart") return "multipart";
  if (m === "POST" && p === "/runs/batch") return "batch";
  if (m === "POST" && p === "/runs") return "runs";
  if (m === "PATCH" && p.startsWith("/runs/")) return "patch";
  if (p.includes("/.well-known/oauth-authorization-server")) return "oauth-metadata";
  if (p.endsWith("/oauth/token")) return "token";
  throw new Error(`${scenarioPath}: ls route ${m} ${p} has no mockls equivalent`);
}

const cfg = {};
const consumed = {};
const faults = [];
for (const r of routes) {
  const name = routeName(r);
  const body = r.json !== undefined ? r.json : r.body;
  if (name === "info" && r.json !== undefined && !r.status) {
    cfg.info = r.json;
    continue;
  }
  if (name === "oauth-metadata" && r.json !== undefined && !r.status) {
    cfg.oauthMetadata = JSON.parse(JSON.stringify(r.json).replaceAll("{{server:ls}}", "{{base}}"));
    continue;
  }
  if (name === "token" && r.json !== undefined && !r.status) {
    cfg.token = r.json;
    continue;
  }
  const from = (consumed[name] ?? 0) + 1;
  if (r.times) consumed[name] = from - 1 + r.times;
  else consumed[name] = Infinity;
  const effect = r.status || r.delayMs || r.drop || (r.headers && Object.keys(r.headers).length);
  const passes = !effect && (body === undefined || JSON.stringify(body) === "{}" || body === "{}");
  if (passes) continue;
  const f = { route: name, from };
  if (r.times) f.to = from - 1 + r.times;
  if (r.status) f.status = r.status;
  if (r.headers) f.headers = r.headers;
  if (r.delayMs) f.delayMs = r.delayMs;
  if (r.drop) f.drop = true;
  if (r.json !== undefined) f.json = r.json;
  else if (r.body) f.body = r.body;
  faults.push(f);
}
if (faults.length) cfg.faults = faults;

delete sc.servers?.ls;
if (sc.servers && Object.keys(sc.servers).length === 0) delete sc.servers;
const out = JSON.stringify(sc).replaceAll("{{server:ls}}", base);
writeFileSync(outScenario, JSON.stringify(JSON.parse(out), null, 2) + "\n");
writeFileSync(outCfg, JSON.stringify(cfg, null, 2) + "\n");
