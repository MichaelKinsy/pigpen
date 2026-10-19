// What each Piglet Binary is driven through in a real terminal. One entry per Piglet: the environment it needs, and the
// steps that exercise its main features, written the way a user would type them. `ok` is the one-turn answer of PiG's
// test-faux provider, which makes every Piglet complete a real turn with no network and no credentials.
//
// A step is `{ say, expect }` (type a line, wait for text), `{ key, expect }` (press keys, wait for text) or
// `{ run: async (t) => {} }` for anything that needs more. The offline rule applies: no step may need the network, a
// credential or another program (mpv, yt-dlp, a browser).
import { chmodSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import { isolatedPath } from './tmux.mjs';

const fakeMpv = fileURLToPath(new URL('./fake-mpv.mjs', import.meta.url));

export const FAUX_TURN = 'reply with exactly: ok';

/** The lines of a pane that are the answer `ok`, and nothing else. */
export const answerLines = (screen) => screen.split('\n').filter((line) => line.trim() === 'ok');

/** Every state a fake herdr was told about, in order, from its call log. */
export function herdrStates(log) {
  return log.split('\n').filter(Boolean).map((line) => (/ --state (\w+)/.exec(line)?.[1] ?? line.split(' ')[1]));
}

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
  return { PATH: `${bin}:${isolatedPath(home)}` };
};

const music = async (t) => {
  await t.say('/music');
  await t.waitFor('Press / to search YouTube Music.');
  t.key('Tab');
  await t.waitFor('the library needs your YouTube account');
  t.key('q');
  await t.waitGone('space play');
};

const herdr = {
  prepare: (home) => {
    const bin = join(home, 'herdr');
    const log = join(home, 'herdr-calls.log');
    writeFileSync(bin, `#!/bin/sh\necho "$@" >> "${log}"\n`);
    chmodSync(bin, 0o755);
    return { HERDR_ENV: '1', HERDR_PANE_ID: 'p_1', HERDR_BIN_PATH: bin, HERDR_SOCKET_PATH: join(home, 'herdr.sock') };
  },
  run: async (t) => {
    const log = join(t.home, 'herdr-calls.log');
    const calls = () => { try { return readFileSync(log, 'utf8'); } catch { return ''; } };
    const until = async (what, ok) => { for (let i = 0; i < 600; i++) { if (ok(herdrStates(calls()))) return; await new Promise((r) => setTimeout(r, 50)); } assert.fail(`herdr: ${what}; saw ${herdrStates(calls()).join(',')}\n${t.screen()}`); };
    await until('the startup idle report', (s) => s.length >= 1);
    await t.say('TUI_LIVE_STREAM');
    await until('working then idle again', (s) => s.join(',').startsWith('idle,working,idle'));
    t.type('/quit');
    t.key('Enter');
    await until('the release on quit', (s) => s.at(-1) === 'release-agent');
    const all = calls().split('\n').filter(Boolean);
    for (const line of all) assert.match(line, /--source custom:pig --agent pig/, 'every call names the custom:pig source');
    const seqs = all.map((line) => Number(/ --seq (\d+)/.exec(line)?.[1] ?? NaN)).filter(Number.isFinite);
    assert.ok(seqs.every((n, i) => i === 0 || n > seqs[i - 1]), `seq must increase: ${seqs}`);
  },
  quits: true, // run() types /quit itself: the release on quit is what it checks
};

export const piglets = {
  a2a: { steps: [{ say: '/a2a', expect: 'a2a: listener off' }] },
  acp: { steps: [{ say: '/acp', expect: 'pig-acp --pig' }] },
  herdr,
  jev: {
    steps: [
      { say: '/jev', expect: 'Jev is off. Nothing is judged and nothing is sent.' },
      { say: '/jev on', expect: 'Turn on Jev for this session?' },
      { key: ['Down', 'Enter'], expect: 'still off; nothing is sent' },
    ],
  },
  'pig-essentials': {
    steps: [
      { say: '/tasks', expect: 'View all tasks (0)' },
      { key: ['Escape'], gone: 'View all tasks (0)' },
      { say: '/agents', expect: 'Agent types:' },
      { run: async (t) => { await t.waitFor(/ponytail: .*FULL/); } },
    ],
  },
  'pig-extension-porter': { steps: [{ say: '/skill:pigpen-pi-extension-port', expect: /\*\*L1\*\*/ }] },
  'pig-games': {
    steps: [
      { run: gameOverlay('/runner', 'PiG RUNNER', /PiG RUNNER {2}score 0*[1-9]/, /PiG Runner score \d/) },
      { run: gameOverlay('/angry-pigs', 'ANGRY PIGS', /level 1\/3/, /Angry Pigs score \d/) },
    ],
  },
  'pig-music': { prepare: musicTools, steps: [{ run: music }] },
  'pig-popular': {
    steps: [
      { say: '/todos', expect: 'No todos yet.' },
      { say: '/goal-list', expect: 'No open goals.' },
      { run: async (t) => { await t.waitFor(/Test Faux \| think:off/); } },
    ],
  },
  'pig-porter': { steps: [{ say: '/skill:pigpen-pi-extension-port', expect: /\*\*L1\*\*/ }] },
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
