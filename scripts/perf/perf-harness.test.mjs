// The perf harness (scripts/perf) must be hermetic: it never writes the user's ~/.pig, and every temporary home it
// gives a pig is gone when it returns. Fake pigs stand in for the real one, so no toolchain is needed.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, describe, it } from 'node:test';
import { root } from '../go-modules.mjs';

const scratch = realpathSync(mkdtempSync(join(tmpdir(), 'pigpen-perf-test-')));
after(() => rmSync(scratch, { recursive: true, force: true }));
const required = JSON.parse(readFileSync(join(root, 'scripts/pig-requirement.json'), 'utf8')).pig;
const has = (command) => spawnSync('sh', ['-c', `command -v ${command}`]).status === 0;

describe('build-singles', () => {
  it('builds with a scratch PiG home, never the user\'s ~/.pig', () => {
    const home = join(scratch, 'home');
    const out = join(scratch, 'singles');
    const record = join(scratch, 'build-env.json');
    mkdirSync(home);
    const fake = join(scratch, 'pig-build');
    // pig keeps receipts and a copy of every Binary it builds under PIG_HOME (default ~/.pig).
    writeFileSync(fake, `#!/bin/sh\nif [ "$1" = --version ]; then echo ${required}; exit 0; fi\n`
      + `node -e 'const e=process.env; require("fs").writeFileSync(process.argv[1], JSON.stringify({ pigHome: e.PIG_HOME || "", agentDir: e.PIG_CODING_AGENT_DIR || "", home: e.HOME }))' "${record}"\n`
      + 'mkdir -p "${PIG_HOME:-$HOME/.pig}/receipts"\nexit 0\n');
    chmodSync(fake, 0o755);
    const r = spawnSync(process.execPath, [join(root, 'scripts/perf/build-singles.mjs'), '--out', out, 'seed-check'], {
      cwd: scratch, encoding: 'utf8', env: { PATH: process.env.PATH, HOME: home, PIG_BIN: fake },
    });
    assert.equal(r.status, 0, r.stdout + r.stderr);
    const seen = JSON.parse(readFileSync(record, 'utf8'));
    assert.ok(seen.pigHome && !seen.pigHome.startsWith(home), `PIG_HOME must be a scratch directory, got ${JSON.stringify(seen.pigHome)}`);
    assert.ok(seen.agentDir && !seen.agentDir.startsWith(home), `PIG_CODING_AGENT_DIR must be a scratch directory, got ${JSON.stringify(seen.agentDir)}`);
    assert.ok(!existsSync(join(home, '.pig')), 'the build wrote ~/.pig');
    assert.ok(!existsSync(seen.pigHome), 'the scratch PiG home must be removed');
    assert.ok(existsSync(join(out, 'piglets/perf-seed-check/piglet.yaml')), 'the one-extension Piglet is staged under --out');
  });
});

describe('measure', { skip: !(has('tmux') && has('taskset')) && 'needs tmux and taskset (Linux)' }, () => {
  it('removes each run\'s temporary home only after the pig in it has exited', async () => {
    const tmp = join(scratch, 'tmp');
    mkdirSync(tmp);
    const envLog = join(scratch, 'pig-env.txt');
    // Like pig, it draws a footer ending in the model name, and when its terminal goes away it still writes its
    // state into HOME for a moment before it exits.
    const fake = join(scratch, 'pig-tui');
    writeFileSync(fake, '#!/bin/sh\n'
      + `env >> '${envLog}'\n`
      + 'trap \'i=0; while [ $i -lt 20 ]; do mkdir -p "$HOME/agent/s$i"; : > "$HOME/agent/s$i/state"; i=$((i+1)); sleep 0.01; done; exit 0\' HUP TERM\n'
      + "printf 'ready\\n  faux-1\\n'\nwhile :; do sleep 0.01; done\n");
    chmodSync(fake, 0o755);
    const r = spawnSync(process.execPath, [join(root, 'scripts/perf/measure.mjs'), 'startup', '--mode', 'tmux', '--n', '3', '--cpus', '0', `fake=${fake}`], {
      cwd: scratch, encoding: 'utf8', env: { ...process.env, TMPDIR: tmp }, timeout: 60000,
    });
    assert.equal(r.status, 0, r.stdout + r.stderr);
    assert.equal(JSON.parse(r.stdout).ms.fake.n, 3);
    await new Promise((done) => setTimeout(done, 500)); // a pig still running would write again by now
    assert.deepEqual(readdirSync(tmp).filter((name) => name.startsWith('pigpen-perf-')), [], 'a temporary home outlived its run');
    const env = readFileSync(envLog, 'utf8');
    assert.doesNotMatch(env, /=undefined$/m, 'an unset variable was passed to pig as the string "undefined"');
  });

  it('reports when the prompt accepts input, not only when it is drawn', () => {
    // pig draws its editor and footer before it has attached its extensions; until then typed text is not shown.
    // This fake draws the footer at once and reads (and shows) what is typed only 300 ms later.
    const fake = join(scratch, 'pig-late-input');
    writeFileSync(fake, '#!/bin/sh\nstty -echo -icanon min 1\nprintf \'ready\\n  faux-1\\n\'\nsleep 0.3\n'
      + 'typed=$(dd bs=1 count=5 2>/dev/null)\nprintf \'%s\\n\' "$typed"\nwhile :; do sleep 0.01; done\n');
    chmodSync(fake, 0o755);
    const r = spawnSync(process.execPath, [join(root, 'scripts/perf/measure.mjs'), 'startup', '--mode', 'tmux', '--n', '2', '--cpus', '0', `fake=${fake}`], {
      cwd: scratch, encoding: 'utf8', env: { ...process.env, TMPDIR: join(scratch, 'tmp') }, timeout: 60000,
    });
    assert.equal(r.status, 0, r.stdout + r.stderr);
    const out = JSON.parse(r.stdout);
    assert.ok(out.ms.fake.median < 250, `drawn at ${out.ms.fake.median} ms`);
    assert.ok(out.acceptsInputMs?.fake?.median >= 300, `the prompt accepted input at ${out.acceptsInputMs?.fake?.median} ms; it cannot before 300 ms`);
  });
});
