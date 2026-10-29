#!/usr/bin/env node
// Workflow hardening gate, part of `npm run quality`: the checks PiG's `make compliance` makes on its own workflows.
// Every workflow under .github/workflows must pin each action to a full commit SHA, ask for a read-only token, use no
// secrets and no pull_request_target, never persist the checkout token, and bound every job. The one exception is
// release.yml (see releaseProblems): PIGLET_SIGNING_KEY, an environment secret read only by a step that runs the pinned
// pig, the release writes it needs, and a tag filter that no Package tag can match. ci.yml must also keep its one
// PiG pin equal to scripts/pig-requirement.json and build every target a Piglet manifest declares on that target's native
// runner (scripts/runners.mjs), so the pin and the target list each have one source. Any other workflow that checks out
// PiG must use the same pin.
//
// Every actions/setup-go step must read the .go-version of the PiG checkout (goToolchainProblems), never a go.mod floor.
//
// This checks hardening and drift, not behaviour: what the workflow does is shown by running it.
import { readdirSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';

const shaPinned = /^[^@\s]+@[0-9a-f]{40}$/;

// The only write permissions a job of release.yml may hold: attestations in the build, the release itself in publish.
const releaseWrites = { build: ['id-token: write', 'attestations: write'], publish: ['contents: write'] };
// npm-publish.yml publishes with npm trusted publishing (OIDC): id-token in its one publish job, nothing else.
const npmWrites = { publish: ['id-token: write'] };

/** A positive timeout-minutes, or `${{ matrix.<key> }}` where every matrix.include entry sets <key> to a positive number. */
function boundedTimeout(job) {
  const value = job['timeout-minutes'];
  if (value > 0 && typeof value === 'number') return true;
  const key = typeof value === 'string' ? /^\$\{\{\s*matrix\.([A-Za-z_][A-Za-z0-9_-]*)\s*\}\}$/.exec(value.trim())?.[1] : undefined;
  const include = job.strategy?.matrix?.include;
  return Boolean(key) && Array.isArray(include) && include.length > 0 && include.every((entry) => typeof entry?.[key] === 'number' && entry[key] > 0);
}

/** Problems with one workflow's hardening; `name` prefixes each message. */
export function hardeningProblems(name, text) {
  const problems = [];
  const workflow = parse(text) ?? {};
  const jobs = Object.entries(workflow.jobs ?? {});
  const writes = (permissions) => (typeof permissions === 'string'
    ? (permissions === 'read-all' ? [] : [permissions])
    : Object.entries(permissions ?? {}).filter(([, level]) => level !== 'read' && level !== 'none').map(([scope, level]) => `${scope}: ${level}`));

  if (workflow.permissions === undefined) problems.push(`${name}: no top-level permissions (the default token may be read-write)`);
  for (const grant of writes(workflow.permissions)) problems.push(`${name}: top-level permissions grant ${grant}`);
  if (workflow.on && typeof workflow.on === 'object' && 'pull_request_target' in workflow.on) problems.push(`${name}: runs on pull_request_target`);
  if (name !== 'release.yml' && /\$\{\{\s*secrets\./.test(text)) problems.push(`${name}: uses a secret`);

  for (const [id, job] of jobs) {
    const allowed = name === 'release.yml' ? releaseWrites[id] ?? [] : name === 'npm-publish.yml' ? npmWrites[id] ?? [] : [];
    for (const grant of writes(job.permissions)) if (!allowed.includes(grant)) problems.push(`${name}: job ${id}: ${grant}`);
    if (job.uses && !job.uses.startsWith('./') && !shaPinned.test(job.uses)) problems.push(`${name}: ${job.uses} is not pinned to a full commit SHA`);
    if (!job.uses && !boundedTimeout(job)) problems.push(`${name}: job ${id} has no timeout-minutes`);
    for (const step of job.steps ?? []) {
      if (!step.uses || step.uses.startsWith('./') || step.uses.startsWith('docker://')) continue;
      if (!shaPinned.test(step.uses)) problems.push(`${name}: ${step.uses} is not pinned to a full commit SHA`);
      if (step.uses.startsWith('actions/checkout@') && step.with?.['persist-credentials'] !== false) {
        problems.push(`${name}: job ${id}: a checkout without persist-credentials: false keeps the token on disk`);
      }
    }
  }
  return problems;
}

/** The one place a workflow reads its Go release: the .go-version of the PiG checkout it builds with. */
export const GO_VERSION_FILE = '.pig-src/.go-version';

/**
 * Every actions/setup-go step reads GO_VERSION_FILE. A go.mod (or go.work) names only the language floor, which can be years behind
 * the patched toolchain, and a literal go-version would be a second pin. PiG's .go-version is its one Go pin (its go.mod toolchain
 * line equals it), so Pigpen builds with exactly the toolchain PiG builds with.
 */
export function goToolchainProblems(name, text) {
  const problems = [];
  const workflow = parse(text) ?? {};
  for (const [id, job] of Object.entries(workflow.jobs ?? {})) {
    for (const step of job.steps ?? []) {
      if (!step.uses?.startsWith('actions/setup-go@')) continue;
      const file = step.with?.['go-version-file'];
      if (step.with?.['go-version'] !== undefined) problems.push(`${name}: job ${id}: setup-go has a literal go-version; read ${GO_VERSION_FILE}`);
      if (file !== GO_VERSION_FILE) problems.push(`${name}: job ${id}: setup-go go-version-file is ${file === undefined ? 'unset' : file}, not ${GO_VERSION_FILE} (a go.mod names only the language floor, not the patched toolchain)`);
    }
  }
  return problems;
}

const SIGNING_KEY = 'PIGLET_SIGNING_KEY';
const secretRef = new RegExp(`\\$\\{\\{\\s*secrets\\.([A-Za-z0-9_]+)`, 'g');

/**
 * release.yml's own rules, on top of hardeningProblems. It runs only on a pushed tag. The one secret is PIGLET_SIGNING_KEY,
 * read only in the env of a step in a job of the `release` environment (whose secret and tag rules are the owner's, see
 * OWNER-ACTIONS.md), and that step must run the pinned pig and no repository script or package manager.
 */
export function releaseProblems(name, text) {
  if (name !== 'release.yml') return [];
  const problems = [];
  const workflow = parse(text) ?? {};
  const triggers = Object.keys(workflow.on ?? {});
  if (triggers.join() !== 'push' || !Array.isArray(workflow.on.push?.tags) || Object.keys(workflow.on.push).join() !== 'tags') {
    problems.push(`${name}: must run only on a pushed tag (on.push.tags), nothing else`);
  }
  const found = [...text.matchAll(secretRef)].map((m) => m[1]);
  for (const other of new Set(found.filter((secret) => secret !== SIGNING_KEY))) problems.push(`${name}: uses secrets.${other}; the only secret is ${SIGNING_KEY}`);
  let legitimate = 0;
  for (const [id, job] of Object.entries(workflow.jobs ?? {})) {
    for (const step of job.steps ?? []) {
      if (!/\$\{\{\s*secrets\.PIGLET_SIGNING_KEY\s*\}\}/.test(String(step.env?.[SIGNING_KEY] ?? ''))) continue;
      legitimate += 1;
      const environment = typeof job.environment === 'string' ? job.environment : job.environment?.name;
      if (environment !== 'release') problems.push(`${name}: job ${id} reads ${SIGNING_KEY} outside environment: release`);
      const run = String(step.run ?? '');
      if (!run.includes('"$PIG_BIN"')) problems.push(`${name}: job ${id}: the step that reads ${SIGNING_KEY} does not run "$PIG_BIN"`);
      if (/(^|[\s;&|(])(npm|npx|node|pnpm|yarn|bun)\s|\.\/|scripts\//.test(run)) problems.push(`${name}: job ${id}: the step that reads ${SIGNING_KEY} runs a repository script or package manager`);
    }
  }
  const planIf = String(workflow.jobs?.plan?.if ?? '');
  if (!planIf.includes("'npm/'")) problems.push(`${name}: the plan job must skip an npm/ tag (if: \${{ !startsWith(github.ref_name, 'npm/') }}): npm-publish.yml owns it`);
  if (found.filter((secret) => secret === SIGNING_KEY).length !== legitimate) problems.push(`${name}: ${SIGNING_KEY} may appear only in the env of a step (as ${SIGNING_KEY}: \${{ secrets.${SIGNING_KEY} }}), nowhere else`);
  return problems;
}


const cacheKey = /key:\s*(gocache-\$\{\{[^\n]*)/;

/**
 * The Go build cache a release restores. A cache saved by a tag run is visible only to that tag, so no release can restore the
 * cache another release saved; it is saved on main by gocache.yml, and release.yml only restores it (the signing job does not
 * write the cache it builds from). The two keys must be the same expression, or the restore never matches what main saved.
 */
export function cacheProblems(workflows) {
  const problems = [];
  const release = workflows['release.yml'];
  const warm = workflows['gocache.yml'];
  if (release && /actions\/cache(@|\/save)/.test(release)) problems.push('release.yml: saves the Go build cache; a cache saved on a tag is invisible to the next release, so only gocache.yml (on main) saves it and release.yml uses actions/cache/restore');
  if (release && /actions\/cache\/restore/.test(release) && !warm) problems.push('release.yml: restores a Go build cache that no workflow on main saves (gocache.yml)');
  if (warm) {
    const on = Object.keys(workflows.parsed?.['gocache.yml']?.on ?? {}).sort().join();
    if (on !== 'push,workflow_dispatch' || workflows.parsed['gocache.yml'].on.push?.branches?.join() !== 'main') problems.push('gocache.yml: must run only on a push to main or a manual run: only the default branch saves a cache every release can see');
    const saved = warm.match(cacheKey)?.[1];
    const restored = release?.match(cacheKey)?.[1];
    if (!saved || saved !== restored) problems.push('gocache.yml: its cache key differs from the one release.yml restores');
  }
  return problems;
}

/**
 * npm-publish.yml: OIDC trusted publishing only (no token anywhere), provenance on. A version bump merged to main must start it
 * (a tag or a manual run alone never did: the workflow has had no run since it was added), a pushed `npm/v*` tag and a manual run
 * stay, and the job that holds the id-token waits for a cheap `pending` job that finds a name@version not yet on npm.
 */
export function npmPublishProblems(name, text) {
  if (name !== 'npm-publish.yml') return [];
  const problems = [];
  const workflow = parse(text) ?? {};
  const triggers = Object.keys(workflow.on ?? {}).sort().join();
  if (triggers !== 'push,workflow_dispatch') problems.push(`${name}: runs only on a push (a merge to main or a tag) and a manual run`);
  const push = workflow.on?.push;
  if (!push || !Array.isArray(push.branches) || push.branches.join() !== 'main' || Object.keys(push).sort().join() !== 'branches,tags') {
    problems.push(`${name}: a merge to main must publish a version bump: push must be branches: [main] and the tag filter npm/v*, nothing else`);
  }
  if (push && !(Array.isArray(push.tags) && push.tags.includes('npm/v*'))) problems.push(`${name}: push must include the tag filter npm/v*`);
  if (/NODE_AUTH_TOKEN|NPM_TOKEN|_authToken/.test(text)) problems.push(`${name}: a token is referenced; publish with trusted publishing (OIDC), never a token`);
  if (!/--provenance/.test(text)) problems.push(`${name}: publish with --provenance`);
  const needs = [workflow.jobs?.publish?.needs ?? []].flat();
  if (!needs.includes('pending')) problems.push(`${name}: the publish job must need the pending job, so a merge that bumps nothing does not run the publish job`);
  if (!/needs\.pending\.outputs\.count/.test(String(workflow.jobs?.publish?.if ?? ''))) problems.push(`${name}: the publish job must run only if needs.pending.outputs.count is not 0`);
  if (!/node scripts\/npm-publish\.mjs --pending/.test(JSON.stringify(workflow.jobs?.pending?.steps ?? []))) problems.push(`${name}: the pending job must run node scripts/npm-publish.mjs --pending`);
  return problems;
}

/** A GitHub tag filter as a RegExp: `*` stays inside a path segment, `**` crosses `/`. Other filter syntax is refused. */
function tagFilter(pattern) {
  if (/[?+\[\]!]/.test(pattern.replace(/\*/g, ''))) throw new Error(`unsupported tag filter ${pattern}`);
  return new RegExp('^' + pattern.split('**').map((part) => part.split('*').map((lit) => lit.replace(/[.*+?^${}()|[\]\\\/]/g, '\\$&')).join('[^/]*')).join('.*') + '$');
}

/** A Piglet tag (`<name>/v<x>`) must start only release.yml, a Package tag (`components/<name>/v<x>`) only release-package.yml. */
export function tagProblems(workflows) {
  const problems = [];
  const piglet = 'herdr/v0.1.0';
  const pkg = 'components/herdr/v0.1.0';
  for (const [file, [wants, refuses, kind, other]] of Object.entries({ 'release.yml': [piglet, pkg, 'Piglet', 'Package'], 'release-package.yml': [pkg, piglet, 'Package', 'Piglet'], 'npm-publish.yml': ['npm/v0.1.0', pkg, 'npm publish', 'Package'] })) {
    const patterns = workflows[file]?.on?.push?.tags;
    if (!patterns) continue;
    const regexes = patterns.map((pattern) => [pattern, tagFilter(pattern)]);
    if (!regexes.some(([, re]) => re.test(wants))) problems.push(`${file}: the tag filter does not match the ${kind} tag ${wants} (filters: ${patterns.join(', ')})`);
    for (const [pattern, re] of regexes) if (re.test(refuses)) problems.push(`${file}: tag filter ${pattern} also matches the ${other} tag ${refuses}, so that tag would start this workflow`);
  }
  return problems;
}

/** ci.yml's PiG pin is the release commit in scripts/pig-requirement.json, and every PiG checkout uses it. */
export function pinProblems(workflow, requirement, name = 'ci.yml') {
  const problems = [];
  const { repository, commit } = requirement.release;
  if (workflow.env?.PIG_COMMIT !== commit) problems.push(`${name}: PIG_COMMIT ${workflow.env?.PIG_COMMIT} is not the pinned release commit ${commit} (scripts/pig-requirement.json)`);
  for (const job of Object.values(workflow.jobs ?? {})) {
    for (const step of job.steps ?? []) {
      if (step.with?.repository === repository && step.with.ref !== '${{ env.PIG_COMMIT }}') problems.push(`${name}: checks out ${repository} at ${step.with.ref}, not \${{ env.PIG_COMMIT }}`);
    }
  }
  return problems;
}

/** ci.yml's platform matrix builds every declared target on that target's native runner. */
export function targetProblems(workflow, declared, runners) {
  const problems = [];
  const include = workflow.jobs?.['platform-matrix']?.strategy?.matrix?.include ?? [];
  const built = new Set(include.map((entry) => entry.target));
  for (const target of declared) if (!built.has(target)) problems.push(`ci.yml: ${target} is a manifest target the platform matrix does not build`);
  for (const { target, runner } of include) {
    if (runners[target] !== runner) problems.push(`ci.yml: ${target} runs on ${runner}, not ${runners[target] ?? 'a runner listed in scripts/runners.mjs'}`);
  }
  return problems;
}

/**
 * ci.yml runs the govulncheck gate (scripts/govulncheck.mjs) on every run, as a job with no `if`, and the one required check,
 * `ci-result`, needs that job and reads its result, so a reachable vulnerability or an unscanned module cannot merge.
 */
export function vulnerabilityProblems(workflow) {
  const problems = [];
  const jobs = Object.entries(workflow.jobs ?? {});
  const scanning = jobs.filter(([, job]) => (job.steps ?? []).some((step) => /(^|[\s;&|(])node\s+scripts\/govulncheck\.mjs(\s|$)/.test(String(step.run ?? ''))));
  if (scanning.length !== 1) {
    problems.push(`ci.yml: ${scanning.length} jobs run scripts/govulncheck.mjs, not one`);
    return problems;
  }
  const [id, job] = scanning[0];
  if (job.if !== undefined) problems.push(`ci.yml: job ${id} (govulncheck) has an if: it must run on every run`);
  const result = workflow.jobs?.['ci-result'];
  const needs = [result?.needs ?? []].flat();
  if (!needs.includes(id)) problems.push(`ci.yml: ci-result does not need job ${id} (govulncheck)`);
  const reads = (result?.steps ?? []).some((step) => Object.values(step.env ?? {}).some((value) => String(value).replace(/\s/g, '') === `\${{needs.${id}.result}}`));
  if (!reads) problems.push(`ci.yml: ci-result does not read needs.${id}.result in an env of its steps`);
  return problems;
}

/**
 * ci.yml verifies every third-party listing (`npm run verify:listings`, scripts/verify-listings.mjs) on every run, as a job with no
 * `if`, and the one required check, `ci-result`, needs that job and reads its result, so a listing that no longer installs, builds
 * or loads with the pinned PiG cannot merge.
 */
export function listingsProblems(workflow) {
  const problems = [];
  const jobs = Object.entries(workflow.jobs ?? {});
  const verifying = jobs.filter(([, job]) => (job.steps ?? []).some((step) => /(^|[\s;&|(])(npm\s+run\s+verify:listings|node\s+scripts\/verify-listings\.mjs)(\s|$)/.test(String(step.run ?? ''))));
  if (verifying.length !== 1) {
    problems.push(`ci.yml: ${verifying.length} jobs verify the third-party listings (npm run verify:listings), not one`);
    return problems;
  }
  const [id, job] = verifying[0];
  if (job.if !== undefined) problems.push(`ci.yml: job ${id} (listings) has an if: it must run on every run`);
  const result = workflow.jobs?.['ci-result'];
  const needs = [result?.needs ?? []].flat();
  if (!needs.includes(id)) problems.push(`ci.yml: ci-result does not need job ${id} (listings)`);
  const reads = (result?.steps ?? []).some((step) => Object.values(step.env ?? {}).some((value) => String(value).replace(/\s/g, '') === `\${{needs.${id}.result}}`));
  if (!reads) problems.push(`ci.yml: ci-result does not read needs.${id}.result in an env of its steps`);
  return problems;
}

async function main() {
  const root = fileURLToPath(new URL('../', import.meta.url));
  const { readManifests } = await import('./generate-index.mjs');
  const { runners } = await import('./runners.mjs');
  const dir = join(root, '.github/workflows');
  const problems = [];
  const parsed = {};
  for (const file of readdirSync(dir).filter((f) => /\.ya?ml$/.test(f)).sort()) {
    const text = readFileSync(join(dir, file), 'utf8');
    parsed[file] = parse(text);
    problems.push(...hardeningProblems(file, text), ...releaseProblems(file, text), ...npmPublishProblems(file, text), ...goToolchainProblems(file, text));
    const requirement = JSON.parse(readFileSync(join(root, 'scripts/pig-requirement.json'), 'utf8'));
    if (file !== 'ci.yml' && /MichaelKinsy\/PiG/.test(text)) problems.push(...pinProblems(parsed[file], requirement, file));
    if (file === 'ci.yml') {
      const workflow = parse(text);
      const declared = [...new Set(readManifests().flatMap(({ manifest }) => manifest.build?.targets ?? []))].sort();
      problems.push(...pinProblems(workflow, requirement), ...targetProblems(workflow, declared, runners), ...vulnerabilityProblems(workflow), ...listingsProblems(workflow));
    }
  }
  problems.push(...tagProblems(parsed));
  const texts = Object.fromEntries(readdirSync(dir).filter((f) => /\.ya?ml$/.test(f)).map((f) => [f, readFileSync(join(dir, f), 'utf8')]));
  problems.push(...cacheProblems({ ...texts, parsed }));
  for (const problem of problems) console.error(`check-workflows: ${problem}`);
  if (problems.length) process.exitCode = 1;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) await main();
