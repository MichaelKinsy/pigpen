// The npm contract of a component Package: the scoped name, the keywords the site searches, the repository metadata, the
// files a Go extension needs to build, and the shared Go libraries a tarball must carry because a sibling directory does
// not exist once it is installed from npm. The install proof with the real pig is scripts/npm-packages-install.test.mjs.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it, after } from 'node:test';
import { SCOPE, buildPackageTree, libraryDeps, manifestProblems, npmName, packedProblems, publishManifest } from './npm-packages.mjs';
import { readPackages, root } from './packages.mjs';

const repository = 'MichaelKinsy/pigpen';
const base = { name: 'pigpen-herdr', version: '0.1.0', private: true, description: 'd', keywords: ['herdr'], license: 'MIT', author: 'A', files: ['extensions', 'README.md'], pi: { extensions: ['extensions/herdr'] } };

describe('publishManifest', () => {
  const m = publishManifest('herdr', base, { repository });
  it('is scoped, public, and findable by the site\'s keyword search', () => {
    assert.equal(m.name, '@pi-in-go/pigpen-herdr');
    assert.equal(npmName('herdr'), m.name);
    assert.equal(SCOPE, '@pi-in-go');
    assert.equal('private' in m, false);
    assert.deepEqual(m.publishConfig, { access: 'public' });
    assert.ok(m.keywords.includes('pig-package'));
    assert.ok(m.keywords.includes('extension'), 'a type word the site reads');
    assert.ok(m.keywords.includes('herdr'), 'existing keywords are kept');
    assert.equal(m.keywords.includes('pi-package'), false, 'a Go extension is not a Pi package');
    assert.equal(new Set(m.keywords).size, m.keywords.length);
  });
  it('names its place in the repository', () => {
    assert.deepEqual(m.repository, { type: 'git', url: 'git+https://github.com/MichaelKinsy/pigpen.git', directory: 'components/herdr' });
    assert.equal(m.homepage, 'https://github.com/MichaelKinsy/pigpen/tree/main/components/herdr#readme');
    assert.equal(m.bugs.url, 'https://github.com/MichaelKinsy/pigpen/issues');
  });
  it('is idempotent and keeps everything else', () => {
    assert.deepEqual(publishManifest('herdr', m, { repository }), m);
    assert.deepEqual(m.pi, base.pi);
    assert.equal(m.version, '0.1.0');
    assert.deepEqual(Object.keys(m).slice(0, 4), ['name', 'version', 'description', 'keywords']);
  });
  it('reads the type words from the resources it declares', () => {
    const all = publishManifest('x', { ...base, keywords: [], pi: { extensions: ['a'], skills: ['b'], prompts: ['c'], themes: ['d'] } }, { repository });
    assert.deepEqual(all.keywords.filter((k) => ['extension', 'skill', 'prompt', 'theme'].includes(k)), ['extension', 'skill', 'prompt', 'theme']);
    assert.equal(publishManifest('lib', { ...base, pi: undefined }, { repository }).keywords.includes('library'), true);
  });
});

describe('manifestProblems', () => {
  it('accepts a manifest that is already the published one, and says what is wrong otherwise', () => {
    const good = publishManifest('herdr', base, { repository });
    assert.deepEqual(manifestProblems('herdr', good, { repository, directory: root }), []);
    const text = (m) => manifestProblems('herdr', m, { repository, directory: root }).join('\n');
    assert.match(text({ ...good, private: true }), /private/);
    assert.match(text({ ...good, name: 'pigpen-herdr' }), /name/);
    assert.match(text({ ...good, keywords: ['herdr'] }), /pig-package/);
    assert.match(text({ ...good, repository: 'x' }), /repository/);
    assert.match(text({ ...good, publishConfig: {} }), /publishConfig/);
    assert.match(text({ ...good, version: '1' }), /version/);
    assert.match(text({ ...good, files: undefined }), /files/);
  });
});

describe('the Packages in this tree', () => {
  const packages = readPackages();
  it('all meet the contract', () => {
    for (const { dir, manifest } of packages) assert.deepEqual(manifestProblems(dir, manifest, { repository, directory: root }), [], dir);
  });
  it('have unique scoped names', () => {
    assert.equal(new Set(packages.map(({ manifest }) => manifest.name)).size, packages.length);
    for (const { dir, manifest } of packages) assert.equal(manifest.name, `@pi-in-go/pigpen-${dir}`);
  });
  it('know which shared libraries a Go extension reaches through its go.work', () => {
    assert.deepEqual(libraryDeps('pig-runner'), ['pig-play']);
    assert.deepEqual(libraryDeps('pi-typesafe').sort(), ['pi-typesafe-api', 'typesafe']);
    assert.deepEqual(libraryDeps('warden'), ['typesafe']);
    assert.deepEqual(libraryDeps('herdr'), []);
  });
});

describe('buildPackageTree', () => {
  const out = mkdtempSync(join(tmpdir(), 'pigpen-npm-tree-'));
  after(() => rmSync(out, { recursive: true, force: true }));
  it('carries the shared library inside the Package and points go.work at it', () => {
    const tree = buildPackageTree('pig-runner', out);
    assert.equal(existsSync(join(tree, 'libs/pig-play/go.mod')), true);
    const work = readFileSync(join(tree, 'extensions/pigrunner/go.work'), 'utf8');
    assert.match(work, /^\s*\.\.\/\.\.\/libs\/pig-play\s*$/m);
    assert.doesNotMatch(work, /\.\.\/\.\.\/\.\.\/pig-play/);
    const manifest = JSON.parse(readFileSync(join(tree, 'package.json'), 'utf8'));
    assert.ok(manifest.files.includes('libs'));
    assert.equal(manifest.name, '@pi-in-go/pigpen-pig-runner');
  });
  it('leaves a Package without shared libraries as it is', () => {
    const tree = buildPackageTree('herdr', out);
    assert.equal(existsSync(join(tree, 'libs')), false);
    assert.equal(readFileSync(join(tree, 'extensions/herdr/go.mod'), 'utf8'), readFileSync(join(root, 'components/herdr/extensions/herdr/go.mod'), 'utf8'));
  });
  it('leaves nothing out that npm would need to build the extension', () => {
    const tree = buildPackageTree('ahp', out);
    const packed = JSON.parse(execFileSync('npm', ['pack', '--dry-run', '--json', '--ignore-scripts'], { cwd: tree, encoding: 'utf8' }))[0].files.map((f) => f.path);
    assert.deepEqual(packedProblems(tree, packed), []);
    assert.ok(packed.some((f) => f.endsWith('third_party/agent-host-protocol-go/go.mod')));
    assert.deepEqual(packedProblems(tree, packed.filter((f) => !f.endsWith('go.sum'))).length > 0, true, 'a missing go.sum is reported');
  });
});
