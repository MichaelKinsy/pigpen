// The oracle fetch: ports/oracles.lock.json names the repository, the commit and the tree digest of every original; scripts/oracles.mjs
// fetches it, refuses any difference, and the check refuses an oracle the lock does not list. These tests use a local git repository.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { after, describe, it } from 'node:test';
import { includeList, lockProblems, materialize, missingOracles, normalizeUrl, planFetch, ensureCommit, treeDigest, withIgnoreBlock, ignoreLines, LOCK_FILE, requireOracles } from './oracles.mjs';

const scratch = mkdtempSync(join(tmpdir(), 'oracles-'));
after(() => rmSync(scratch, { recursive: true, force: true }));
const put = (base, rel, text) => { mkdirSync(dirname(join(base, rel)), { recursive: true }); writeFileSync(join(base, rel), text); };
const git = (cwd, ...args) => execFileSync('git', ['-c', 'commit.gpgsign=false', '-c', 'user.name=T', '-c', 'user.email=t@example.com', ...args], { cwd, encoding: 'utf8', env: { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_SYSTEM: '/dev/null' } }).trim();

/** A local "upstream" repository with a package under pkg/, and its commit. */
function upstream() {
  const dir = mkdtempSync(join(scratch, 'up-'));
  git(dir, 'init', '-q', '-b', 'main');
  put(dir, 'pkg/LICENSE', 'MIT upstream\n');
  put(dir, 'pkg/src/index.ts', 'export default 1;\n');
  put(dir, 'pkg/src/util.ts', 'export const u = 2;\n');
  put(dir, 'pkg/test/a.test.ts', 'test\n');
  put(dir, 'other/readme.md', 'not part of the oracle\n');
  git(dir, 'add', '-A');
  git(dir, 'commit', '-q', '-m', 'one');
  return { dir, commit: git(dir, 'rev-parse', 'HEAD') };
}

/** A repository tree with one component whose provenance pins `up`, and a lock entry that fetches pkg/ minus the tests. */
function tree(up, { sha256, source = 'fetched', extra = {} } = {}) {
  const dir = mkdtempSync(join(scratch, 'tree-'));
  put(dir, 'components/c/provenance.json', JSON.stringify({ upstreams: [{ url: up.dir, revision: up.commit, path: 'port/oracle' }] }));
  const place = join(dir, 'components/c/port/oracle');
  put(place, 'LICENSE', 'MIT upstream\n');
  const entry = { id: 'c', component: 'c', path: 'port/oracle', repository: up.dir, commit: up.commit, source, map: [{ from: 'pkg', to: '' }], include: ['src/'], kept: ['LICENSE'], files: 3, bytes: 0, sha256: sha256 ?? '0'.repeat(64), ...extra };
  return { dir, place, entry, cache: join(dir, 'cache') };
}
const expected = (up) => {
  const want = mkdtempSync(join(scratch, 'want-'));
  put(want, 'LICENSE', 'MIT upstream\n'); put(want, 'src/index.ts', 'export default 1;\n'); put(want, 'src/util.ts', 'export const u = 2;\n');
  void up; return treeDigest(want);
};

describe('treeDigest', () => {
  it('covers paths and contents, not modes', () => {
    const a = mkdtempSync(join(scratch, 'd-')); put(a, 'x/y.txt', 'one'); put(a, 'z.txt', 'two');
    const before = treeDigest(a);
    chmodSync(join(a, 'z.txt'), 0o755);
    assert.deepEqual(treeDigest(a), before);
    writeFileSync(join(a, 'z.txt'), 'twp');
    assert.notEqual(treeDigest(a).sha256, before.sha256);
    assert.equal(before.files, 2);
  });
});

describe('fetching an oracle', () => {
  it('fetches the pinned commit, keeps the tracked licence, and matches the lock digest', () => {
    const up = upstream();
    const t = tree(up, { sha256: expected(up).sha256 });
    const result = materialize(t.entry, { directory: t.dir, cache: t.cache });
    assert.equal(result.fetched, true);
    assert.equal(readFileSync(join(t.place, 'src/index.ts'), 'utf8'), 'export default 1;\n');
    assert.equal(existsSync(join(t.place, 'test')), false, 'a file outside include is not fetched');
    assert.equal(materialize(t.entry, { directory: t.dir, cache: t.cache }).fetched, false, 'a second run finds it in place');
  });
  it('refuses a tree that is not the one the lock records (red case)', () => {
    const up = upstream();
    const t = tree(up, { sha256: 'f'.repeat(64) });
    assert.throws(() => materialize(t.entry, { directory: t.dir, cache: t.cache }), /does not match the lock/);
  });
  it('refuses a commit the upstream does not have', () => {
    const up = upstream();
    const t = tree(up, { extra: { commit: 'a'.repeat(40) } });
    assert.throws(() => ensureCommit(t.entry.repository, t.entry.commit, t.cache), /cannot fetch/);
  });
  it('plans exactly the included files, and a single file maps to itself', () => {
    const up = upstream();
    const gitDir = ensureCommit(up.dir, up.commit, join(scratch, 'plan-cache'));
    assert.deepEqual([...planFetch({ id: 'c', commit: up.commit, repository: up.dir, map: [{ from: 'pkg', to: '' }], include: ['src/index.ts', 'LICENSE'] }, gitDir).keys()].sort(), ['LICENSE', 'src/index.ts']);
    assert.deepEqual([...planFetch({ id: 'f', commit: up.commit, repository: up.dir, map: [{ from: 'pkg/src/util.ts', to: '' }] }, gitDir).keys()], ['']);
  });
  it('reports a fetched oracle that is not in place, and says what to run', () => {
    const up = upstream();
    const t = tree(up, { sha256: expected(up).sha256 });
    put(t.dir, LOCK_FILE, JSON.stringify({ schemaVersion: 1, oracles: [t.entry] }));
    assert.match(missingOracles(t.dir).join(), /does not match ports\/oracles\.lock\.json/);
    assert.throws(() => requireOracles(t.dir), /npm run oracles:fetch/);
    materialize(t.entry, { directory: t.dir, cache: t.cache });
    assert.deepEqual(missingOracles(t.dir), []);
  });
});

describe('the lock check', () => {
  it('passes for a lock that matches the tree and the pin', () => {
    const up = upstream();
    const t = tree(up, { sha256: expected(up).sha256 });
    put(t.dir, LOCK_FILE, JSON.stringify({ schemaVersion: 1, oracles: [t.entry] }));
    assert.deepEqual(lockProblems(t.dir), []);
  });
  it('refuses an oracle directory the lock does not list, with the way out', () => {
    const up = upstream();
    const t = tree(up);
    put(t.dir, LOCK_FILE, JSON.stringify({ schemaVersion: 1, oracles: [] }));
    assert.match(lockProblems(t.dir).join('\n'), /components\/c\/port\/oracle is an oracle that ports\/oracles\.lock\.json does not list/);
  });
  it('refuses a commit that is not the port\'s pin, a vendored copy with no reason, and a stale vendored digest', () => {
    const up = upstream();
    const t = tree(up, { source: 'vendored', extra: { commit: 'b'.repeat(40) } });
    put(t.dir, LOCK_FILE, JSON.stringify({ schemaVersion: 1, oracles: [t.entry] }));
    const problems = lockProblems(t.dir).join('\n');
    assert.match(problems, /is not a pin in components\/c\/provenance\.json/);
    assert.match(problems, /must say why it is not fetched/);
    assert.match(problems, /no longer matches its digest/);
  });
  it('writes the git-ignore block from the fetched entries', () => {
    const lock = { oracles: [{ source: 'fetched', component: 'a', path: 'port/oracle' }, { source: 'vendored', component: 'b', path: 'port/oracle' }, { source: 'fetched', component: 'c', path: 'port/oracle/x.ts', isFile: true }] };
    assert.deepEqual(ignoreLines(lock), ['/components/a/port/oracle/', '/components/c/port/oracle/x.ts']);
    const once = withIgnoreBlock('node_modules/\n', lock);
    assert.equal(withIgnoreBlock(once, lock), once);
  });
  it('compares repository URLs by their repository, and lists what an include must select', () => {
    assert.equal(normalizeUrl('https://github.com/Gotgenes/pi-packages/tree/main/packages/x.git'), 'https://github.com/gotgenes/pi-packages');
    assert.deepEqual(includeList(['a', 'src/x', 'src/y', 'docs/z'], new Set(['a', 'src/x', 'src/y'])), ['a', 'src/']);
    assert.deepEqual(includeList(['src/x', 'src/y'], new Set(['src/x'])), ['src/x']);
  });
});
