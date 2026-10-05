// Runs the real `pig` in a detached tmux pane and drives the pig-music hello overlay (milestone 2):
// /music fills the terminal, echoes keys, redraws from a timer, re-lays out on resize and closes on q,
// Esc and ctrl+c. PiG's hermetic test-faux provider, a temporary HOME, PIG_HOME and PIG_CODING_AGENT_DIR
// (nothing under ~/.pig is read or written).
//
//   PIG_BIN=/path/to/pig npm run test:pig-music
//
// Needs tmux and a Go toolchain (pig builds the extension from source in the pane). Not run in CI.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { after, before, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { goCaches } from '../../../scripts/go-modules.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const extension = join(root, 'components/pig-music/extensions/pig-music');
// pig-music targets PiG 0.4 (Go SDK extensions/sdk v0.4.0), which scripts/pig-requirement.json (0.3.1) does not yet
// name, so this test takes PIG_BIN as given and prints its version instead of using requirePig.
const pig = process.env.PIG_BIN;
if (!pig) throw new Error('PIG_BIN is not set: point it at a pig 0.4.0 or later (pig --version prints 0.4.0+1.0.0)');

let session = '';
let dir = '';
const tmux = (...args) => execFileSync('tmux', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
const type = (text) => tmux('send-keys', '-t', session, text, 'Enter');
const key = (name) => tmux('send-keys', '-t', session, name);
const literal = (text) => tmux('send-keys', '-t', session, '-l', text);
const screen = () => tmux('capture-pane', '-p', '-t', session);
const resize = (x, y) => tmux('resize-window', '-t', session, '-x', String(x), '-y', String(y));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function until(what, ready, ms = 20_000) {
  const deadline = Date.now() + ms;
  while (!ready()) {
    assert.ok(Date.now() < deadline, `timed out waiting for ${what}\n--- screen ---\n${screen()}`);
    await sleep(50);
  }
}
const has = (text) => () => screen().includes(text);
const rows = (height) => screen().split('\n').slice(0, height);

function start(width, height, extra = []) {
  session = `pig-music-${process.pid}-${Date.now() % 100000}`;
  dir = mkdtempSync(join(tmpdir(), 'pig-music-pane-'));
  for (const sub of ['agent', 'home', 'work', 'pighome']) mkdirSync(join(dir, sub));
  const env = {
    HOME: join(dir, 'home'), PIG_HOME: join(dir, 'pighome'), PIG_CODING_AGENT_DIR: join(dir, 'agent'),
    PI_CODING_AGENT_DIR: join(dir, 'agent'), PIG_TEST_FAUX: '1', PIG_TEST_FAUX_SCENARIO: 'parity-basic',
    TERM: 'xterm-256color', COLORTERM: 'truecolor', PATH: `${dirname(process.execPath)}:${process.env.PATH}`,
    // pig builds the Go extension in the pane under this HOME; the Go caches come from the test, not from the tmux server.
    ...goCaches(),
  };
  const exports = Object.entries(env).map(([name, value]) => `${name}='${value}'`).join(' ');
  const command = `env ${exports} '${pig}' --model test-faux/faux-1 ${extra.join(' ')} -e '${extension}'`;
  tmux('new-session', '-d', '-s', session, '-x', String(width), '-y', String(height), '-c', join(dir, 'work'), command);
}
function stop() {
  try { tmux('kill-session', '-t', session); } catch { /* already gone */ }
  rmSync(dir, { recursive: true, force: true });
}

for (const mode of ['fullscreen', 'regular']) {
  describe(`pig-music hello overlay in a real terminal (${mode} TUI)`, () => {
    before(async () => {
      start(100, 30, ['--tui-mode', mode]);
      await until('the editor', has('faux-1'), 120_000);
    });
    after(stop);

    it('starts nothing by itself', () => {
      assert.ok(!screen().includes('pig-music hello'));
    });

    it('/music fills the terminal exactly: box, size, no overlong line', async () => {
      type('/music hello');
      await until('the overlay', has('keys received 0'));
      const lines = rows(30);
      assert.ok(lines[0].startsWith('+- pig-music hello -') && lines[0].endsWith('+'), lines[0]);
      assert.ok(lines[29].startsWith('+--') && lines[29].endsWith('+'), lines[29]);
      assert.match(lines[1], /terminal 100 columns x 30 rows/);
      for (const line of lines) assert.ok([...line].length <= 100, line);
    });

    it('echoes keys as named keys with their raw bytes', async () => {
      for (const name of ['a', 'Up', 'C-Right', 'C-a', 'Space', 'Tab', 'BTab', 'F5', 'Delete']) { key(name); await sleep(120); }
      literal('é');
      await until('nine keys and é', has('keys received 10'));
      const text = screen();
      for (const want of ['up ', 'ctrl+right', 'ctrl+a', 'space', 'tab', 'shift+tab', 'f5', 'delete', '\\u00e9']) {
        assert.ok(text.includes(want), `no ${want} in\n${text}`);
      }
    });

    it('redraws from its timer once a second', async () => {
      const ticks = () => Number(/redraw timer ticks (\d+)/.exec(screen())?.[1] ?? NaN);
      const first = ticks();
      await sleep(2300);
      const second = ticks();
      assert.ok(second >= first + 2 && second <= first + 3, `ticks went ${first} -> ${second} in 2.3 s`);
    });

    it('re-lays out on a width and a height change with no input', async () => {
      resize(70, 20);
      await until('70x20', has('terminal 70 columns x 20 rows'));
      let lines = rows(20);
      assert.ok(lines[19].startsWith('+--') && lines[19].endsWith('+'), lines[19]);
      for (const line of lines) assert.ok([...line].length <= 70, line);
      resize(70, 26); // the host reports a height change to the extension without a width change
      await until('70x26', has('terminal 70 columns x 26 rows'), 3000);
      lines = rows(26);
      assert.ok(lines[25].startsWith('+--') && lines[25].endsWith('+'), lines[25]);
      resize(100, 30);
      await until('100x30', has('terminal 100 columns x 30 rows'));
    });

    it('q closes, reports the counts and gives the session back', async () => {
      key('q');
      await until('the report', has('pig-music hello closed: '));
      assert.ok(!screen().includes('keys received'));
      assert.match(screen(), /closed: 11 keys, \d+ timer redraws/);
      assert.ok(screen().includes('faux-1'));
    });

    it('opening again shows the key log it was left with; Esc and ctrl+c close it', async () => {
      type('/music hello');
      await until('the overlay again', has('keys received 11'));
      key('Escape');
      await until('closed by Esc', has('pig-music hello closed: 12 keys'));
      type('/music hello');
      await until('the overlay a third time', has('keys received 12'));
      key('C-c');
      await until('closed by ctrl+c', has('pig-music hello closed: 13 keys'));
    });

    it('an agent turn streaming under the overlay finishes and the transcript stays intact', async () => {
      type('TUI_LIVE_STREAM');
      await sleep(300);
      type('/music hello');
      await until('the overlay over a streaming agent', has('keys received'));
      await sleep(6000);
      key('q');
      await until('the stream finished', has('LIVE-STREAM-24'), 30_000);
    });
  });
}
