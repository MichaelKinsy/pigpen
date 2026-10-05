// fetch-pig downloads the pinned PiG release for a platform and refuses anything that does not match the pin.
// A local server stands in for GitHub; the archive holds a fake `pig` that prints a version.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { after, before, describe, it } from 'node:test';
import { fetchPig, readRequirement } from './fetch-pig.mjs';
import { hostTarget, targetOf } from './host-target.mjs';

const posix = process.platform !== 'win32';
const scratch = mkdtempSync(join(tmpdir(), 'fetch-pig-test-'));
const target = hostTarget().target;
let server;
let baseUrl;
const archives = new Map();

/** A release archive `pig-<version>-<os>-<arch>.tar.gz` whose pig prints `version`. */
function archiveWith(version, folder = 'pig-0.4.0-fake') {
  const root = join(scratch, `src-${archives.size}`);
  mkdirSync(join(root, folder), { recursive: true });
  const pig = join(root, folder, 'pig');
  writeFileSync(pig, `#!/bin/sh\nprintf '%s\\n' '${version}'\n`);
  chmodSync(pig, 0o755);
  const file = join(scratch, `${folder}-${archives.size}.tar.gz`);
  assert.equal(spawnSync('tar', ['-czf', file, '-C', root, folder]).status, 0);
  return { file, folder, bytes: readFileSync(file) };
}
const sha = (bytes) => createHash('sha256').update(bytes).digest('hex');
/** A requirement whose release pins `archive` for this host. */
function pinned(archive, over = {}) {
  return { pig: '0.4.0', release: { repository: 'x/y', tag: 'v0.4.0', commit: 'c', assets: { [target]: { file: `${archive.folder}.tar.gz`, sha256: sha(archive.bytes), ...over } } } };
}

before(async () => {
  server = createServer((req, res) => {
    const body = archives.get(req.url.slice(1));
    if (!body) { res.writeHead(404).end(); return; }
    res.writeHead(200).end(body);
  });
  await new Promise((done) => server.listen(0, '127.0.0.1', done));
  baseUrl = `http://127.0.0.1:${server.address().port}`;
});
after(() => { server.close(); rmSync(scratch, { recursive: true, force: true }); });

describe('the committed pin', () => {
  const requirement = readRequirement();
  it('names a release asset with a sha256 for every target the Piglet manifests build', () => {
    for (const t of ['linux/amd64', 'linux/arm64', 'darwin/arm64', 'darwin/amd64', 'windows/amd64']) {
      const asset = requirement.release.assets[t];
      assert.ok(asset, `no pinned asset for ${t}`);
      assert.match(asset.sha256, /^[0-9a-f]{64}$/);
      assert.ok(asset.file.startsWith(`pig-${requirement.pig}-${t.replace('/', '-')}.`), `${asset.file} is not the ${t} archive of ${requirement.pig}`);
    }
  });
  it('pins the release tag and source commit of the version it requires', () => {
    assert.equal(requirement.release.tag, `v${requirement.pig}`);
    assert.match(requirement.release.commit, /^[0-9a-f]{40}$/);
  });
});

describe('host-target', () => {
  it('names Go targets and rejects platforms PiG does not build for', () => {
    assert.deepEqual(targetOf('win32', 'x64'), { goos: 'windows', goarch: 'amd64', target: 'windows/amd64', exe: '.exe' });
    assert.equal(targetOf('darwin', 'arm64').target, 'darwin/arm64');
    assert.equal(targetOf('linux', 'arm64').exe, '');
    assert.equal(targetOf('freebsd', 'x64'), undefined);
    assert.equal(targetOf('linux', 'ia32'), undefined);
  });
});

describe('fetchPig', { skip: !posix && 'the fake release is a shell script' }, () => {
  it('unpacks a release whose checksum matches the pin and returns the pig inside', async () => {
    const archive = archiveWith('0.4.0+1.0.0');
    archives.set(`${archive.folder}.tar.gz`, archive.bytes);
    const out = join(scratch, 'ok');
    const exe = await fetchPig({ target, out, baseUrl, requirement: pinned(archive) });
    assert.equal(exe, join(out, archive.folder, 'pig'));
    assert.equal(spawnSync(exe, ['--version'], { encoding: 'utf8' }).stdout.trim(), '0.4.0+1.0.0');
    assert.deepEqual(readdirSync(out), [archive.folder], 'the archive itself is removed after unpacking');
  });

  it('refuses a download whose sha256 differs from the pin and unpacks nothing', async () => {
    const archive = archiveWith('0.4.0+1.0.0', 'pig-0.4.0-tampered');
    archives.set(`${archive.folder}.tar.gz`, archive.bytes);
    const out = join(scratch, 'tampered');
    await assert.rejects(fetchPig({ target, out, baseUrl, requirement: pinned(archive, { sha256: 'a'.repeat(64) }) }), /refusing to unpack/);
    assert.equal(existsSync(out), false);
  });

  it('refuses a target the pin has no asset for, naming what is pinned', async () => {
    await assert.rejects(fetchPig({ target: 'linux/riscv64', out: join(scratch, 'none'), baseUrl }), /no pinned PiG release asset for linux\/riscv64 \(pinned: .*linux\/amd64/);
  });

  it('reports a missing release asset with its URL and status', async () => {
    const archive = archiveWith('0.4.0+1.0.0', 'pig-0.4.0-missing');
    await assert.rejects(fetchPig({ target, out: join(scratch, 'missing'), baseUrl, requirement: pinned(archive) }), /pig-0\.4\.0-missing\.tar\.gz: HTTP 404/);
  });

  it('refuses a pig that reports a different version than the pin', async () => {
    const archive = archiveWith('0.3.0+0.87.1', 'pig-0.4.0-old');
    archives.set(`${archive.folder}.tar.gz`, archive.bytes);
    await assert.rejects(fetchPig({ target, out: join(scratch, 'old'), baseUrl, requirement: pinned(archive) }), /reports "0\.3\.0\+0\.87\.1", the pin is 0\.4\.0/);
  });

  it('refuses an archive without the pig executable', async () => {
    const root = join(scratch, 'empty-src', 'pig-0.4.0-empty');
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, 'LICENSE'), 'x');
    const file = join(scratch, 'pig-0.4.0-empty.tar.gz');
    assert.equal(spawnSync('tar', ['-czf', file, '-C', join(scratch, 'empty-src'), 'pig-0.4.0-empty']).status, 0);
    const bytes = readFileSync(file);
    archives.set('pig-0.4.0-empty.tar.gz', bytes);
    await assert.rejects(fetchPig({ target, out: join(scratch, 'empty'), baseUrl, requirement: pinned({ folder: 'pig-0.4.0-empty', bytes }) }), /has no pig-0\.4\.0-empty\/pig/);
  });
});
