#!/usr/bin/env node
// Wire differential (Skill kind 4): the same scripted ACP client drives the original, pi-acp 0.0.34 running
// under Pi, and the port, pig-acp running pig. Both talk to a scripted model (`pigeq llm`) with the same
// turns and the same configuration files. The two wire transcripts are normalized (rules below, each named
// and counted) and compared entry by entry. A difference is either recorded in findings.json, with the
// exact entries on both sides, or it fails the run; a recorded finding that no longer occurs fails too.
//
//   node run.mjs --oracle-cmd '<command that starts pi-acp>' --port-cmd '<command that starts pig-acp --pig <pig>>' \
//        --pigeq <pigeq> --pi <real pi> --out <dir> [--only name[,name]] [--side both|oracle|port]
//        [--bench N [--sides oracle,port,prev --prev-cmd '<the previous release of the port>']]
//
// The oracle's `pi` is a shim in a private bin directory that answers `--version` with a plain semver (the real
// pi prints 1.3.0.<sha>, which the original's semver test rejects) and runs the real pi otherwise.
// `npm` is a shim for both sides (the update lookup: the original asks for @earendil-works/pi-coding-agent, the port for @pi-in-go/pig).
// Each side gets its own temporary HOME, PIG_HOME and agent directory; nothing outside them is read or written.
import { spawn } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { scenarios } from './scenarios.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);
const opt = (n, d) => (args.includes(n) ? args[args.indexOf(n) + 1] : d);
const need = (n) => {
  const v = opt(n);
  if (!v) throw new Error(`set ${n}`);
  return v;
};
const ORACLE_CMD = opt('--oracle-cmd');
const PORT_CMD = opt('--port-cmd');
const PIGEQ = need('--pigeq');
const REAL_PI = opt('--pi', '/opt/pp-shared-pig/bin/pi');
const OUT = need('--out');
const only = opt('--only') ? new Set(opt('--only').split(',')) : null;
const PREV_CMD = opt('--prev-cmd'); // --bench only: the previous release of the port
const sides = opt('--sides') ? opt('--sides').split(',') : opt('--side', 'both') === 'both' ? ['oracle', 'port'] : [opt('--side')];
const NEWER = '99.0.0';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const within = (p, ms, what) => Promise.race([p, new Promise((_, rej) => setTimeout(() => rej(new Error(`timed out: ${what}`)), ms).unref())]);

function lookup(vars, path) {
  return path.split('.').reduce((o, k) => (o == null ? undefined : o[k]), vars);
}
function subst(value, vars) {
  if (typeof value === 'string') {
    const whole = value.match(/^\$\{([^}]+)\}$/);
    if (whole) return lookup(vars, whole[1]);
    return value.replace(/\$\{([^}]+)\}/g, (_, p) => String(lookup(vars, p)));
  }
  if (Array.isArray(value)) return value.map((v) => subst(v, vars));
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, subst(v, vars)]));
  return value;
}

// ---------------------------------------------------------------------------------------------- one side

async function runSide(scenario, side) {
  const root = mkdtempSync(join(tmpdir(), `acp-diff-${side}-`));
  const dirs = { HOME: join(root, 'home'), PIG_HOME: join(root, 'pighome'), AGENT: join(root, 'agent'), CWD: join(root, 'work'), BIN: join(root, 'bin') };
  for (const d of Object.values(dirs)) mkdirSync(d, { recursive: true });
  const cleanup = [];
  try {
    // The scripted model.
    const turns = scenario.turns ?? [];
    writeFileSync(join(root, 'turns.json'), JSON.stringify(turns));
    const llm = spawn(PIGEQ, ['llm', '--script', join(root, 'turns.json'), '--log', join(root, 'llm.log')], { stdio: ['pipe', 'pipe', 'inherit'] });
    cleanup.push(() => llm.kill());
    const baseURL = await within(new Promise((res) => llm.stdout.once('data', (d) => res(String(d).trim()))), 15000, 'pigeq llm address');

    // Configuration both agents read (PIG_CODING_AGENT_DIR and PI_CODING_AGENT_DIR are the same directory).
    if (scenario.models !== 'none') {
      writeFileSync(
        join(dirs.AGENT, 'models.json'),
        JSON.stringify({ providers: { 'acp-llm': { baseUrl: baseURL, api: 'openai-completions', apiKey: 'acp-key', models: [{ id: 'acp-1', name: 'ACP One', reasoning: true, input: ['text', 'image'], contextWindow: 100000, maxTokens: 4096, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }] } } }),
      );
    }
    writeFileSync(join(dirs.AGENT, 'settings.json'), JSON.stringify({ defaultProvider: 'acp-llm', defaultModel: 'acp-1', quietStartup: scenario.quiet !== false }));
    for (const [rel, body] of Object.entries(scenario.files ?? {})) {
      const [where, ...rest] = rel.split('/');
      const target = join(where === 'work' ? dirs.CWD : dirs.AGENT, ...rest);
      mkdirSync(dirname(target), { recursive: true });
      writeFileSync(target, body);
    }
    // The original reads prompts and extensions from a fixed ~/.pi/agent, not from PI_CODING_AGENT_DIR (agent.ts, buildStartupInfo).
    for (const rel of Object.keys(scenario.files ?? {})) {
      if (side === 'oracle' && rel.startsWith('agent/')) {
        const target = join(dirs.HOME, '.pi', 'agent', ...rel.split('/').slice(1));
        mkdirSync(dirname(target), { recursive: true });
        writeFileSync(target, scenario.files[rel]);
      }
    }
    // The port reads PiG's project directory (.pig); the original reads .pi. Mirror project skills so both see them.
    for (const rel of Object.keys(scenario.files ?? {})) {
      if (rel.startsWith('work/.pi/')) {
        const target = join(dirs.CWD, '.pig', ...rel.split('/').slice(2));
        mkdirSync(dirname(target), { recursive: true });
        writeFileSync(target, scenario.files[rel]);
      }
    }

    // Shims: pi (a plain semver for --version) and npm (the original's update lookup).
    writeFileSync(join(dirs.BIN, 'pi'), `#!/bin/sh\nif [ "$1" = "--version" ]; then echo 1.3.0; exit 0; fi\nexec ${REAL_PI} "$@"\n`);
    // The original never looks at an offline switch, so "offline" is an npm that fails, as it does without a network.
    writeFileSync(join(dirs.BIN, 'npm'), scenario.update === 'newer' ? `#!/bin/sh\ncase "$1 $2 $3" in "view @earendil-works/pi-coding-agent version" | "view @pi-in-go/pig version") echo ${NEWER}; exit 0;; esac\nexit 1\n` : '#!/bin/sh\nexit 1\n');
    chmodSync(join(dirs.BIN, 'pi'), 0o755);
    chmodSync(join(dirs.BIN, 'npm'), 0o755);

    const env = {
      PATH: `${dirs.BIN}:${process.env.PATH}`,
      HOME: dirs.HOME,
      PIG_HOME: dirs.PIG_HOME,
      PIG_CODING_AGENT_DIR: dirs.AGENT,
      PI_CODING_AGENT_DIR: dirs.AGENT,
      PI_TELEMETRY: '0',
      NO_COLOR: '1',
    };
    // The port honors the offline switches; the original has none, so for it "offline" is the failing npm shim above.
    if (scenario.update !== 'newer') Object.assign(env, { PIG_OFFLINE: '1', PI_OFFLINE: '1', PI_SKIP_VERSION_CHECK: '1' });

    const cmd = { oracle: ORACLE_CMD, port: PORT_CMD, prev: PREV_CMD }[side].split(' ');
    const spawnedAt = performance.now();
    const adapter = spawn(cmd[0], cmd.slice(1), { env, cwd: dirs.CWD, stdio: ['pipe', 'pipe', 'inherit'] });
    const timings = [];
    cleanup.push(() => adapter.kill());
    const exited = new Promise((res) => adapter.on('exit', (code, signal) => res({ code, signal })));

    // The wire.
    const log = [];
    let last = Date.now();
    const pending = new Map();
    let nextId = 0;
    let permission = 'first';
    const send = (msg) => {
      log.push({ dir: 'client->agent', msg });
      last = Date.now();
      adapter.stdin.write(JSON.stringify(msg) + '\n');
    };
    let buf = '';
    adapter.stdout.on('data', (chunk) => {
      buf += chunk.toString('utf8');
      let i;
      while ((i = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, i);
        buf = buf.slice(i + 1);
        if (!line.trim()) continue;
        const msg = JSON.parse(line);
        log.push({ dir: 'agent->client', msg });
        last = Date.now();
        if (msg.method && msg.id !== undefined) {
          // A request from the agent: permission answers per the script, everything else is refused.
          if (msg.method === 'session/request_permission') {
            const options = msg.params?.options ?? [];
            const outcome = permission === 'cancelled' || !options.length ? { outcome: 'cancelled' } : { outcome: 'selected', optionId: typeof permission === 'string' && permission !== 'first' ? permission : options[0].optionId };
            send({ jsonrpc: '2.0', id: msg.id, result: { outcome } });
          } else {
            send({ jsonrpc: '2.0', id: msg.id, error: { code: -32601, message: 'Method not found' } });
          }
        } else if (msg.id !== undefined && pending.has(msg.id)) {
          pending.get(msg.id)(msg);
          pending.delete(msg.id);
        }
      }
    });
    const settle = async (quiet = 500, max = 8000) => {
      const start = Date.now();
      while (Date.now() - last < quiet && Date.now() - start < max) await sleep(50);
    };
    const request = async (method, params) => {
      const id = ++nextId;
      const answered = new Promise((res) => pending.set(id, res));
      const t0 = timings.length ? performance.now() : spawnedAt; // the first request also pays the process start
      send({ jsonrpc: '2.0', id, method, params });
      const reply = await within(answered, 120000, `${scenario.name}: ${method}`);
      timings.push({ step: method === 'session/prompt' ? `prompt ${String(params.prompt?.[0]?.text ?? '').slice(0, 24)}` : method, ms: performance.now() - t0 });
      await settle();
      return reply;
    };

    const vars = { ...dirs };
    for (const step of scenario.steps) {
      if (step.init) {
        const reply = await request('initialize', step.init);
        vars.init = reply.result;
      } else if (step.req) {
        const reply = await request(step.req, subst(step.params ?? {}, vars));
        if (step.as) vars[step.as] = reply.result;
      } else if (step.notify) {
        send({ jsonrpc: '2.0', method: step.notify, params: subst(step.params ?? {}, vars) });
        await settle();
      } else if (step.permission) {
        permission = step.permission;
      } else if (step.sleep) {
        await sleep(step.sleep);
      }
    }

    // Peak resident memory of the adapter process itself (Linux), before it is told to stop.
    let peakKB = null;
    try {
      const m = /VmHWM:\s+(\d+) kB/.exec(readFileSync(`/proc/${adapter.pid}/status`, 'utf8'));
      if (m) peakKB = Number(m[1]);
    } catch {}
    adapter.stdin.end();
    const exit = await within(exited, 30000, `${scenario.name}: the adapter to exit`).catch(() => ({ code: 'timeout' }));
    log.push({ dir: 'process', msg: { exit: exit.code, signal: exit.signal ?? null } });
    return { log, dirs, root, timings, peakKB, llmLog: existsSync(join(root, 'llm.log')) ? readFileSync(join(root, 'llm.log'), 'utf8') : '' };
  } finally {
    for (const c of cleanup) c();
    // The directories are kept until the caller has normalized the log (it needs the paths).
  }
}

// ---------------------------------------------------------------------------------------------- normalization

// Every rule has an id and a reason; the report lists how often each fired, so a normalization is a
// recorded decision, not a silent one.
const rules = [
  { id: 'N1', why: 'the agent name: pi-acp / pig-acp (D8)', fn: (s) => s.replace(/\bpig?-acp\b/g, '<ACP>') },
  { id: 'N2', why: 'the agent version line of the startup block and the agent version (D11)', fn: (s) => s.replace(/^pig? v\S+/gm, '<AGENT> v<VERSION>') },
  { id: 'N3', why: 'the terminal login label (D8)', fn: (s) => s.replace(/\bLaunch pig?\b/g, 'Launch <AGENT>') },
  { id: 'N4', why: 'the update notice text: package, command and installed version differ by design (D12, D14)', fn: (s) => s.replace(/New version available: v([\d.]+) \(installed v[^)]*\)\. Run: .*$/gm, 'New version available: v$1 (installed <VERSION>). Run: <UPDATE-COMMAND>') },
  { id: 'N5', why: 'timestamps', fn: (s) => s.replace(/\d{4}-\d\d-\d\d[T_]\d\d[:-]\d\d[:-]\d\d(?:[.-]\d+)?Z?/g, '<TS>') },
  { id: 'N6', why: 'session and message identifiers (uuids, in order of first appearance)', fn: null },
];

function normalizer(dirs, root) {
  const uuids = new Map();
  const fired = {};
  const count = (id) => (fired[id] = (fired[id] ?? 0) + 1);
  const paths = [
    // Pi names a session directory after the cwd: "--tmp-acp-diff-x-work--".
    [`--${dirs.CWD.replace(/^\//, '').replace(/[/\\:]/g, '-')}--`, '<CWD-ENCODED>'],
    [join(dirs.CWD), '<CWD>'],
    [join(dirs.AGENT), '<AGENT>'],
    [join(dirs.PIG_HOME), '<PIGHOME>'],
    [join(dirs.HOME), '<HOME>'],
    [root, '<ROOT>'],
  ];
  const str = (s) => {
    let out = s;
    for (const [p, name] of paths) out = out.split(p).join(name);
    for (const r of rules) {
      if (!r.fn) continue;
      const next = r.fn(out);
      if (next !== out) count(r.id);
      out = next;
    }
    const next = out.replace(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi, (u) => {
      if (!uuids.has(u)) uuids.set(u, `<UUID${uuids.size + 1}>`);
      return uuids.get(u);
    });
    if (next !== out) count('N6');
    return next;
  };
  const walk = (v, key) => {
    if (typeof v === 'string') return str(v);
    if (Array.isArray(v)) return v.map((x) => walk(x));
    if (v && typeof v === 'object') return Object.fromEntries(Object.entries(v).map(([k, x]) => [str(k), walk(x, k)]));
    return v;
  };
  return { walk, fired };
}

function entries(log, dirs, root) {
  const { walk, fired } = normalizer(dirs, root);
  const agentIds = new Map(); // the agent's request ids, renumbered a1, a2, ...
  const methodOf = new Map(); // "<c|a><id>" -> method
  const out = [];
  for (const { dir, msg } of log) {
    if (dir === 'process') {
      out.push({ kind: 'exit', ...msg });
      continue;
    }
    const fromClient = dir === 'client->agent';
    const e = { dir: fromClient ? 'c>a' : 'a>c' };
    if (msg.method && msg.id !== undefined) {
      e.kind = 'request';
      e.method = msg.method;
      if (fromClient) e.id = msg.id;
      else {
        agentIds.set(msg.id, `a${agentIds.size + 1}`);
        e.id = agentIds.get(msg.id);
      }
      methodOf.set(`${fromClient ? 'c' : 'a'}${msg.id}`, msg.method);
      e.params = walk(msg.params);
    } else if (msg.method) {
      e.kind = 'notification';
      e.method = msg.method;
      e.params = walk(msg.params);
    } else {
      // A response travels the other way from its request.
      e.kind = 'response';
      e.id = fromClient ? agentIds.get(msg.id) : msg.id;
      e.method = methodOf.get(`${fromClient ? 'a' : 'c'}${msg.id}`);
      if ('result' in msg) e.result = walk(msg.result);
      if ('error' in msg) e.error = walk(msg.error);
    }
    out.push(e);
  }
  return { entries: out, fired };
}

// ---------------------------------------------------------------------------------------------- comparison

const canon = (v) => JSON.stringify(v, (_, x) => (x && typeof x === 'object' && !Array.isArray(x) ? Object.fromEntries(Object.entries(x).sort(([a], [b]) => (a < b ? -1 : 1))) : x));

// An order-preserving diff (longest common subsequence) of two entry lists, as gaps between identical entries.
function gaps(a, b) {
  const ca = a.map(canon);
  const cb = b.map(canon);
  const n = ca.length;
  const m = cb.length;
  const dp = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--) dp[i][j] = ca[i] === cb[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
  const out = [];
  let cur = { oracle: [], port: [] };
  const flush = () => {
    if (cur.oracle.length || cur.port.length) out.push(cur);
    cur = { oracle: [], port: [] };
  };
  let i = 0;
  let j = 0;
  while (i < n || j < m) {
    if (i < n && j < m && ca[i] === cb[j]) {
      flush();
      i++;
      j++;
    } else if (j >= m || (i < n && dp[i + 1][j] >= dp[i][j + 1])) cur.oracle.push(a[i++]);
    else cur.port.push(b[j++]);
  }
  flush();
  return { gaps: out, same: dp[0][0] };
}

// What an entry is, for pairing and for naming a difference.
const head = (e) => [e.dir, e.kind, e.method ?? '', e.kind === 'notification' && e.method === 'session/update' ? e.params?.update?.sessionUpdate ?? '' : ''].filter((x) => x !== '').join(' ');

// Leaf differences between two JSON values: [{ path, oracle, port }].
function leaves(o, p, path = '') {
  if (canon(o) === canon(p)) return [];
  const isObj = (v) => v && typeof v === 'object';
  if (isObj(o) && isObj(p) && Array.isArray(o) === Array.isArray(p)) {
    const keys = [...new Set([...Object.keys(o), ...Object.keys(p)])];
    return keys.flatMap((k) => leaves(o[k], p[k], path ? `${path}.${k}` : k));
  }
  return [{ path, oracle: o === undefined ? '<absent>' : o, port: p === undefined ? '<absent>' : p }];
}

// Pair the entries of a gap by what they are (first fit, in order); the rest are only on one side.
function pairUp(gap) {
  const port = [...gap.port];
  const diffs = [];
  const only = [];
  for (const o of gap.oracle) {
    const k = port.findIndex((q) => head(q) === head(o) && (o.kind !== 'response' || o.id === q.id));
    if (k < 0) {
      only.push({ side: 'oracle', at: head(o), entry: o });
      continue;
    }
    const [q] = port.splice(k, 1);
    for (const l of leaves(o, q)) diffs.push({ at: head(o), ...l });
  }
  for (const q of port) only.push({ side: 'port', at: head(q), entry: q });
  return { diffs, only };
}

// ---------------------------------------------------------------------------------------------- main

mkdirSync(OUT, { recursive: true });

// --bench N: N interleaved runs of each scenario named by --only on both sides; medians of every step, and of
// the adapter's peak resident memory. The first request's time includes starting the process (cold start).
if (opt('--bench')) {
  const n = Number(opt('--bench'));
  const median = (xs) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)];
  const report = {};
  for (const scenario of scenarios.filter((sc) => !only || only.has(sc.name))) {
    const samples = Object.fromEntries(sides.map((sd) => [sd, []]));
    for (let i = 0; i < n; i++) {
      for (const side of sides) {
        const run = await runSide(scenario, side);
        samples[side].push({ timings: run.timings, peakKB: run.peakKB });
        rmSync(run.root, { recursive: true, force: true });
      }
    }
    report[scenario.name] = {};
    for (const side of sides) {
      const steps = samples[side][0].timings.map((t) => t.step);
      report[scenario.name][side] = {
        runs: n,
        medianMs: Object.fromEntries(steps.map((st, k) => [`${k}:${st}`, Math.round(median(samples[side].map((x) => x.timings[k].ms)) * 10) / 10])),
        medianPeakRssKB: median(samples[side].map((x) => x.peakKB ?? 0)),
      };
    }
    console.log(scenario.name, JSON.stringify(report[scenario.name]));
  }
  writeFileSync(join(OUT, 'bench.json'), JSON.stringify(report, null, 1) + '\n');
  process.exit(0);
}

const findings = JSON.parse(readFileSync(join(here, 'findings.json'), 'utf8')).findings;
const usedFindings = new Set();
if (new Set(findings.map((f) => f.id)).size !== findings.length) throw new Error('duplicate finding ids');
const unrecorded = [];
const summary = [];
const firedTotal = {};

for (const scenario of scenarios) {
  if (only && !only.has(scenario.name)) continue;
  const result = {};
  for (const side of sides) {
    const run = await runSide(scenario, side);
    const { entries: es, fired } = entries(run.log, run.dirs, run.root);
    for (const [k, v] of Object.entries(fired)) firedTotal[k] = (firedTotal[k] ?? 0) + v;
    result[side] = es;
    writeFileSync(join(OUT, `${scenario.name}.${side}.json`), JSON.stringify(es, null, 1) + '\n');
    rmSync(run.root, { recursive: true, force: true });
  }
  if (sides.length !== 2) {
    summary.push(`${scenario.name}: ${result[sides[0]].length} entries (${sides[0]} only)`);
    continue;
  }
  const g = gaps(result.oracle, result.port);
  const mine = findings.filter((f) => f.scenario === scenario.name || f.scenario === '*');
  const claim = (match) => {
    const f = mine.find(match);
    if (!f) return false;
    usedFindings.add(f.id);
    return true;
  };
  const stray = [];
  let nDiffs = 0;
  for (const gap of g.gaps) {
    const { diffs, only } = pairUp(gap);
    nDiffs += diffs.length + only.length;
    for (const d of diffs) if (!claim((f) => f.at === d.at && f.path === d.path && canon(f.oracle) === canon(d.oracle) && canon(f.port) === canon(d.port))) stray.push({ scenario: scenario.name, ...d });
    for (const o of only) if (!claim((f) => f.side === o.side && canon(f.entry) === canon(o.entry))) stray.push({ scenario: scenario.name, ...o });
  }
  unrecorded.push(...stray);
  summary.push(`${stray.length ? 'DIFF ' : 'ok   '} ${scenario.name}: ${g.same} identical entries, ${nDiffs} differences, ${stray.length} unrecorded`);
  console.log(summary.at(-1));
}

const stale = sides.length === 2 ? findings.filter((f) => (!only || f.scenario === '*' || only.has(f.scenario)) && !usedFindings.has(f.id)) : [];
writeFileSync(join(OUT, 'unrecorded.json'), JSON.stringify(unrecorded, null, 1) + '\n');
console.log(`\nnormalization rules fired: ${JSON.stringify(firedTotal)}`);
console.log(`${summary.length} scenarios; ${unrecorded.length} unrecorded difference(s); ${stale.length} recorded finding(s) no longer occur`);
if (stale.length) console.log(`stale findings: ${stale.map((f) => f.id).join(', ')}`);
process.exit(unrecorded.length || stale.length ? 1 : 0);
