// pig-music inside the pig-with-batteries Piglet Binary, in a real terminal (detached tmux), with PiG's hermetic test-faux
// provider and a throwaway HOME, PIG_HOME, agent directory and runtime directory (nothing under ~/.pig is read or written).
//
//   PIG_BIN=/path/to/pig0.4.0 npm run test:pig-music-batteries
//   PIG_BATTERIES_BINARY=/path/to/built-binary PIG_BATTERIES_BASELINE=/path/to/binary-without-music npm run test:pig-music-batteries
//
// Without PIG_BATTERIES_BINARY it builds the Piglet Binary (CGO_ENABLED=0). With the throwaway PIG_HOME that build needs
// PIG_SOURCE_ROOT, a git checkout of the PiG source at the pig's version (v0.4.0): without it the build fails with "go: updates to
// go.mod needed" (the module download of PiG has no nested extensions/sdk). PIG_BATTERIES_BASELINE (a Binary built before
// pig-music was added) turns on the startup comparison. Parts:
//   1. it does nothing until /music: spy mpv and yt-dlp that record every call, the process tree, sockets, files, the screen;
//   2. startup latency and memory against the baseline (printed; a loose bound is asserted);
//   3. it works: /music opens, a search plays (real mpv --ao=null, yt-dlp, network: PIG_MUSIC_SMOKE=1), q hides, /reload keeps
//      the music, /quit stops mpv.
// Needs tmux and `ss`; part 3 also needs mpv and yt-dlp. Not run in CI.
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { after, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { goCaches } from '../../../scripts/go-modules.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const tmux = (...args) => execFileSync('tmux', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });

function buildBinary() {
  const scratch = mkdtempSync(join(tmpdir(), 'pmb-bin-'));
  const out = join(scratch, 'pig-with-batteries');
  // pig publishes every Binary it builds (artifacts and receipts) under PIG_HOME, ~/.pig by default: a throwaway one here.
  const built = spawnSync('npm', ['run', 'build:piglet', '--', 'pig-with-batteries', '--out', out], {
    cwd: root, encoding: 'utf8', env: { ...process.env, CGO_ENABLED: '0', PIG_HOME: join(scratch, 'pighome') },
  });
  assert.equal(built.status, 0, `the Piglet build failed:\n${built.stdout}\n${built.stderr}`);
  return out;
}
const binary = process.env.PIG_BATTERIES_BINARY ?? buildBinary();
const baseline = process.env.PIG_BATTERIES_BASELINE;

/** One pig-with-batteries in its own tmux pane and throwaway directories. */
class Pane {
  constructor(bin, extraEnv = {}, size = [110, 32], wrap = '') {
    this.session = `pm-batt-${process.pid}-${Math.floor(Math.random() * 1e6)}`;
    this.dir = mkdtempSync(join(tmpdir(), 'pmb-'));
    this.run = join(this.dir, 'r');
    for (const sub of ['agent', 'home', 'work', 'pighome', 'r', 'spy']) mkdirSync(join(this.dir, sub), { mode: 0o700 });
    const env = {
      HOME: join(this.dir, 'home'), PIG_HOME: join(this.dir, 'pighome'), PIG_CODING_AGENT_DIR: join(this.dir, 'agent'),
      PI_CODING_AGENT_DIR: join(this.dir, 'agent'), PIG_TEST_FAUX: '1', PIG_TEST_FAUX_SCENARIO: 'parity-basic',
      TERM: 'xterm-256color', COLORTERM: 'truecolor', XDG_RUNTIME_DIR: this.run,
      // PiG's own version check would open a connection; the test is about what pig-music does on its own.
      PIG_OFFLINE: '1', PI_SKIP_VERSION_CHECK: '1',
      PATH: `${dirname(process.execPath)}:${process.env.PATH}`, ...goCaches(), ...extraEnv,
    };
    const exports = Object.entries(env).map(([n, v]) => `${n}='${v}'`).join(' ');
    this.started = Date.now();
    tmux('new-session', '-d', '-s', this.session, '-x', String(size[0]), '-y', String(size[1]), '-c', join(this.dir, 'work'),
      `env ${exports} ${wrap} '${bin}' --model test-faux/faux-1`);
  }
  screen() { return tmux('capture-pane', '-p', '-t', this.session); }
  type(text) { tmux('send-keys', '-t', this.session, text, 'Enter'); }
  key(name) { tmux('send-keys', '-t', this.session, name); }
  literal(text) { tmux('send-keys', '-t', this.session, '-l', text); }
  pid() { return Number(tmux('display-message', '-p', '-t', this.session, '#{pane_pid}').trim()); }
  async until(what, ready, ms = 30_000) {
    const deadline = Date.now() + ms;
    while (!ready()) {
      assert.ok(Date.now() < deadline, `timed out waiting for ${what}\n--- screen ---\n${this.screen()}`);
      await sleep(20);
    }
  }
  has(text) { return () => this.screen().includes(text); }
  async ready(ms = 120_000) { await this.until('the editor', this.has('faux-1'), ms); return Date.now() - this.started; }
  mpvPids() {
    return spawnSync('pgrep', ['-f', '--', `--input-ipc-server=${this.run}/pig-music/mpv.sock`], { encoding: 'utf8' }).stdout.split('\n').filter(Boolean);
  }
  stop() {
    try { tmux('kill-session', '-t', this.session); } catch { /* gone */ }
    for (const pid of this.mpvPids()) { try { process.kill(Number(pid)); } catch { /* gone */ } }
    rmSync(this.dir, { recursive: true, force: true });
  }
}

const descendants = (pid) => {
  const out = spawnSync('ps', ['-eo', 'pid=,ppid=,args='], { encoding: 'utf8' }).stdout.split('\n').filter(Boolean)
    .map((l) => { const m = /^\s*(\d+)\s+(\d+)\s+(.*)$/.exec(l); return m && { pid: Number(m[1]), ppid: Number(m[2]), args: m[3] }; }).filter(Boolean);
  const found = [];
  const walk = (p) => { for (const c of out.filter((x) => x.ppid === p)) { found.push(c); walk(c.pid); } };
  walk(pid);
  return found;
};
const sockets = (pid) => spawnSync('ss', ['-H', '-tunaxp'], { encoding: 'utf8' }).stdout.split('\n').filter((l) => l.includes(`pid=${pid},`));
const cpuSeconds = (pid) => { const f = readFileSync(`/proc/${pid}/stat`, 'utf8').split(') ')[1].split(' '); return (Number(f[11]) + Number(f[12])) / 100; };
const rssMiB = (pid) => Number(/VmRSS:\s+(\d+) kB/.exec(readFileSync(`/proc/${pid}/status`, 'utf8'))[1]) / 1024;
const files = (dir) => readdirSync(dir, { recursive: true, withFileTypes: true }).filter((e) => e.isFile()).map((e) => join(e.parentPath ?? e.path, e.name));
const hasStrace = spawnSync('strace', ['-V']).status === 0;
const median = (xs) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)];
/** The program interpreter an ELF executable asks for (its PT_INTERP), or '' for a static one. 64-bit little-endian only. */
const elfInterpreter = (path) => {
  const b = readFileSync(path);
  assert.equal(b.toString('latin1', 0, 4), '\x7fELF', `${path} is not an ELF file`);
  const phoff = Number(b.readBigUInt64LE(0x20)), size = b.readUInt16LE(0x36), count = b.readUInt16LE(0x38);
  for (let i = 0; i < count; i++) {
    const h = phoff + i * size;
    if (b.readUInt32LE(h) !== 3) continue; // PT_INTERP
    const off = Number(b.readBigUInt64LE(h + 8)), len = Number(b.readBigUInt64LE(h + 32));
    return b.toString('latin1', off, off + len).replace(/\0+$/, '');
  }
  return '';
};

describe('pig-music in the batteries Binary does nothing until /music', () => {
  let pane;

  it('keeps the Binary a static executable (it needs no system C library to start)', { skip: process.platform === 'linux' ? false : 'ELF check on Linux only' }, () => {
    // The extension never plays audio itself; an audio library linked into it (oto through purego) made the whole Binary
    // ask for glibc's loader, so it would not start on musl, in a static container or under Termux.
    assert.equal(elfInterpreter(binary), '', 'the Binary is dynamically linked');
    if (baseline) assert.equal(elfInterpreter(baseline), '', 'the baseline Binary is dynamically linked');
  });
  const spyLog = () => join(pane.dir, 'spy.log');
  after(() => pane?.stop());

  it('spawns nothing, opens no socket, writes nothing and draws nothing by itself', async () => {
    const spies = join(mkdtempSync(join(tmpdir(), 'pmb-spies-')), 'bin');
    mkdirSync(spies);
    for (const name of ['mpv', 'yt-dlp', 'deno', 'node']) {
      const path = join(spies, name);
      // Every call is recorded with the tool's name; the exit code is 1, so a call can never "work".
      writeFileSync(path, `#!/bin/sh\necho "${name} $*" >> "$PIG_SPY_LOG"\nexit 1\n`);
      chmodSync(path, 0o755);
    }
    const log = join(tmpdir(), `pmb-spy-${process.pid}.log`);
    rmSync(log, { force: true });
    pane = new Pane(binary, { PIG_SPY_LOG: log, PIG_MUSIC_MPV: join(spies, 'mpv'), PIG_MUSIC_YTDLP: join(spies, 'yt-dlp'), PATH: `${spies}:${process.env.PATH}:${dirname(process.execPath)}` });
    await pane.ready();
    const pid = pane.pid();
    await sleep(3000); // PiG's own start-up work settles
    const cpu0 = cpuSeconds(pid);
    await sleep(4000); // time for a ticker or a late spawn to show
    const cpu = (cpuSeconds(pid) - cpu0) / 4;

    assert.ok(!existsSync(log), `something called mpv, yt-dlp, deno or node:\n${existsSync(log) ? readFileSync(log, 'utf8') : ''}`);
    assert.deepEqual(descendants(pid).map((d) => d.args), [], 'pig has child processes');
    assert.deepEqual(sockets(pid), [], 'pig has open network or unix sockets');
    assert.deepEqual(pane.mpvPids(), [], 'an mpv is running');
    const written = [...files(join(pane.dir, 'agent')), ...files(join(pane.dir, 'pighome')), ...files(pane.run)].filter((f) => /music|mpv|yt-dlp|cookie|\.sock$/i.test(f));
    assert.deepEqual(written, [], 'pig-music wrote files before /music');
    const screen = pane.screen();
    assert.ok(!/music|♪|mpv|yt-dlp|Playing/i.test(screen), `the screen shows music before /music:\n${screen}`);
    console.log(`idle batteries Binary: ${(cpu * 100).toFixed(2)}% of a core, ${rssMiB(pid).toFixed(0)} MiB resident, ${descendants(pid).length} child processes`);
    assert.ok(cpu < 0.05, `the idle Binary uses ${(cpu * 100).toFixed(1)}% of a core`);
  });

  it('makes no system call that starts a program or reaches the network (strace)', { skip: hasStrace ? false : 'strace is not installed' }, async () => {
    // A snapshot of the process tree and sockets misses something short-lived; strace sees every call from the start.
    const trace = join(mkdtempSync(join(tmpdir(), 'pmb-strace-')), 'trace');
    const traced = new Pane(binary, {}, [110, 32], `strace -f -qq -e trace=execve,socket,connect -e signal=none -o '${trace}'`);
    try {
      await traced.ready();
      await sleep(5000);
      const lines = readFileSync(trace, 'utf8').split('\n').filter(Boolean).map((l) => { const m = /^(\d+)\s+(.*)$/.exec(l); return { pid: m?.[1], call: m?.[2] ?? l }; });
      // PiG itself asks tmux about the terminal's features (the baseline Binary does the same): those tmux processes and
      // their calls are PiG's. Nothing else may start.
      const tmuxPids = new Set(lines.filter((l) => /^execve\("[^"]*\/tmux", \["tmux", "(display-message|show)"/.test(l.call)).map((l) => l.pid));
      const calls = lines.filter((l) => !tmuxPids.has(l.pid)).map((l) => l.call);
      const started = calls.filter((c) => c.startsWith('execve(') && !c.startsWith(`execve("${binary}"`));
      assert.deepEqual(started, [], 'programs started');
      assert.deepEqual(calls.filter((c) => /socket\(AF_INET6?\b/.test(c)), [], 'a network socket was opened');
      // The one connection allowed: a look for pig-music's own mpv socket (absent here), which keeps the footer right after a
      // /reload when music is already playing.
      const connects = calls.filter((c) => c.startsWith('connect('));
      assert.deepEqual(connects.filter((c) => !c.includes(`sun_path="${traced.run}/pig-music/mpv.sock"`)), [], `connections:\n${connects.join('\n')}`);
    } finally { traced.stop(); }
  });

  it('a /music subcommand that needs nothing running does not start anything either', async () => {
    // /music stop with nothing playing and /music now say so without spawning mpv or yt-dlp.
    pane.type('/music stop');
    await pane.until('the answer', () => /nothing is playing/i.test(pane.screen()), 20_000);
    pane.type('/music now');
    await sleep(1500);
    assert.ok(!existsSync(join(tmpdir(), `pmb-spy-${process.pid}.log`)), 'mpv or yt-dlp was called');
    assert.deepEqual(pane.mpvPids(), []);
  });
});

describe('startup latency and memory against the Binary without pig-music', { skip: baseline ? false : 'set PIG_BATTERIES_BASELINE to compare' }, () => {
  it('starts within a small margin of the baseline', async () => {
    const runs = Number(process.env.PIG_BATTERIES_RUNS ?? 5);
    const times = { baseline: [], withMusic: [] };
    const rss = { baseline: [], withMusic: [] };
    const idle = { baseline: [], withMusic: [] };
    for (let i = 0; i < runs; i++) {
      for (const [label, bin] of [['baseline', baseline], ['withMusic', binary]]) {
        const pane = new Pane(bin);
        try {
          times[label].push(await pane.ready());
          await sleep(3000);
          const c0 = cpuSeconds(pane.pid());
          await sleep(3000);
          idle[label].push((cpuSeconds(pane.pid()) - c0) / 3);
          rss[label].push(rssMiB(pane.pid()));
        } finally { pane.stop(); }
      }
    }
    const size = (p) => statSync(p).size;
    const row = (label) => `${label}: startup median ${median(times[label])} ms (min ${Math.min(...times[label])}, max ${Math.max(...times[label])}), resident ${median(rss[label]).toFixed(1)} MiB, idle ${(median(idle[label]) * 100).toFixed(2)}% of a core`;
    console.log(`${row('baseline')}\n${row('withMusic')}\ndelta: ${median(times.withMusic) - median(times.baseline)} ms, ${(median(rss.withMusic) - median(rss.baseline)).toFixed(1)} MiB\nBinary size: ${size(baseline)} -> ${size(binary)} bytes (+${size(binary) - size(baseline)}, ${(((size(binary) - size(baseline)) / size(baseline)) * 100).toFixed(1)}%)`);
    assert.ok(median(times.withMusic) - median(times.baseline) < 500, 'pig-music slows the start by more than half a second');
    assert.ok(median(idle.withMusic) - median(idle.baseline) < 0.02, 'pig-music makes the idle Binary busier');
  });
});

describe('pig-music works inside the batteries Binary (real mpv --ao=null, yt-dlp, network)', { skip: process.env.PIG_MUSIC_SMOKE === '1' ? false : 'set PIG_MUSIC_SMOKE=1' }, () => {
  let pane;
  after(() => pane?.stop());

  it('/music opens, a search plays, q hides it and the music goes on', async () => {
    pane = new Pane(binary, { PIG_MUSIC_MPV_ARGS: '--ao=null' });
    await pane.ready();
    pane.type('/music');
    await pane.until('the player', pane.has('Press / to search'));
    pane.key('/');
    pane.literal('never gonna give you up');
    pane.key('Enter');
    await pane.until('results', pane.has('Never Gonna Give You Up'), 60_000);
    pane.key('Enter');
    await pane.until('playing', () => /Playing/.test(pane.screen()) && /^ [>|] 0:0[2-9]/m.test(pane.screen()), 60_000);
    assert.equal(pane.mpvPids().length, 1);
    pane.key('q');
    await pane.until('the editor again', () => !pane.screen().includes('Up Next'));
    assert.equal(pane.mpvPids().length, 1, 'hiding the player stopped the music');
  });

  it('/reload keeps the same mpv playing', async () => {
    const pids = pane.mpvPids();
    await sleep(1000);
    pane.type('/reload');
    await pane.until('the editor after the reload', pane.has('faux-1'), 120_000);
    await sleep(1500);
    assert.deepEqual(pane.mpvPids(), pids, 'mpv changed or stopped across the reload');
  });

  it('/quit stops mpv', async () => {
    await sleep(1000);
    pane.type('/quit');
    const deadline = Date.now() + 20_000;
    while (pane.mpvPids().length > 0 && Date.now() < deadline) await sleep(200);
    assert.deepEqual(pane.mpvPids(), [], 'mpv still running after /quit');
  });
});
