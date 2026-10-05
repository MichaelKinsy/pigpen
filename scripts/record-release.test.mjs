// `npm run record` turns a finished release into the committed record the index is generated from: a Package's tag, or a
// Piglet's signed receipt after it verifies against the pinned key. Nothing is written for something that does not verify.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it, beforeEach, afterEach } from 'node:test';
import { fileURLToPath } from 'node:url';
import { recordPigletReceipt, recordPackageRelease } from './record-release.mjs';
import { pair, pem, receipt, repository, payload, digest } from './test-receipts.mjs';

const git = (cwd, ...args) => execFileSync('git', ['-c', 'commit.gpgsign=false', '-c', 'user.name=T', '-c', 'user.email=t@example.com', ...args], { cwd, encoding: 'utf8', env: { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_SYSTEM: '/dev/null' } }).trim();
const script = fileURLToPath(new URL('./record-release.mjs', import.meta.url));

describe('record-release', () => {
  let dir;
  const author = pair();
  beforeEach(() => {
    dir = mkdtempSync(join(tmpdir(), 'pigpen-record-'));
    git(dir, 'init', '-q', '-b', 'main');
    mkdirSync(join(dir, 'components/herdr'), { recursive: true });
    mkdirSync(join(dir, 'release-keys'));
    writeFileSync(join(dir, 'components/herdr/package.json'), JSON.stringify({ name: 'pigpen-herdr', version: '0.1.0', description: 'd', license: 'MIT' }));
    writeFileSync(join(dir, 'release-keys/piglets.pub'), pem(author.publicKey));
    git(dir, 'add', '-A');
    git(dir, 'commit', '-q', '-m', 'one');
    git(dir, 'tag', 'components/herdr/v0.1.0');
  });
  afterEach(() => rmSync(dir, { recursive: true, force: true }));

  it('writes releases/packages/<name>.json for a tag that exists', () => {
    const file = recordPackageRelease('herdr', '0.1.0', { directory: dir });
    assert.equal(file, join(dir, 'releases/packages/herdr.json'));
    const record = JSON.parse(readFileSync(file, 'utf8'));
    assert.equal(record.tag, 'components/herdr/v0.1.0');
    assert.equal(record.commit, git(dir, 'rev-parse', 'HEAD'));
    assert.throws(() => recordPackageRelease('herdr', '0.9.0', { directory: dir }), /no tag/);
    assert.equal(existsSync(join(dir, 'releases/packages/herdr.json')), true);
  });

  it('writes releases/piglets/<name>/<version>.json for a receipt that verifies, byte for byte', () => {
    const text = receipt(author);
    const file = recordPigletReceipt('herdr', '0.1.0', text, { directory: dir, repository });
    assert.equal(file, join(dir, 'releases/piglets/herdr/0.1.0.json'));
    assert.equal(readFileSync(file, 'utf8'), text);
  });

  it('writes nothing for a receipt that does not verify, or that is already recorded with other bytes', () => {
    assert.throws(() => recordPigletReceipt('herdr', '0.1.0', receipt(pair()), { directory: dir, repository }), /not a pinned key/);
    assert.throws(() => recordPigletReceipt('herdr', '0.2.0', receipt(author), { directory: dir, repository }), /version 0\.1\.0, not 0\.2\.0/);
    assert.equal(existsSync(join(dir, 'releases')), false);
    const recorded = receipt(author);
    recordPigletReceipt('herdr', '0.1.0', recorded, { directory: dir, repository });
    // A different but valid receipt for the same release (another build): a release is immutable.
    const rebuilt = receipt(author, payload({ binaries: { 'linux/amd64': { url: 'pig-herdr-linux-amd64', sha256: digest('9'), size: 5 } } }));
    assert.throws(() => recordPigletReceipt('herdr', '0.1.0', rebuilt, { directory: dir, repository }), /already recorded with different bytes/);
    assert.equal(readFileSync(join(dir, 'releases/piglets/herdr/0.1.0.json'), 'utf8'), recorded, 'unchanged');
  });

  it('runs as `npm run record -- piglet <name> <version> --file <receipt>`', () => {
    const file = join(dir, 'receipt.json');
    writeFileSync(file, receipt(author));
    const out = execFileSync(process.execPath, [script, 'piglet', 'herdr', '0.1.0', '--file', file, '--repo', repository], { cwd: dir, encoding: 'utf8', env: { ...process.env, RECORD_ROOT: dir } });
    assert.match(out, /Recorded releases\/piglets\/herdr\/0\.1\.0\.json/);
    assert.match(out, /npm run generate/);
    assert.equal(existsSync(join(dir, 'releases/piglets/herdr/0.1.0.json')), true);
  });

  it('refuses unknown usage', () => {
    assert.throws(() => execFileSync(process.execPath, [script, 'nonsense'], { encoding: 'utf8', stdio: 'pipe', env: { ...process.env, RECORD_ROOT: dir } }), /usage/);
  });
});
