#!/usr/bin/env node
// Copies the bench Piglets' Go extensions from a PiG checkout at the pinned revision into Pigpen components, so no copy is made by
// hand and none drifts. The spec is bench/sync-spec.json; the revision it pins is what pi-arena's manifests build against.
//
//   node scripts/sync-bench-piglets.mjs --pig <PiG checkout> [--check]
//
// Without --check it rewrites components/<name>/extensions/<dir> from `git show <revision>:<from>` (files only, no go.mod, go.sum or
// go.work: those are Pigpen's own). With --check it writes nothing and fails when a copied file differs from the pinned revision.
import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync, existsSync, rmSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { root } from './go-modules.mjs';

const OWN = new Set(['go.mod', 'go.sum', 'go.work', 'go.work.sum']);

function git(pig, args) {
  const result = spawnSync('git', ['-C', pig, ...args], { encoding: 'buffer', maxBuffer: 1 << 28 });
  if (result.status !== 0) throw new Error(`git ${args.join(' ')}: ${result.stderr.toString().trim()}`);
  return result.stdout;
}

/** The files of `from` at `revision`, as [relative path, bytes]. */
export function filesAt(pig, revision, from) {
  const names = git(pig, ['ls-tree', '-r', '--name-only', revision, '--', from]).toString().split('\n').filter(Boolean);
  return names.map((name) => [name.slice(from.length + 1), git(pig, ['show', `${revision}:${name}`])]).filter(([relative]) => !OWN.has(relative));
}

/** The file as Pigpen keeps it: each patch of the spec replaces one exact string, and a string that is absent fails the sync. */
function patched(component, relative, bytes) {
  let text = null;
  for (const patch of component.patches ?? []) {
    if (patch.file !== relative) continue;
    text ??= bytes.toString('utf8');
    if (!text.includes(patch.from)) throw new Error(`${relative}: patch text not found: ${patch.from}`);
    text = text.replace(patch.from, () => patch.to);
  }
  return text === null ? bytes : Buffer.from(text);
}

export function sync(pig, spec, { check = false } = {}) {
  const drift = [];
  for (const [name, component] of Object.entries(spec.components)) {
    const revision = component.revision ?? spec.pig.revision;
    const target = join(root, 'components', name, 'extensions', component.extensionDir);
    const files = filesAt(pig, revision, component.from).filter(([relative]) => !(component.exclude ?? []).some((prefix) => relative.startsWith(prefix)));
    if (!files.length) throw new Error(`${component.from} has no files at ${revision}`);
    for (const rootFile of component.rootFiles ?? []) {
      const bytes = git(pig, ['show', `${revision}:${rootFile.from}`]);
      const path = join(root, 'components', name, rootFile.to);
      if (existsSync(path) && Buffer.compare(readFileSync(path), bytes) === 0) continue;
      drift.push(`${name}: ${rootFile.to}`);
      if (!check) { mkdirSync(dirname(path), { recursive: true }); writeFileSync(path, bytes); }
    }
    for (const [relative, original] of files) {
      const bytes = patched(component, relative, original);
      const path = join(target, relative);
      const current = existsSync(path) ? readFileSync(path) : null;
      if (current && Buffer.compare(current, bytes) === 0) continue;
      drift.push(`${name}: ${relative}`);
      if (!check) { mkdirSync(dirname(path), { recursive: true }); writeFileSync(path, bytes); }
    }
  }
  return drift;
}

function main(argv) {
  const pigIndex = argv.indexOf('--pig');
  if (pigIndex < 0 || !argv[pigIndex + 1]) { console.error('usage: node scripts/sync-bench-piglets.mjs --pig <PiG checkout> [--check]'); return 2; }
  const spec = JSON.parse(readFileSync(join(root, 'bench/sync-spec.json'), 'utf8'));
  const check = argv.includes('--check');
  const drift = sync(resolve(argv[pigIndex + 1]), spec, { check });
  if (check) {
    for (const line of drift) console.error(`differs from ${spec.pig.revision}: ${line}`);
    console.log(drift.length ? `${drift.length} file(s) differ` : 'bench components match their pinned PiG revisions');
    return drift.length ? 1 : 0;
  }
  console.log(`${drift.length} file(s) written from PiG ${spec.pig.revision}`);
  return 0;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) process.exit(main(process.argv.slice(2)));
