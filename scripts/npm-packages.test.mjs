// The npm contract of a component Package: the scoped name, the keywords the site searches, the repository metadata, the
// files a Go extension needs to build, and the shared Go libraries a tarball must carry because a sibling directory does
// not exist once it is installed from npm. The install proof with the real pig is scripts/npm-packages-install.test.mjs.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
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
  it('depends on the shared library Package and points go.work at its sibling directory', () => {
    const tree = buildPackageTree('pig-runner', out);
    assert.equal(existsSync(join(tree, 'libs')), false, 'no private copy of the library');
    const work = readFileSync(join(tree, 'extensions/pigrunner/go.work'), 'utf8');
    assert.match(work, /^\s*\.\.\/\.\.\/\.\.\/pigpen-pig-play\s*$/m);
    const manifest = JSON.parse(readFileSync(join(tree, 'package.json'), 'utf8'));
    assert.equal(manifest.dependencies['@pi-in-go/pigpen-pig-play'], `^${JSON.parse(readFileSync(join(root, 'components/pig-play/package.json'), 'utf8')).version}`);
    assert.equal(manifest.files.includes('libs'), false);
    assert.equal(manifest.name, '@pi-in-go/pigpen-pig-runner');
  });
  it('gives two Packages that share a library the same library directory (PiG fuses a Go module from one directory only)', () => {
    // Installed side by side under node_modules/@pi-in-go, each Package's go.work must name the one directory of the library.
    const installed = (dir, workFile) => {
      const tree = buildPackageTree(dir, out);
      const nodeModules = '/agent/npm/node_modules/@pi-in-go';
      const use = [...readFileSync(join(tree, workFile), 'utf8').matchAll(/^\s*(\.\.\/\S*pig-play)\s*$/gm)].map((m) => m[1]);
      assert.equal(use.length, 1, `${dir} go.work names the library once`);
      return resolve(nodeModules, `pigpen-${dir}`, dirname(workFile), use[0]);
    };
    const runner = installed('pig-runner', 'extensions/pigrunner/go.work');
    assert.equal(runner, '/agent/npm/node_modules/@pi-in-go/pigpen-pig-play');
    assert.equal(installed('angry-pigs', 'extensions/angrypigs/go.work'), runner);
  });
  it('leaves a Package without shared libraries as it is', () => {
    const tree = buildPackageTree('herdr', out);
    assert.equal(existsSync(join(tree, 'libs')), false);
    assert.equal(JSON.parse(readFileSync(join(tree, 'package.json'), 'utf8')).dependencies, undefined);
    assert.equal(readFileSync(join(tree, 'extensions/herdr/go.mod'), 'utf8'), readFileSync(join(root, 'components/herdr/extensions/herdr/go.mod'), 'utf8'));
  });
  it('leaves nothing out that npm would need to build the extension', () => {
    const tree = buildPackageTree('ahp', out);
    const packed = JSON.parse(execFileSync('npm', ['pack', '--dry-run', '--json', '--ignore-scripts'], { cwd: tree, encoding: 'utf8' }))[0].files.map((f) => f.path);
    assert.deepEqual(packedProblems(tree, packed), []);
    assert.ok(packed.some((f) => f.endsWith('third_party/agent-host-protocol-go/go.mod')));
    assert.deepEqual(packedProblems(tree, packed.filter((f) => !f.endsWith('go.sum'))).length > 0, true, 'a missing go.sum is reported');
  });
  it('does not ask for the go.mod of a tree the Package does not ship, and still asks for the one of a tree it does', () => {
    const tree = buildPackageTree('kagent', out);
    const packed = JSON.parse(execFileSync('npm', ['pack', '--dry-run', '--json', '--ignore-scripts'], { cwd: tree, encoding: 'utf8' }))[0].files.map((f) => f.path);
    assert.ok(existsSync(join(tree, 'harness/adapter/go.mod')), 'the harness module is in the component directory');
    assert.equal(packed.some((f) => f.startsWith('harness/')), false, 'and npm does not pack it');
    assert.deepEqual(packedProblems(tree, packed), []);
    assert.ok(packedProblems(tree, packed.filter((f) => !f.endsWith('kagent-policy/go.mod'))).includes('extensions/kagent-policy/go.mod is not packed'));
  });
  describe('go.mod and go.sum of a module the Package builds with, not covered by a Resource entry', () => {
    // hardening has no extension (its `pi` is empty), so only the go.mod rule and the go.work `use` rule can report these.
    const packedOf = (tree) => JSON.parse(execFileSync('npm', ['pack', '--dry-run', '--json', '--ignore-scripts'], { cwd: tree, encoding: 'utf8' }))[0].files.map((f) => f.path);
    it('asks for the go.sum of a packed module', () => {
      const tree = buildPackageTree('hardening', out);
      const packed = packedOf(tree);
      assert.deepEqual(packedProblems(tree, packed), []);
      assert.deepEqual(packedProblems(tree, packed.filter((f) => f !== 'policy/cedar/go.sum')), ['policy/cedar/go.sum is not packed']);
      assert.deepEqual(packedProblems(tree, packed.filter((f) => f !== 'go.mod')), ['go.mod is not packed']);
    });
    it('asks for a module the packed go.work uses even when none of its files is packed', () => {
      const tree = buildPackageTree('hardening', out);
      const packed = packedOf(tree);
      assert.deepEqual(packedProblems(tree, packed.filter((f) => !f.startsWith('policy/cedar/'))), ['policy/cedar/go.mod is not packed', 'policy/cedar/go.sum is not packed']);
    });
    it('asks for the go.mod of a directory the Package ships, no go.work needed, and not for one it does not ship', () => {
      const tree = buildPackageTree('hardening', out);
      const packed = packedOf(tree);
      mkdirSync(join(tree, 'tools/side'), { recursive: true });
      writeFileSync(join(tree, 'tools/side/go.mod'), 'module side\n');
      assert.deepEqual(packedProblems(tree, packed), [], 'nothing of tools/side is packed: a choice');
      assert.deepEqual(packedProblems(tree, [...packed, 'tools/side/side.go']), ['tools/side/go.mod is not packed'], 'a file of it is packed: a defect');
    });
  });
});
