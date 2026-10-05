// A Piglet release receipt is what pig piglet publish signs: a DSSE envelope around the release index. The catalog
// trusts it only when the pinned public key verifies it and every identity matches the tag and the repository.
// Throwaway keys are generated here; nothing reads a real key.
import assert from 'node:assert/strict';
import { createHash, generateKeyPairSync } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it, before, after } from 'node:test';
import { repository, pair, rawOf, pem, digest, payload, receipt } from './test-receipts.mjs';
import { keyIdOf, parsePinnedKey, readPinnedKeys, verifyReceipt, releaseFromReceipt, receiptPath, readReceipts } from './receipts.mjs';

const check = (text, keys, over = {}) => verifyReceipt(text, { keys, repository, name: 'herdr', version: '0.1.0', ...over });

describe('pinned keys', () => {
  it('names a key the way pig does: ed25519: and the first 16 bytes of the SHA-256 of the raw key', () => {
    const { publicKey } = pair();
    const raw = rawOf(publicKey);
    assert.equal(keyIdOf(raw), 'ed25519:' + createHash('sha256').update(raw).digest('hex').slice(0, 32));
    assert.match(keyIdOf(raw), /^ed25519:[0-9a-f]{32}$/);
  });
  it('reads the PEM that `pig piglet keygen` writes and refuses anything else', () => {
    const { publicKey, privateKey } = pair();
    assert.equal(parsePinnedKey(pem(publicKey)).keyId, keyIdOf(rawOf(publicKey)));
    assert.throws(() => parsePinnedKey(privateKey.export({ format: 'pem', type: 'pkcs8' })), /public key/);
    assert.throws(() => parsePinnedKey('not a key'), /public key/);
    assert.throws(() => parsePinnedKey(pem(generateKeyPairSync('ec', { namedCurve: 'P-256' }).publicKey)), /Ed25519/);
  });
  describe('in a directory', () => {
    let dir;
    before(() => { dir = mkdtempSync(join(tmpdir(), 'pigpen-keys-')); });
    after(() => rmSync(dir, { recursive: true, force: true }));
    it('reads every .pub in release-keys and none when the directory is absent', () => {
      assert.equal(readPinnedKeys(dir).size, 0);
      mkdirSync(join(dir, 'release-keys'));
      const a = pair().publicKey;
      writeFileSync(join(dir, 'release-keys', 'a.pub'), pem(a));
      writeFileSync(join(dir, 'release-keys', 'README.md'), 'ignored');
      const keys = readPinnedKeys(dir);
      assert.deepEqual([...keys.keys()], [keyIdOf(rawOf(a))]);
      assert.equal(keys.get(keyIdOf(rawOf(a))).file, 'a.pub');
    });
  });
});

describe('verifying a receipt', () => {
  const author = pair();
  const keys = new Map([[keyIdOf(rawOf(author.publicKey)), { ...parsePinnedKey(pem(author.publicKey)), file: 'piglets.pub' }]]);

  it('accepts a receipt signed by the pinned key with matching identities', () => {
    const verified = check(receipt(author), keys);
    assert.equal(verified.piglet, 'herdr');
    assert.equal(verified.version, '0.1.0');
    assert.equal(verified.keyId, keyIdOf(rawOf(author.publicKey)));
    assert.deepEqual(Object.keys(verified.binaries), ['linux/amd64', 'windows/amd64']);
  });
  it('refuses a key that is not pinned, even when the receipt verifies against its own embedded key', () => {
    const stranger = pair();
    assert.throws(() => check(receipt(stranger), keys), /not a pinned key/);
  });
  it('refuses a payload whose signer fields name the pinned key but whose signature is from another', () => {
    const stranger = pair();
    const id = keyIdOf(rawOf(author.publicKey));
    const forged = receipt(stranger, payload({ signer: { keyId: id, publicKey: rawOf(author.publicKey).toString('base64') } }), { keyid: id });
    assert.throws(() => check(forged, keys), /does not verify/);
  });
  it('refuses a payload that embeds a different public key than the pinned one', () => {
    const other = pair();
    const text = receipt(author, payload({ signer: { keyId: keyIdOf(rawOf(author.publicKey)), publicKey: rawOf(other.publicKey).toString('base64') } }));
    assert.throws(() => check(text, keys), /embeds a public key/);
  });
  it('refuses altered bytes anywhere in the signed payload', () => {
    const envelope = JSON.parse(receipt(author));
    const body = JSON.parse(Buffer.from(envelope.payload, 'base64'));
    body.binaries['linux/amd64'].sha256 = digest('9');
    envelope.payload = Buffer.from(JSON.stringify(body)).toString('base64');
    assert.throws(() => check(JSON.stringify(envelope), keys), /does not verify/);
  });
  it('refuses a swapped signature or payload type', () => {
    const envelope = JSON.parse(receipt(author));
    assert.throws(() => check(JSON.stringify({ ...envelope, payloadType: 'application/json' }), keys), /payload type/);
    const other = JSON.parse(receipt(author, payload({ version: '0.2.0' }), {}));
    assert.throws(() => check(JSON.stringify({ ...envelope, signatures: other.signatures }), keys), /does not verify/);
  });
  it('refuses a receipt that is not exactly one envelope with one signature', () => {
    const envelope = JSON.parse(receipt(author));
    assert.throws(() => check('{', keys), /JSON/);
    assert.throws(() => check(JSON.stringify({ ...envelope, extra: 1 }), keys), /unexpected/);
    assert.throws(() => check(JSON.stringify({ ...envelope, signatures: [...envelope.signatures, ...envelope.signatures] }), keys), /exactly one signature/);
    assert.throws(() => check(JSON.stringify({ ...envelope, signatures: [] }), keys), /exactly one signature/);
    assert.throws(() => check(receipt(author) + '{}', keys), /JSON|trailing/);
  });
  it('requires the name, the version, the repository and the tag prefix to equal what the tag says', () => {
    const base = payload();
    const names = (over, message) => assert.throws(() => check(receipt(author, { ...base, ...over }), keys), message);
    names({ piglet: 'a2a' }, /Piglet a2a, not herdr/);
    names({ version: '0.1.1' }, /version 0\.1\.1, not 0\.1\.0/);
    names({ github: { repository: 'someone/else', tagPrefix: 'herdr/' } }, /repository someone\/else/);
    names({ github: { repository, tagPrefix: '' } }, /tag prefix/);
    names({ github: { repository, tagPrefix: 'a2a/' } }, /tag prefix/);
    names({ github: undefined }, /github/);
  });
  it('accepts the absolute URL of the same asset, which is what the bare name resolves to', () => {
    const url = 'https://github.com/MichaelKinsy/pigpen/releases/download/herdr%2Fv0.1.0/pig-herdr-linux-amd64';
    assert.ok(check(receipt(author, payload({ binaries: { 'linux/amd64': { url, sha256: digest('1'), size: 1 } } })), keys));
  });
  it('requires every Binary URL to be this repository, this tag and this Piglet', () => {
    const bad = (url) => payload({ binaries: { 'linux/amd64': { url, sha256: digest('1'), size: 1 } } });
    const attempts = [
      'https://github.com/someone/else/releases/download/herdr%2Fv0.1.0/pig-herdr-linux-amd64',
      'https://github.com/MichaelKinsy/pigpen/releases/download/herdr%2Fv0.1.1/pig-herdr-linux-amd64',
      'https://github.com/MichaelKinsy/pigpen/releases/download/herdr/v0.1.0/pig-herdr-linux-amd64',
      'https://github.com/MichaelKinsy/pigpen/releases/download/herdr%2Fv0.1.0/pig-a2a-linux-amd64',
      'https://github.com/MichaelKinsy/pigpen/releases/download/herdr%2Fv0.1.0/pig-herdr-linux-arm64',
      'https://evil.example/MichaelKinsy/pigpen/releases/download/herdr%2Fv0.1.0/pig-herdr-linux-amd64',
      'http://github.com/MichaelKinsy/pigpen/releases/download/herdr%2Fv0.1.0/pig-herdr-linux-amd64',
      'pig-a2a-linux-amd64', 'pig-herdr-linux-arm64', '../pig-herdr-linux-amd64', '/pig-herdr-linux-amd64', 'pig-herdr-linux-amd64?x=1', '//evil.example/pig-herdr-linux-amd64',
    ];
    for (const url of attempts) assert.throws(() => check(receipt(author, bad(url)), keys), /URL/, url);
    // the Windows asset carries .exe
    assert.throws(() => check(receipt(author, payload({ binaries: { 'windows/amd64': { url: 'pig-herdr-windows-amd64', sha256: digest('1'), size: 1 } } })), keys), /URL/);
  });
  it('requires well-formed targets, digests and sizes, and at least one Binary', () => {
    const one = (key, value) => payload({ binaries: { [key]: value } });
    const ok = { url: 'pig-herdr-linux-amd64', sha256: digest('1'), size: 1 };
    assert.throws(() => check(receipt(author, payload({ binaries: {} })), keys), /no Binaries/);
    assert.throws(() => check(receipt(author, one('linux/riscv64', ok)), keys), /target/);
    assert.throws(() => check(receipt(author, one('linux/amd64', { ...ok, sha256: 'AB'.repeat(32) })), keys), /sha256/);
    assert.throws(() => check(receipt(author, one('linux/amd64', { ...ok, sha256: 'ab' })), keys), /sha256/);
    assert.throws(() => check(receipt(author, one('linux/amd64', { ...ok, size: 0 })), keys), /size/);
    assert.throws(() => check(receipt(author, one('linux/amd64', { ...ok, size: 1.5 })), keys), /size/);
    assert.throws(() => check(receipt(author, one('linux/amd64', { ...ok, extra: 1 })), keys), /unexpected/);
  });
  it('refuses unknown payload fields, as pig does', () => {
    assert.throws(() => check(receipt(author, { ...payload(), surprise: true }), keys), /unexpected/);
  });
  it('turns a verified receipt into the index release', () => {
    const verified = check(receipt(author), keys);
    const release = releaseFromReceipt(verified, { repository, keys, ref: 'main' });
    assert.deepEqual(release, {
      version: '0.1.0', signed: true,
      indexUrl: 'https://github.com/MichaelKinsy/pigpen/releases/download/herdr%2Fv0.1.0/piglet-release.json',
      pullCommand: "pig piglet pull 'github:MichaelKinsy/pigpen/herdr@0.1.0'",
      platforms: ['linux/amd64', 'windows/amd64'],
      verificationKey: { id: keyIdOf(rawOf(author.publicKey)), publicKeyUrl: 'https://raw.githubusercontent.com/MichaelKinsy/pigpen/main/release-keys/piglets.pub' },
    });
  });
});

describe('a receipt written by pig itself', () => {
  const dir = new URL('./fixtures/pig-0.4.0-receipt/', import.meta.url);
  const text = readFileSync(new URL('piglet-release.json', dir), 'utf8');
  const pinned = parsePinnedKey(readFileSync(new URL('throwaway.pub', dir), 'utf8'));
  const keys = new Map([[pinned.keyId, { ...pinned, file: 'throwaway.pub' }]]);
  it('verifies against its key with the identities of its tag', () => {
    const verified = check(text, keys);
    assert.equal(verified.keyId, pinned.keyId);
    assert.deepEqual(Object.keys(verified.binaries), ['linux/amd64']);
  });
  it('does not verify with another key, another version or one altered byte', () => {
    assert.throws(() => check(text, new Map([[keyIdOf(rawOf(pair().publicKey)), keys.get(pinned.keyId)]])), /not a pinned key/);
    assert.throws(() => check(text, keys, { version: '0.1.1' }), /version 0\.1\.0, not 0\.1\.1/);
    const envelope = JSON.parse(text);
    const body = JSON.parse(Buffer.from(envelope.payload, 'base64'));
    body.binaries['linux/amd64'].size += 1;
    envelope.payload = Buffer.from(JSON.stringify(body)).toString('base64');
    assert.throws(() => check(JSON.stringify(envelope), keys), /does not verify/);
  });
});

describe('the key this repository pins', () => {
  const root = new URL('../', import.meta.url).pathname;
  it('is the Piglet signing key: one Ed25519 public key, nothing else in release-keys', () => {
    const keys = readPinnedKeys(root);
    assert.deepEqual([...keys.keys()], ['ed25519:fc4fbe0a1ba0f640a360bfd5a1c98874']);
    assert.equal(keys.get('ed25519:fc4fbe0a1ba0f640a360bfd5a1c98874').file, 'pigpen-piglets.pub');
    assert.doesNotMatch(readFileSync(new URL('../release-keys/pigpen-piglets.pub', import.meta.url), 'utf8'), /PRIVATE/);
  });
});

describe('committed receipts', () => {
  const author = pair();
  const keys = new Map([[keyIdOf(rawOf(author.publicKey)), { ...parsePinnedKey(pem(author.publicKey)), file: 'piglets.pub' }]]);
  let dir;
  before(() => { dir = mkdtempSync(join(tmpdir(), 'pigpen-receipts-')); });
  after(() => rmSync(dir, { recursive: true, force: true }));
  const put = (name, version, text) => {
    mkdirSync(join(dir, 'releases', 'piglets', name), { recursive: true });
    writeFileSync(receiptPath(dir, name, version), text);
  };
  it('lives at releases/piglets/<name>/<version>.json', () => {
    assert.equal(receiptPath('/r', 'herdr', '0.1.0'), '/r/releases/piglets/herdr/0.1.0.json');
    assert.throws(() => receiptPath('/r', '../x', '0.1.0'), /Invalid/);
    assert.throws(() => receiptPath('/r', 'herdr', '../0.1.0'), /Invalid/);
  });
  it('reads none when there are none, then every verified receipt newest first', () => {
    assert.deepEqual([...readReceipts(dir, { keys, repository })], []);
    put('herdr', '0.1.0', receipt(author));
    put('herdr', '0.10.0', receipt(author, payload({ version: '0.10.0', binaries: { 'linux/amd64': { url: 'pig-herdr-linux-amd64', sha256: digest('3'), size: 5 } } })));
    put('herdr', '0.2.0', receipt(author, payload({ version: '0.2.0', binaries: { 'linux/amd64': { url: 'https://github.com/MichaelKinsty/x', sha256: digest('3'), size: 5 } } })).replace(/^/, ''));
    assert.throws(() => readReceipts(dir, { keys, repository }), /0\.2\.0/);
    rmSync(receiptPath(dir, 'herdr', '0.2.0'));
    const all = readReceipts(dir, { keys, repository });
    assert.deepEqual([...all.keys()], ['herdr']);
    assert.deepEqual(all.get('herdr').map((r) => r.version), ['0.10.0', '0.1.0']);
  });
  it('checks the file name against the receipt, so a receipt cannot be filed under another version', () => {
    put('herdr', '0.3.0', receipt(author)); // signed for 0.1.0
    assert.throws(() => readReceipts(dir, { keys, repository }), /0\.3\.0.*version 0\.1\.0/s);
    rmSync(receiptPath(dir, 'herdr', '0.3.0'));
  });
  it('refuses a stray file in a Piglet directory', () => {
    writeFileSync(join(dir, 'releases', 'piglets', 'herdr', 'notes.txt'), 'x');
    assert.throws(() => readReceipts(dir, { keys, repository }), /notes\.txt/);
    rmSync(join(dir, 'releases', 'piglets', 'herdr', 'notes.txt'));
  });
});
