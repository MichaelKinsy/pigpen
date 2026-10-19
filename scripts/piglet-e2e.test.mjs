// Every Piglet Binary, driven in a real terminal through its main features (scripts/e2e/piglets.mjs).
//
//   PIG_BIN=/path/to/pig PIG_SOURCE_ROOT=/path/to/pig-git-checkout npm run test:piglet-e2e
//   PIGLET_E2E_BIN_DIR=dir npm run test:piglet-e2e     (use Binaries already built there, named by Piglet)
//   PIGLET_E2E_ONLY=a2a,pig-games npm run test:piglet-e2e
//
// Each test starts one Binary in its own tmux server with PATH=/usr/bin:/bin, so no Node, Go or other tool is available:
// a Go-only Piglet Binary must start, load every fused extension, answer a turn and exit cleanly on its own. The model is
// PiG's test-faux provider; nothing uses the network. POSIX only (tmux).
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { root } from './go-modules.mjs';
import { hostTarget } from './host-target.mjs';
import { requirePig } from './pig-bin.mjs';
import { build, stagedPiglets } from './platform-smoke.mjs';
import { FAUX_TURN, answerLines, piglets } from './e2e/piglets.mjs';
import { Terminal, isolatedPath, makeSystemBin, promptReady } from './e2e/tmux.mjs';

const hasTmux = spawnSync('tmux', ['-V'], { encoding: 'utf8' }).status === 0;
const skip = process.platform === 'win32' ? 'tmux is not available on Windows' : !hasTmux ? 'tmux is not installed' : false;

/** The Binary for a Piglet: from PIGLET_E2E_BIN_DIR, else built into dist/bin from the staged manifest. */
function binaryFor(name) {
  const given = process.env.PIGLET_E2E_BIN_DIR;
  if (given) {
    const file = join(given, name);
    assert.ok(existsSync(file), `PIGLET_E2E_BIN_DIR has no Binary ${file}`);
    return file;
  }
  const out = join(root, 'dist/bin');
  mkdirSync(out, { recursive: true });
  const built = build(requirePig(), name, out, hostTarget());
  assert.ok(!built.error, `building ${name}: ${built.error}`);
  return built.target;
}

describe('Piglet Binaries in a real terminal', { skip }, () => {
  it('runs with no Node, Go or other tool on PATH', () => {
    const home = mkdtempSync(join(tmpdir(), 'pigpen-e2e-path-'));
    try {
      makeSystemBin(home);
      const found = spawnSync('sh', ['-c', 'command -v node || command -v go || command -v npm || command -v npx || command -v python3'], { env: { PATH: isolatedPath(home) }, encoding: 'utf8' });
      assert.notEqual(found.status, 0, `the isolated PATH finds ${found.stdout.trim()}`);
      assert.ok(spawnSync('sh', ['-c', 'command -v git'], { env: { PATH: isolatedPath(home) } }).status === 0, 'git is linked');
    } finally {
      rmSync(home, { recursive: true, force: true });
    }
  });

  const only = process.env.PIGLET_E2E_ONLY?.split(',').filter(Boolean);
  const names = Object.keys(piglets).filter((name) => !only || only.includes(name));

  it('has a scenario for every Piglet in the repository', () => {
    const staged = stagedPiglets();
    assert.deepEqual(staged.filter((name) => !(name in piglets)), [], 'a Piglet without an end-to-end scenario: add it to scripts/e2e/piglets.mjs');
    assert.deepEqual(Object.keys(piglets).filter((name) => !staged.includes(name)), [], 'a scenario for a Piglet that does not exist');
  });

  for (const name of names) {
    it(`${name}: starts, answers a turn, runs its features, and exits cleanly`, async () => {
      const spec = piglets[name];
      const bin = binaryFor(name);
      const t = new Terminal(bin, { prepare: spec.prepare });
      try {
        await t.until('the editor', promptReady);
        assert.doesNotMatch(t.screen(), /Failed to load extension/, 'an extension did not load');

        await t.say(FAUX_TURN);
        await t.until('the answer `ok`', (screen) => answerLines(screen).length >= 1);

        for (const step of spec.steps ?? []) {
          if (step.run) await step.run(t);
          else {
            if (step.say !== undefined) await t.say(step.say);
            if (step.key) t.key(...step.key);
            if (step.expect !== undefined) await t.waitFor(step.expect);
            if (step.gone !== undefined) await t.waitGone(step.gone);
          }
        }
        if (spec.run) await spec.run(t);

        assert.doesNotMatch(t.screen(), /Failed to load extension|If this looks like a pig bug/, 'a failure reached the screen');
        if (t.alive() && !spec.quits) {
          await t.say('/quit');
        }
        const status = await t.waitExit();
        assert.equal(status, 0, `${name} exited with status ${status}\n${t.screen()}`);
      } finally {
        t.close();
      }
    });
  }
});
