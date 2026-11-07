// The licence guard. A Package a Piglet selects is fused into that Piglet's Binary, so its licence must be compatible with
// the Piglet's own (scripts/licenses.mjs has the table). Refuses, with a message that names both licences, when a
// component is under a copyleft licence the Piglet is not, when either licence is missing or unknown, and when a Piglet
// takes a Package from a source whose licence cannot be read here. Also checks that every copyleft Package says so in its
// README, which is the Package's page.
//
//   node scripts/license-guard.mjs [--root <repository>] [<piglet>...]
//
// Wired into: npm run quality (CI), npm run stage (so npm run validate, build:piglet and the release builds), the
// validation and release `plan` (scripts/build-matrix.mjs) and validate-manifests.
import { existsSync, readFileSync, readdirSync, realpathSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { readManifests, root } from './generate-index.mjs';
import { canFuse, componentLicense, isCopyleft, licenseIds, parseSpdx, pigletLicense, refusal, spdxIds } from './licenses.mjs';

const slash = (path) => path.split('\\').join('/');

/** Problems (strings) for the Piglets of `directory`, or only those named in `only`. Empty when every fusion is allowed. */
export function fusionProblems(directory = root, only) {
  const problems = [];
  for (const { path, manifest } of readManifests(directory)) {
    if (only && !only.includes(manifest.name)) continue;
    const owner = dirname(path);
    const target = pigletLicense(owner);
    if (target === undefined) problems.push(`Piglet "${manifest.name}" declares no licence (provenance.json "license"), so nothing can be fused into it: refused.`);
    for (const [alias, source] of Object.entries(manifest.packages ?? {})) {
      if (typeof source !== 'string' || !source.startsWith('local:')) {
        problems.push(`refused: Piglet "${manifest.name}" selects Package "${alias}" from ${JSON.stringify(source)}, whose licence cannot be read here. Vendor it as components/${alias} with its licence declared, or remove it.`);
        continue;
      }
      const packageRoot = resolve(owner, source.slice('local:'.length));
      const owned = slash(relative(owner, packageRoot));
      if (owned && owned !== '..' && !owned.startsWith('../')) continue; // the Piglet's own resource: it carries the Piglet's licence
      const shared = slash(relative(directory, packageRoot));
      if (!/^components\/[a-z0-9][a-z0-9.-]*$/.test(shared) || !existsSync(packageRoot)) {
        problems.push(`refused: Piglet "${manifest.name}" selects Package "${alias}" from ${source}, which is not a components/<name> directory of this repository, so its licence cannot be read.`);
        continue;
      }
      const license = componentLicense(packageRoot);
      const verdict = target === undefined ? { ok: false, reason: 'the Piglet declares no licence' } : canFuse(license, target);
      if (!verdict.ok) problems.push(refusal({ component: shared.slice('components/'.length), componentLicense: license, piglet: manifest.name, pigletLicense: target, reason: verdict.reason }));
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
    const missing = spdxIds(parseSpdx(license)).filter((id) => isCopyleft(licenseIds(id)[0]) && !readme.includes(id));
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
