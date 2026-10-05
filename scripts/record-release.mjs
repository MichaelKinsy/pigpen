// Record a finished release so the index can list it (OWNER-ACTIONS.md):
//   npm run record -- package <name> <version>                 the tag components/<name>/v<version> must exist in this clone
//   npm run record -- piglet <name> <version> [--file <path>]  the signed piglet-release.json of release <name>/v<version>
//                                                              (default: downloaded with `gh release download`)
// A receipt is written only after it verifies against a key pinned in release-keys/ (scripts/receipts.mjs). Then run
// `npm run generate`, review the diff of index.json, and commit.
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { recordPackage, root } from './packages.mjs';
import { readPinnedKeys, receiptPath, verifyReceipt } from './receipts.mjs';

const config = JSON.parse(readFileSync(join(root, 'catalog.config.json'), 'utf8'));

export function recordPackageRelease(name, version, { directory = root } = {}) {
  const record = recordPackage(name, version, { directory });
  const file = join(directory, 'releases', 'packages', `${name}.json`);
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, JSON.stringify(record, null, 2) + '\n');
  return file;
}

export function recordPigletReceipt(name, version, text, { directory = root, repository = config.repository } = {}) {
  const keys = readPinnedKeys(directory);
  if (!keys.size) throw new Error('release-keys/ holds no public key: commit the signing key\'s .pub first (OWNER-ACTIONS.md)');
  verifyReceipt(text, { keys, repository, name, version });
  const file = receiptPath(directory, name, version);
  if (existsSync(file) && readFileSync(file, 'utf8') !== text) throw new Error(`${relative(directory, file)} is already recorded with different bytes; a release is immutable`);
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, text);
  return file;
}

function main(argv) {
  const directory = process.env.RECORD_ROOT ?? root;
  const [kind, name, version, ...rest] = argv;
  const option = (flag) => (rest.includes(flag) ? rest[rest.indexOf(flag) + 1] : undefined);
  if (!['package', 'piglet'].includes(kind) || !name || !version) throw new Error('usage: record-release.mjs package <name> <version> | piglet <name> <version> [--file <path>] [--repo <owner/repo>]');
  let file;
  if (kind === 'package') file = recordPackageRelease(name, version, { directory });
  else {
    const repository = option('--repo') ?? config.repository;
    const source = option('--file');
    const text = source ? readFileSync(source, 'utf8')
      : execFileSync('gh', ['release', 'download', `${name}/v${version}`, '--repo', repository, '--pattern', 'piglet-release.json', '--output', '-'], { encoding: 'utf8' });
    file = recordPigletReceipt(name, version, text, { directory, repository });
  }
  console.log(`Recorded ${relative(directory, file)}. Next: npm run generate, review the diff of index.json, commit both.`);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main(process.argv.slice(2));
