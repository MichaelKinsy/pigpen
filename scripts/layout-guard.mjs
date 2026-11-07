#!/usr/bin/env node
// The shape of the repository: which entries may sit at the top, and which kinds of file may sit directly in a component. A stray entry
// fails `npm run quality` with the place it belongs in, so the tree does not drift back to a root full of notes and scratch files.
//
//   node scripts/layout-guard.mjs [DIR]
import { existsSync, lstatSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { listOwners } from './layout.mjs';

// What a checkout may hold at its top, besides untracked tooling output (.git, node_modules, dist, .pig-src, .pig-bin, .upstream).
export const TOP_DIRECTORIES = ['.github', 'bench', 'catalog', 'components', 'demo', 'docs', 'listings', 'piglets', 'ports', 'release-keys', 'releases', 'scripts'];
export const TOP_FILES = ['.gitignore', 'CHANGELOG.md', 'CONTRIBUTING.md', 'LICENSE', 'README.md', 'SECURITY.md', 'package-lock.json', 'package.json'];
const IGNORED = new Set(['.git', 'node_modules', 'dist', '.pig-src', '.pig-bin', '.upstream']);

// Directly inside components/<name>/ (and bench/components/<name>/): Package metadata, licence and credit texts, and for a Go library
// module its root Go files. Everything else goes in a directory.
const COMPONENT_FILES = /^(README\.md|LICEN[CS]E[\w.-]*|NOTICE[\w.-]*|CREDITS\.md|THIRD_PARTY_NOTICES\.md|CONTRACT[\w.-]*\.md|provenance\.json|package\.json|archon-plugin\.json|go\.(mod|sum|work|work\.sum)|[\w.-]+\.go|[\w.-]+\.(md|json))$/;
// The directories of a Package. A Go library module (a component with a root go.mod) may also hold its own package directories.
const COMPONENT_DIRECTORIES = new Set(['port', 'proof', 'extensions', 'skills', 'libraries', 'assets', 'internal', 'third_party', 'upstream', 'licenses', 'mutations', 'testdata', 'tests', 'test', 'src', 'scripts', 'fixtures', 'ui']);

const ARCHIVE = /\.(zip|tar|tgz|gz|bz2|xz|7z|rar)$/i;
const SCRIPT = /\.(sh|bash|py|mjs|cjs|js|ts)$/i;
const IMAGE = /\.(png|jpe?g|gif|svg|webp|ico)$/i;

/** Where a stray top-level entry belongs, as a sentence. */
function topPlace(name, isDirectory) {
  if (/^bench[-_]/.test(name)) return 'bench/ (the benchmark Piglets and components)';
  if (isDirectory) return 'components/ (a Package), piglets/ (a Piglet), docs/, scripts/ or bench/; a new top-level directory needs a change to scripts/layout-guard.mjs and an owner decision';
  if (/(^|[-_.])(schema|index|catalog)[\w.-]*\.json$/i.test(name) || /\.config\.json$/.test(name)) return 'catalog/ (the generated index, its schemas and the repository config) or ports/';
  if (ARCHIVE.test(name)) return 'nowhere: do not commit an archive, fetch or generate it';
  if (SCRIPT.test(name)) return 'scripts/';
  if (/\.patch$/.test(name)) return 'docs/maintainers/';
  if (/\.md$/.test(name)) return 'docs/ (an owner runbook or an open plan: docs/maintainers/; what a release contained: CHANGELOG.md)';
  if (/\.json$/.test(name)) return 'catalog/ or ports/ (data), or the directory of the component it describes';
  return 'docs/, scripts/ or the component it belongs to';
}

/** Where a stray file directly inside a component belongs, as a sentence. */
function componentPlace(name) {
  if (ARCHIVE.test(name)) return 'nowhere: do not commit an archive';
  if (SCRIPT.test(name)) return 'scripts/ (repository tooling) or port/ (the port\'s evidence) of the component, or the repository\'s scripts/';
  if (IMAGE.test(name)) return 'assets/ of the component';
  return 'port/ (the port\'s evidence), extensions/ (source) or docs in the README';
}

/** Every way `directory` departs from the layout, as sentences with the place each belongs in. Empty when it holds. */
export function layoutProblems(directory) {
  const problems = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (IGNORED.has(entry.name)) continue;
    const isDirectory = entry.isDirectory();
    const allowed = isDirectory ? TOP_DIRECTORIES.includes(entry.name) : TOP_FILES.includes(entry.name);
    if (!allowed) problems.push(`${entry.name}${isDirectory ? '/' : ''} is not allowed at the top of the repository; it belongs in ${topPlace(entry.name, isDirectory)}`);
  }
  for (const component of listOwners(directory, 'components')) {
    if (!component.isDirectory) { problems.push(`${component.rel} is a file; components/ holds only directories, one per Package`); continue; }
    const library = existsSync(join(component.path, 'go.mod'));
    for (const entry of readdirSync(component.path, { withFileTypes: true })) {
      if (lstatSync(join(component.path, entry.name)).isSymbolicLink()) { problems.push(`${component.rel}/${entry.name} is a symlink; symlinks are not distributable source`); continue; }
      if (entry.isDirectory()) {
        if (!library && !COMPONENT_DIRECTORIES.has(entry.name)) problems.push(`${component.rel}/${entry.name}/ is not a directory of a Package; it belongs in extensions/, skills/, libraries/, assets/ or port/ of ${component.name}`);
      } else if (!COMPONENT_FILES.test(entry.name)) problems.push(`${component.rel}/${entry.name} is not a file a Package keeps at its root; it belongs in ${componentPlace(entry.name)}`);
    }
  }
  return problems;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const directory = resolve(process.argv[2] || fileURLToPath(new URL('../', import.meta.url)));
  const problems = layoutProblems(directory);
  if (problems.length) {
    console.error(problems.join('\n'));
    process.exitCode = 1;
  } else console.log('Layout guard passed');
}
