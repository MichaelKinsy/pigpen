// The smoke check must fail for every way a Binary can be broken, not just run one. Fake Binaries stand in for pig.
import assert from 'node:assert/strict';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, describe, it } from 'node:test';
import { FAUX_PROMPT, loadErrors, loadsInPig, smokeBinary, smokeEnv, stagedPiglets } from './platform-smoke.mjs';
import { requiredPigVersion } from './pig-bin.mjs';

const posix = process.platform !== 'win32';
const scratch = mkdtempSync(join(tmpdir(), 'platform-smoke-test-'));
after(() => rmSync(scratch, { recursive: true, force: true }));
let n = 0;

/** A fake Binary: `version` for --version, and for a print-mode run `turn` = { stdout, stderr, status }. It also records its environment. */
function fake({ version = `${requiredPigVersion()}+1.0.0`, versionStatus = 0, stdout = 'ok', stderr = '', status = 0 } = {}) {
  const file = join(scratch, `bin-${n++}`);
  const q = (text) => `'${text.replace(/'/g, `'\\''`)}'`;
  writeFileSync(file, `#!/bin/sh
if [ "$1" = "--version" ]; then printf '%s\\n' ${q(version)}; exit ${versionStatus}; fi
env > "${file}.env"
printf '%s\\n' "$*" > "${file}.args"
printf '%s\\n' ${q(stdout)}
printf '%s\\n' ${q(stderr)} >&2
exit ${status}
`);
  chmodSync(file, 0o755);
  return file;
}
const result = (checks) => Object.fromEntries(checks.map((c) => [c.name, c.ok]));

describe('smokeBinary', { skip: !posix && 'fake Binaries are shell scripts' }, () => {
  it('passes a Binary that reports the pinned version and answers the faux turn', () => {
    assert.deepEqual(result(smokeBinary(fake())), { '--version': true, 'faux turn': true });
  });

  it('runs the faux turn in print mode with the test model, no session, and an isolated environment', () => {
    const bin = fake();
    smokeBinary(bin);
    assert.equal(readFileSync(`${bin}.args`, 'utf8').trim(), `-p ${FAUX_PROMPT} --model test-faux/faux-1 --no-session`);
    const env = Object.fromEntries(readFileSync(`${bin}.env`, 'utf8').trim().split('\n').map((l) => [l.slice(0, l.indexOf('=')), l.slice(l.indexOf('=') + 1)]));
    assert.equal(env.PIG_TEST_FAUX, '1');
    assert.equal(env.PIG_OFFLINE, '1');
    for (const name of ['HOME', 'PIG_HOME', 'PIG_CODING_AGENT_DIR']) assert.ok(env[name].startsWith(tmpdir()), `${name}=${env[name]} is not under the temp directory`);
    assert.equal(env.ANTHROPIC_API_KEY, undefined);
  });

  it('removes the throwaway HOME afterwards', () => {
    const bin = fake();
    smokeBinary(bin);
    const home = readFileSync(`${bin}.env`, 'utf8').match(/^HOME=(.*)$/m)[1];
    assert.equal(existsSync(home), false);
  });

  it('fails a Binary built against another pig version', () => {
    assert.equal(result(smokeBinary(fake({ version: '0.3.0+0.87.1' })))['--version'], false);
  });

  it('fails a Binary whose --version exits nonzero', () => {
    assert.equal(result(smokeBinary(fake({ versionStatus: 3 })))['--version'], false);
  });

  it('fails an extension that did not load, even when the turn still answers ok and exits 0', () => {
    const checks = smokeBinary(fake({ stderr: 'Error: Failed to load extension "/x": boom' }));
    const turn = checks.find((c) => c.name === 'faux turn');
    assert.equal(turn.ok, false);
    assert.match(turn.detail, /Failed to load extension/);
  });

  it('fails a turn that exits nonzero', () => {
    const turn = smokeBinary(fake({ stdout: '', stderr: 'test-faux: unhandled request "hi"', status: 1 })).find((c) => c.name === 'faux turn');
    assert.equal(turn.ok, false);
    assert.match(turn.detail, /exit 1/);
  });

  it('fails a turn that exits 0 but gives another answer', () => {
    assert.equal(result(smokeBinary(fake({ stdout: 'something else' })))['faux turn'], false);
  });

  it('fails a Binary that cannot be run at all', () => {
    const checks = smokeBinary(join(scratch, 'missing'));
    assert.deepEqual(result(checks), { '--version': false, 'faux turn': false });
  });
});

describe('helpers', () => {
  it('finds load errors in either stream and ignores other text', () => {
    assert.deepEqual(loadErrors('hi\nError: Failed to load extension "/a": b\nHint: x'), ['Error: Failed to load extension "/a": b']);
    assert.deepEqual(loadErrors('Warning: something else\r\nok'), []);
  });

  it('keeps nothing of the caller\'s PIG or credential variables', () => {
    process.env.PIG_EXTENSIONS_DIR = '/leak';
    process.env.OPENAI_API_KEY = 'secret';
    try {
      const env = smokeEnv('/h');
      assert.equal(env.PIG_EXTENSIONS_DIR, undefined);
      assert.equal(env.OPENAI_API_KEY, undefined);
      assert.equal(env.HOME, '/h');
      assert.equal(env.USERPROFILE, '/h');
    } finally {
      delete process.env.PIG_EXTENSIONS_DIR;
      delete process.env.OPENAI_API_KEY;
    }
  });

  it('lists staged Piglets and nothing for a missing directory', () => {
    assert.deepEqual(stagedPiglets(join(scratch, 'none')), []);
    mkdirSync(join(scratch, 'staged', 'b'), { recursive: true });
    mkdirSync(join(scratch, 'staged', 'a'), { recursive: true });
    mkdirSync(join(scratch, 'staged', 'junk'), { recursive: true });
    writeFileSync(join(scratch, 'staged', 'a', 'piglet.yaml'), '');
    writeFileSync(join(scratch, 'staged', 'b', 'piglet.yaml'), '');
    assert.deepEqual(stagedPiglets(join(scratch, 'staged')), ['a', 'b']);
  });
});

// An extension that registers a native Provider cannot go through `pig install --validate-only`, so validate loads it
// by running pig on it with the test model, which reports a load failure and exits nonzero.
describe('loadsInPig', { skip: !posix && 'fake pigs are shell scripts' }, () => {
  it('passes an extension that loads and lets the faux turn answer', () => {
    const pig = fake();
    assert.equal(loadsInPig(pig, '/some/extension').ok, true);
    assert.equal(readFileSync(`${pig}.args`, 'utf8').trim(), `-e /some/extension -p ${FAUX_PROMPT} --model test-faux/faux-1 --no-session`);
  });

  it('fails an extension that did not load, even when the turn still answers', () => {
    const r = loadsInPig(fake({ stderr: 'Error: Failed to load extension "/x": no factory' }), '/x');
    assert.equal(r.ok, false);
    assert.match(r.detail, /Failed to load extension/);
  });

  it('fails a run that exits nonzero or answers something else', () => {
    assert.equal(loadsInPig(fake({ status: 1 }), '/x').ok, false);
    assert.equal(loadsInPig(fake({ stdout: 'no' }), '/x').ok, false);
  });

  it('runs in a throwaway home that is removed afterwards', () => {
    const pig = fake();
    loadsInPig(pig, '/x');
    const env = readFileSync(`${pig}.env`, 'utf8');
    const home = /^HOME=(.*)$/m.exec(env)[1];
    assert.equal(existsSync(home), false);
    assert.match(env, /^PIG_TEST_FAUX=1$/m);
  });
});
