// Every Piglet's npm source, packed as published and registered the way a user does it: `pig piglet add npm:@pi-in-go/pigpen-piglet-<name>`
// from a local registry holding the Piglet sources and every Package, then `pig piglet validate` on the registered Piglet. With
// PIG_SOURCE_ROOT set (a PiG checkout at the pinned commit) pig-games, whose two Packages share a library, is also built into a
// fused Binary from the registered source. Needs PIG_BIN and npm; nothing is published.
import assert from 'node:assert/strict';
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readdirSync, realpathSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { readManifests } from './generate-index.mjs';
import { buildPackageTree } from './npm-packages.mjs';
import { buildPigletTree, pigletNpmName } from './npm-piglets.mjs';
import { isolatedEnv, run } from './package-smoke.mjs';
import { readPackages } from './packages.mjs';
import { requirePig } from './pig-bin.mjs';

const pig = process.env.PIG_BIN && requirePig();
const hostTarget = `${process.platform === 'win32' ? 'windows' : process.platform}/${process.arch === 'x64' ? 'amd64' : process.arch}`;

test('every Piglet registers from npm as published', { skip: !pig && 'set PIG_BIN', timeout: 1800000 }, async (t) => {
  const scratch = realpathSync(mkdtempSync(join(tmpdir(), 'pigpen-npm-piglets-')));
  t.after(() => rmSync(scratch, { recursive: true, force: true }));
  const tarballs = join(scratch, 'tarballs');
  mkdirSync(tarballs);
  const pack = (tree) => execFileSync('npm', ['pack', '--pack-destination', tarballs, '--ignore-scripts', '--silent'], { cwd: tree, stdio: 'ignore' });
  for (const { dir } of readPackages()) pack(buildPackageTree(dir, join(scratch, 'packages')));
  const piglets = readManifests().map(({ manifest }) => manifest.name);
  for (const name of piglets) pack(buildPigletTree(name, join(scratch, 'piglets')));
  const server = spawn(process.execPath, [fileURLToPath(new URL('./local-registry.mjs', import.meta.url)), ...readdirSync(tarballs).map((f) => join(tarballs, f))], { stdio: ['ignore', 'pipe', 'inherit'] });
  t.after(() => server.kill());
  const url = await new Promise((resolve, reject) => { server.stdout.once('data', (d) => resolve(String(d).trim())); server.once('exit', () => reject(new Error('registry exited'))); });
  const work = join(scratch, 'work');
  mkdirSync(work);
  const envFor = (name) => isolatedEnv(scratch, name, { NPM_CONFIG_REGISTRY: url, npm_config_registry: url, npm_config_audit: 'false', npm_config_fund: 'false' });

  for (const name of piglets) {
    await t.test(`pig piglet add npm:${pigletNpmName(name)}`, () => {
      const env = envFor(name);
      const added = run(pig, env, work, 'piglet', 'add', `npm:${pigletNpmName(name)}`);
      assert.match(added, new RegExp(`Added ${name} `));
      run(pig, env, work, 'piglet', 'validate', join(env.PIG_HOME, 'piglets', `${name}.yaml`));
      run(pig, env, work, 'piglet', 'remove', name, '--all');
      assert.doesNotMatch(run(pig, env, work, 'piglet', 'list'), new RegExp(`^${name}\\s`, 'm'));
    });
  }

  await t.test('a fused Binary builds from the registered pig-games source (two Packages share pig-play)', { skip: !process.env.PIG_SOURCE_ROOT && 'set PIG_SOURCE_ROOT' }, () => {
    const env = envFor('binary');
    run(pig, env, work, 'piglet', 'add', `npm:${pigletNpmName('pig-games')}`);
    const out = join(scratch, 'pig-games-binary');
    run(pig, env, work, 'piglet', 'build', 'pig-games', '--format', 'binary', '--builder', 'native', '--targets', hostTarget, '--out', out, '--no-input');
    assert.ok(existsSync(out));
    const version = spawnSync(out, ['--version'], { encoding: 'utf8', env });
    assert.equal(version.status, 0, version.stderr);
  });
});
