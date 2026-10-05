// Startup and idle measurements for Piglet Binaries and plain pig (docs/plan/progress/pigpen-perf.md).
//
//   node scripts/perf/measure.mjs startup  [--n 20] [--cpus 0-3] [--mode print|tmux|trace] name=path ...
//
// tmux mode reports two times per run: `ms`, until the editor's footer is drawn, and `acceptsInputMs`, until text
// typed the moment the footer appears is shown in the editor. pig draws its editor before it has attached its
// extensions (PiG publishes the model catalog to them first), so with any extension the two differ by about 100 ms.
//   node scripts/perf/measure.mjs idle     [--settle 30] [--window 60] [--cpus 0-3] [--out dir] name=path ...
//
// Every run uses a fresh temporary HOME, PIG_HOME and agent directory, PIG_TEST_FAUX=1 with
// --model test-faux/faux-1, and `taskset` so the Go runtime sees a small fixed CPU set (a 196-CPU
// host would otherwise start a GC worker per CPU and make RSS and thread counts meaningless).
// Binaries are measured round-robin so cache and frequency drift hit every column alike.
// Output: one JSON document on stdout.
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, readdirSync, readlinkSync, rmSync, writeFileSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const argv = process.argv.slice(2);
const command = argv.shift();
const opts = { n: 20, cpus: '0-3', mode: 'print', settle: 30, window: 60, out: '' };
const targets = [];
for (let i = 0; i < argv.length; i++) {
  const a = argv[i];
  if (a === '--n') opts.n = Number(argv[++i]);
  else if (a === '--cpus') opts.cpus = argv[++i];
  else if (a === '--mode') opts.mode = argv[++i];
  else if (a === '--settle') opts.settle = Number(argv[++i]);
  else if (a === '--window') opts.window = Number(argv[++i]);
  else if (a === '--out') opts.out = argv[++i];
  else if (a.includes('=')) targets.push({ name: a.slice(0, a.indexOf('=')), path: resolve(a.slice(a.indexOf('=') + 1)) });
  else throw new Error(`unknown argument ${a}`);
}
if (!['startup', 'idle'].includes(command) || targets.length === 0) {
  console.error('usage: measure.mjs startup|idle [options] name=path ...');
  process.exit(2);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const PROMPT = 'What is 20+22?';

function freshEnv() {
  const dir = mkdtempSync(join(tmpdir(), 'pigpen-perf-'));
  const env = {
    PATH: process.env.PATH, TERM: 'xterm-256color', LANG: 'C.UTF-8',
    HOME: dir, PIG_HOME: join(dir, 'pig'), PIG_CODING_AGENT_DIR: join(dir, 'agent'), PI_CODING_AGENT_DIR: join(dir, 'pi'),
    PIG_TEST_FAUX: '1', PIG_TEST_FAUX_SCENARIO: 'parity-basic', PIG_OFFLINE: '1', PI_SKIP_VERSION_CHECK: '1',
  };
  // Go's caches, only when set: an unset one must not reach the tmux pane as the string "undefined".
  for (const name of ['GOCACHE', 'GOMODCACHE']) if (process.env[name]) env[name] = process.env[name];
  mkdirSync(join(dir, 'work'));
  return { dir, env };
}

function stats(values) {
  const v = [...values].sort((a, b) => a - b);
  const q = (p) => v[Math.min(v.length - 1, Math.ceil(p * v.length) - 1)];
  const mean = v.reduce((s, x) => s + x, 0) / v.length;
  return { n: v.length, min: v[0], median: q(0.5), p90: q(0.9), max: v[v.length - 1], mean: Number(mean.toFixed(2)) };
}
const r1 = (x) => Number(x.toFixed(1));

async function printRun(path) {
  const { dir, env } = freshEnv();
  const started = process.hrtime.bigint();
  const child = spawn('taskset', ['-c', opts.cpus, path, '--model', 'test-faux/faux-1', '-p', PROMPT], { cwd: join(dir, 'work'), env, stdio: ['ignore', 'pipe', 'pipe'] });
  let out = '';
  let err = '';
  child.stdout.on('data', (d) => { out += d; });
  child.stderr.on('data', (d) => { err += d; });
  const code = await new Promise((r) => child.on('close', r));
  const ms = Number(process.hrtime.bigint() - started) / 1e6;
  rmSync(dir, { recursive: true, force: true });
  if (code !== 0 || !out.includes('42')) throw new Error(`${path}: exit ${code}, stdout ${JSON.stringify(out)}, stderr ${JSON.stringify(err)}`);
  return ms;
}

// PIG_STARTUP_TRACE lines: "[startup] <mark> <n>ms". Returns the marks and, per extension, handshake-done minus spawn-start.
async function traceRun(path) {
  const { dir, env } = freshEnv();
  env.PIG_STARTUP_TRACE = '1';
  const child = spawn('taskset', ['-c', opts.cpus, path, '--model', 'test-faux/faux-1', '-p', PROMPT], { cwd: join(dir, 'work'), env, stdio: ['ignore', 'pipe', 'pipe'] });
  let err = '';
  child.stderr.on('data', (d) => { err += d; });
  child.stdout.resume();
  await new Promise((r) => child.on('close', r));
  rmSync(dir, { recursive: true, force: true });
  const marks = {};
  for (const m of err.matchAll(/\[startup\] (\S+)\s+(\d+)ms/g)) marks[m[1]] = Number(m[2]);
  return marks;
}

// pig still writes its state into its HOME after its terminal goes away, so a run's temporary home is removed only
// once the process has exited (otherwise the removal fails, or pig recreates the directory and it is left behind).
async function waitExit(pid, timeoutMs = 10000) {
  const until = Date.now() + timeoutMs;
  while (pid > 0 && existsSync(`/proc/${pid}`)) {
    if (Date.now() > until) throw new Error(`pid ${pid} did not exit within ${timeoutMs} ms`);
    await sleep(5);
  }
}

class Tmux {
  constructor() { this.sock = `perf${process.pid}`; }
  run(...a) { return spawnSync('tmux', ['-L', this.sock, ...a], { encoding: 'utf8' }); }
  screen(target) { return this.run('capture-pane', '-p', '-t', target).stdout ?? ''; }
  stop() { this.run('kill-server'); }
}

const PROBE = 'zqxjv'; // typed at the drawn prompt; the prompt accepts input once it shows

async function launchInteractive(tmux, session, path, env, dir, extraEnv = {}, { probe = false } = {}) {
  const exports = Object.entries({ ...env, ...extraEnv }).map(([k, v]) => `${k}='${v}'`).join(' ');
  const err = join(dir, 'stderr.txt');
  const started = process.hrtime.bigint();
  const r = tmux.run('new-session', '-d', '-s', session, '-x', '100', '-y', '30', '-c', join(dir, 'work'),
    `exec env -i ${exports} taskset -c ${opts.cpus} '${path}' --model test-faux/faux-1 2>'${err}'`);
  if (r.status !== 0) throw new Error(`tmux: ${r.stderr}`);
  for (;;) {
    if (/faux-1\s*$/m.test(tmux.screen(session))) break; // the footer, drawn with the editor
    if (Number(process.hrtime.bigint() - started) / 1e6 > 30000) throw new Error(`${path}: no prompt in 30 s:\n${tmux.screen(session)}`);
    await sleep(2);
  }
  const ms = Number(process.hrtime.bigint() - started) / 1e6;
  let acceptsInputMs;
  if (probe) {
    tmux.run('send-keys', '-t', session, '-l', PROBE);
    while (!tmux.screen(session).includes(PROBE)) {
      if (Number(process.hrtime.bigint() - started) / 1e6 > 30000) throw new Error(`${path}: typed text not shown in 30 s:\n${tmux.screen(session)}`);
      await sleep(2);
    }
    acceptsInputMs = Number(process.hrtime.bigint() - started) / 1e6;
  }
  const pid = Number(tmux.run('display', '-p', '-t', session, '#{pane_pid}').stdout.trim());
  return { ms, acceptsInputMs, pid, err };
}

function procStat(pid) {
  const status = Object.fromEntries(readFileSync(`/proc/${pid}/status`, 'utf8').split('\n').filter(Boolean).map((l) => { const i = l.indexOf(':'); return [l.slice(0, i), l.slice(i + 1).trim()]; }));
  const stat = readFileSync(`/proc/${pid}/stat`, 'utf8');
  const f = stat.slice(stat.lastIndexOf(')') + 2).split(' ');
  return {
    rssKiB: parseInt(status.VmRSS), anonKiB: parseInt(status.RssAnon), threads: Number(status.Threads),
    ticks: Number(f[11]) + Number(f[12]), // utime + stime (clock ticks)
    ...switches(pid),
  };
}

// Context switches summed over every thread: /proc/<pid>/status alone counts only the main thread.
function switches(pid) {
  let voluntary = 0;
  let involuntary = 0;
  for (const tid of readdirSync(`/proc/${pid}/task`)) {
    try {
      const text = readFileSync(`/proc/${pid}/task/${tid}/status`, 'utf8');
      voluntary += Number(/^voluntary_ctxt_switches:\s*(\d+)/m.exec(text)?.[1] ?? 0);
      involuntary += Number(/^nonvoluntary_ctxt_switches:\s*(\d+)/m.exec(text)?.[1] ?? 0);
    } catch { /* thread exited */ }
  }
  return { voluntary, involuntary };
}

function children(pid) {
  const found = [];
  for (const d of readdirSync('/proc')) {
    if (!/^\d+$/.test(d)) continue;
    try {
      const stat = readFileSync(`/proc/${d}/stat`, 'utf8');
      const f = stat.slice(stat.lastIndexOf(')') + 2).split(' ');
      if (Number(f[1]) === pid) found.push(`${d}:${readFileSync(`/proc/${d}/comm`, 'utf8').trim()}`);
    } catch { /* exited */ }
  }
  return found;
}

function fdKinds(pid) {
  const kinds = {};
  for (const fd of readdirSync(`/proc/${pid}/fd`)) {
    let target;
    try { target = readlinkSync(`/proc/${pid}/fd/${fd}`); } catch { continue; }
    const kind = target.startsWith('socket:') ? 'socket' : target.startsWith('pipe:') ? 'pipe' : target.startsWith('anon_inode:') ? target : target.startsWith('/dev/pts') || target === '/dev/null' || target === '/dev/tty' ? 'tty' : 'file';
    kinds[kind] = (kinds[kind] ?? 0) + 1;
    if (kind === 'file') (kinds.files ??= []).push(target);
  }
  return kinds;
}

// Goroutines from a SIGQUIT dump: total, runtime-internal, and the user ones with their top frames.
const RUNTIME_STATES = /\[(idle|force gc \(idle\)|GC sweep wait|GC scavenge wait|GOMAXPROCS updater \(idle\)|finalizer wait|GC worker \(idle\)|runnable|running|syscall, locked to thread)\b/;
function parseDump(text) {
  const blocks = text.split(/\n\n/).filter((b) => /^goroutine \d+ /.test(b));
  const user = [];
  let internal = 0;
  for (const b of blocks) {
    const head = b.split('\n')[0];
    if (RUNTIME_STATES.test(head) && !/pig|Pigpen|MichaelKinsy/.test(b)) { internal++; continue; }
    const lines = b.split('\n');
    user.push({ state: /\[([^\]]+)\]/.exec(head)?.[1], top: lines.slice(1, 5).filter((l) => !l.startsWith('\t')).map((l) => l.replace(/\(0x.*$/, '').trim()) });
  }
  const timers = blocks.filter((b) => /time\.\(\*Ticker\)|time\.Sleep|time\.After|time\.NewTimer|time\.\(\*Timer\)|\[sleep\]/.test(b)).length;
  return { total: blocks.length, runtime: internal, user: user.length, timerGoroutines: timers, userStacks: user };
}

async function startup() {
  const results = Object.fromEntries(targets.map((t) => [t.name, []]));
  const traces = Object.fromEntries(targets.map((t) => [t.name, []]));
  const accepts = Object.fromEntries(targets.map((t) => [t.name, []]));
  const tmux = new Tmux();
  try {
    for (let i = 0; i < opts.n + 1; i++) { // the first round is a warm-up and is dropped
      for (const t of targets) {
        let v;
        if (opts.mode === 'print') v = await printRun(t.path);
        else if (opts.mode === 'trace') v = await traceRun(t.path);
        else {
          const { dir, env } = freshEnv();
          const run = await launchInteractive(tmux, `s${i}${t.name}`.replace(/[^\w]/g, ''), t.path, env, dir, {}, { probe: true });
          tmux.run('kill-session', '-t', `s${i}${t.name}`.replace(/[^\w]/g, ''));
          await waitExit(run.pid);
          rmSync(dir, { recursive: true, force: true });
          v = run.ms;
          if (i > 0) accepts[t.name].push(run.acceptsInputMs);
        }
        if (i > 0) (opts.mode === 'trace' ? traces : results)[t.name].push(v);
      }
    }
  } finally { tmux.stop(); }
  const summary = (series) => Object.fromEntries(Object.entries(series).map(([k, v]) => [k, stats(v)]));
  if (opts.mode === 'print') return { mode: opts.mode, cpus: opts.cpus, ms: summary(results) };
  if (opts.mode === 'tmux') return { mode: opts.mode, cpus: opts.cpus, ms: summary(results), acceptsInputMs: summary(accepts) };
  // Trace mode: median of every mark, and per extension the median handshake span.
  const out = {};
  for (const [name, runs] of Object.entries(traces)) {
    const marks = {};
    for (const key of Object.keys(runs[0])) marks[key] = stats(runs.map((m) => m[key] ?? NaN).filter((x) => !Number.isNaN(x))).median;
    const exts = {};
    for (const key of Object.keys(marks)) {
      const m = /^extension\.(.+)\.spawn-start$/.exec(key);
      if (m) exts[m[1]] = { spawnToHandshakeDoneMs: stats(runs.map((r) => r[`extension.${m[1]}.handshake-done`] - r[key])).median, readyAtMs: marks[`extension.${m[1]}.handshake-done`] };
    }
    out[name] = { marks, extensions: exts };
  }
  return { mode: 'trace', cpus: opts.cpus, traces: out };
}

async function idle() {
  const tmux = new Tmux();
  const dirs = [];
  const live = [];
  try {
    // Launch all, in parallel on separate cpu sets would skew; they share the cpu set but idle.
    for (const t of targets) {
      const { dir, env } = freshEnv();
      dirs.push(dir);
      const session = t.name.replace(/[^\w]/g, '');
      const run = await launchInteractive(tmux, session, t.path, env, dir);
      live.push({ t, session, dir, ...run });
    }
    await sleep(opts.settle * 1000);
    const first = live.map((l) => ({ ...procStat(l.pid), at: Date.now() }));
    const fds = live.map((l) => fdKinds(l.pid));
    const kids = live.map((l) => children(l.pid));
    await sleep(opts.window * 1000);
    const second = live.map((l) => ({ ...procStat(l.pid), at: Date.now() }));
    const hz = Number(spawnSync('getconf', ['CLK_TCK'], { encoding: 'utf8' }).stdout) || 100;
    const result = {};
    for (let i = 0; i < live.length; i++) {
      const l = live[i];
      const secs = (second[i].at - first[i].at) / 1000;
      process.kill(l.pid, 'SIGQUIT');
      await sleep(1500);
      const dump = readFileSync(l.err, 'utf8');
      if (opts.out) { mkdirSync(opts.out, { recursive: true }); writeFileSync(join(opts.out, `${l.t.name}.goroutines.txt`), dump); }
      const g = parseDump(dump);
      result[l.t.name] = {
        startMs: r1(l.ms), rssMiB: r1(first[i].rssKiB / 1024), anonMiB: r1(first[i].anonKiB / 1024), rssAfterWindowMiB: r1(second[i].rssKiB / 1024),
        threads: first[i].threads, goroutines: g.total, goroutinesRuntime: g.runtime, goroutinesUser: g.user, timerGoroutines: g.timerGoroutines,
        cpuMsPerSecIdle: r1(((second[i].ticks - first[i].ticks) * 1000 / hz) / secs),
        cpuTicksInWindow: second[i].ticks - first[i].ticks,
        wakeupsPerSec: r1((second[i].voluntary + second[i].involuntary - first[i].voluntary - first[i].involuntary) / secs),
        fds: fds[i], children: kids[i],
      };
    }
    return { cpus: opts.cpus, settleS: opts.settle, windowS: opts.window, idle: result };
  } finally {
    tmux.stop();
    for (const l of live) await waitExit(l.pid);
    for (const d of dirs) rmSync(d, { recursive: true, force: true });
  }
}

const result = command === 'startup' ? await startup() : await idle();
console.log(JSON.stringify(result, null, 2));
