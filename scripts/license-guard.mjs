// The licence guard. A Package a Piglet selects is fused into that Piglet's Binary, so its licence must be compatible with
// the Piglet's own (scripts/licenses.mjs has the table). Refuses, with a message that names both licences, when a
// component is under a copyleft licence the Piglet is not, when either licence is missing or unknown, and when a Piglet
// takes a Package, extension or skill from a source whose licence cannot be read here. It checks the effective selection:
// a Piglet that `extends` another inherits the base's Packages, extensions and skills, so those are checked too, against
// the licence of the Piglet that is built. Several Packages are also checked together, against one alternative of an OR licence. Also checks that every copyleft Package says so in its
// README, which is the Package's page.
//
//   node scripts/license-guard.mjs [--root <repository>] [<piglet>...]
//
// Wired into: npm run quality (CI), npm run stage (so npm run validate, build:piglet and the release builds), the
// validation and release `plan` (scripts/build-matrix.mjs) and validate-manifests.
import { existsSync, readFileSync, readdirSync, realpathSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseDocument } from 'yaml';
import { readManifests, root } from './generate-index.mjs';
import { npmName } from './npm-packages.mjs';
import { canFuse, componentLicense, isCopyleft, licenseIds, parseSpdx, pigletLicense, refusal, spdxIds } from './licenses.mjs';

const slash = (path) => path.split('\\').join('/');
const MAX_EXTENDS_DEPTH = 8; // PiG's own limit (coding/piglet/extends.go)
const inside = (parent, child) => { const rel = slash(relative(parent, child)); return rel === '' || (rel !== '..' && !rel.startsWith('../') && !/^[A-Za-z]:/.test(rel)); };

/**
 * A Piglet's effective selection, the way PiG's mergePiglets builds it: the base's Packages, extensions and skills (minus
 * extends.remove), then the Piglet's own, which replace an inherited entry of the same alias or name. Every entry remembers
 * the directory of the manifest that wrote it, because a relative source is read from there. `problems` collects what
 * cannot be followed (a remote or missing base, a cycle). `bases` are the Piglets inherited from, nearest first: their own
 * resources are part of this Piglet's Binary under their licence.
 */
function effective(path, directory, trail = []) {
  const owner = dirname(path);
  const document = parseDocument(readFileSync(path, 'utf8'), { uniqueKeys: true });
  if (document.errors.length) throw document.errors[0];
  const manifest = document.toJS();
  const state = { name: manifest.name, owner, packages: new Map(), extensions: new Map(), skills: new Map(), bases: [], problems: [] };
  const extending = manifest.extends;
  if (extending !== undefined && extending !== null) {
    const source = typeof extending === 'object' ? extending.source : undefined;
    const bad = (why) => state.problems.push(`refused: Piglet "${manifest.name}" extends ${JSON.stringify(source)}, ${why}. Its Packages cannot be checked, so the Piglet cannot be built. Extend a local: Piglet of this repository.`);
    if (typeof source !== 'string' || !source.startsWith('local:')) bad('which is not a local: Piglet whose manifest can be read here');
    else {
      const basePath = resolve(owner, source.slice('local:'.length));
      if (!existsSync(basePath) || !inside(directory, basePath)) bad('which is not a manifest inside this repository');
      else if (trail.includes(realpathSync(basePath))) bad('which closes an extends cycle');
      else if (trail.length >= MAX_EXTENDS_DEPTH) bad(`which is deeper than PiG's extends limit of ${MAX_EXTENDS_DEPTH}`);
      else {
        const base = effective(basePath, directory, [...trail, realpathSync(path)]);
        state.problems.push(...base.problems);
        state.bases.push({ name: base.name, license: pigletLicense(dirname(basePath)) }, ...base.bases);
        for (const key of ['packages', 'extensions', 'skills']) for (const [id, entry] of base[key]) state[key].set(id, entry);
        for (const key of ['packages', 'extensions', 'skills']) for (const id of extending.remove?.[key] ?? []) state[key].delete(id);
      }
    }
  }
  for (const [alias, source] of Object.entries(manifest.packages ?? {})) state.packages.set(alias, { source, owner, piglet: manifest.name });
  for (const key of ['extensions', 'skills']) for (const entry of manifest[key] ?? []) state[key].set(entry.name, { origins: entry.origins ?? [], owner, piglet: manifest.name });
  return state;
}

/** The licence of a Package named by an npm: source of the Pigpen catalogue (@pi-in-go/pigpen-<name>), from its component here. */
function catalogueComponent(source, directory) {
  const named = /^npm:(@[^/@]+\/[^@]+)(?:@.+)?$/.exec(source)?.[1];
  const base = join(directory, 'components');
  const found = named && existsSync(base) && readdirSync(base, { withFileTypes: true }).find((entry) => entry.isDirectory() && npmName(entry.name) === named);
  return found?.name;
}

/**
 * Where a source in a manifest leads: {owned: true} (the Piglet's own resource), {component: <dir name>} (a Package of this
 * repository whose licence is read here), or {problem} (a licence nobody can read here).
 */
function locate(source, definedIn, directory, what) {
  const unreadable = (why) => ({ problem: `refused: ${what} ${JSON.stringify(source)}, whose licence cannot be read here (${why}). Add it as a Package under components/<name> with its licence declared and select that Package, or remove it.` });
  if (typeof source !== 'string') return unreadable('not a string source');
  if (source.startsWith('local:')) {
    const target = resolve(definedIn, source.slice('local:'.length));
    if (inside(definedIn, target)) return { owned: true };
    const shared = slash(relative(directory, target));
    if (/^components\/[a-z0-9][a-z0-9.-]*$/.test(shared) && existsSync(target)) return { component: shared.slice('components/'.length) };
    return unreadable('not inside the Piglet or a components/<name> directory of this repository');
  }
  const catalogue = catalogueComponent(source, directory);
  if (catalogue) return { component: catalogue };
  return unreadable('not a Package of the Pigpen catalogue');
}

/** Problems (strings) for the Piglets of `directory`, or only those named in `only`. Empty when every fusion is allowed. */
export function fusionProblems(directory = root, only) {
  const problems = [];
  for (const { path, manifest } of readManifests(directory)) {
    if (only && !only.includes(manifest.name)) continue;
    const owner = dirname(path);
    const target = pigletLicense(owner);
    if (target === undefined) problems.push(`Piglet "${manifest.name}" declares no licence (provenance.json "license"), so nothing can be fused into it: refused.`);
    const state = effective(path, directory);
    problems.push(...state.problems);
    const parts = []; // [{label, license}] fused into this Piglet's Binary
    const seen = new Set();
    const addComponent = (component) => {
      if (seen.has(component)) return;
      seen.add(component);
      parts.push({ kind: 'Package', name: component, license: componentLicense(join(directory, 'components', component)) });
    };
    for (const base of state.bases) parts.push({ kind: 'Piglet', name: base.name, license: base.license });
    for (const [alias, { source, owner: definedIn, piglet }] of state.packages) {
      const where = locate(source, definedIn, directory, `Piglet "${manifest.name}" selects Package "${alias}" (written in "${piglet}") from`);
      if (where.problem) problems.push(where.problem);
      else if (where.component) addComponent(where.component);
    }
    for (const kind of ['extensions', 'skills']) {
      for (const [name, { origins, owner: definedIn, piglet }] of state[kind]) {
        for (const origin of origins) {
          if (typeof origin === 'string' && origin.startsWith('package:')) continue; // a declared Package: checked with the packages
          const where = locate(origin, definedIn, directory, `Piglet "${manifest.name}" takes ${kind === 'skills' ? 'skill' : 'extension'} "${name}" (written in "${piglet}") from the origin`);
          if (where.problem) problems.push(where.problem);
          else if (where.component) addComponent(where.component);
        }
      }
    }
    let failed = false;
    for (const part of parts) {
      const verdict = target === undefined ? { ok: false, reason: 'the Piglet declares no licence' } : canFuse(part.license, target);
      if (!verdict.ok) {
        failed = true;
        problems.push(refusal({ kind: part.kind, component: part.name, componentLicense: part.license, piglet: manifest.name, pigletLicense: target, reason: verdict.reason }));
      }
    }
    // Each part may fit on its own and still not fit together: a Piglet offered under `GPL-2.0-only OR GPL-3.0-only` carries ONE
    // licence per Binary, so one alternative has to accept every part.
    if (!failed && target !== undefined && parts.length > 1) {
      const together = canFuse(parts.map((part) => `(${part.license})`).join(' AND '), target);
      if (!together.ok) problems.push(`refused: the parts fused into the Piglet "${manifest.name}" (${parts.map((part) => `${part.name} ${part.license}`).join(', ')}) cannot be carried by any one alternative of its licence (${target}): ${together.reason}. A Binary has one licence; split the Piglet, or have the owner relicense it.`);
    }
  }
  return problems;
}

/** Problems for Packages under a copyleft licence whose README (the Package's page) does not state that licence. */
export function readmeProblems(directory = root) {
  const problems = [];
  for (const entry of readdirSync(join(directory, 'components'), { withFileTypes: true })) {
    if (!entry.isDirectory()) continue;
    const dir = join(directory, 'components', entry.name);
    const license = componentLicense(dir);
    if (license === undefined || !licenseIds(license).some(isCopyleft)) continue;
    const readme = existsSync(join(dir, 'README.md')) ? readFileSync(join(dir, 'README.md'), 'utf8') : '';
    const stated = (id) => new RegExp(`(?<![A-Za-z0-9.+-])${id.replace(/[.+]/g, '\\$&')}(?![A-Za-z0-9+-]|\\.[A-Za-z0-9])`).test(readme);
    const missing = spdxIds(parseSpdx(license)).filter((id) => isCopyleft(licenseIds(id)[0]) && !stated(id));
    if (missing.length) problems.push(`components/${entry.name}/README.md must state the licence ${missing.join(', ')} (the package is ${license}, copyleft), so the Package's page shows it.`);
  }
  return problems;
}

/** Throws one Error listing every problem; used by stage, validate and the release plan. */
export function assertLicensesCompatible(directory = root, only) {
  const problems = fusionProblems(directory, only);
  if (problems.length) throw new Error(problems.join('\n'));
}

if (process.argv[1] && realpathSync(resolve(process.argv[1])) === realpathSync(fileURLToPath(import.meta.url))) {
  const args = process.argv.slice(2);
  const at = args.indexOf('--root');
  const directory = at === -1 ? root : resolve(args.splice(at, 2)[1] ?? '.');
  const problems = [...fusionProblems(directory, args.length ? args : undefined), ...(args.length ? [] : readmeProblems(directory))];
  if (problems.length) {
    console.error(problems.join('\n'));
    process.exit(1);
  }
  console.log('Licence guard: every Package fused into a Piglet is licence-compatible with it');
}
