// Deterministic, generated view of authored Piglet manifests. Never hand-edit index.json.
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, writeFileSync, lstatSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseDocument } from 'yaml';
import { packageEntry, readPackageRecords } from './packages.mjs';
import { readPinnedKeys, readReceipts, releaseFromReceipt } from './receipts.mjs';
import Ajv from 'ajv';
import addFormats from 'ajv-formats';

export const root = fileURLToPath(new URL('../', import.meta.url));
const config = JSON.parse(readFileSync(join(root, 'catalog.config.json')));
const ajv = new Ajv({ allErrors: true, strictRequired: false });
addFormats(ajv);
const validIndex = ajv.compile(JSON.parse(readFileSync(join(root, 'index.schema.json'))));
const validMetadata = ajv.compile({ type: 'object', additionalProperties: false,
  required: ['featured', 'updatedAt', 'languages', 'notes'], properties: {
    // No status here: a Piglet is `available` exactly when a verified release receipt is committed, else `planned`.
    featured: { type: 'boolean' }, updatedAt: { type: 'string', format: 'date' },
    languages: { type: 'array', uniqueItems: true, items: { type: 'string', minLength: 1 } },
    notes: { type: 'array', items: { type: 'string', minLength: 1 } },
  } });

const PLANNED_NOTE = 'Not released yet: no signed Piglet Binary is published and no remote install command is offered. The source composition is in this repository; see RELEASE-BLOCKERS.md.';

export function readManifests(directory = root) {
  return readdirSync(join(directory, 'piglets'), { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name, 'en')).map(dir => {
    if (!dir.isDirectory() || !/^[a-z0-9][a-z0-9.-]*$/.test(dir.name)) throw new Error('Invalid Piglet directory: ' + dir.name);
    const path = join(directory, 'piglets', dir.name, 'piglet.yaml');
    if (lstatSync(path).isSymbolicLink()) throw new Error('Piglet manifests must not be symlinks');
    const raw = readFileSync(path, 'utf8');
    const document = parseDocument(raw, { uniqueKeys: true });
    if (document.errors.length) throw document.errors[0];
    const manifest = document.toJS();
    if (manifest.name !== dir.name) throw new Error('Directory must match manifest.name');
    if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(manifest.release?.version)) throw new Error('Manifest must have release.version');
    const metadata = JSON.parse(readFileSync(join(directory, 'piglets', dir.name, 'catalog.json')));
    if (!validMetadata(metadata)) throw new Error(ajv.errorsText(validMetadata.errors));
    return { path, raw, manifest, metadata };
  });
}

export function validateIndex(index) {
  if (!validIndex(index)) throw new Error(ajv.errorsText(validIndex.errors));
}

export function generateIndex(directory = root, repository = config.repository) {
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository)) throw new Error('Invalid repository');
  const manifests = readManifests(directory);
  // A Package is listed once its release record is committed, and then only as `available`: the record is checked
  // against the git tag it names and the entry is built from the tagged tree (packages.mjs).
  // A Piglet is `available` exactly when a committed receipt verifies: signed by a key pinned in release-keys/, for this
  // repository, this Piglet, this version and its tag (receipts.mjs). The receipt's own public key is never trusted.
  const keys = readPinnedKeys(directory);
  const receipts = readReceipts(directory, { keys, repository });
  for (const name of receipts.keys()) if (!manifests.some(({ manifest }) => manifest.name === name)) throw new Error(`releases/piglets/${name}: there is no Piglet manifest piglets/${name}`);
  const released = [...readPackageRecords(directory)].map(([name, record]) => ({ record, entry: packageEntry(name, record, { directory, repository }) }));
  const index = { schemaVersion: 1, updatedAt: [...manifests.map(m => m.metadata.updatedAt), ...released.map(({ record }) => record.date)].sort().at(-1),
    entries: [...manifests.map(({ manifest, metadata, raw }) => pigletEntry({ manifest, metadata, raw }, receipts.get(manifest.name) ?? [], { repository, keys })), ...released.map(({ entry }) => entry)] };
  validateIndex(index);
  return index;
}

function pigletEntry({ manifest, metadata, raw }, receipts, { repository, keys }) {
  const releases = receipts.map((verified) => releaseFromReceipt(verified, { repository, keys, ref: config.ref }));
  const newest = releases[0];
  return {
      id: manifest.name, kind: 'piglet', name: manifest.name, description: manifest.description,
      maintainer: repository.split('/')[0], official: false, featured: metadata.featured,
      status: newest ? 'available' : 'planned', languages: metadata.languages, repository: 'https://github.com/' + repository,
      source: { type: 'git', spec: 'git:https://github.com/' + repository + '.git',
        url: `https://github.com/${repository}/tree/${config.ref}/piglets/${manifest.name}` },
      manifestSha256: createHash('sha256').update(raw).digest('hex'),
      releases, notes: [newest ? availableNote(newest) : PLANNED_NOTE, ...metadata.notes],
  };
}

const availableNote = (release) => `Signed Piglet Binaries for ${release.platforms.join(', ')} from release ${release.version}, signed with ${release.verificationKey.id}. Pull one with the command above, after checking that key against ${release.verificationKey.publicKeyUrl}. pig pins the signer on the first pull.`;

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const text = JSON.stringify(generateIndex(), null, 2) + '\n';
  const target = join(root, 'index.json');
  if (process.argv.includes('--check')) {
    if (readFileSync(target, 'utf8') !== text) throw new Error('index.json is stale; run npm run generate');
    console.log('Piglets index matches the manifests');
  } else {
    writeFileSync(target, text);
    console.log('Generated index.json from Piglet manifests');
  }
}
