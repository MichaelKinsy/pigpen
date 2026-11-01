// Archon plugins are the one component kind that is not a PiG Package (scripts/archon-plugins.mjs): each script that walks
// components/ must tell the two apart and apply the right rules, and no directory may be skipped.
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, before, describe, it } from 'node:test';
import { checkDescriptor, checkPluginTag, componentKind, pluginVersion, readArchonPlugins, validatePlugin } from './archon-plugins.mjs';
import { readPackages, root } from './packages.mjs';
import { reservedNames } from './generate-index.mjs';

const good = { schemaVersion: 1, kind: 'provider', name: 'provider-x', description: 'Runs X.', executable: 'archon-provider-x' };
let dir;
before(() => {
  dir = mkdtempSync(join(tmpdir(), 'archon-plugins-'));
  const plugin = join(dir, 'components', 'archon-provider-x');
  mkdirSync(join(plugin, 'src'), { recursive: true });
  writeFileSync(join(plugin, 'archon-plugin.json'), JSON.stringify(good));
  writeFileSync(join(plugin, 'src', 'main.ts'), "const VERSION = '1.2.3';\n");
  mkdirSync(join(dir, 'components', 'a-package'));
  writeFileSync(join(dir, 'components', 'a-package', 'package.json'), '{"name":"a","version":"1.0.0"}');
  mkdirSync(join(dir, 'components', 'neither'));
});
after(() => rmSync(dir, { recursive: true, force: true }));

describe('component kinds', () => {
  it('tells a Package from an Archon plugin and refuses a directory that is neither or both', () => {
    assert.equal(componentKind(join(dir, 'components', 'a-package')), 'package');
    assert.equal(componentKind(join(dir, 'components', 'archon-provider-x')), 'archon-plugin');
    assert.throws(() => componentKind(join(dir, 'components', 'neither')), /neither package\.json/);
    writeFileSync(join(dir, 'components', 'archon-provider-x', 'package.json'), '{}');
    assert.throws(() => componentKind(join(dir, 'components', 'archon-provider-x')), /both/);
    rmSync(join(dir, 'components', 'archon-provider-x', 'package.json'));
  });
  it('lists plugins apart from Packages', () => {
    assert.deepEqual(readArchonPlugins(dir).map((p) => p.dir), ['archon-provider-x']);
    assert.deepEqual(readPackages(dir).map((p) => p.dir), ['a-package']);
  });
  it('every component of this repository is exactly one kind', () => {
    const plugins = readArchonPlugins(root).map((p) => p.dir);
    assert.ok(plugins.includes('archon-provider-pig'));
    assert.equal(plugins.some((name) => readPackages(root).some((p) => p.dir === name)), false);
  });
  it('reserves a plugin name against third-party listings', () => {
    assert.ok(reservedNames(root).has('archon-provider-pig'));
  });
});

describe('descriptor rules', () => {
  it('accepts the shipped descriptor shape', () => checkDescriptor(good));
  it('rejects each broken field', () => {
    for (const [patch, message] of [
      [{ schemaVersion: 2 }, /schemaVersion/], [{ kind: 'agent' }, /kind/], [{ name: 'Bad Name' }, /name/], [{ description: ' ' }, /description/],
      [{ executable: '../x' }, /executable/], [{ executable: undefined }, /executable/], [{ extra: 1 }, /unknown field extra/],
    ]) assert.throws(() => checkDescriptor({ ...good, ...patch }), message);
    assert.throws(() => checkDescriptor([]), /object/);
  });
});

describe('plugin tags', () => {
  it('reads the version the plugin answers and checks the tag against it', () => {
    const plugin = join(dir, 'components', 'archon-provider-x');
    assert.equal(pluginVersion(plugin), '1.2.3');
    assert.deepEqual(checkPluginTag('components/archon-provider-x/v1.2.3', { directory: dir }), { name: 'archon-provider-x', version: '1.2.3' });
    assert.throws(() => checkPluginTag('components/archon-provider-x/v1.2.4', { directory: dir }), /answers version 1\.2\.3/);
    assert.throws(() => checkPluginTag('components/a-package/v1.0.0', { directory: dir }), /no Archon plugin/);
    assert.throws(() => checkPluginTag('archon-provider-x/v1.2.3', { directory: dir }), /not a component tag/);
  });
  it('the shipped plugin answers the version its README installs', () => {
    assert.ok(checkPluginTag('components/archon-provider-pig/v0.1.0'));
  });
});

describe('validatePlugin', () => {
  const plugin = () => join(dir, 'components', 'archon-provider-x');
  it('checks the descriptor, then runs bun test in the plugin', () => {
    const calls = [];
    validatePlugin(plugin(), { run: (...call) => { calls.push(call); return { status: 0 }; } });
    assert.equal(calls.length, 1);
    assert.deepEqual(calls[0].slice(0, 2), ['bun', ['test']]);
    assert.equal(calls[0][2].cwd, plugin());
  });
  it('fails when the tests fail or bun is missing, never skipping', () => {
    assert.throws(() => validatePlugin(plugin(), { run: () => ({ status: 1 }) }), /bun test failed/);
    assert.throws(() => validatePlugin(plugin(), { run: () => ({ error: new Error('ENOENT') }) }), /bun is required/);
  });
  it('does not run the tests of a plugin with a broken descriptor', () => {
    writeFileSync(join(plugin(), 'archon-plugin.json'), JSON.stringify({ ...good, schemaVersion: 9 }));
    let ran = false;
    assert.throws(() => validatePlugin(plugin(), { run: () => { ran = true; return { status: 0 }; } }), /schemaVersion/);
    assert.equal(ran, false);
    writeFileSync(join(plugin(), 'archon-plugin.json'), JSON.stringify(good));
  });
});
