// Runs the real `pig` in a detached tmux pane with the real mpv (--ao=null) and yt-dlp, over the network, and drives
// the pig-music player (milestone 4): /music opens it full screen, a search lists results, enter plays, space pauses,
// q hides it and the music goes on, /music shows it again at the position mpv reached. Throwaway HOME, PIG_HOME, agent
// dir and runtime dir; no cookies; nothing under ~/.pig is read or written.
//
//   PIG_MUSIC_SMOKE=1 PIG_BIN=/path/to/pig npm run test:pig-music-player
//
// Needs tmux, Go, mpv and yt-dlp. Not run in CI.
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { after, before, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { goCaches } from '../../../scripts/go-modules.mjs';

if (process.env.PIG_MUSIC_SMOKE !== '1') {
  console.error('Set PIG_MUSIC_SMOKE=1 to run the real player test (it uses the network, mpv and yt-dlp).');
  process.exit(2);
}
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const extension = join(root, 'components/pig-music/extensions/pig-music');
const pig = process.env.PIG_BIN;
if (!pig) throw new Error('PIG_BIN is not set: point it at a pig 0.4.0 or later');

let session = '';
let dir = '';
let run = '';
const tmux = (...args) => execFileSync('tmux', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
const type = (text) => tmux('send-keys', '-t', session, text, 'Enter');
const key = (name) => tmux('send-keys', '-t', session, name);
const literal = (text) => tmux('send-keys', '-t', session, '-l', text);
const screen = () => tmux('capture-pane', '-p', '-t', session);
const resize = (x, y) => tmux('resize-window', '-t', session, '-x', String(x), '-y', String(y));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// A slash command typed while the previous one's handler is still returning can be lost: give the editor a moment first.
const quick = async (text) => { await sleep(1000); type(text); };
const has = (text) => () => screen().includes(text);
const mpvPids = () => spawnSync('pgrep', ['-f', '--', `--input-ipc-server=${run}/pig-music/mpv.sock`], { encoding: 'utf8' }).stdout.split('\n').filter(Boolean);
const playBarSeconds = () => {
  // The play bar is " > 0:05 ████░░░░ 3:34 " (or "|" while paused); the header and the table have other shapes.
  const bar = screen().split('\n').find((l) => /^ [>|] \d+:\d\d .* \d+:\d\d\s*$/.test(l));
  const m = bar && /^ [>|] (\d+):(\d\d) /.exec(bar);
  return m ? Number(m[1]) * 60 + Number(m[2]) : NaN;
};

async function until(what, ready, ms = 30_000) {
  const deadline = Date.now() + ms;
  while (!ready()) {
    assert.ok(Date.now() < deadline, `timed out waiting for ${what}\n--- screen ---\n${screen()}`);
    await sleep(100);
  }
}

describe('pig-music player in a real terminal (real mpv --ao=null and yt-dlp)', () => {
  before(async () => {
    session = `pig-music-player-${process.pid}-${Date.now() % 100000}`;
    dir = mkdtempSync(join(tmpdir(), 'pmp-'));
    run = join(dir, 'r');
    for (const sub of ['agent', 'home', 'work', 'pighome', 'r']) mkdirSync(join(dir, sub), { mode: 0o700 });
    const env = {
      HOME: join(dir, 'home'), PIG_HOME: join(dir, 'pighome'), PIG_CODING_AGENT_DIR: join(dir, 'agent'),
      PI_CODING_AGENT_DIR: join(dir, 'agent'), PIG_TEST_FAUX: '1', PIG_TEST_FAUX_SCENARIO: 'parity-basic',
      TERM: 'xterm-256color', COLORTERM: 'truecolor', XDG_RUNTIME_DIR: run, PIG_MUSIC_MPV_ARGS: '--ao=null',
      PATH: `${dirname(process.execPath)}:${process.env.PATH}`, ...goCaches(),
    };
    const exports = Object.entries(env).map(([n, v]) => `${n}='${v}'`).join(' ');
    tmux('new-session', '-d', '-s', session, '-x', '110', '-y', '32', '-c', join(dir, 'work'),
      `env ${exports} '${pig}' --model test-faux/faux-1 -e '${extension}'`);
    await until('the editor', has('faux-1'), 120_000);
  });
  after(() => {
    try { tmux('kill-session', '-t', session); } catch { /* gone */ }
    for (const pid of mpvPids()) { try { process.kill(Number(pid)); } catch { /* gone */ } }
    rmSync(dir, { recursive: true, force: true });
  });

  it('/music opens the player over the whole terminal with the search screen', async () => {
    type('/music');
    await until('the player', has('Press / to search'));
    const lines = screen().split('\n').slice(0, 32);
    assert.match(lines[1], /Search.*Library.*Player/);
    assert.match(lines[31] ?? lines[30], /space play/);
    for (const l of lines) assert.ok([...l].length <= 110, l);
  });

  it('searches, lists results and plays one: the player screen shows it playing', async () => {
    key('/');
    literal('never gonna give you up');
    key('Enter');
    await until('results', has('Never Gonna Give You Up'), 60_000);
    // A songs search lists titles only; the selected row is enriched with its artist and length from its own page.
    await until('the selected result enriched', () => /Rick Astley/.test(screen()) && /\b[34]:\d\d\b/.test(screen().split('\n').find((l) => l.includes('Never Gonna Give You Up')) ?? ''), 60_000);
    key('Enter');
    await until('the player screen', has('Up Next'));
    await until('playing', () => /Playing/.test(screen()) && playBarSeconds() >= 2, 60_000);
    assert.match(screen(), /Now Playing/);
    // Up Next fills in artist and length for the queued tracks in the background (a songs search lists titles only), and Now
    // Playing shows the artist, the album and the length.
    const rowsWithLength = () => screen().split('\n').filter((l) => l.includes('│') && /\s\d+:\d\d\s*$/.test(l)).length;
    // The Player screen's left panel has the cover (half blocks or shades) or, until it loads, the braille disc.
    await until('a disc or a cover in Now Playing', () => screen().split('\n').some((l) => l.includes('│') && /[\u2800-\u28ff▀░▒▓█]{4}/.test(l.split('│')[0])), 60_000);
    await until('lengths in Up Next for several queued tracks', () => rowsWithLength() >= 4, 90_000);
    await until('Now Playing with artist, album and length', () => /Rick Astley/.test(screen().split('│')[0] ?? '') || /Length \d+:\d\d/.test(screen()), 60_000);
    assert.equal(mpvPids().length, 1);
  });

  it('space pauses and resumes; the volume key changes the volume', async () => {
    key('Space');
    await until('paused', has('Paused'));
    const at = playBarSeconds();
    await sleep(2500);
    assert.equal(playBarSeconds(), at, 'the position moved while paused');
    key('Space');
    await until('playing again', () => /Playing/.test(screen()));
    key('-');
    await until('volume 95%', has('vol 95%'));
    key('l');
    await until('repeat all', has('Repeat: all'));
    key('s');
    await until('shuffle on', has('Shuffle: on'));
    key('s');
    await until('shuffle off again (mpv >= 0.37)', has('Shuffle: off'));
    key('l');
    key('l');
    await until('repeat off again', has('Repeat: off'));
    key('o');
    await until('the settings screen', () => /Settings/.test(screen()) && /\[x\] Stop the music when PiG quits/.test(screen()));
    key('Escape');
    await until('back on the player', has('Up Next'));
  });

  it('a resize re-lays the screen out with no overlong line', async () => {
    resize(70, 20);
    await until('70x20', () => screen().split('\n')[1]?.includes('Search'));
    for (const l of screen().split('\n').slice(0, 20)) assert.ok([...l].length <= 70, l);
    resize(110, 32);
    await until('110x32', has('Now Playing'));
  });

  it('q hides the player, the music goes on, and /music shows it again further along', async () => {
    const before = playBarSeconds();
    const pids = mpvPids();
    key('q');
    await until('the editor again', () => !screen().includes('Up Next') && screen().includes('faux-1'));
    await sleep(4000);
    assert.deepEqual(mpvPids(), pids, 'mpv changed or stopped while hidden');
    type('/music');
    await until('the player again', has('Up Next'));
    await until('a later position', () => playBarSeconds() >= before + 3);
    assert.match(screen(), /Never Gonna Give You Up/);
  });

  it('q hides it again, the agent session is intact, and the footer shows the track', async () => {
    key('q');
    await until('the editor', () => !screen().includes('Up Next'));
    assert.ok(screen().includes('faux-1'));
    await until('the track in the footer', () => /^\|*>? ?.*Never Gonna Give You Up.*\d+:\d\d\/\d+:\d\d/m.test(screen().split('\n').slice(-3).join('\n')), 20_000);
  });

  it('/music settings opens a small box with the basic settings; left lowers the volume; q closes it', async () => {
    type('/music settings');
    await until('the quick settings', () => /quick settings/.test(screen()) && /Library browser/.test(screen()), 20_000);
    assert.ok(!screen().includes('Up Next'), 'the full player must not open');
    const vol = () => Number(/Volume\s+< (\d+) >/.exec(screen())?.[1]);
    const before = vol();
    assert.ok(before >= 5, `volume ${before}`);
    key('Left');
    await until('the volume five lower', () => vol() === before - 5, 10_000);
    key('Right');
    await until('the volume back', () => vol() === before, 10_000);
    key('q');
    await until('the editor again', () => !/quick settings/.test(screen()) && screen().includes('faux-1'), 10_000);
  });

  it('quick commands answer in one line without opening the player: pause, resume, vol, now, queue', async () => {
    await quick('/music pause');
    await until('paused', () => /pig-music: paused/.test(screen()), 20_000);
    assert.ok(!screen().includes('Up Next'), 'a quick command must not open the player');
    await quick('/music resume');
    await until('playing again', () => /pig-music: playing/.test(screen()), 20_000);
    await quick('/music vol 55');
    await until('volume 55', () => /volume 55/.test(screen()), 20_000);
    await quick('/music now');
    await until('now', () => /pig-music: playing  .*Never Gonna Give You Up/.test(screen()), 20_000);
    await quick('/music queue');
    await until('the queue list', () => /pig-music queue:/.test(screen()) && />\s+\d+\s+/.test(screen()), 20_000);
    assert.ok(!screen().includes('Up Next'));
  });

  it('measures the CPU of pig and the extension with the pulse running (shown) and with the player hidden (info, not a limit)', async () => {
    const pids = () => {
      // this test's pig (its command line names this checkout's extension) and the processes it started
      const top = spawnSync('pgrep', ['-f', '--', `-e ${extension}`], { encoding: 'utf8' }).stdout.split('\n').filter(Boolean);
      const all = new Set(top);
      for (const p of top) for (const c of spawnSync('pgrep', ['-P', p], { encoding: 'utf8' }).stdout.split('\n').filter(Boolean)) all.add(c);
      return [...all];
    };
    const cpu = (pid) => {
      try {
        const f = readFileSync(`/proc/${pid}/stat`, 'utf8');
        const rest = f.slice(f.lastIndexOf(')') + 2).split(' ');
        return (Number(rest[11]) + Number(rest[12])) * 10; // ms
      } catch { return 0; }
    };
    const total = (list) => list.reduce((a, p) => a + cpu(p), 0);
    const sample = async (what) => {
      const list = pids();
      const before = total(list);
      const t0 = Date.now();
      await sleep(10_000);
      const pct = ((total(list) - before) / (Date.now() - t0)) * 100;
      console.log(`CPU ${what}: pig and its extension, ${pct.toFixed(2)}% of a core (${list.length} processes)`);
    };
    key('M-m');
    await until('the player', has('Up Next'));
    await sleep(2000);
    await sample('player shown, pulse on (TERM has colour: palette and pulse active)');
    key('q');
    await until('the editor', () => !screen().includes('Up Next'));
    await sleep(1000);
    await sample('player hidden, music playing');
  });

  it('alt+m opens the player like /music, and q hides it', async () => {
    key('M-m');
    await until('the player', has('Up Next'));
    key('q');
    await until('the editor', () => !screen().includes('Up Next'));
  });

  it('/reload does not interrupt the music: same mpv, the footer returns, /music shows the same queue', async () => {
    const pids = mpvPids();
    assert.equal(pids.length, 1);
    type('/reload');
    await sleep(3000);
    await until('the editor after the reload', has('faux-1'), 120_000);
    assert.deepEqual(mpvPids(), pids, 'mpv changed or stopped across the reload');
    await until('the footer after the reload', () => screen().split('\n').slice(-3).join('\n').includes('Never Gonna Give You Up'), 60_000);
    type('/music');
    await until('the player shows the queue, not an empty search', has('Up Next'), 30_000);
    assert.match(screen(), /Never Gonna Give You Up/);
    key('q');
    await until('the editor', () => !screen().includes('Up Next'));
  });

  it('/music stop ends the music; with stopOnExit (default) quitting PiG does too', async () => {
    type('/music stop');
    await until('mpv gone', () => mpvPids().length === 0, 20_000);
    await until('the footer cleared', () => !screen().split('\n').slice(-3).join('\n').includes('Never Gonna Give You Up'), 20_000);
    // play again from the player and quit PiG
    type('/music');
    await until('the player again (on the screen it was left on)', () => /Up Next|Press \/ to search/.test(screen()), 60_000);
    key('/');
    literal('never gonna give you up');
    key('Enter');
    await until('results', has('Never Gonna Give You Up'), 60_000);
    key('Enter');
    await until('playing', () => /Playing/.test(screen()), 60_000);
    assert.equal(mpvPids().length, 1);
    key('q');
    await until('the editor', () => !screen().includes('Up Next'));
    type('/quit');
    await until('mpv stopped by quitting PiG', () => mpvPids().length === 0, 30_000);
  });
});
