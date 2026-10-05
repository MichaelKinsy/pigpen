// The index is generated from authored Piglet manifests, the Package release records and the verified Piglet receipts.
// These tests build small repositories; the committed index.json is checked by `npm run check`.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it, beforeEach, afterEach } from 'node:test';
import { generateIndex } from './generate-index.mjs';
import { recordPackage } from './packages.mjs';
import { pair, pem, receipt, payload, digest } from './test-receipts.mjs';

const repository = 'MichaelKinsy/pigpen';
const git = (cwd, ...args) => execFileSync('git', ['-c', 'commit.gpgsign=false', '-c', 'user.name=T', '-c', 'user.email=t@example.com', ...args], { cwd, encoding: 'utf8', env: { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_SYSTEM: '/dev/null' } }).trim();
const write = (dir, file, text) => { mkdirSync(join(dir, file, '..'), { recursive: true }); writeFileSync(join(dir, file), typeof text === 'string' ? text : JSON.stringify(text, null, 2)); };

function fixture() {
  const dir = mkdtempSync(join(tmpdir(), 'pigpen-index-'));
  git(dir, 'init', '-q', '-b', 'main');
  write(dir, 'piglets/herdr/piglet.yaml', 'name: herdr\ndescription: "PiG with herdr"\nrelease:\n  version: 0.1.0\n');
  write(dir, 'piglets/herdr/catalog.json', { featured: false, updatedAt: '2026-09-30', languages: ['go'], notes: ['A note.'] });
  write(dir, 'components/herdr/package.json', { name: 'pigpen-herdr', version: '0.1.0', description: 'Reports state', license: 'MIT', pi: { extensions: ['extensions/herdr'] } });
  write(dir, 'components/herdr/extensions/herdr/go.mod', 'module x\n');
  git(dir, 'add', '-A');
  git(dir, 'commit', '-q', '-m', 'one');
  return dir;
}

describe('generateIndex', () => {
  let dir;
  beforeEach(() => { dir = fixture(); });
  afterEach(() => rmSync(dir, { recursive: true, force: true }));

  it('lists a Piglet as planned and no Package before one is released', () => {
    const index = generateIndex(dir, repository);
    assert.deepEqual(index.entries.map((e) => [e.kind, e.id, e.status]), [['piglet', 'herdr', 'planned']]);
    assert.deepEqual(index.entries[0].releases, []);
  });

  it('lists a Package as available once its release record is committed, built from the tagged tree', () => {
    git(dir, 'tag', 'components/herdr/v0.1.0');
    write(dir, 'releases/packages/herdr.json', recordPackage('herdr', '0.1.0', { directory: dir }));
    write(dir, 'components/herdr/package.json', { name: 'pigpen-herdr', version: '0.2.0', description: 'Changed', license: 'MIT', pi: { extensions: ['extensions/herdr'] } });
    const index = generateIndex(dir, repository);
    assert.deepEqual(index.entries.map((e) => [e.kind, e.id, e.status]), [['piglet', 'herdr', 'planned'], ['package', 'pigpen-herdr', 'available']]);
    const entry = index.entries[1];
    assert.equal(entry.version, '0.1.0');
    assert.equal(entry.description, 'Reports state');
    assert.match(entry.installCommand, /^pig install 'git:https:\/\/github\.com\/MichaelKinsy\/pigpen\.git@components\/herdr\/v0\.1\.0#subdirectory=components%2Fherdr'$/);
    assert.equal(index.updatedAt >= '2026-09-30', true);
  });

  it('is deterministic', () => {
    git(dir, 'tag', 'components/herdr/v0.1.0');
    write(dir, 'releases/packages/herdr.json', recordPackage('herdr', '0.1.0', { directory: dir }));
    assert.equal(JSON.stringify(generateIndex(dir, repository)), JSON.stringify(generateIndex(dir, repository)));
  });

  it('fails on a record the tag does not confirm', () => {
    git(dir, 'tag', 'components/herdr/v0.1.0');
    write(dir, 'releases/packages/herdr.json', { ...recordPackage('herdr', '0.1.0', { directory: dir }), commit: '1'.repeat(40) });
    assert.throws(() => generateIndex(dir, repository), /not in this clone/);
  });

  describe('Piglet receipts', () => {
    const author = pair();
    const put = (name, version, text) => write(dir, `releases/piglets/${name}/${version}.json`, text);
    const pin = () => write(dir, 'release-keys/piglets.pub', pem(author.publicKey));

    it('lists a Piglet as available, with its releases newest first, once a receipt verifies against the pinned key', () => {
      pin();
      put('herdr', '0.1.0', receipt(author));
      put('herdr', '0.2.0', receipt(author, payload({ version: '0.2.0', binaries: { 'linux/amd64': { url: 'pig-herdr-linux-amd64', sha256: digest('4'), size: 7 } } })));
      const entry = generateIndex(dir, repository).entries[0];
      assert.equal(entry.status, 'available');
      assert.deepEqual(entry.releases.map((r) => r.version), ['0.2.0', '0.1.0']);
      assert.deepEqual(entry.releases[0].platforms, ['linux/amd64']);
      assert.equal(entry.releases[1].pullCommand, "pig piglet pull 'github:MichaelKinsy/pigpen/herdr@0.1.0'");
      assert.equal(entry.releases[0].indexUrl, 'https://github.com/MichaelKinsy/pigpen/releases/download/herdr%2Fv0.2.0/piglet-release.json');
      assert.match(entry.releases[0].verificationKey.id, /^ed25519:[0-9a-f]{32}$/);
      assert.equal(entry.releases[0].verificationKey.publicKeyUrl, 'https://raw.githubusercontent.com/MichaelKinsy/pigpen/main/release-keys/piglets.pub');
      assert.doesNotMatch(entry.notes.join(' '), /Not released yet/);
      assert.match(entry.notes[0], /0\.2\.0/);
    });
    it('is available only for the Piglet that has a receipt', () => {
      write(dir, 'piglets/a2a/piglet.yaml', 'name: a2a\ndescription: "a2a"\nrelease:\n  version: 0.1.0\n');
      write(dir, 'piglets/a2a/catalog.json', { featured: false, updatedAt: '2026-09-30', languages: ['go'], notes: [] });
      pin();
      put('herdr', '0.1.0', receipt(author));
      assert.deepEqual(generateIndex(dir, repository).entries.map((e) => [e.id, e.status]), [['a2a', 'planned'], ['herdr', 'available']]);
    });
    it('refuses a receipt that no pinned key verifies, so the key must be committed first', () => {
      put('herdr', '0.1.0', receipt(author));
      assert.throws(() => generateIndex(dir, repository), /not a pinned key/);
    });
    it('refuses a receipt signed by another key, a receipt filed under another version, and a receipt for no Piglet here', () => {
      pin();
      put('herdr', '0.1.0', receipt(pair()));
      assert.throws(() => generateIndex(dir, repository), /not a pinned key/);
      put('herdr', '0.1.0', receipt(author));
      put('herdr', '0.3.0', receipt(author));
      assert.throws(() => generateIndex(dir, repository), /0\.3\.0.*version 0\.1\.0/s);
      rmSync(join(dir, 'releases/piglets/herdr/0.3.0.json'));
      put('ghost', '0.1.0', receipt(author, payload({ piglet: 'ghost', github: { repository, tagPrefix: 'ghost/' }, binaries: { 'linux/amd64': { url: 'pig-ghost-linux-amd64', sha256: digest('1'), size: 1 } } })));
      assert.throws(() => generateIndex(dir, repository), /ghost.*no Piglet manifest/);
    });
    it('refuses an index whose available Piglet has no release (schema)', async () => {
      const { validateIndex } = await import('./generate-index.mjs');
      const index = generateIndex(dir, repository);
      index.entries[0].status = 'available';
      assert.throws(() => validateIndex(index), /releases/);
    });
  });
});
