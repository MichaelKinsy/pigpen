// Optional selections of a Piglet (piglets/<name>/optional.json): off in the default stage, on with --with or PIGPEN_WITH.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, describe, it } from 'node:test';
import { parse } from 'yaml';
import { root } from './go-modules.mjs';

const scratch = [];
after(() => { for (const dir of scratch) rmSync(dir, { recursive: true, force: true }); });

const OPTIONAL = {
  extra: { description: 'optional extension', extensions: [{ name: 'extra-ext', origins: ['package:extra'], tools: ['extra_tool'] }] },
};

/** A repository of one Piglet that selects one Package by default and offers another extension of it optionally. */
function fixture() {
  const dir = mkdtempSync(join(tmpdir(), 'stage-optional-'));
  scratch.push(dir);
  writeFileSync(join(dir, 'LICENSE'), 'MIT\n');
  mkdirSync(join(dir, 'components/extra'), { recursive: true });
  writeFileSync(join(dir, 'components/extra/package.json'), '{"name":"extra"}\n');
  mkdirSync(join(dir, 'piglets/demo'), { recursive: true });
  writeFileSync(join(dir, 'piglets/demo/piglet.yaml'), 'name: demo\ndescription: demo\nrelease:\n  version: 0.1.0\npackages:\n  extra: local:../../components/extra\nextensions:\n  - name: base-ext\n    origins: [package:extra]\n');
  writeFileSync(join(dir, 'piglets/demo/catalog.json'), '{"featured":false,"updatedAt":"2026-10-20","languages":["go"],"notes":["demo"]}\n');
  writeFileSync(join(dir, 'piglets/demo/optional.json'), JSON.stringify(OPTIONAL));
  return dir;
}

function stage(dir, args = [], env = {}) {
  const result = spawnSync(process.execPath, [join(root, 'scripts/stage-piglets.mjs'), ...args, dir], { encoding: 'utf8', env: { ...process.env, PIGPEN_WITH: '', ...env } });
  const manifest = join(dir, 'dist/staged/piglets/demo/piglet.yaml');
  return { ...result, extensions: existsSync(manifest) ? parse(readFileSync(manifest, 'utf8')).extensions.map((e) => e.name) : undefined, text: existsSync(manifest) ? readFileSync(manifest, 'utf8') : '' };
}

describe('optional Piglet selections', () => {
  it('are off in the default stage', () => {
    const r = stage(fixture());
    assert.equal(r.status, 0, r.stderr);
    assert.deepEqual(r.extensions, ['base-ext']);
    assert.ok(!r.stdout.includes('optional:'));
  });

  it('are on with --with, before or after the root, with their tools', () => {
    const dir = fixture();
    const r = stage(dir, ['--with', 'demo:extra']);
    assert.equal(r.status, 0, r.stderr);
    assert.deepEqual(r.extensions, ['base-ext', 'extra-ext']);
    assert.deepEqual(parse(r.text).extensions[1], { name: 'extra-ext', origins: ['package:extra'], tools: ['extra_tool'] });
    assert.ok(r.stdout.includes('optional: demo:extra'));
  });

  it('are on with PIGPEN_WITH, and a default stage afterwards is off again', () => {
    const dir = fixture();
    assert.deepEqual(stage(dir, [], { PIGPEN_WITH: 'demo:extra' }).extensions, ['base-ext', 'extra-ext']);
    assert.deepEqual(stage(dir).extensions, ['base-ext']);
  });

  it('refuse an unknown selection, an unknown Piglet and a malformed switch', () => {
    const dir = fixture();
    let r = stage(dir, ['--with', 'demo:nope']);
    assert.notEqual(r.status, 0);
    assert.match(r.stderr, /no optional selection "nope" \(declared: extra\)/);
    r = stage(dir, ['--with', 'other:extra']);
    assert.notEqual(r.status, 0);
    assert.match(r.stderr, /no Piglet named other/);
    r = stage(dir, ['--with', 'extra']);
    assert.match(r.stderr, /--with needs <piglet>:<selection>/);
    r = stage(dir, ['--bogus']);
    assert.match(r.stderr, /unknown option --bogus/);
  });

  it('refuse a selection that duplicates a selected extension or names an undeclared Package', () => {
    const dir = fixture();
    writeFileSync(join(dir, 'piglets/demo/optional.json'), JSON.stringify({ extra: { description: 'x', extensions: [{ name: 'base-ext', origins: ['package:extra'] }] } }));
    assert.match(stage(dir, ['--with', 'demo:extra']).stderr, /already selects/);
    writeFileSync(join(dir, 'piglets/demo/optional.json'), JSON.stringify({ extra: { description: 'x', extensions: [{ name: 'new-ext', origins: ['package:missing'] }] } }));
    assert.match(stage(dir, ['--with', 'demo:extra']).stderr, /does not declare/);
  });
});

describe('pig-essentials optional superpowers extensions', () => {
  const optional = JSON.parse(readFileSync(join(root, 'piglets/pig-essentials/optional.json'), 'utf8'))['superpowers-extensions'];
  const authored = parse(readFileSync(join(root, 'piglets/pig-essentials/piglet.yaml'), 'utf8'));

  it('are declared, and not selected by default', () => {
    assert.deepEqual(optional.extensions.map((e) => e.name), ['superpowers', 'superpowers-tools']);
    for (const e of optional.extensions) assert.ok(!authored.extensions.some((a) => a.name === e.name), `${e.name} must not be in the authored manifest`);
    assert.equal(authored.packages.superpowers, 'local:../../components/superpowers');
  });

  it('name extensions and tools that the Package ships', () => {
    const pkg = JSON.parse(readFileSync(join(root, 'components/superpowers/package.json'), 'utf8'));
    for (const e of optional.extensions) {
      assert.ok(pkg.pi.extensions.includes(`extensions/${e.name}`), `${e.name} is in the Package's pi.extensions`);
      assert.ok(existsSync(join(root, 'components/superpowers/extensions', e.name, 'go.mod')));
      for (const tool of e.tools ?? []) assert.ok(readFileSync(join(root, 'components/superpowers/extensions', e.name, 'tools.go'), 'utf8').includes(`"${tool}"`), tool);
    }
  });

  it('stage on with --with pig-essentials:superpowers-extensions and off without', () => {
    const dir = mkdtempSync(join(tmpdir(), 'stage-essentials-'));
    scratch.push(dir);
    cpSync(root, dir, { recursive: true, filter: (src) => !/(^|[\\/])(\.git|node_modules|dist)([\\/]|$)/.test(src) && !/[\\/]components[\\/][^\\/]+[\\/](port|upstream)([\\/]|$)/.test(src) });
    const run = (args) => spawnSync(process.execPath, [join(root, 'scripts/stage-piglets.mjs'), ...args, dir], { encoding: 'utf8', env: { ...process.env, PIGPEN_WITH: '' } });
    const names = () => parse(readFileSync(join(dir, 'dist/staged/piglets/pig-essentials/piglet.yaml'), 'utf8')).extensions.map((e) => e.name);
    let r = run([]);
    assert.equal(r.status, 0, r.stderr);
    assert.ok(!names().includes('superpowers') && !names().includes('superpowers-tools'));
    r = run(['--with', 'pig-essentials:superpowers-extensions']);
    assert.equal(r.status, 0, r.stderr);
    assert.ok(names().includes('superpowers') && names().includes('superpowers-tools'));
    const staged = parse(readFileSync(join(dir, 'dist/staged/piglets/pig-essentials/piglet.yaml'), 'utf8'));
    assert.deepEqual(staged.extensions.find((e) => e.name === 'superpowers-tools').tools, ['brainstorm_server', 'render_skill_graphs']);
    assert.equal(staged.packages.superpowers, 'local:./packages/superpowers');
  });
});
