// Deterministic, generated view of authored Piglet manifests. Never hand-edit index.json.
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, writeFileSync, lstatSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseDocument } from 'yaml';
import Ajv from 'ajv';
import addFormats from 'ajv-formats';

export const root = fileURLToPath(new URL('../', import.meta.url));
const config = JSON.parse(readFileSync(join(root, 'catalog.config.json')));
const ajv = new Ajv({ allErrors: true, strictRequired: false });
addFormats(ajv);
const validIndex = ajv.compile(JSON.parse(readFileSync(join(root, 'index.schema.json'))));
const validMetadata = ajv.compile({ type: 'object', additionalProperties: false,
  required: ['status', 'featured', 'updatedAt', 'languages', 'notes'], properties: {
    // Available entries are deliberately blocked until the release contract lands.
    status: { const: 'planned' }, featured: { type: 'boolean' }, updatedAt: { type: 'string', format: 'date' },
    languages: { type: 'array', uniqueItems: true, items: { type: 'string', minLength: 1 } },
    notes: { type: 'array', items: { type: 'string', minLength: 1 } },
  } });

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

export function generateIndex(directory = root, repository = config.repository) {
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository)) throw new Error('Invalid repository');
  const manifests = readManifests(directory);
  const index = { schemaVersion: 1, updatedAt: manifests.map(m => m.metadata.updatedAt).sort().at(-1),
    entries: manifests.map(({ manifest, metadata, raw }) => ({
      id: manifest.name, kind: 'piglet', name: manifest.name, description: manifest.description,
      maintainer: repository.split('/')[0], official: true, featured: metadata.featured,
      status: metadata.status, languages: metadata.languages, repository: 'https://github.com/' + repository,
      source: { type: 'git', spec: 'git:https://github.com/' + repository + '.git',
        url: `https://github.com/${repository}/tree/${config.ref}/piglets/${manifest.name}` },
      manifestSha256: createHash('sha256').update(raw).digest('hex'),
      releases: [], notes: metadata.notes,
    })) };
  if (!validIndex(index)) throw new Error(ajv.errorsText(validIndex.errors));
  return index;
}

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
