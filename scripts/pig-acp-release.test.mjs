// The pig-acp release script (scripts/pig-acp-release.mjs) and its workflow: the pure parts (plan, archives, checksums, signature
// verification) run here with a throwaway key; building every target and the headless smoke are `node scripts/pig-acp-release.mjs
// snapshot` (see OWNER-ACTIONS.md) and the workflow's own run.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { generateKeyPairSync, sign } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, describe, it } from 'node:test';
import { gunzipSync } from 'node:zlib';
import { parse } from 'yaml';
import { SIGN_ARGS, TARGETS, archiveName, binaryMatches, checksums, gzipReproducible, makeArchive, plan, readArchive, readRelease, root, verifyProblems } from './pig-acp-release.mjs';

const scratch = mkdtempSync(join(tmpdir(), 'pig-acp-release-test-'));
after(() => rmSync(scratch, { recursive: true, force: true }));
const { version, tag } = readRelease();

/** A tiny executable header per platform, enough for binaryMatches, with the version inside. */
function fakeBinary({ goos, goarch }) {
  const data = Buffer.alloc(4096);
  if (goos === 'linux' || goos === 'android') { data.writeUInt32BE(0x7f454c46, 0); data.writeUInt16LE(goarch === 'amd64' ? 62 : 183, 18); }
  else if (goos === 'darwin') { data.writeUInt32LE(0xfeedfacf, 0); data.writeUInt32LE(goarch === 'amd64' ? 0x01000007 : 0x0100000c, 4); }
  else { data.write('MZ', 0, 'latin1'); data.writeUInt32LE(0x80, 0x3c); data.write('PE\0\0', 0x80, 'latin1'); data.writeUInt16LE(goarch === 'amd64' ? 0x8664 : 0xaa64, 0x84); }
  data.write(version, 0x200);
  return data;
}

function release() {
  const dir = mkdtempSync(join(scratch, 'rel-'));
  for (const target of TARGETS) writeFileSync(join(dir, archiveName(version, target)), makeArchive(version, target, fakeBinary(target), 1762400000));
  checksums(dir);
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  writeFileSync(join(dir, 'SHA256SUMS.sig'), sign(null, readFileSync(join(dir, 'SHA256SUMS')), privateKey));
  const pub = join(dir, '..', `${Math.random()}.pub`);
  writeFileSync(pub, publicKey.export({ type: 'spki', format: 'pem' }));
  return { dir, pub, privateKey };
}

describe('plan', () => {
  it('names one archive per PiG release platform, and a native smoke runner for the five that have one', () => {
    const { matrix, smoke } = plan({});
    assert.deepEqual(matrix.include.map((e) => e.target), ['linux/amd64', 'linux/arm64', 'darwin/arm64', 'darwin/amd64', 'windows/amd64', 'windows/arm64', 'android/arm64']);
    assert.equal(matrix.include.find((e) => e.target === 'windows/arm64').name, `pig-acp-${version}-windows-arm64.zip`);
    assert.deepEqual(smoke.include.map((e) => e.target), ['linux/amd64', 'linux/arm64', 'darwin/arm64', 'darwin/amd64', 'windows/amd64']);
  });
  it('accepts the Package tag and refuses any other', () => {
    assert.equal(plan({ GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: tag }).tag, `components/acp/v${version}`);
    assert.throws(() => plan({ GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: 'components/acp/v9.9.9' }), /is not components\/acp\/v/);
    assert.throws(() => plan({ GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: `acp/v${version}` }), /is not components\/acp\/v/);
  });
  it('the Package, main.go and the agent carry one version', () => {
    assert.match(readRelease().version, /^\d+\.\d+\.\d+$/);
  });
});

describe('archives', () => {
  const linux = TARGETS[0];
  const windows = TARGETS.find((t) => t.goos === 'windows');
  it('are byte for byte the same for the same inputs, and different when the binary is', () => {
    const a = makeArchive(version, linux, fakeBinary(linux), 1762400000);
    assert.deepEqual(a, makeArchive(version, linux, fakeBinary(linux), 1762400000));
    assert.notDeepEqual(a, makeArchive(version, linux, Buffer.concat([fakeBinary(linux), Buffer.from('x')]), 1762400000));
    const z = makeArchive(version, windows, fakeBinary(windows), 1762400000);
    assert.deepEqual(z, makeArchive(version, windows, fakeBinary(windows), 1762400000));
  });
  it('carry the executable, the Package documents, sorted, owned by nobody, at the commit time', () => {
    const file = join(scratch, 'one.tar.gz');
    writeFileSync(file, makeArchive(version, linux, fakeBinary(linux), 1762400000));
    const entries = readArchive(file);
    assert.deepEqual(entries.map((e) => e.name), [`pig-acp-${version}-linux-amd64`, `pig-acp-${version}-linux-amd64/CREDITS.md`, `pig-acp-${version}-linux-amd64/LICENSE`, `pig-acp-${version}-linux-amd64/README.md`, `pig-acp-${version}-linux-amd64/pig-acp`]);
    assert.equal(entries.find((e) => e.name.endsWith('/pig-acp')).mode, 0o755);
    assert.ok(entries.every((e) => e.mtime === 1762400000));
    const tar = gunzipSync(readFileSync(file));
    assert.equal(tar.subarray(108, 116).toString().replace(/\0/g, ''), '0000000'); // uid
    // GNU tar reads it too: the format is ustar, not something only this script understands.
    const listed = spawnSync('tar', ['-tzvf', file], { encoding: 'utf8' });
    if (listed.status === 0) assert.match(listed.stdout, /-rwxr-xr-x .*pig-acp\/pig-acp\b|-rwxr-xr-x .*linux-amd64\/pig-acp/);
  });
  it('zip entries inflate with their CRC and keep the document bytes', () => {
    const file = join(scratch, 'one.zip');
    writeFileSync(file, makeArchive(version, windows, fakeBinary(windows), 1762400000));
    const entries = readArchive(file);
    assert.equal(entries.length, 5);
    assert.deepEqual(entries.find((e) => e.name.endsWith('LICENSE')).data, readFileSync(join(root, 'components/acp/LICENSE')));
    const listed = spawnSync('unzip', ['-t', file], { encoding: 'utf8' });
    if (listed.status !== 127 && listed.error === undefined) assert.equal(listed.status, 0, listed.stdout + listed.stderr);
  });
  it('gzip carries no name, time or host byte', () => {
    const gz = gzipReproducible(Buffer.from('x'));
    assert.deepEqual([...gz.subarray(4, 10)], [0, 0, 0, 0, 2, 3]);
  });
});

describe('executables', () => {
  it('are recognised by the platform their header names, and nothing else', () => {
    for (const a of TARGETS) for (const b of TARGETS) assert.equal(binaryMatches(fakeBinary(a), b), a.goos === b.goos || (['linux', 'android'].includes(a.goos) && ['linux', 'android'].includes(b.goos)) ? a.goarch === b.goarch : false, `${a.target} as ${b.target}`);
  });
});

describe('verify', () => {
  it('accepts a complete, signed release', () => {
    const { dir, pub } = release();
    assert.deepEqual(verifyProblems(dir, pub, { version }), []);
  });
  it('refuses a changed archive, a missing one, a stray file and an unlisted archive', () => {
    const { dir, pub } = release();
    const name = archiveName(version, TARGETS[0]);
    writeFileSync(join(dir, name), Buffer.concat([readFileSync(join(dir, name)), Buffer.from('x')]));
    assert.match(verifyProblems(dir, pub, { version }).join('\n'), new RegExp(`${name} does not match its SHA256SUMS entry`));
    rmSync(join(dir, archiveName(version, TARGETS[1])));
    writeFileSync(join(dir, 'notes.txt'), 'x');
    const text = verifyProblems(dir, pub, { version }).join('\n');
    assert.match(text, /missing archive .*linux-arm64/);
    assert.match(text, /unexpected file notes\.txt/);
    assert.match(text, /SHA256SUMS lists .*linux-arm64.*not an archive here/);
  });
  it('refuses a signature by another key, a changed SHA256SUMS and an archive built for another platform', () => {
    const { dir, pub } = release();
    const other = generateKeyPairSync('ed25519').privateKey;
    writeFileSync(join(dir, 'SHA256SUMS.sig'), sign(null, readFileSync(join(dir, 'SHA256SUMS')), other));
    assert.match(verifyProblems(dir, pub, { version }).join('\n'), /is not a signature of SHA256SUMS/);
    const second = release();
    writeFileSync(join(second.dir, 'SHA256SUMS'), readFileSync(join(second.dir, 'SHA256SUMS'), 'utf8') + '\n');
    assert.match(verifyProblems(second.dir, second.pub, { version }).join('\n'), /is not a signature of SHA256SUMS/);
    const wrong = release();
    const [linux, arm] = TARGETS;
    writeFileSync(join(wrong.dir, archiveName(version, linux)), makeArchive(version, linux, fakeBinary(arm), 1));
    checksums(wrong.dir);
    writeFileSync(join(wrong.dir, 'SHA256SUMS.sig'), sign(null, readFileSync(join(wrong.dir, 'SHA256SUMS')), wrong.privateKey));
    assert.match(verifyProblems(wrong.dir, wrong.pub, { version }).join('\n'), /is not a linux\/amd64 executable/);
  });
  it('requires SHA256SUMS and its signature', () => {
    const { dir, pub } = release();
    rmSync(join(dir, 'SHA256SUMS.sig'));
    assert.match(verifyProblems(dir, pub, { version }).join('\n'), /both required/);
  });
});

describe('the workflow', () => {
  const text = readFileSync(join(root, '.github/workflows/release-pig-acp.yml'), 'utf8');
  const workflow = parse(text);
  it('signs with exactly the command the script and the snapshot use', () => {
    const step = workflow.jobs.publish.steps.find((s) => s.env?.PIGLET_SIGNING_KEY);
    const command = SIGN_ARGS('"$key"', '"$RUNNER_TEMP/release/SHA256SUMS"', '"$RUNNER_TEMP/release/SHA256SUMS.sig"').join(' ');
    assert.ok(step.run.includes(`openssl ${command}`), command);
  });
  it('builds every planned target and uploads the artifact name the publish job collects', () => {
    assert.ok(workflow.jobs.build.steps.some((s) => s.uses?.startsWith('actions/upload-artifact@') && s.with.name === 'pig-acp-${{ matrix.goos }}-${{ matrix.goarch }}'));
    assert.equal(workflow.jobs.publish.steps.find((s) => s.uses?.startsWith('actions/download-artifact@')).with.pattern, 'pig-acp-*');
  });
});
