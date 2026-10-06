// Every Package, packed the way it is published and installed the way a user installs it: `pig install npm:@pi-in-go/pigpen-<dir>`
// from a local registry that serves the tarballs. Go extensions build and register, the Package validates, and it removes
// cleanly. Needs PIG_BIN and npm; nothing is published and no real registry is contacted.
import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import { mkdirSync, mkdtempSync, readdirSync, realpathSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { buildPackageTree, packedProblems } from './npm-packages.mjs';
import { isolatedEnv, smokePackage } from './package-smoke.mjs';
import { readPackages } from './packages.mjs';
import { requirePig } from './pig-bin.mjs';

const pig = process.env.PIG_BIN && requirePig();

test('every Package installs from npm as published', { skip: !pig && 'set PIG_BIN', timeout: 1800000 }, async (t) => {
  const scratch = realpathSync(mkdtempSync(join(tmpdir(), 'pigpen-npm-install-')));
  t.after(() => rmSync(scratch, { recursive: true, force: true }));
  const trees = join(scratch, 'trees');
  const tarballs = join(scratch, 'tarballs');
  mkdirSync(tarballs);
  const packages = readPackages();
  for (const { dir } of packages) {
    const tree = buildPackageTree(dir, trees);
    const dry = JSON.parse(execFileSync('npm', ['pack', '--dry-run', '--json', '--ignore-scripts'], { cwd: tree, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }))[0].files.map((f) => f.path);
    assert.deepEqual(packedProblems(tree, dry), [], dir);
    execFileSync('npm', ['pack', '--pack-destination', tarballs, '--ignore-scripts', '--silent'], { cwd: tree, stdio: 'ignore' });
  }
  // A separate process: the installs below use spawnSync, which would block a registry living in this one.
  const server = spawn(process.execPath, [fileURLToPath(new URL('./local-registry.mjs', import.meta.url)), ...readdirSync(tarballs).map((f) => join(tarballs, f))], { stdio: ['ignore', 'pipe', 'inherit'] });
  t.after(() => server.kill());
  const registry = { url: await new Promise((resolve, reject) => { server.stdout.once('data', (d) => resolve(String(d).trim())); server.once('exit', () => reject(new Error('registry exited'))); }) };
  const work = join(scratch, 'work');
  mkdirSync(work);
  for (const { dir, manifest } of packages) {
    await t.test(`${manifest.name}@${manifest.version}`, () => {
      const env = isolatedEnv(scratch, dir, { NPM_CONFIG_REGISTRY: registry.url, npm_config_registry: registry.url, npm_config_audit: 'false', npm_config_fund: 'false' });
      smokePackage({ pig, env, cwd: work, name: dir, version: manifest.version, spec: `npm:${manifest.name}` });
    });
  }
});
