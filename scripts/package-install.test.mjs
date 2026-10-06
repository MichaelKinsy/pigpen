// Every component Package installs with the command the index will show, from a tag, with the real pig.
// The repository is a local bare clone of HEAD that git serves for https://github.com/<repo>.git (url.<file>.insteadOf),
// so the exact production spec is exercised without the network. Needs PIG_BIN and, for Go extensions, a Go toolchain.
// Uncommitted changes to a Package are not in the clone; commit first.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { requirePig } from './pig-bin.mjs';
import { packageSpec, packageTag, readPackages, root } from './packages.mjs';
import { installedRoot, isolatedEnv, run, smokePackage } from './package-smoke.mjs';

const pig = process.env.PIG_BIN && requirePig();
const repository = 'MichaelKinsy/pigpen';
const gitEnv = (scratch) => ({ ...process.env, GIT_CONFIG_GLOBAL: join(scratch, 'gitconfig'), GIT_CONFIG_SYSTEM: '/dev/null', GIT_TERMINAL_PROMPT: '0',
  GIT_AUTHOR_NAME: 'T', GIT_AUTHOR_EMAIL: 't@example.com', GIT_COMMITTER_NAME: 'T', GIT_COMMITTER_EMAIL: 't@example.com' });
const git = (scratch, cwd, ...args) => execFileSync('git', ['-c', 'commit.gpgsign=false', ...args], { cwd, encoding: 'utf8', env: gitEnv(scratch) }).trim();

test('every component Package installs from its tag with the real pig', { skip: !pig && 'set PIG_BIN', timeout: 1800000 }, async (t) => {
  const scratch = realpathSync(mkdtempSync(join(tmpdir(), 'pigpen-install-')));
  t.after(() => rmSync(scratch, { recursive: true, force: true }));
  const bare = join(scratch, 'pigpen.git');
  const work = join(scratch, 'work');
  mkdirSync(work);
  writeFileSync(join(scratch, 'gitconfig'), `[url "file://${bare}"]\n\tinsteadOf = https://github.com/${repository}.git\n[protocol "file"]\n\tallow = always\n`);
  git(scratch, scratch, 'clone', '-q', '--bare', root, bare);
  const env = (name) => isolatedEnv(scratch, name, gitEnv(scratch));
  const packages = readPackages();
  for (const { dir, manifest } of packages) git(scratch, bare, 'tag', '-f', packageTag(dir, manifest.version), 'HEAD');

  for (const { dir, manifest } of packages) {
    await t.test(`${dir}: ${packageSpec(repository, dir, manifest.version)}`, () => {
      smokePackage({ pig, env: env(dir), cwd: work, repository, name: dir, version: manifest.version });
    });
  }

  await t.test('two Packages of one checkout, then a newer tag replaces the first and update keeps a pinned tag', () => {
    const siblings = env('siblings');
    const one = packageSpec(repository, 'herdr', '0.1.0');
    const two = packageSpec(repository, 'jev', '0.1.0');
    run(pig, siblings, work, 'install', one);
    run(pig, siblings, work, 'install', two);
    const first = installedRoot(pig, siblings, work, one);
    assert.equal(installedRoot(pig, siblings, work, two).replace(/jev$/, 'herdr'), first, 'both Packages share one checkout');
    // Release herdr 0.2.0 in the "remote".
    const clone = join(scratch, 'clone');
    git(scratch, scratch, 'clone', '-q', bare, clone);
    const manifestPath = join(clone, 'components/herdr/package.json');
    writeFileSync(manifestPath, readFileSync(manifestPath, 'utf8').replace('"version": "0.1.0"', '"version": "0.2.0"'));
    git(scratch, clone, 'commit', '-qam', 'herdr 0.2.0');
    git(scratch, clone, 'push', '-q', 'origin', 'HEAD:refs/heads/main');
    git(scratch, clone, 'tag', packageTag('herdr', '0.2.0'));
    git(scratch, clone, 'push', '-q', 'origin', packageTag('herdr', '0.2.0'));
    // A pinned tag is not moved by `pig update`.
    run(pig, siblings, work, 'update', '--extensions');
    assert.equal(JSON.parse(readFileSync(join(first, 'package.json'), 'utf8')).version, '0.1.0');
    // Installing the new tag replaces the old source of the same Package.
    const next = packageSpec(repository, 'herdr', '0.2.0');
    run(pig, siblings, work, 'install', next);
    const listed = run(pig, siblings, work, 'package', 'list');
    assert.ok(listed.includes(next), listed);
    assert.ok(!listed.includes(one), listed);
    assert.ok(listed.includes(two), listed);
    assert.equal(JSON.parse(readFileSync(join(installedRoot(pig, siblings, work, next), 'package.json'), 'utf8')).version, '0.2.0');
  });
});
