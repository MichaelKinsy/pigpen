// Publish every Package, then every Piglet source, to npm; skip a name@version that is already there.
//   node scripts/npm-publish.mjs [--dry-run] [--provenance] [--out dist/npm]
// Packages go first because a Piglet names them (`npm:@pi-in-go/pigpen-<dir>@^<version>`) and PiG resolves them when the
// Piglet is added. The first failure stops the run, so no Piglet is published after a failed Package. Authentication is
// whatever npm has: the owner's `npm login` (scripts/first-npm-publish.sh) or, in .github/workflows/npm-publish.yml,
// npm trusted publishing (OIDC; --provenance). No token is read, written or accepted by this script.
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { readManifests, root } from './generate-index.mjs';
import { buildPackageTree, npmName } from './npm-packages.mjs';
import { buildPigletTree, npmPigletSource, pigletNpmName } from './npm-piglets.mjs';
import { readPackages } from './packages.mjs';

/** [{kind, name, version, dir, needs?}] Packages first, then Piglets. `skipBuild` only computes names and directories. */
export function publishPlan(outRoot, { skipBuild = false, directory = root } = {}) {
  const repository = JSON.parse(readFileSync(join(directory, 'catalog.config.json'), 'utf8')).repository;
  const packages = readPackages(directory).map(({ dir, manifest }) => {
    const tree = skipBuild ? join(outRoot, 'packages', dir) : buildPackageTree(dir, join(outRoot, 'packages'), { directory, repository });
    return { kind: 'package', name: npmName(dir), version: manifest.version, dir: tree };
  });
  const piglets = readManifests(directory).map(({ manifest }) => {
    const tree = skipBuild ? join(outRoot, 'piglets', manifest.name) : buildPigletTree(manifest.name, join(outRoot, 'piglets'), { directory, repository });
    const needs = Object.values(npmPigletSource(manifest.name, { directory }).data.packages ?? {});
    return { kind: 'piglet', name: pigletNpmName(manifest.name), version: manifest.release.version, dir: tree, needs };
  });
  return [...packages, ...piglets];
}

/** Run `plan` through the injected `exists` and `publish`; returns {published, skipped} as name@version lists. */
export function publishAll(plan, { exists, publish, provenance = false, dryRun = false, satisfied } = {}) {
  const result = { published: [], skipped: [] };
  const inRun = new Set(plan.filter((e) => e.kind === 'package').map((e) => `${e.name}@${e.version}`));
  const have = satisfied ?? ((need) => {
    const m = /^npm:(@[^/]+\/[^@]+)@\^(.+)$/.exec(need);
    return !!m && (inRun.has(`${m[1]}@${m[2]}`) || exists(m[1], m[2]));
  });
  for (const entry of plan) {
    const id = `${entry.name}@${entry.version}`;
    for (const need of entry.needs ?? []) if (!have(need)) throw new Error(`${id} needs ${need}, which is neither in this run nor on npm`);
    if (exists(entry.name, entry.version)) { result.skipped.push(id); console.log(`skip     ${id} (already on npm)`); continue; }
    try {
      publish(entry, { provenance, dryRun });
    } catch (error) {
      throw new Error(`${id}: ${error.message}`);
    }
    result.published.push(id);
    console.log(`${dryRun ? 'dry-run ' : 'publish '} ${id}`);
  }
  return result;
}

/** What the owner enters on npmjs.com for each package so later releases run from .github/workflows/npm-publish.yml with no token. */
export function trustedPublisherInstructions(plan, { repository, workflow = 'npm-publish.yml' }) {
  const [owner, repo] = repository.split('/');
  return [
    'Add this Trusted Publisher to EACH package below (npmjs.com > the package > Settings > Trusted Publisher > GitHub Actions):',
    '',
    `  Organization or user: ${owner}`,
    `  Repository: ${repo}`,
    `  Workflow filename: ${workflow}`,
    '  Environment name: (leave empty)',
    '',
    'Settings pages:',
    ...plan.map((e) => `  https://www.npmjs.com/package/${e.name}/access`),
    '',
    `After that, a pushed tag npm/v<x> (or a manual run of ${workflow}) publishes new versions with provenance, authenticated by OIDC (no stored credential).`,
  ].join('\n');
}

/** Whether name@version is on the registry npm is configured for; throws on anything but a clear yes or no. */
export function npmExists(name, version) {
  const r = spawnSync('npm', ['view', `${name}@${version}`, 'version'], { encoding: 'utf8' });
  if (r.status === 0) return r.stdout.trim() === version;
  if (/E404|404 Not Found|is not in this registry/.test(r.stderr + r.stdout)) return false;
  throw new Error(`npm view ${name}@${version} failed: ${(r.stderr || r.stdout).trim().split('\n')[0]}`);
}

export function npmPublish(entry, { provenance, dryRun }) {
  const args = ['publish', '--access', 'public', '--ignore-scripts', ...(provenance ? ['--provenance'] : []), ...(dryRun ? ['--dry-run'] : [])];
  const r = spawnSync('npm', args, { cwd: entry.dir, stdio: 'inherit' });
  if (r.status !== 0) throw new Error(`npm ${args.join(' ')} exited ${r.status}`);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  const args = process.argv.slice(2);
  if (args.includes('--trusted-publishers')) {
    console.log(trustedPublisherInstructions(publishPlan(resolve(root, 'dist/npm'), { skipBuild: true }), { repository: JSON.parse(readFileSync(join(root, 'catalog.config.json'), 'utf8')).repository }));
    process.exit(0);
  }
  const out = resolve(root, args.includes('--out') ? args[args.indexOf('--out') + 1] : 'dist/npm');
  const result = publishAll(publishPlan(out), { exists: npmExists, publish: npmPublish, provenance: args.includes('--provenance'), dryRun: args.includes('--dry-run') });
  console.log(`\n${result.published.length} ${args.includes('--dry-run') ? 'would be ' : ''}published, ${result.skipped.length} already on npm`);
}
