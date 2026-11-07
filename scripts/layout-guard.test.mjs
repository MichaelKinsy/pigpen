// The layout guard: the repository's own tree passes, and a stray entry fails with the place it belongs in (the red cases).
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { after, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { layoutProblems } from './layout-guard.mjs';

const scratch = mkdtempSync(join(tmpdir(), 'layout-'));
after(() => rmSync(scratch, { recursive: true, force: true }));
const tree = (files) => {
  const dir = mkdtempSync(join(scratch, 't-'));
  for (const file of files) { mkdirSync(dirname(join(dir, file)), { recursive: true }); writeFileSync(join(dir, file), ''); }
  return dir;
};
const good = ['README.md', 'LICENSE', 'package.json', 'catalog/index.json', 'components/a/package.json', 'components/a/README.md', 'components/a/extensions/a/go.mod', 'bench/components/b/package.json', 'piglets/p/piglet.yaml'];

describe('the layout guard', () => {
  it('passes for this repository', () => {
    assert.deepEqual(layoutProblems(join(dirname(fileURLToPath(import.meta.url)), '..')), []);
  });
  it('passes for an agreed tree, ignoring tooling output', () => {
    assert.deepEqual(layoutProblems(tree([...good, 'node_modules/x/index.js', 'dist/staged/y', '.git/HEAD'])), []);
  });
  it('refuses a stray file at the top and names where it belongs (red)', () => {
    const problems = layoutProblems(tree([...good, 'index.json', 'NOTES.md', 'release.sh', 'bundle.tgz']));
    assert.equal(problems.length, 4);
    assert.match(problems.find((p) => p.startsWith('index.json')), /belongs in catalog\//);
    assert.match(problems.find((p) => p.startsWith('NOTES.md')), /belongs in docs\//);
    assert.match(problems.find((p) => p.startsWith('release.sh')), /belongs in scripts\//);
    assert.match(problems.find((p) => p.startsWith('bundle.tgz')), /do not commit an archive/);
  });
  it('refuses a stray directory at the top, a bench directory outside bench/ most of all (red)', () => {
    const problems = layoutProblems(tree([...good, 'bench-extra/readme.md', 'misc/x.txt']));
    assert.match(problems.find((p) => p.startsWith('bench-extra/')), /belongs in bench\//);
    assert.match(problems.find((p) => p.startsWith('misc/')), /components\/ \(a Package\), piglets\//);
  });
  it('refuses a file kind a component does not keep at its root, with the place it belongs in (red)', () => {
    const problems = layoutProblems(tree([...good, 'components/a/run.sh', 'components/a/logo.png', 'components/a/out.tgz', 'components/a/stuff/x.txt']));
    assert.equal(problems.length, 4);
    assert.match(problems.find((p) => p.includes('run.sh')), /belongs in scripts\//);
    assert.match(problems.find((p) => p.includes('logo.png')), /belongs in assets\//);
    assert.match(problems.find((p) => p.includes('out.tgz')), /do not commit an archive/);
    assert.match(problems.find((p) => p.includes('stuff/')), /belongs in extensions\/, skills\/, libraries\/, assets\/ or port\//);
  });
  it('lets a Go library module keep its package directories and Go files at its root', () => {
    assert.deepEqual(layoutProblems(tree([...good, 'components/lib/go.mod', 'components/lib/lib.go', 'components/lib/widthx/w.go'])), []);
  });
});
