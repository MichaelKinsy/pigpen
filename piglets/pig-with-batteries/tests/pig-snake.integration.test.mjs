// Runs the real `pig` (or a real Piglet Binary) in a tmux pane and plays Pig Snake through
// the real TUI overlay, with PiG's hermetic test-faux provider and a temporary HOME, PIG_HOME and
// PIG_CODING_AGENT_DIR (nothing under ~/.pig is read or written).
//
//   PIG_BIN=/path/to/pig PIG_SOURCE_ROOT=/path/to/PiG npm run test:pig-snake   builds the staged pig-with-batteries Piglet into a Binary, then plays it
//   PIG_SNAKE_BINARY=/path/to/piglet-binary npm run test:pig-snake             a built Piglet Binary instead (PIG_BIN and PIG_SOURCE_ROOT not needed)
//
// pig-with-batteries strips the Go extension SDK (strip.features: extension-sdk-go), so `pig --piglet <staged manifest>`
// cannot build the Go Packages it selects at run time: only a Binary with them fused in runs it. The test therefore always
// plays a Binary, built from the staged manifest unless PIG_SNAKE_BINARY names one.
//
// Needs tmux (every extension of the composition is Go, so no Node.js; the herdr reporter stays
// idle outside a herdr pane). It stages first; it is not run in CI.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { after, before, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { goCaches } from '../../../scripts/go-modules.mjs';
import { hostTarget } from '../../../scripts/host-target.mjs';
import { requirePig } from '../../../scripts/pig-bin.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const manifest = join(root, 'dist/staged/piglets/pig-with-batteries/piglet.yaml');
let binary = process.env.PIG_SNAKE_BINARY;
let builtDir;
if (!binary) {
  // A Piglet Binary carries its own pig; the build runs PIG_BIN, which must be the pig Pigpen targets, on PiG's source.
  const pig = requirePig();
  if (!process.env.PIG_SOURCE_ROOT) throw new Error('PIG_SOURCE_ROOT is not set: a Piglet Binary builds from a git checkout of the PiG source (or set PIG_SNAKE_BINARY to a built Binary)');
  execFileSync(process.execPath, [join(root, 'scripts/stage-piglets.mjs')], { stdio: 'pipe' });
  builtDir = mkdtempSync(join(tmpdir(), 'pig-snake-binary-'));
  binary = join(builtDir, 'pig-with-batteries');
  // The manifest lists every release platform; pig builds only the machine it runs on.
  execFileSync(pig, ['piglet', 'build', manifest, '--format', 'binary', '--out', binary, '--targets', hostTarget().target], {
    stdio: 'pipe', env: { ...process.env, ...goCaches(), GIT_TERMINAL_PROMPT: '0' },
  });
  process.on('exit', () => rmSync(builtDir, { recursive: true, force: true }));
}

let session = '';
let dir = '';
const tmux = (...args) => execFileSync('tmux', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
const type = (text) => tmux('send-keys', '-t', session, text, 'Enter');
const key = (name) => tmux('send-keys', '-t', session, name);
const screen = () => tmux('capture-pane', '-p', '-t', session);
const resize = (x, y) => tmux('resize-window', '-t', session, '-x', String(x), '-y', String(y));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const statePath = () => join(dir, 'pighome/state/pigpen/pig-snake.json');

async function until(what, ready, ms = 20_000) {
  const deadline = Date.now() + ms;
  while (!ready()) {
    assert.ok(Date.now() < deadline, `timed out waiting for ${what}\n--- screen ---\n${screen()}`);
    await sleep(50);
  }
}
const has = (text) => () => screen().includes(text);

function start(width, height) {
  session = `pig-snake-${process.pid}-${Date.now() % 100000}`;
  dir = mkdtempSync(join(tmpdir(), 'pig-snake-pane-'));
  for (const sub of ['agent', 'home', 'work', 'pighome']) mkdirSync(join(dir, sub));
  const env = {
    HOME: join(dir, 'home'), PIG_HOME: join(dir, 'pighome'), PIG_CODING_AGENT_DIR: join(dir, 'agent'),
    PI_CODING_AGENT_DIR: join(dir, 'agent'), PIG_TEST_FAUX: '1', PIG_TEST_FAUX_SCENARIO: 'parity-basic',
    TERM: 'xterm-256color', COLORTERM: 'truecolor', PATH: `${dirname(process.execPath)}:${process.env.PATH}`,
    // pig builds the Go extensions in the pane under this HOME; the Go caches come from the test, not from the tmux server.
    ...goCaches(),
  };
  const exports = Object.entries(env).map(([name, value]) => `${name}='${value}'`).join(' ');
  const command = `env ${exports} '${binary}' --model test-faux/faux-1`;
  tmux('new-session', '-d', '-s', session, '-x', String(width), '-y', String(height), '-c', join(dir, 'work'), command);
}
function stop() {
  try { tmux('kill-session', '-t', session); } catch { /* already gone */ }
  rmSync(dir, { recursive: true, force: true });
}

describe(`real terminal: Piglet Binary`, () => {
  before(async () => {
    start(100, 30);
    await until('the editor', has('faux-1'), 60_000);
  });
  after(stop);

  it('starts nothing by itself', () => {
    assert.ok(!screen().includes('PIG SNAKE'));
    assert.ok(!existsSync(statePath()), 'no state before the first game');
  });

  it('/pig-snake opens the title screen; m switches walls and wrap', async () => {
    type('/pig-snake');
    await until('the title', has('space start'));
    assert.match(screen(), /walls/);
    assert.match(screen(), /┌ Pig Snake/);
    key('m');
    await until('wrap mode', () => / wrap /.test(screen()));
    key('m');
    await until('walls mode', () => / walls /.test(screen()));
  });

  it('plays, pauses, crashes into the wall and retries', async () => {
    key('Space');
    await until('running', has('every apple adds a pig'));
    assert.match(screen(), /score 0000 {2}herd 1/);
    key('p');
    await until('paused', has('PAUSED'));
    key('p');
    await until('resumed', has('every apple adds a pig'));
    await until('crash', has('CRASHED into the wall'), 30_000);
    key('r');
    await until('a new game', has('every apple adds a pig'));
  });

  it('a too-small terminal pauses the game and enlarging it waits for p', async () => {
    resize(30, 10);
    await until('the resize hint', has('terminal too small'));
    assert.match(screen(), /needs 50x16/); // the 12x6 board of a 100x30 terminal at 4 px pigs
    resize(100, 30);
    await until('the paused game', has('PAUSED'));
    await sleep(600);
    assert.ok(screen().includes('PAUSED'), 'the game must stay paused until the user resumes it');
    key('p');
    await until('running again', () => screen().includes('every apple adds a pig') || screen().includes('CRASHED'));
  });

  it('q leaves, reports the score and saves the state', async () => {
    key('q');
    await until('the report', has('Pig Snake score'));
    assert.ok(!screen().includes('┌ Pig Snake'));
    await until('the state file', () => existsSync(statePath()));
    const saved = JSON.parse(readFileSync(statePath(), 'utf8'));
    assert.deepEqual(Object.keys(saved).sort(), ['highScore', 'wrapHighScore']);
  });

  it('/snake is the same game; wrap mode has no wall to crash into', async () => {
    type('/snake');
    await until('the title', has('space start'));
    key('m');
    await until('wrap mode', () => / wrap /.test(screen()));
    key('Space');
    await sleep(3500); // a wall would have ended a walls game after about a second
    assert.ok(!screen().includes('CRASHED'), screen());
    key('q');
    await until('the report', () => /Pig Snake \(wrap\) score/.test(screen()));
  });

  it('agent work goes on under the open game and the transcript stays intact', async () => {
    type('TUI_LIVE_STREAM');
    await sleep(300);
    type('/pig-snake');
    await until('the game over a streaming agent', has('space start'));
    await sleep(6000);
    key('q');
    await until('the stream finished', has('LIVE-STREAM-24'), 30_000);
    assert.ok(screen().includes('LIVE-STREAM-'), screen());
  });
});

for (const [width, height] of [[80, 24], [140, 40]]) {
  describe(`real terminal ${width}x${height}: Piglet Binary`, () => {
    before(async () => {
      start(width, height);
      await until('the editor', has('faux-1'), 60_000);
    });
    after(stop);
    it('fills the overlay exactly: box, board and two HUD lines', async () => {
      type('/pig-snake');
      await until('the title', has('space start'));
      const lines = screen().split('\n').slice(0, height);
      assert.equal(lines.length, height);
      assert.ok(lines[0].startsWith('┌ Pig Snake'));
      assert.ok(lines[height - 1].startsWith('└'));
      assert.match(lines[height - 3], /PIG SNAKE {2}score 0000/);
      assert.ok([...lines[1]].length >= width - 1, 'board row spans the overlay');
      key('q');
      await until('back in the editor', has('Pig Snake score'));
    });
  });
}
