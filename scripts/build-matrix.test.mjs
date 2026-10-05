// scripts/build-matrix.mjs is the one place a release tag is matched to a Piglet and its native runners, for the release
// workflow and for the validation workflow's plan. These run it the way a workflow does: environment in, GITHUB_OUTPUT out.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it, before, after } from 'node:test';
import { fileURLToPath } from 'node:url';
import { readManifests } from './generate-index.mjs';
import { runners } from './runners.mjs';

const script = fileURLToPath(new URL('./build-matrix.mjs', import.meta.url));
let dir;
before(() => { dir = mkdtempSync(join(tmpdir(), 'pigpen-matrix-')); });
after(() => rmSync(dir, { recursive: true, force: true }));

function run(env) {
  const out = join(dir, 'output');
  writeFileSync(out, '');
  const result = spawnSync(process.execPath, [script], { encoding: 'utf8', env: { PATH: process.env.PATH, GITHUB_OUTPUT: out, ...env } });
  const outputs = Object.fromEntries(readFileSync(out, 'utf8').split('\n').filter(Boolean).map((line) => [line.slice(0, line.indexOf('=')), line.slice(line.indexOf('=') + 1)]));
  return { ...result, outputs };
}
const tag = (name) => ({ GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: name });
const herdr = readManifests().find(({ manifest }) => manifest.name === 'herdr').manifest;

describe('a release tag', () => {
  it('selects the one Piglet it names and every target that Piglet builds, each on its native runner', () => {
    const result = run(tag(`herdr/v${herdr.release.version}`));
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.outputs.name, 'herdr');
    const { include } = JSON.parse(result.outputs.matrix);
    assert.deepEqual(include.map((e) => e.target).sort(), [...herdr.build.targets].sort());
    for (const entry of include) {
      assert.equal(entry.runner, runners[entry.target]);
      assert.equal(entry.target, `${entry.goos}/${entry.goarch}`);
      assert.equal(entry.name, 'herdr');
    }
  });
  it('is refused, with nothing written, when the version is not the manifest release.version', () => {
    const result = run(tag('herdr/v9.9.9'));
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /Tag herdr\/v9\.9\.9 matches 0 Piglets/);
    assert.deepEqual(result.outputs, {});
  });
  it('is refused for a bare version, an unknown name and a Package tag', () => {
    for (const ref of [`v${herdr.release.version}`, 'nope/v0.1.0', `components/herdr/v${herdr.release.version}`, `herdr/v${herdr.release.version}-rc.1`]) {
      const result = run(tag(ref));
      assert.notEqual(result.status, 0, ref);
      assert.match(result.stderr, /matches 0 Piglets/, ref);
    }
  });
  it('is not a release when the ref is a branch named like a tag', () => {
    const result = run({ GITHUB_REF_TYPE: 'branch', GITHUB_REF_NAME: `herdr/v${herdr.release.version}` });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.outputs.name, undefined);
    assert.ok(JSON.parse(result.outputs.matrix).include.length > 5, 'a plan with no tag covers every Piglet');
  });
});
