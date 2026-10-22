// What each Piglet Binary is driven through in a real terminal. One entry per Piglet: the environment it needs, and the
// steps that exercise its main features, written the way a user would type them. `ok` is the one-turn answer of PiG's
// test-faux provider, which makes every Piglet complete a real turn with no network and no credentials.
//
// A step is `{ say, expect }` (type a line, wait for text), `{ key, expect }` (press keys, wait for text) or
// `{ run: async (t) => {} }` for anything that needs more. An entry may also carry `prepare(home)` (the environment the
// Binary needs, made before it starts), `run(t)` (after the steps) and `cleanup(t)`. The offline rule applies: no step
// may need the network, a credential or another program (mpv, yt-dlp, a browser).
import { chmodSync, mkdirSync, symlinkSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import { isolatedPath } from './tmux.mjs';

const fakeMpv = fileURLToPath(new URL('./fake-mpv.mjs', import.meta.url));

export const FAUX_TURN = 'reply with exactly: ok';

/** The lines of a pane that are the answer `ok`, and nothing else. */
export const answerLines = (screen) => screen.split('\n').filter((line) => line.trim() === 'ok');

/**
 * A stand-in for herdr's socket. herdr is told about a pane's agent by newline-delimited JSON requests over
 * HERDR_SOCKET_PATH (`pane.report_agent`, `pane.release_agent`), one request per connection, and answers each with one
 * JSON line. The server records every request in order. It is listening when this returns, before the Binary starts.
 */
export function fakeHerdr(path) {
  const requests = [];
  const open = new Set(); // net.Server.close waits for open connections: a run that failed must not keep the test process alive
  const server = createServer((socket) => {
    let pending = '';
    open.add(socket);
    socket.on('close', () => open.delete(socket));
    socket.setEncoding('utf8');
    socket.on('error', () => {});
    socket.on('data', (chunk) => {
      pending += chunk;
      for (let end = pending.indexOf('\n'); end >= 0; end = pending.indexOf('\n')) {
        const line = pending.slice(0, end);
        pending = pending.slice(end + 1);
        if (line === '') continue;
        try { requests.push(JSON.parse(line)); } catch { requests.push({ unparsed: line }); }
        socket.write('{"result":{}}\n');
      }
    });
  });
  server.listen(path);
  return { requests, close: () => { for (const socket of open) socket.destroy(); server.close(); } };
}

/** What a fake herdr was told, in order: the reported state, or `release-agent` for the release on quit. */
export const herdrStates = (requests) => requests.map((request) => (request.method === 'pane.release_agent' ? 'release-agent' : (request.params?.state ?? request.method ?? 'unparsed')));

const gameOverlay = (command, title, started, done) => async (t) => {
  await t.say(command);
  await t.waitFor(title);
  t.key('Space');
  await t.waitFor(started, 20_000);
  t.key('q');
  await t.waitFor(done);
  await t.waitGone(title);
};

/**
 * pig-music checks the machine before it opens: mpv and a current yt-dlp. These stand-ins answer `--version` (yt-dlp with
 * today's date; mpv answers its IPC socket and plays nothing) so the player opens on any machine without either program;
 * nothing is played or searched.
 */
const musicTools = (home) => {
  const bin = join(home, 'fakebin');
  mkdirSync(bin, { recursive: true });
  const date = new Date().toISOString().slice(0, 10).replaceAll('-', '.');
  writeFileSync(join(bin, 'yt-dlp'), `#!/bin/sh
case "$1" in --version) echo ${date};; *) echo "[debug] yt-dlp version ${date}" >&2; exit 2;; esac
`);
  writeFileSync(join(bin, 'mpv'), `#!${process.execPath}\nimport(${JSON.stringify(fakeMpv)});\n`);
  chmodSync(join(bin, 'yt-dlp'), 0o755);
  chmodSync(join(bin, 'mpv'), 0o755);
  // PULSE_SERVER satisfies pig-music's audio check on a machine with no sound stack (nothing is played: mpv is the stand-in above).
  return { PATH: `${bin}:${isolatedPath(home)}`, PULSE_SERVER: 'unix:/nonexistent' };
};

const music = async (t) => {
  await t.say('/music');
  await t.waitFor('Press / to search YouTube Music.');
  t.key('Tab');
  await t.waitFor('the library needs your YouTube account');
  t.key('q');
  await t.waitGone('space play');
};

// One fake herdr per run, keyed by the run's home: `prepare` starts it before the Binary does, `run` reads it, `cleanup` closes it.
const fakeHerdrs = new Map();

const herdr = {
  prepare: (home) => {
    const socket = join(home, 'herdr.sock');
    fakeHerdrs.set(home, fakeHerdr(socket));
    return { HERDR_ENV: '1', HERDR_PANE_ID: 'p_1', HERDR_SOCKET_PATH: socket };
  },
  cleanup: (t) => {
    fakeHerdrs.get(t.home)?.close();
    fakeHerdrs.delete(t.home);
  },
  run: async (t) => {
    const { requests } = fakeHerdrs.get(t.home);
    const states = () => herdrStates(requests);
    const until = async (what, ok) => {
      for (let i = 0; i < 600; i++) {
        if (ok(states())) return;
        await new Promise((resolve) => setTimeout(resolve, 50));
      }
      assert.fail(`herdr: ${what}; saw ${states().join(',')}\n${t.screen()}`);
    };
    const working = () => states().filter((state) => state === 'working').length;
    await until('the startup idle report', (s) => s.length >= 1);
    await until('the faux turn reported working, then idle', (s) => s.includes('working') && s.at(-1) === 'idle');
    const before = working();
    await t.say('TUI_LIVE_STREAM');
    await until('working then idle again for a second turn', (s) => working() > before && s.at(-1) === 'idle');
    t.type('/quit');
    t.key('Enter');
    await until('the release on quit', (s) => s.at(-1) === 'release-agent');
    for (const request of requests) {
      assert.equal(request.params?.source, 'pig', `every request names the pig source: ${JSON.stringify(request)}`);
      assert.equal(request.params?.agent, 'pig', `every request names the pig agent: ${JSON.stringify(request)}`);
      assert.equal(request.params?.pane_id, 'p_1', `every request names the pane: ${JSON.stringify(request)}`);
    }
    const seqs = requests.map((request) => request.params?.seq);
    assert.ok(seqs.every((n, i) => Number.isFinite(n) && (i === 0 || n > seqs[i - 1])), `seq must increase: ${seqs}`);
  },
  quits: true, // run() types /quit itself: the release on quit is what it checks
};

/** The two games of pig-games, played and quit. bench-standard carries the same two. */
const games = [
  { run: gameOverlay('/runner', 'PiG RUNNER', /PiG RUNNER {2}score 0*[1-9]/, /PiG Runner score \d/) },
  { run: gameOverlay('/angry-pigs', 'ANGRY PIGS', /level 1\/3/, /Angry Pigs score \d/) },
];

/**
 * The porting Skill (pig-extension-porter, and pig-porter, which extends it): found in the command list, then run. The
 * faux model only answers a prompt that carries `reply with exactly:`; a bare `/skill:` line fails the turn and the failure
 * echoes the whole Skill (over 500 lines) into a screen of 40 rows, so a marker inside the Skill, such as its `**L1**`
 * rule, is never on the screen. The end of the expansion always is: `</skill>` follows the complete Skill text, and the
 * faux answer to the instruction after it shows the turn completed.
 */
const porterSkill = [{
  run: async (t) => {
    t.type('/skill:pigpen-pi-ex');
    await t.waitFor(/→ skill:pigpen-pi-extension-port\s+Port a Pi TypeScript extension to a Go PiG extension/);
    await t.say(`tension-port ${FAUX_TURN}`);
    await t.waitFor('</skill>');
    await t.until('the faux answer `done`', (screen) => screen.split('\n').some((line) => line.trim() === 'done'));
    assert.doesNotMatch(t.screen(), /unhandled request/, 'the Skill prompt reached the faux model as an unhandled request');
  },
}];

/**
 * The Piglets that carry TypeScript extensions run them on Node, as any user of them has it; every other Binary here runs
 * with no Node at all. This links the Node that runs the test, and nothing else, onto the Binary's PATH.
 */
const nodeTool = (home) => {
  const bin = join(home, 'nodebin');
  mkdirSync(bin, { recursive: true });
  symlinkSync(process.execPath, join(bin, 'node'));
  return { PATH: `${bin}:${isolatedPath(home)}` };
};

/** /aot-runtime status: it reads the slot only, and the compiler is not downloaded until a TypeScript set asks for it. */
const aotRuntime = { say: '/aot-runtime', expect: /Pi-Bolt [\d.]+\)\s+platform: \S+/ };

/** /bun-runtime in a home with no Bun installed: nothing to download or remove. */
const bunRuntime = [
  { say: '/bun-runtime', expect: 'Bun runtime: not usable' },
  { say: '/bun-runtime remove', expect: 'Bun runtime: nothing to remove.' },
];

/** The ten bench-ext commands (bench-01 to bench-10), each once, listed by the editor's command completion. */
const benchCommands = async (t) => {
  t.type('/bench-');
  await t.waitFor('(1/10)');
  await t.waitFor(/bench-01\s.*Bench command 01/);
  t.key('Down', 'Down', 'Down', 'Down', 'Down', 'Down', 'Down', 'Down', 'Down');
  await t.waitFor('(10/10)');
  await t.waitFor(/bench-10\s.*Bench command 10/);
  t.key('Escape');
  await t.waitGone('(10/10)');
  t.key('C-u');
  await t.waitGone(/^\/bench-/m);
};

/**
 * bench-lean strips every built-in extension. The core's own slash commands stay (`/session`, `/compact`), and the
 * command of a stripped built-in (`/mcp`, which the stock Binaries list beside `/compact`) is gone from the list.
 */
const lean = async (t) => {
  await t.say('/session');
  await t.waitFor('Session Info');
  t.type('/mc');
  await t.waitFor('Manually compact the session context');
  assert.doesNotMatch(t.screen(), /Manage MCP servers/, 'the stripped mcp extension still has its command');
  t.key('Escape');
  await t.waitGone('Manually compact the session context');
  t.key('C-u');
  await t.waitGone(/^\/mc/m);
};

export const piglets = {
  a2a: { steps: [{ say: '/a2a', expect: 'a2a: listener off' }] },
  acp: { steps: [{ say: '/acp', expect: 'pig-acp --pig' }] },
  'bench-aot': { steps: [aotRuntime] },
  'bench-bun': { steps: bunRuntime },
  'bench-ext10': { steps: [{ run: benchCommands }] },
  'bench-ext10-aot': { prepare: nodeTool, steps: [aotRuntime, ...bunRuntime, { run: benchCommands }] },
  'bench-ext10-split': { prepare: nodeTool, steps: [aotRuntime, ...bunRuntime, { run: benchCommands }] },
  'bench-lean': { steps: [{ run: lean }] },
  'bench-standard': { steps: [...games, ...bunRuntime] },
  herdr,
  jev: {
    steps: [
      { say: '/jev', expect: 'Jev is off. Nothing is judged and nothing is sent.' },
      { say: '/ahp', expect: 'AHP listener is off' },
      // An argument other than refresh answers locally: no Ollama server is contacted.
      { say: '/ollama nope', expect: 'Usage: /ollama refresh' },
      { say: '/jev on', expect: 'Turn on Jev for this session?' },
      { key: ['Down', 'Enter'], expect: 'still off; nothing is sent' },
    ],
  },
  'pig-essentials': {
    steps: [
      { say: '/tasks', expect: 'View all tasks (0)' },
      { key: ['Escape'], gone: 'View all tasks (0)' },
      { say: '/agents', expect: 'Agent types:' },
      // The dev-skills are listed by the skill completion (typing only: a skill turn is not part of the faux provider's script).
      { run: async (t) => { t.type('/skill:pigpen-td'); await t.waitFor('Test-driven development'); t.key('C-u'); } },
      { run: async (t) => { await t.waitFor(/ponytail: .*FULL/); } },
    ],
  },
  'pig-extension-porter': { steps: porterSkill },
  'pig-games': { steps: games },
  'pig-music': { prepare: musicTools, steps: [{ run: music }] },
  'pig-popular': {
    steps: [
      { say: '/todos', expect: 'No todos yet.' },
      { say: '/goal-list', expect: 'No open goals.' },
      { run: async (t) => { await t.waitFor(/Test Faux \| think:off/); } },
    ],
  },
  'pig-porter': { steps: porterSkill },
  'pig-typesafe': { steps: [{ say: '/typesafe', expect: 'TypeSafe: disabled.' }] },
  'pig-warden': {
    steps: [
      { say: '/warden', expect: 'warden is off.' },
      { say: '/warden enable', expect: "Where should warden's judgments come from?" },
      // A bare Escape is also the start of an escape sequence: wait for its effect before typing anything else.
      { key: ['Escape'], expect: 'warden: nothing changed.' },
    ],
  },
  'pig-with-batteries': {
    prepare: musicTools,
    steps: [
      { say: '/cost', expect: 'Session Cost (Main Agent)' },
      { say: '/tools', expect: /✓ a2a_agents/ },
      { say: '/a2a', expect: 'a2a: listener off' },
      { say: '/jev', expect: 'Jev is off. Nothing is judged and nothing is sent.' },
      { run: gameOverlay('/runner', 'PiG RUNNER', /PiG RUNNER {2}score 0*[1-9]/, /PiG Runner score \d/) },
      { run: gameOverlay('/pig-snake', 'PIG SNAKE', /PIG SNAKE {2}score/, /Pig Snake score \d/) },
      { run: music },
    ],
  },
};
