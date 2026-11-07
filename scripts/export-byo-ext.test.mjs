// scripts/export-byo-ext.sh builds the self-contained byo-ext harness tree from the canonical Pigpen tree and checks one for drift.
import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync, appendFileSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { after, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const script = join(root, 'scripts/export-byo-ext.sh');
const scratch = mkdtempSync(join(tmpdir(), 'export-byo-ext-test-'));
after(() => rmSync(scratch, { recursive: true, force: true }));
const run = (...args) => spawnSync('sh', [script, ...args], { encoding: 'utf8' });

function files(dir, base = dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => (entry.isDirectory() ? files(join(dir, entry.name), base) : [join(dir, entry.name).slice(base.length + 1)]));
}

describe('export-byo-ext.sh', () => {
  const out = join(scratch, 'tree');
  it('writes the whole build context: Dockerfile, adapter, policy extension, rewritten Piglet, licences', () => {
    const result = run(out);
    assert.equal(result.status, 0, result.stderr);
    for (const path of ['Dockerfile', '.dockerignore', 'LICENSE', 'NOTICE', 'pig-kagent.sh', 'adapter/go.mod', 'adapter/cmd/kagent-pig/main.go', 'chart/pig-kagent/Chart.yaml', 'policy/go.mod', 'policy/LICENSE', 'piglet/piglet.yaml']) {
      assert.ok(existsSync(join(out, path)), `${path} is exported`);
    }
    assert.ok(statSync(join(out, 'pig-kagent.sh')).mode & 0o100, 'the installer stays executable');
  });
  it('drops test-only files, testdata and the proof directory that lives next to harness/', () => {
    const all = files(out);
    assert.deepEqual(all.filter((path) => /_test\.go$/.test(path) || /(^|\/)(testdata|proof)\//.test(path)), []);
    assert.ok(existsSync(join(root, 'components/kagent/proof/RESULTS.md')), 'the proof directory exists in the repository');
    assert.ok(!existsSync(join(out, 'proof')), 'proof/ is not part of the exported tree');
  });
  it('rewrites the Piglet to the Piglet-local origin and keeps no Package or outside path', () => {
    const text = readFileSync(join(out, 'piglet/piglet.yaml'), 'utf8');
    assert.match(text, /origins: \[local:\.\/extensions\/kagent-policy\]/);
    assert.doesNotMatch(text, /package:|\.\.\/|^packages:/m);
  });
  it('refuses a non-empty target and exports sources byte for byte', () => {
    assert.equal(run(out).status, 2);
    assert.equal(readFileSync(join(out, 'adapter/go.mod'), 'utf8'), readFileSync(join(root, 'components/kagent/harness/adapter/go.mod'), 'utf8'));
  });
  it('--check passes on a fresh export and fails on a changed, added or missing file', () => {
    assert.equal(run('--check', out).status, 0);
    appendFileSync(join(out, 'adapter/go.mod'), '// drift\n');
    assert.equal(run('--check', out).status, 1);
    const again = join(scratch, 'tree2');
    assert.equal(run(again).status, 0);
    writeFileSync(join(again, 'extra.txt'), 'x');
    assert.equal(run('--check', again).status, 1);
    rmSync(join(again, 'extra.txt'));
    rmSync(join(again, 'NOTICE'));
    assert.equal(run('--check', again).status, 1);
    assert.equal(run('--check', join(scratch, 'missing')).status, 1);
  });
});
