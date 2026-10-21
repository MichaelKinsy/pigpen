// The herdr plugin's scripts (pig-support.sh), run against a fake `pig` that keeps its package list in a file and logs
// every call. Covers install, update of an outdated pin, uninstall, status and the startup refresh that herdr-pig's plugin
// has (`refresh` keeps an installed copy in step with the plugin and never installs on its own). pig-support.ps1 is the
// same logic for Windows; the tests keep its pin and its commands equal to the shell script's.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { afterEach, beforeEach, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';

const root = resolve(fileURLToPath(new URL('.', import.meta.url)), '../../..');
const plugin = join(root, 'piglets/herdr/herdr-plugin');
const sh = join(plugin, 'scripts/pig-support.sh');
const ps1 = join(plugin, 'scripts/pig-support.ps1');
const manifest = readFileSync(join(plugin, 'herdr-plugin.toml'), 'utf8');
const version = JSON.parse(readFileSync(join(root, 'components/herdr/package.json'), 'utf8')).version;
const NAME = 'npm:@pi-in-go/pigpen-herdr';

let dir;
beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), 'herdr-plugin-'));
  mkdirSync(join(dir, 'bin'));
  writeFileSync(join(dir, 'state'), '');
  writeFileSync(join(dir, 'calls'), '');
  writeFileSync(join(dir, 'bin/pig'), `#!/bin/sh
echo "$*" >> "$FAKE_DIR/calls"
case "$1" in
list) cat "$FAKE_DIR/state" ;;
install) [ -n "$FAKE_INSTALL_FAILS" ] && exit 1; echo "$2" >> "$FAKE_DIR/state" ;;
remove) grep -vxF "$2" "$FAKE_DIR/state" > "$FAKE_DIR/state.new"; mv "$FAKE_DIR/state.new" "$FAKE_DIR/state" ;;
esac
`);
  chmodSync(join(dir, 'bin/pig'), 0o755);
});
afterEach(() => rmSync(dir, { recursive: true, force: true }));

const run = (command, env = {}) => spawnSync('/bin/sh', [sh, command], {
  encoding: 'utf8',
  env: { PATH: `${join(dir, 'bin')}:/usr/bin:/bin`, HOME: dir, FAKE_DIR: dir, ...env },
});
const installedList = () => readFileSync(join(dir, 'state'), 'utf8').split('\n').filter(Boolean);
const calls = () => readFileSync(join(dir, 'calls'), 'utf8').split('\n').filter(Boolean);
const setInstalled = (...entries) => writeFileSync(join(dir, 'state'), entries.map((e) => `${e}\n`).join(''));
const mutating = () => calls().filter((c) => !c.startsWith('list'));

describe('pig-support.sh', () => {
  it('pins the Package version this repository holds, in both scripts', () => {
    assert.match(readFileSync(sh, 'utf8'), new RegExp(`^PACKAGE_VERSION="${version.replaceAll('.', '\\.')}"$`, 'm'));
    assert.match(readFileSync(ps1, 'utf8'), new RegExp(`^\\$PackageVersion = "${version.replaceAll('.', '\\.')}"$`, 'm'));
  });

  it('install adds the pinned Package once', () => {
    assert.equal(run('install').status, 0);
    assert.deepEqual(installedList(), [`${NAME}@${version}`]);
    assert.equal(run('install').status, 0);
    assert.deepEqual(installedList(), [`${NAME}@${version}`]);
    assert.deepEqual(mutating(), [`install ${NAME}@${version}`]);
  });

  it('install updates an outdated pinned entry', () => {
    setInstalled(`${NAME}@0.0.1`);
    const result = run('install');
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(installedList(), [`${NAME}@${version}`]);
    assert.match(result.stdout, /updated PiG support/);
  });

  it('status says outdated, current or not installed', () => {
    assert.match(run('status').stdout, /not installed/);
    setInstalled(`${NAME}@0.0.1`);
    assert.match(run('status').stdout, /outdated/);
    setInstalled(`${NAME}@${version}`);
    assert.match(run('status').stdout, /^installed \(/);
  });

  it('uninstall removes the entry that is installed, whatever its version', () => {
    setInstalled(`${NAME}@0.0.1`);
    assert.equal(run('uninstall').status, 0);
    assert.deepEqual(installedList(), []);
    assert.match(run('uninstall').stdout, /not installed/);
  });

  it('refresh never installs on its own', () => {
    assert.equal(run('refresh').status, 0);
    assert.deepEqual(mutating(), []);
  });

  it('refresh replaces an outdated pinned entry by the pin', () => {
    setInstalled(`${NAME}@0.0.1`, 'npm:other@1.0.0');
    const result = run('refresh');
    assert.equal(result.status, 0);
    assert.deepEqual(installedList().sort(), [`${NAME}@${version}`, 'npm:other@1.0.0'].sort());
    assert.deepEqual(mutating(), [`remove ${NAME}@0.0.1`, `install ${NAME}@${version}`]);
    assert.match(result.stdout, /refreshed/);
  });

  it('refresh leaves a current install, an unpinned entry and another source alone', () => {
    for (const entry of [`${NAME}@${version}`, NAME, '/home/me/pigpen/components/herdr']) {
      setInstalled(entry);
      writeFileSync(join(dir, 'calls'), '');
      assert.equal(run('refresh').status, 0);
      assert.deepEqual(mutating(), [], entry);
    }
    setInstalled(`${NAME}@0.0.1`);
    writeFileSync(join(dir, 'calls'), '');
    assert.equal(run('refresh', { PIGPEN_HERDR_PACKAGE: '/some/checkout' }).status, 0);
    assert.deepEqual(mutating(), []);
  });

  it('refresh does not fail herdr when pig cannot install, and says so', () => {
    setInstalled(`${NAME}@0.0.1`);
    const result = run('refresh', { FAKE_INSTALL_FAILS: '1' });
    assert.equal(result.status, 0);
    assert.match(result.stderr, /could not refresh/);
  });

  it('refresh does nothing when pig is not on PATH, or with PIG_USE_PI_DIRS=1', () => {
    setInstalled(`${NAME}@0.0.1`);
    assert.equal(spawnSync('/bin/sh', [sh, 'refresh'], { encoding: 'utf8', env: { PATH: '/usr/bin:/bin', HOME: dir } }).status, 0);
    assert.equal(run('refresh', { PIG_USE_PI_DIRS: '1' }).status, 0);
    assert.deepEqual(mutating(), []);
  });

  it('install refuses PIG_USE_PI_DIRS=1', () => {
    const result = run('install', { PIG_USE_PI_DIRS: '1' });
    assert.equal(result.status, 1);
    assert.match(result.stderr, /PIG_USE_PI_DIRS=1/);
  });
});

describe('herdr-plugin.toml', () => {
  it('has a startup entry for every platform, like the original', () => {
    const startups = manifest.split('[[startup]]').slice(1).map((block) => block.split(/\n\[\[/)[0]);
    assert.equal(startups.length, 2);
    assert.ok(startups.some((b) => /scripts\/pig-support\.sh", "refresh"/.test(b) && /"linux", "macos"/.test(b)));
    assert.ok(startups.some((b) => /scripts\/pig-support\.ps1", "refresh"/.test(b) && /"windows"/.test(b)));
  });
});
