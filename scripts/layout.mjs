// Where Pigpen keeps what it ships and what it only measures.
//
//   piglets/, components/                shipped: catalogued in catalog/index.json, released with tags, published to npm
//   bench/piglets/, bench/components/    the benchmark Piglets for pi-arena and the components only they select: released and published
//                                        to npm like the rest (pi-arena pins them), but never catalogued
//
// A name is unique across both roots. Every script that walks Piglets or components asks here.
import { existsSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

export const ROOTS = { piglets: { shipped: 'piglets', bench: 'bench/piglets' }, components: { shipped: 'components', bench: 'bench/components' } };

/** The directories of `kind` ('piglets' or 'components'): [{name, rel, path, bench}] sorted by name; the bench root only with `bench`. */
export function listOwners(directory, kind, { bench = true } = {}) {
  const found = [];
  for (const [which, rel] of Object.entries(ROOTS[kind])) {
    if (which === 'bench' && !bench) continue;
    const base = join(directory, rel);
    if (!existsSync(base)) continue;
    for (const entry of readdirSync(base, { withFileTypes: true })) {
      found.push({ name: entry.name, rel: `${rel}/${entry.name}`, path: join(base, entry.name), bench: which === 'bench', isDirectory: entry.isDirectory() });
    }
  }
  found.sort((a, b) => a.name.localeCompare(b.name, 'en'));
  for (let i = 1; i < found.length; i++) if (found[i].name === found[i - 1].name) throw new Error(`${kind} ${found[i].name} exists in both ${found[i - 1].rel} and ${found[i].rel}`);
  return found;
}

/** The repository-relative directory of the named Piglet or component (components/<name> or bench/components/<name>), or null. */
export function ownerRel(directory, kind, name) {
  for (const rel of Object.values(ROOTS[kind])) if (existsSync(join(directory, rel, name))) return `${rel}/${name}`;
  return null;
}

/** A component directory relative to the repository root: components/<name> or bench/components/<name>. */
export const COMPONENT_REL = /^(?:bench\/)?components\/([a-z0-9][a-z0-9.-]*)$/;
