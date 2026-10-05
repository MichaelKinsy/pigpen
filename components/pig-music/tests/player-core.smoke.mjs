// Real smoke of the player core (milestone 3): the pigmusic command line against a real mpv (--ao=null, no sound
// card) and a real yt-dlp, over the network, in a throwaway HOME. It checks what the unit tests can only model:
// that yt-dlp's output parses, that mpv resolves and plays a YouTube Music track, and that mpv outlives each command.
//
//   PIG_MUSIC_SMOKE=1 PIG_SDK_DIR=<PiG checkout>/extensions/sdk node components/pig-music/tests/player-core.smoke.mjs [query]
//
// PIG_SDK_DIR (or PIG_SOURCE_ROOT, or PIG_BIN, as for `npm run test:go`) locates the SDK for the build. Needs mpv,
// yt-dlp and Go on PATH. It never reads browser cookies and writes nothing outside its temporary directory.
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { goCaches, goModules, goWorkEnv, root } from '../../../scripts/go-modules.mjs';

if (process.env.PIG_MUSIC_SMOKE !== '1') {
  console.error('Set PIG_MUSIC_SMOKE=1 to run the real smoke (it uses the network).');
  process.exit(2);
}
const query = process.argv[2] ?? 'never gonna give you up';
const dir = mkdtempSync(join(tmpdir(), 'pm-smoke-'));
const run = join(dir, 'r');
for (const d of ['r', 'home', 'agent']) mkdirSync(join(dir, d), { mode: 0o700 });
const bin = join(dir, 'pigmusic');
const env = {
  PATH: process.env.PATH, HOME: join(dir, 'home'), XDG_RUNTIME_DIR: run, PIG_CODING_AGENT_DIR: join(dir, 'agent'),
  PIG_MUSIC_MPV_ARGS: '--ao=null', LANG: 'C.UTF-8',
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const say = (text) => console.log(text);

function pigmusic(...args) {
  const r = spawnSync(bin, args, { encoding: 'utf8', env, timeout: 90_000 });
  say(`$ pigmusic ${args.join(' ')}\n${r.stdout}${r.stderr ? `[stderr] ${r.stderr}` : ''}[exit ${r.status}]`);
  return r;
}
const mpvPids = () => {
  const r = spawnSync('pgrep', ['-f', '--', `--input-ipc-server=${run}/pig-music/mpv.sock`], { encoding: 'utf8' });
  return r.stdout.split('\n').filter(Boolean);
};
const position = (out) => {
  const m = /(\d+):(\d\d) \/ (\d+):(\d\d)/.exec(out);
  return m ? Number(m[1]) * 60 + Number(m[2]) : NaN;
};

try {
  // Build the command with the same go.work the repository's tests use.
  const modules = goModules().filter((m) => m.includes('components/pig-music'));
  execFileSync('go', ['build', '-o', bin, '.'], {
    cwd: resolve(root, 'components/pig-music/extensions/pig-music/cmd/pigmusic'),
    env: { ...goWorkEnv(modules), ...goCaches() }, stdio: 'inherit',
  });

  say(`mpv:    ${execFileSync('mpv', ['--version'], { encoding: 'utf8' }).split('\n')[0]}`);
  say(`yt-dlp: ${execFileSync('yt-dlp', ['--version'], { encoding: 'utf8' }).trim()}`);
  assert.equal(pigmusic('check').status, 0, 'mpv and yt-dlp must both be installed');
  const found = pigmusic('search', query, '-n', '5');
  assert.equal(found.status, 0, 'search failed');
  assert.match(found.stdout, /^ 1 {2}\S{11} /m, 'no result lines');
  const play = pigmusic('play', '1');
  assert.equal(play.status, 0, 'play failed');
  const pids = mpvPids();
  assert.equal(pids.length, 1, `expected one mpv, found ${pids}`);

  await sleep(4000); // the stream has to resolve and start
  const first = pigmusic('status');
  await sleep(3000);
  const second = pigmusic('status');
  assert.deepEqual(mpvPids(), pids, 'a command started another mpv');
  assert.ok(position(second.stdout) > position(first.stdout), `position did not advance: ${first.stdout} -> ${second.stdout}`);
  for (const args of [['pause'], ['resume'], ['next'], ['prev']]) {
    assert.equal(pigmusic(...args).status, 0, `${args.join(' ')} failed`);
  }
  await sleep(5000); // a track that has just started cannot be sought until its stream has opened
  for (const args of [['seek', '15'], ['volume', '50'], ['queue']]) {
    assert.equal(pigmusic(...args).status, 0, `${args.join(' ')} failed`);
  }
  assert.deepEqual(mpvPids(), pids, 'the same mpv served every command');
  assert.equal(pigmusic('stop').status, 0);
  await sleep(500);
  assert.equal(mpvPids().length, 0, 'mpv survived stop');
  say('SMOKE PASSED');
} catch (error) {
  say(`SMOKE FAILED: ${error.message}`);
  const log = join(run, 'pig-music', 'mpv.log');
  if (existsSync(log)) say(`--- mpv.log (last 40 lines) ---\n${readFileSync(log, 'utf8').split('\n').slice(-40).join('\n')}`);
  spawnSync('pkill', ['-f', '--', `--input-ipc-server=${run}/pig-music/mpv.sock`]);
  process.exitCode = 1;
} finally {
  rmSync(dir, { recursive: true, force: true });
}
