// How a component Package is released and installed: the tag scheme, the install command, and the committed release
// record that turns a Package into an `available` index entry. The real `pig install` of every Package is
// scripts/package-install.test.mjs (`npm run test:packages`).
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it, before, after } from 'node:test';
import { packageTag, parsePackageTag, packageSpec, packageInstallCommand, readPackages, packageEntry, verifyPackageRecord, readPackageRecords, recordPackage, checkPackageTag } from './packages.mjs';
import { pigletTagPattern } from './receipts.mjs';

const repository = 'MichaelKinsy/pigpen';
const git = (cwd, ...args) => execFileSync('git', ['-c', 'commit.gpgsign=false', '-c', 'user.name=T', '-c', 'user.email=t@example.com', ...args], { cwd, encoding: 'utf8', env: { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_SYSTEM: '/dev/null' } }).trim();

describe('tag scheme', () => {
  it('tags a Package components/<name>/v<version>, which no Piglet tag can equal', () => {
    assert.equal(packageTag('herdr', '0.1.0'), 'components/herdr/v0.1.0');
    assert.equal(packageTag('jev', '1.2.3-rc.1'), 'components/jev/v1.2.3-rc.1');
    assert.equal(pigletTagPattern.test('components/herdr/v0.1.0'), false);
    assert.equal(parsePackageTag('herdr/v0.1.0'), undefined);
  });
  it('parses only a well-formed Package tag', () => {
    assert.deepEqual(parsePackageTag('components/pig-snake/v0.1.0'), { name: 'pig-snake', version: '0.1.0' });
    for (const bad of ['components/herdr/0.1.0', 'components/herdr/v1.2', 'components//v1.2.3', 'components/a/b/v1.2.3', 'components/../x/v1.2.3', 'components/Herdr/v1.2.3', 'v1.2.3', 'components/herdr/v1.2.3 ', 'components/herdr/v01.2.3']) {
      assert.equal(parsePackageTag(bad), undefined, bad);
    }
  });
  it('a Piglet tag is one slash, a Package tag two', () => {
    assert.equal(pigletTagPattern.test('herdr/v0.1.0'), true);
    assert.equal(pigletTagPattern.test('components/herdr/v0.1.0'), false);
  });
});

describe('install command', () => {
  it('names the tag and selects the Package directory of the monorepo', () => {
    assert.equal(packageSpec(repository, 'herdr', '0.1.0'), 'git:https://github.com/MichaelKinsy/pigpen.git@components/herdr/v0.1.0#subdirectory=components%2Fherdr');
    assert.equal(packageInstallCommand(repository, 'herdr', '0.1.0'), "pig install 'git:https://github.com/MichaelKinsy/pigpen.git@components/herdr/v0.1.0#subdirectory=components%2Fherdr'");
  });
  it('refuses a name or repository that could change the command', () => {
    assert.throws(() => packageSpec(repository, "herdr'; rm -rf ~; '", '0.1.0'));
    assert.throws(() => packageSpec(repository, 'herdr', '0.1.0; x'));
    assert.throws(() => packageSpec('a/b/c', 'herdr', '0.1.0'));
  });
});

describe('the Packages in this tree', () => {
  const packages = readPackages();
  it('are every component, each with a name, a semver version and a license', () => {
    assert.ok(packages.length >= 21);
    for (const { dir, manifest } of packages) {
      // The npm-style name need not repeat the directory (pigpen-rpiv-todo lives in components/todo); it is the index id.
      assert.match(manifest.name, /^pigpen-[a-z0-9][a-z0-9._-]*$/, dir);
      assert.match(manifest.version, /^\d+\.\d+\.\d+$/);
      assert.ok(manifest.license, dir);
      assert.ok(manifest.description, dir);
    }
    assert.equal(new Set(packages.map(({ manifest }) => manifest.name)).size, packages.length);
  });
  it('never collide with a Piglet id', async () => {
    const { readManifests } = await import('./generate-index.mjs');
    const piglets = new Set(readManifests().map(({ manifest }) => manifest.name));
    for (const { manifest } of packages) assert.equal(piglets.has(manifest.name), false, manifest.name);
  });
});

describe('release records', () => {
  let repo;
  let commit;
  before(() => {
    repo = mkdtempSync(join(tmpdir(), 'pigpen-pkg-'));
    git(repo, 'init', '-q', '-b', 'main');
    mkdirSync(join(repo, 'components/herdr/extensions/herdr'), { recursive: true });
    mkdirSync(join(repo, 'components/lib'), { recursive: true });
    writeFileSync(join(repo, 'components/herdr/package.json'), JSON.stringify({ name: 'pigpen-herdr', version: '0.1.0', description: 'Reports state', license: 'MIT', keywords: ['pig-package'], pi: { extensions: ['extensions/herdr'] } }));
    writeFileSync(join(repo, 'components/herdr/extensions/herdr/go.mod'), 'module x\n');
    writeFileSync(join(repo, 'components/lib/package.json'), JSON.stringify({ name: 'pigpen-lib', version: '0.2.0', description: 'A library', license: 'MIT' }));
    git(repo, 'add', '-A');
    git(repo, 'commit', '-q', '-m', 'one', '--date=2026-10-06T12:00:00Z');
    commit = git(repo, 'rev-parse', 'HEAD');
    git(repo, 'tag', 'components/herdr/v0.1.0');
    git(repo, 'tag', 'components/lib/v0.2.0');
    // The working tree moves on after the release: the entry must describe the tagged version.
    writeFileSync(join(repo, 'components/herdr/package.json'), JSON.stringify({ name: 'pigpen-herdr', version: '0.2.0', description: 'Changed since', license: 'Apache-2.0', pi: { extensions: ['extensions/herdr'], skills: ['skills/x'] } }));
    git(repo, 'commit', '-q', '-am', 'two');
  });
  after(() => rmSync(repo, { recursive: true, force: true }));

  it('records the tag, the commit it points at and its date, and nothing the tag cannot confirm', () => {
    const record = recordPackage('herdr', '0.1.0', { directory: repo });
    assert.deepEqual(Object.keys(record).sort(), ['commit', 'date', 'tag']);
    assert.equal(record.tag, 'components/herdr/v0.1.0');
    assert.equal(record.commit, commit);
    assert.match(record.date, /^\d{4}-\d{2}-\d{2}$/);
  });
  it('refuses to record a tag that does not exist, or whose manifest has another version', () => {
    assert.throws(() => recordPackage('herdr', '0.3.0', { directory: repo }), /tag components\/herdr\/v0\.3\.0/);
    git(repo, 'tag', 'components/herdr/v0.2.1');
    assert.throws(() => recordPackage('herdr', '0.2.1', { directory: repo }), /version 0\.2\.0/);
    git(repo, 'tag', '-d', 'components/herdr/v0.2.1');
  });
  it('refuses a name that is not a component', () => {
    assert.throws(() => recordPackage('nope', '0.1.0', { directory: repo }), /no component/);
    assert.throws(() => recordPackage('../x', '0.1.0', { directory: repo }), /Invalid/);
  });
  it('builds an available entry from the tagged tree, not from the working tree', () => {
    const record = recordPackage('herdr', '0.1.0', { directory: repo });
    const entry = packageEntry('herdr', record, { directory: repo, repository });
    assert.equal(entry.kind, 'package');
    assert.equal(entry.status, 'available');
    assert.equal(entry.id, 'pigpen-herdr');
    assert.equal(entry.version, '0.1.0');
    assert.equal(entry.license, 'MIT');
    assert.equal(entry.description, 'Reports state');
    assert.deepEqual(entry.resources, ['extension']);
    assert.deepEqual(entry.languages, ['go']);
    assert.equal(entry.official, false);
    assert.equal(entry.piCompatibility, 'not-applicable');
    assert.equal(entry.source.spec, 'git:https://github.com/MichaelKinsy/pigpen.git@components/herdr/v0.1.0#subdirectory=components%2Fherdr');
    assert.equal(entry.source.url, 'https://github.com/MichaelKinsy/pigpen/tree/components/herdr/v0.1.0/components/herdr');
    assert.equal(entry.installCommand, "pig install 'git:https://github.com/MichaelKinsy/pigpen.git@components/herdr/v0.1.0#subdirectory=components%2Fherdr'");
    assert.ok(entry.notes.length > 0);
  });
  it('says a Package without Resources is a library', () => {
    const entry = packageEntry('lib', recordPackage('lib', '0.2.0', { directory: repo }), { directory: repo, repository });
    assert.deepEqual(entry.resources, []);
    assert.match(entry.notes.join(' '), /library/i);
  });
  it('rejects a record whose commit is not the tag, is unknown, or whose file name disagrees', () => {
    const good = recordPackage('herdr', '0.1.0', { directory: repo });
    const head = git(repo, 'rev-parse', 'HEAD');
    assert.throws(() => verifyPackageRecord('herdr', { ...good, commit: head }, { directory: repo }), /points at/);
    assert.throws(() => verifyPackageRecord('herdr', { ...good, commit: '0'.repeat(40) }, { directory: repo }), /not in this clone|points at/);
    assert.throws(() => verifyPackageRecord('lib', good, { directory: repo }), /names herdr/);
    assert.throws(() => verifyPackageRecord('herdr', { ...good, tag: 'herdr/v0.1.0' }, { directory: repo }), /Package tag/);
    assert.throws(() => verifyPackageRecord('herdr', { ...good, extra: 1 }, { directory: repo }), /unexpected/);
  });
  it('reads the committed records of a tree, none when there is no releases directory', () => {
    assert.deepEqual([...readPackageRecords(repo)], []);
    mkdirSync(join(repo, 'releases/packages'), { recursive: true });
    writeFileSync(join(repo, 'releases/packages/herdr.json'), JSON.stringify(recordPackage('herdr', '0.1.0', { directory: repo })));
    assert.deepEqual([...readPackageRecords(repo).keys()], ['herdr']);
    writeFileSync(join(repo, 'releases/packages/ghost.json'), '{}');
    assert.throws(() => verifyPackageRecord('ghost', readPackageRecords(repo).get('ghost'), { directory: repo }), /tag/);
  });
});

describe('the tag a release workflow was started by', () => {
  it('names a component whose manifest has that version', () => {
    const { dir, manifest } = readPackages().find(({ dir }) => dir === 'herdr');
    assert.deepEqual(checkPackageTag(`components/${dir}/v${manifest.version}`), { name: 'herdr', version: manifest.version });
  });
  it('is refused for a Piglet tag, another version, an unknown component or a malformed tag', () => {
    const { manifest } = readPackages().find(({ dir }) => dir === 'herdr');
    assert.throws(() => checkPackageTag(`herdr/v${manifest.version}`), /not a Package tag/);
    assert.throws(() => checkPackageTag('components/herdr/v9.9.9'), /manifest has version/);
    assert.throws(() => checkPackageTag('components/nope/v0.1.0'), /no component/);
    assert.throws(() => checkPackageTag('components/herdr/0.1.0'), /not a Package tag/);
  });
});
