// The npm form of a component Package. Packages are published to npm as `@pi-in-go/pigpen-<dir>` and found by pi-in-go.dev
// through the `pig-package` keyword (build-npm-catalog.mjs there reads the type words `extension`, `skill`, `prompt`,
// `theme` from the keywords). `components/<dir>/package.json` already holds the published manifest; this module is the
// contract it must meet (npm run check) and the one-time migration (`node scripts/npm-packages.mjs --write`).
//
// A Go extension reaches a shared library module (pig-play, typesafe, pi-typesafe-api) through a go.work that says
// `../../../<lib>`: a sibling of the Package. Installed from npm there is no sibling, so the published tree carries each
// library under `libs/<lib>` and its go.work points there. `buildPackageTree` generates that tree into dist/npm/packages/<dir>;
// nothing under components/ is edited for it.
import { execFileSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { packageResources, readPackages, root } from './packages.mjs';

export const SCOPE = '@pi-in-go';
export const npmName = (dir) => `${SCOPE}/pigpen-${dir}`;
const TYPE_WORDS = { extension: 'extension', skill: 'skill', prompt: 'prompt', theme: 'theme' };
const ORDER = ['name', 'version', 'description', 'keywords', 'license', 'author', 'repository', 'homepage', 'bugs', 'publishConfig', 'files'];
const ROOT_FILES = /^(go\.mod|go\.sum|LICENSE.*|NOTICE.*|CREDITS.*|CONTRACT.*|README.*|provenance\.json)$/;

const rootEntries = (dir, directory) => {
  const path = join(directory, 'components', dir);
  return existsSync(path) ? readdirSync(path, { withFileTypes: true }).filter((e) => e.isFile() && ROOT_FILES.test(e.name)).map((e) => e.name).sort() : [];
};

/** The manifest as published: scoped, public, findable, located in the repository. Idempotent. */
export function publishManifest(dir, manifest, { repository, directory = root }) {
  const { private: _private, ...rest } = manifest;
  const resources = packageResources(rest);
  const wanted = ['pig-package', 'pigpen', ...resources.map((r) => TYPE_WORDS[r]).filter(Boolean), ...(resources.length || rest.pi ? [] : ['library'])];
  const keywords = [...new Set([...(rest.keywords ?? []), ...wanted])];
  const files = [...(rest.files ?? [])];
  for (const name of rootEntries(dir, directory)) if (!files.includes(name)) files.push(name);
  const out = {
    ...rest, name: npmName(dir), keywords, files,
    repository: { type: 'git', url: `git+https://github.com/${repository}.git`, directory: `components/${dir}` },
    homepage: `https://github.com/${repository}/tree/main/components/${dir}#readme`,
    bugs: { url: `https://github.com/${repository}/issues` },
    publishConfig: { access: 'public' },
  };
  const ordered = {};
  for (const key of ORDER) if (key in out && out[key] !== undefined) ordered[key] = out[key];
  for (const key of Object.keys(out)) if (!(key in ordered) && out[key] !== undefined) ordered[key] = out[key];
  return ordered;
}

/** What differs between `manifest` and the published form, as sentences. */
export function manifestProblems(dir, manifest, { repository, directory = root }) {
  const want = publishManifest(dir, manifest, { repository, directory });
  const problems = [];
  const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
  if (manifest.private !== undefined) problems.push(`${dir}: "private" must be removed to publish`);
  if (manifest.name !== want.name) problems.push(`${dir}: name must be ${want.name}, not ${manifest.name}`);
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(String(manifest.version))) problems.push(`${dir}: version ${manifest.version} is not SemVer`);
  for (const word of want.keywords) if (!(manifest.keywords ?? []).includes(word)) problems.push(`${dir}: keywords must include ${word}`);
  for (const key of ['repository', 'homepage', 'bugs', 'publishConfig']) if (!same(manifest[key], want[key])) problems.push(`${dir}: ${key} must be ${JSON.stringify(want[key])}`);
  if (!Array.isArray(manifest.files)) problems.push(`${dir}: files is required`);
  else for (const file of want.files) if (!manifest.files.includes(file)) problems.push(`${dir}: files must include ${file}`);
  return problems;
}

/** Shared library Packages a component's go.work files `use` from outside the Package, sorted. */
export function libraryDeps(dir, directory = root) {
  const base = join(directory, 'components', dir);
  const libs = new Set();
  const walk = (path) => {
    for (const entry of readdirSync(path, { withFileTypes: true })) {
      if (['node_modules', '.git', 'port', 'testdata'].includes(entry.name)) continue;
      const full = join(path, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (entry.name === 'go.work') {
        for (const used of useEntries(readFileSync(full, 'utf8'))) {
          const target = relative(join(directory, 'components'), resolve(dirname(full), used)).split(sep);
          if (target[0] && target[0] !== '..' && target[0] !== dir) libs.add(target[0]);
        }
      }
    }
  };
  if (existsSync(base)) walk(base);
  return [...libs].sort();
}

/** The directories a go.work `use` names. */
function useEntries(text) {
  const out = [];
  const block = /^use\s*\(([\s\S]*?)^\)/gm;
  for (const m of text.matchAll(block)) for (const line of m[1].split('\n')) { const t = line.replace(/\/\/.*$/, '').trim(); if (t) out.push(t); }
  for (const m of text.matchAll(/^use\s+([^\s(][^\s]*)\s*$/gm)) out.push(m[1]);
  return out;
}

const packList = (cwd) => JSON.parse(execFileSync('npm', ['pack', '--dry-run', '--json', '--ignore-scripts'], { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }))[0].files.map((f) => f.path);

/**
 * Generate the tree npm packs for one component: the component as it is, the shared libraries under libs/, go.work pointing at
 * them and the published manifest. Returns the directory (outRoot/<dir>).
 */
export function buildPackageTree(dir, outRoot, { directory = root, repository = JSON.parse(readFileSync(join(directory, 'catalog.config.json'), 'utf8')).repository } = {}) {
  const source = join(directory, 'components', dir);
  const target = join(outRoot, dir);
  rmSync(target, { recursive: true, force: true });
  mkdirSync(dirname(target), { recursive: true });
  cpSync(source, target, { recursive: true, filter: (path) => !/(^|[\\/])(node_modules|\.git)([\\/]|$)/.test(path) });
  const libs = libraryDeps(dir, directory);
  for (const lib of libs) {
    const libSource = join(directory, 'components', lib);
    for (const file of packList(libSource)) {
      if (file.startsWith('port/')) continue;
      mkdirSync(dirname(join(target, 'libs', lib, file)), { recursive: true });
      cpSync(join(libSource, file), join(target, 'libs', lib, file));
    }
  }
  const rewrite = (path) => {
    for (const entry of readdirSync(path, { withFileTypes: true })) {
      const full = join(path, entry.name);
      if (entry.isDirectory()) { if (!['node_modules', 'libs', 'port'].includes(entry.name)) rewrite(full); continue; }
      if (entry.name !== 'go.work') continue;
      let text = readFileSync(full, 'utf8');
      for (const used of useEntries(text)) {
        const lib = relative(join(directory, 'components'), resolve(dirname(join(source, relative(target, full))), used)).split(sep)[0];
        if (!libs.includes(lib) || used.startsWith('./') || used === '.') continue;
        text = text.replace(new RegExp(`^(\\s*)${used.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*$`, 'm'), `$1${relative(dirname(full), join(target, 'libs', lib)).split(sep).join('/')}`);
      }
      writeFileSync(full, text);
    }
  };
  rewrite(target);
  const manifest = publishManifest(dir, JSON.parse(readFileSync(join(source, 'package.json'), 'utf8')), { repository, directory });
  if (libs.length && !manifest.files.includes('libs')) manifest.files.push('libs');
  writeFileSync(join(target, 'package.json'), JSON.stringify(manifest, null, 2) + '\n');
  return target;
}

/** What a packed list lacks that a Go extension or the docs need: go.mod and go.sum anywhere, the root documents, every Resource file. */
export function packedProblems(tree, packed) {
  const have = new Set(packed);
  const problems = [];
  const walk = (path) => {
    for (const entry of readdirSync(path, { withFileTypes: true })) {
      if (['node_modules', '.git', 'port'].includes(entry.name)) continue;
      const full = join(path, entry.name);
      const rel = relative(tree, full).split(sep).join('/');
      if (entry.isDirectory()) walk(full);
      else if (/(^|\/)(go\.mod|go\.sum)$/.test(rel) && !have.has(rel)) problems.push(`${rel} is not packed`);
    }
  };
  walk(tree);
  for (const name of readdirSync(tree)) if (ROOT_FILES.test(name) && !have.has(name)) problems.push(`${name} is not packed`);
  const manifest = JSON.parse(readFileSync(join(tree, 'package.json'), 'utf8'));
  for (const entries of [manifest.pi, manifest.pig]) for (const list of Object.values(entries ?? {})) {
    for (const entry of Array.isArray(list) ? list : []) {
      const base = join(tree, entry.replace(/\/?\*.*$/, ''));
      if (!existsSync(base)) continue;
      const files = [];
      const collect = (p) => { for (const e of readdirSync(p, { withFileTypes: true })) { const f = join(p, e.name); if (e.isDirectory()) collect(f); else files.push(f); } };
      try { collect(base); } catch { files.push(base); }
      for (const file of files) { const rel = relative(tree, file).split(sep).join('/'); if (!have.has(rel)) problems.push(`${rel} is not packed`); }
    }
  }
  return problems;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  const config = JSON.parse(readFileSync(join(root, 'catalog.config.json'), 'utf8'));
  if (process.argv.includes('--write')) {
    for (const { dir, path, manifest } of readPackages()) writeFileSync(path, JSON.stringify(publishManifest(dir, manifest, { repository: config.repository }), null, 2) + '\n');
    console.log('Wrote the published manifest of every component');
  } else {
    const problems = readPackages().flatMap(({ dir, manifest }) => manifestProblems(dir, manifest, { repository: config.repository }));
    for (const problem of problems) console.error('npm-packages: ' + problem);
    if (problems.length) process.exitCode = 1;
    else console.log(`npm-packages: ${readPackages().length} Packages meet the npm contract`);
  }
}
