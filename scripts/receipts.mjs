// Verifies a Piglet release receipt (the signed `piglet-release.json` that `pig piglet publish` uploads) against a public
// key pinned in this repository, and turns it into an index release. The receipt's own `signer.publicKey` is never
// trusted: it must equal a pinned key, and the DSSE signature must verify under that pinned key.
import { createHash, createPublicKey, verify } from 'node:crypto';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

/** A Piglet tag is `<name>/v<version>`: exactly one slash. A Package tag (`components/<name>/v<version>`) has two. */
export const pigletTagPattern = /^[a-z0-9][a-z0-9.-]*\/v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/;
export const PAYLOAD_TYPE = 'application/vnd.pig.piglet-release+json';
const NAME = /^[a-z0-9][a-z0-9.-]*$/;
const SEMVER = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/;
const TARGETS = new Set(['linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64', 'windows/amd64', 'windows/arm64']);
const B64 = /^[A-Za-z0-9+/]+={0,2}$/;

/** DSSE v1 pre-authentication encoding, as PiG's signature.PAE. */
export const pae = (type, payload) => Buffer.concat([Buffer.from(`DSSEv1 ${Buffer.byteLength(type)} ${type} ${payload.length} `), payload]);
export const keyIdOf = (raw) => 'ed25519:' + createHash('sha256').update(raw).digest('hex').slice(0, 32);

/** {key, raw, keyId} from the PKIX PEM `pig piglet keygen` writes next to the private key. */
export function parsePinnedKey(text) {
  if (!/^-----BEGIN PUBLIC KEY-----/.test(String(text).trim())) throw new Error('Not a PEM public key (expected the .pub file, never the private key)');
  let key;
  try { key = createPublicKey(text); } catch { throw new Error('Not a PEM public key'); }
  if (key.asymmetricKeyType !== 'ed25519') throw new Error('The pinned key must be Ed25519');
  const raw = key.export({ format: 'der', type: 'spki' }).subarray(-32);
  return { key, raw, keyId: keyIdOf(raw) };
}

/** Map(keyId -> {key, raw, keyId, file}) of every release-keys/*.pub; empty without the directory. */
export function readPinnedKeys(directory) {
  const dir = join(directory, 'release-keys');
  const keys = new Map();
  if (!existsSync(dir)) return keys;
  for (const file of readdirSync(dir).sort().filter((f) => f.endsWith('.pub'))) {
    const parsed = parsePinnedKey(readFileSync(join(dir, file), 'utf8'));
    keys.set(parsed.keyId, { ...parsed, file });
  }
  return keys;
}

function strict(label, value, keys) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`);
  const extra = Object.keys(value).filter((k) => !keys.includes(k));
  if (extra.length) throw new Error(`${label} has unexpected fields: ${extra.join(', ')}`);
  for (const k of keys) if (!(k in value)) throw new Error(`${label} is missing ${k}`);
}

const assetName = (name, target) => `pig-${name}-${target.replace('/', '-')}${target.startsWith('windows/') ? '.exe' : ''}`;
const downloadBase = (repository, name, version) => `https://github.com/${repository}/releases/download/${encodeURIComponent(`${name}/v${version}`)}/`;

/**
 * Verify `text` and return the payload plus the signing key. Throws on the first thing wrong.
 * `name`, `version` and `repository` come from the tag and the repository configuration, not from the receipt.
 */
export function verifyReceipt(text, { keys, repository, name, version }) {
  let envelope;
  try {
    envelope = JSON.parse(text);
  } catch (error) {
    throw new Error('Receipt is not one JSON document: ' + error.message);
  }
  strict('Receipt envelope', envelope, ['payloadType', 'payload', 'signatures']);
  if (envelope.payloadType !== PAYLOAD_TYPE) throw new Error(`Receipt has payload type ${envelope.payloadType}, not ${PAYLOAD_TYPE}`);
  if (!Array.isArray(envelope.signatures) || envelope.signatures.length !== 1) throw new Error('Receipt must carry exactly one signature');
  strict('Receipt signature', envelope.signatures[0], ['keyid', 'sig']);
  const { keyid, sig } = envelope.signatures[0];
  const pinned = keys.get(keyid);
  if (!pinned) throw new Error(`Receipt is signed by ${keyid}, which is not a pinned key (release-keys/)`);
  if (typeof envelope.payload !== 'string' || !B64.test(envelope.payload) || typeof sig !== 'string' || !B64.test(sig)) throw new Error('Receipt payload and signature must be base64');
  const bytes = Buffer.from(envelope.payload, 'base64');
  if (!verify(null, pae(PAYLOAD_TYPE, bytes), pinned.key, Buffer.from(sig, 'base64'))) {
    throw new Error(`Receipt signature by ${keyid} does not verify: the payload or signature was altered`);
  }
  const body = JSON.parse(bytes.toString('utf8'));
  strict('Receipt payload', body, ['piglet', 'version', 'pigVersion', 'sourceRef', 'github', 'signer', 'binaries']);
  strict('Receipt signer', body.signer, ['keyId', 'publicKey']);
  if (body.signer.keyId !== pinned.keyId) throw new Error(`Receipt names signer ${body.signer.keyId}, not the key ${pinned.keyId} that signed it`);
  if (body.signer.publicKey !== pinned.raw.toString('base64')) throw new Error('Receipt embeds a public key other than the pinned key');
  if (body.piglet !== name) throw new Error(`Receipt is for Piglet ${body.piglet}, not ${name}`);
  if (body.version !== version) throw new Error(`Receipt is for version ${body.version}, not ${version}`);
  if (!SEMVER.test(body.version)) throw new Error('Receipt version is not SemVer');
  for (const field of ['pigVersion', 'sourceRef']) if (typeof body[field] !== 'string' || !body[field] || body[field] !== body[field].trim()) throw new Error(`Receipt ${field} is empty`);
  strict('Receipt github', body.github, ['repository', 'tagPrefix']);
  if (body.github.repository !== repository) throw new Error(`Receipt is for repository ${body.github.repository}, not ${repository}`);
  if (body.github.tagPrefix !== `${name}/`) throw new Error(`Receipt tag prefix ${JSON.stringify(body.github.tagPrefix)} is not ${name}/`);
  const targets = Object.keys(body.binaries ?? {});
  if (!targets.length) throw new Error('Receipt lists no Binaries');
  const base = downloadBase(repository, name, version);
  for (const target of targets.sort()) {
    if (!TARGETS.has(target)) throw new Error(`Receipt target ${target} is not a supported target`);
    const binary = body.binaries[target];
    strict(`Receipt Binary ${target}`, binary, ['url', 'sha256', 'size']);
    // pig writes the bare asset name and resolves it against the index URL when pulling; the absolute form is the same place.
    const asset = assetName(name, target);
    if (binary.url !== asset && binary.url !== base + asset) throw new Error(`Receipt Binary ${target} URL ${binary.url} is not ${asset} (or ${base + asset})`);
    if (!/^[0-9a-f]{64}$/.test(binary.sha256)) throw new Error(`Receipt Binary ${target} sha256 must be 64 lowercase hex digits`);
    if (!Number.isSafeInteger(binary.size) || binary.size <= 0) throw new Error(`Receipt Binary ${target} size must be a positive integer`);
  }
  return { ...body, keyId: pinned.keyId, keyFile: pinned.file };
}

/** The index `release` object for a verified receipt. */
export function releaseFromReceipt(verified, { repository, keys, ref }) {
  const { piglet: name, version } = verified;
  return {
    version, signed: true,
    indexUrl: downloadBase(repository, name, version) + 'piglet-release.json',
    pullCommand: `pig piglet pull 'github:${repository}/${name}@${version}'`,
    platforms: Object.keys(verified.binaries).sort(),
    verificationKey: { id: verified.keyId, publicKeyUrl: `https://raw.githubusercontent.com/${repository}/${ref}/release-keys/${keys.get(verified.keyId).file}` },
  };
}

export function receiptPath(directory, name, version) {
  if (!NAME.test(String(name))) throw new Error('Invalid Piglet name: ' + name);
  if (!SEMVER.test(String(version))) throw new Error('Invalid version: ' + version);
  return join(directory, 'releases', 'piglets', name, `${version}.json`);
}

const compare = (a, b) => {
  const [x, y] = [a, b].map((v) => v.split('-')[0].split('.').map(Number));
  for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return y[i] - x[i];
  return (a.includes('-') ? 0 : 1) - (b.includes('-') ? 0 : 1) || a.localeCompare(b, 'en');
};

/** Map(name -> [verified receipt, newest version first]) of every committed releases/piglets/<name>/<version>.json. */
export function readReceipts(directory, { keys, repository }) {
  const dir = join(directory, 'releases', 'piglets');
  const result = new Map();
  if (!existsSync(dir)) return result;
  for (const name of readdirSync(dir).sort()) {
    const versions = [];
    for (const file of readdirSync(join(dir, name)).sort()) {
      const m = /^(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)\.json$/.exec(file);
      if (!m) throw new Error(`Unexpected file in releases/piglets/${name}: ${file}`);
      try {
        versions.push(verifyReceipt(readFileSync(join(dir, name, file), 'utf8'), { keys, repository, name, version: m[1] }));
      } catch (error) {
        throw new Error(`releases/piglets/${name}/${file}: ${error.message}`);
      }
    }
    if (versions.length) result.set(name, versions.sort((a, b) => compare(a.version, b.version)));
  }
  return result;
}
