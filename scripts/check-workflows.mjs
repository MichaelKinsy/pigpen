#!/usr/bin/env node
// Workflow hardening gate, part of `npm run quality`: the checks PiG's `make compliance` makes on its own workflows.
// Every workflow under .github/workflows must pin each action to a full commit SHA, ask for a read-only token, use no
// secrets and no pull_request_target, never persist the checkout token, and bound every job. Four workflows are excepted,
// each with its own rules below. release.yml (releaseProblems) reads PIGLET_SIGNING_KEY, an environment secret read only
// by a step that runs the pinned pig, holds the release writes it needs, and has a tag filter that no Package tag can
// match. release-archon-provider.yml (archonReleaseProblems) reads ARCHON_PROVIDER_PIG_TOKEN, an environment secret read only
// by a step that runs git and gh and no repository script, on a pushed Archon plugin tag. discord-notify.yml (notifyProblems)
// reads DISCORD_WEBHOOK_* URLs from the notifications environment, only in the
// env of a step that posts to Discord, and runs on nothing but a push to main and a published release. ci.yml must also keep its one
// PiG pin equal to scripts/pig-requirement.json and build every target a Piglet manifest declares on that target's native
// runner (scripts/runners.mjs), so the pin and the target list each have one source. Any other workflow that checks out
// PiG must use the same pin.
//
// Every actions/setup-go step must read the .go-version of the PiG checkout (goToolchainProblems), never a go.mod floor.
//
// This checks hardening and drift, not behaviour: what the workflow does is shown by running it.
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';

const shaPinned = /^[^@\s]+@[0-9a-f]{40}$/;

// The only write permissions a job of release.yml may hold: attestations in the build, the release itself in publish.
const releaseWrites = { build: ['id-token: write', 'attestations: write'], publish: ['contents: write'] };
// npm-publish.yml publishes with npm trusted publishing (OIDC): id-token in its one publish job, nothing else.
const npmWrites = { publish: ['id-token: write'] };
// kagent-harness-image.yml: the registry push in build (never id-token), the keyless signature and the attestations in publish (the one job with id-token), the release in release.
const KAGENT_IMAGE_WORKFLOW = 'kagent-harness-image.yml';
const KAGENT_IMAGE_TAG = 'components/kagent/harness/v*';
const kagentImageWrites = { build: ['packages: write'], publish: ['packages: write', 'id-token: write', 'attestations: write', 'artifact-metadata: write'], release: ['contents: write'] };

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
  if (![RELEASE_WORKFLOW, NOTIFY_WORKFLOW, ARCHON_RELEASE_WORKFLOW].includes(name) && /\$\{\{\s*secrets\./.test(text)) problems.push(`${name}: uses a secret`);

  for (const [id, job] of jobs) {
    const allowed = name === RELEASE_WORKFLOW ? releaseWrites[id] ?? [] : name === KAGENT_IMAGE_WORKFLOW ? kagentImageWrites[id] ?? [] : name === 'npm-publish.yml' ? npmWrites[id] ?? [] : [];
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
const RELEASE_WORKFLOW = 'release.yml';
const NOTIFY_WORKFLOW = 'discord-notify.yml';
const ARCHON_RELEASE_WORKFLOW = 'release-archon-provider.yml';
const ARCHON_TOKEN = 'ARCHON_PROVIDER_PIG_TOKEN';
const WEBHOOK_SECRET = /^DISCORD_WEBHOOK_[A-Z0-9_]+$/;
const secretRef = new RegExp(`\\$\\{\\{\\s*secrets\\.([A-Za-z0-9_]+)`, 'g');

/**
 * release.yml's own rules, on top of hardeningProblems. It runs only on a pushed tag. The one secret is PIGLET_SIGNING_KEY,
 * read only in the env of a step in a job of the `release` environment (whose secret and tag rules are the owner's, see
 * OWNER-ACTIONS.md), and that step must run the pinned pig and no repository script or package manager.
 */
export function releaseProblems(name, text) {
  if (name !== RELEASE_WORKFLOW) return [];
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

/**
 * release-archon-provider.yml's own rules, on top of hardeningProblems. It publishes components/archon-provider-pig to its own repository,
 * which a workflow's own token cannot write, so it holds one personal access token, ARCHON_PROVIDER_PIG_TOKEN: read only in the env of a
 * step in a job of the `archon-provider-release` environment (whose secret and tag policy are the owner's, see OWNER-ACTIONS.md), by a
 * step that runs no repository script and no package manager. It runs only on a pushed tag, and the tag filter must start it for the
 * Archon plugin's tag and no other.
 */
export function archonReleaseProblems(name, text) {
  if (name !== ARCHON_RELEASE_WORKFLOW) return [];
  const problems = [];
  const workflow = parse(text) ?? {};
  const triggers = Object.keys(workflow.on ?? {});
  if (triggers.join() !== 'push' || !Array.isArray(workflow.on.push?.tags) || Object.keys(workflow.on.push).join() !== 'tags') {
    problems.push(`${name}: must run only on a pushed tag (on.push.tags), nothing else`);
  }
  const found = [...text.matchAll(secretRef)].map((m) => m[1]);
  for (const other of new Set(found.filter((secret) => secret !== ARCHON_TOKEN))) problems.push(`${name}: uses secrets.${other}; the only secret is ${ARCHON_TOKEN}`);
  let legitimate = 0;
  for (const [id, job] of Object.entries(workflow.jobs ?? {})) {
    for (const step of job.steps ?? []) {
      if (!new RegExp(`^\\$\\{\\{\\s*secrets\\.${ARCHON_TOKEN}\\s*\\}\\}$`).test(String(step.env?.[ARCHON_TOKEN] ?? '').trim())) continue;
      legitimate += 1;
      const environment = typeof job.environment === 'string' ? job.environment : job.environment?.name;
      if (environment !== 'archon-provider-release') problems.push(`${name}: job ${id} reads ${ARCHON_TOKEN} outside environment: archon-provider-release`);
      const run = String(step.run ?? '');
      if (!/\bgit\s/.test(run) || !/\bgh\s/.test(run)) problems.push(`${name}: job ${id}: the step that reads ${ARCHON_TOKEN} must publish with git and gh`);
      if (/(^|[\s;&|(])(npm|npx|node|pnpm|yarn|bun)\s|\.\/|scripts\//.test(run)) problems.push(`${name}: job ${id}: the step that reads ${ARCHON_TOKEN} runs a repository script or package manager`);
    }
  }
  if (found.filter((secret) => secret === ARCHON_TOKEN).length !== legitimate) problems.push(`${name}: ${ARCHON_TOKEN} may appear only in the env of a step (as ${ARCHON_TOKEN}: \${{ secrets.${ARCHON_TOKEN} }}), nowhere else`);
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
  if (!needs.includes('pending')) problems.push(`${name}: the publish job must have needs: pending, so a merge that bumps nothing does not run the publish job`);
  if (!/needs\.pending\.outputs\.count/.test(String(workflow.jobs?.publish?.if ?? ''))) problems.push(`${name}: the publish job must run only if needs.pending.outputs.count is not 0`);
  if (!/node scripts\/npm-publish\.mjs --pending/.test(JSON.stringify(workflow.jobs?.pending?.steps ?? []))) problems.push(`${name}: the pending job must run node scripts/npm-publish.mjs --pending`);
  return problems;
}

/** A GitHub tag filter as a RegExp: `*` stays inside a path segment, `**` crosses `/`. Other filter syntax is refused. */
function tagFilter(pattern) {
  if (/[?+\[\]!]/.test(pattern.replace(/\*/g, ''))) throw new Error(`unsupported tag filter ${pattern}`);
  return new RegExp('^' + pattern.split('**').map((part) => part.split('*').map((lit) => lit.replace(/[.*+?^${}()|[\]\\\/]/g, '\\$&')).join('[^/]*')).join('.*') + '$');
}

/** The grype policy of the kagent harness image gate: the one file an exception may be written in. */
export const GRYPE_CONFIG = 'components/kagent/release/.grype.yaml';

/**
 * The gate fails on every fixed High or worse finding. An exception is allowed only as a reviewed, dated, narrowly scoped ignore entry:
 * a vulnerability id plus the exact package name and version, its type and the exact file location (never a bare vulnerability or a package wildcard, so the same
 * finding anywhere else still fails), at most five entries, and a `# expires: YYYY-MM-DD` line no more than 120 days after the
 * `# reviewed:` date line, which the release workflow also enforces at run time.
 */
export function grypeIgnoreProblems(text) {
  const problems = [];
  const config = parse(text) ?? {};
  const unknown = Object.keys(config).filter((key) => key !== 'ignore');
  if (unknown.length) problems.push(`${GRYPE_CONFIG}: only an ignore list is allowed, not ${unknown.join(', ')}`);
  const ignore = config.ignore ?? [];
  if (!Array.isArray(ignore)) return [...problems, `${GRYPE_CONFIG}: ignore must be a list`];
  if (ignore.length > 5) problems.push(`${GRYPE_CONFIG}: ${ignore.length} ignore entries; at most 5`);
  ignore.forEach((entry, index) => {
    const where = `${GRYPE_CONFIG}: ignore[${index}]`;
    if (typeof entry?.vulnerability !== 'string' || !/^[A-Za-z0-9-]+$/.test(entry.vulnerability)) problems.push(`${where}: needs one vulnerability id`);
    const pkg = entry?.package;
    if (!pkg?.name || !pkg?.version || !pkg?.type || /[*?]/.test(`${pkg.name}${pkg.version}`)) problems.push(`${where}: needs a package with an exact name, an exact version and a type, no wildcard`);
    // grype reads the file only from package.location; a top-level location key is silently ignored, which would leave the entry unscoped.
    if (typeof pkg?.location !== 'string' || !/^\/[^*?]+$/.test(pkg.location)) problems.push(`${where}: package needs an exact absolute location (the path of the one binary), no wildcard`);
    for (const key of Object.keys(entry ?? {})) if (!['vulnerability', 'package'].includes(key)) problems.push(`${where}: ${key} widens the exception; only vulnerability and package are allowed`);
  });
  if (ignore.length) {
    const expires = /^# expires: (\d{4}-\d{2}-\d{2})$/m.exec(text)?.[1];
    const reviewed = /^# reviewed: (\d{4}-\d{2}-\d{2})$/m.exec(text)?.[1];
    if (!expires || !reviewed) problems.push(`${GRYPE_CONFIG}: an exception needs a "# reviewed: YYYY-MM-DD" line and a "# expires: YYYY-MM-DD" line`);
    else if ((Date.parse(expires) - Date.parse(reviewed)) / 86400000 > 120 || Date.parse(expires) <= Date.parse(reviewed)) problems.push(`${GRYPE_CONFIG}: the exception must expire after its review date and within 120 days of it`);
  }
  return problems;
}

/**
 * kagent-harness-image.yml's own rules, on top of hardeningProblems. It builds, signs and publishes the pig-kagent container image on a
 * pushed `components/kagent/harness/v*` tag and nothing else. It uses no secret (the registry login and the release use the job's own
 * token), builds only push by digest and never hold id-token, and only `publish` (and `release`) run in the protected `release-image`
 * environment: signing and attesting happen in `publish` alone, so the signing identity is one workflow, one job, one tag pattern.
 * The build matrix names both architectures, each on its native runner (scripts/runners.mjs), because PiG's Piglet build runs `go build`
 * for the machine it runs on.
 */
export function kagentImageProblems(name, text, runners = {}) {
  if (name !== KAGENT_IMAGE_WORKFLOW) return [];
  const problems = [];
  const workflow = parse(text) ?? {};
  const on = workflow.on ?? {};
  const tags = on.push?.tags;
  if (Object.keys(on).join() !== 'push' || Object.keys(on.push ?? {}).join() !== 'tags' || !Array.isArray(tags) || tags.join() !== KAGENT_IMAGE_TAG) {
    problems.push(`${name}: must run only on a pushed ${KAGENT_IMAGE_TAG} tag, nothing else`);
  }
  const jobs = workflow.jobs ?? {};
  for (const id of ['plan', 'build', 'publish', 'release']) if (!jobs[id]) problems.push(`${name}: job ${id} is missing`);
  if (![jobs.publish?.needs ?? []].flat().includes('build')) problems.push(`${name}: publish must need build, so nothing is signed before both architectures built and passed the scan`);
  if (![jobs.release?.needs ?? []].flat().includes('publish')) problems.push(`${name}: release must need publish`);
  const include = jobs.build?.strategy?.matrix?.include ?? [];
  for (const arch of ['amd64', 'arm64']) {
    const entry = include.find((candidate) => candidate.arch === arch);
    if (!entry) problems.push(`${name}: the build matrix has no ${arch} entry`);
    else if (runners[`linux/${arch}`] && entry.runner !== runners[`linux/${arch}`]) problems.push(`${name}: ${arch} builds on ${entry.runner}, not the native ${runners[`linux/${arch}`]}`);
  }
  for (const [id, job] of Object.entries(jobs)) {
    const environment = typeof job.environment === 'string' ? job.environment : job.environment?.name;
    if (['publish', 'release'].includes(id) && environment !== 'release-image') problems.push(`${name}: job ${id} must run in environment: release-image`);
    for (const step of job.steps ?? []) {
      const uses = String(step.uses ?? '');
      const run = String(step.run ?? '');
      if (uses.startsWith('docker/login-action@') && String(step.with?.password ?? '').trim() !== '${{ github.token }}') {
        problems.push(`${name}: job ${id}: the registry login must use \${{ github.token }}`);
      }
      if (uses.startsWith('docker/build-push-action@') && !/push-by-digest=true/.test(String(step.with?.outputs ?? ''))) {
        problems.push(`${name}: job ${id}: an image is pushed by digest only (outputs must include push-by-digest=true), so no tag exists before the index is signed`);
      }
      if (id !== 'publish' && (/(^|[\s;&|(])cosign\s+sign\b/.test(run) || /^(actions\/attest(-build-provenance)?|sigstore\/cosign-installer)@/.test(uses))) {
        problems.push(`${name}: job ${id}: signing and attesting belong in the publish job only`);
      }
      if (id !== 'release' && /(^|[\s;&|(])gh\s+release\b/.test(run)) problems.push(`${name}: job ${id}: a GitHub Release is created in the release job only`);
      if (uses.startsWith('anchore/scan-action@')) {
        if (step.with?.config !== GRYPE_CONFIG) problems.push(`${name}: job ${id}: the vulnerability gate must read ${GRYPE_CONFIG}, the one place an exception can be written`);
        if (step.with?.['only-fixed'] !== true || step.with?.['fail-build'] !== true || step.with?.['severity-cutoff'] !== 'high') {
          problems.push(`${name}: job ${id}: the vulnerability gate must keep only-fixed: true, severity-cutoff: high and fail-build: true`);
        }
      }
    }
  }
  // The steps that make the gate and the signature real must exist, not only be well formed when present.
  const steps = (id) => jobs[id]?.steps ?? [];
  const count = (id, test) => steps(id).filter((step) => test(String(step.uses ?? ''), String(step.run ?? ''))).length;
  const scans = count('build', (uses) => uses.startsWith('anchore/scan-action@'));
  if (scans !== 1) problems.push(`${name}: job build must have exactly one anchore/scan-action step (the vulnerability gate), found ${scans}`);
  if (count('publish', (_, run) => /(^|[\s;&|(])cosign\s+sign\b/.test(run)) !== 1) problems.push(`${name}: job publish must have exactly one cosign sign step`);
  if (count('publish', (uses) => uses.startsWith('actions/attest-build-provenance@')) !== 1) problems.push(`${name}: job publish must have exactly one actions/attest-build-provenance step`);
  if (count('publish', (uses) => uses.startsWith('actions/attest@')) !== 2) problems.push(`${name}: job publish must have exactly two actions/attest steps (the amd64 and the arm64 SBOM)`);
  return problems;
}

/** A Piglet tag (`<name>/v<x>`) must start only release.yml, a Package tag (`components/<name>/v<x>`) only release-package.yml. */
export function tagProblems(workflows) {
  const problems = [];
  const piglet = 'herdr/v0.1.0';
  const pkg = 'components/herdr/v0.1.0';
  for (const [file, [wants, refuses, kind, other]] of Object.entries({ 'release.yml': [piglet, pkg, 'Piglet', 'Package'], 'release-package.yml': [pkg, piglet, 'Package', 'Piglet'], 'npm-publish.yml': ['npm/v0.1.0', pkg, 'npm publish', 'Package'], [ARCHON_RELEASE_WORKFLOW]: ['components/archon-provider-pig/v0.1.0', pkg, 'Archon plugin', 'Package'] })) {
    const patterns = workflows[file]?.on?.push?.tags;
    if (!patterns) continue;
    const regexes = patterns.map((pattern) => [pattern, tagFilter(pattern)]);
    if (!regexes.some(([, re]) => re.test(wants))) problems.push(`${file}: the tag filter does not match the ${kind} tag ${wants} (filters: ${patterns.join(', ')})`);
    for (const [pattern, re] of regexes) if (re.test(refuses)) problems.push(`${file}: tag filter ${pattern} also matches the ${other} tag ${refuses}, so that tag would start this workflow`);
  }
  // The kagent harness image tag (components/kagent/harness/v<x>) must start kagent-harness-image.yml and no other workflow.
  const harness = 'components/kagent/harness/v0.1.0';
  for (const [file, workflow] of Object.entries(workflows)) {
    if (file === KAGENT_IMAGE_WORKFLOW) continue;
    for (const pattern of workflow?.on?.push?.tags ?? []) {
      if (tagFilter(pattern).test(harness)) problems.push(`${file}: tag filter ${pattern} also matches the kagent harness image tag ${harness}, so that tag would start this workflow`);
    }
  }
  const imagePatterns = workflows[KAGENT_IMAGE_WORKFLOW]?.on?.push?.tags;
  if (imagePatterns) {
    const regexes = imagePatterns.map((pattern) => [pattern, tagFilter(pattern)]);
    if (!regexes.some(([, re]) => re.test(harness))) problems.push(`${KAGENT_IMAGE_WORKFLOW}: the tag filter does not match the kagent harness image tag ${harness} (filters: ${imagePatterns.join(', ')})`);
    for (const [pattern, re] of regexes) {
      for (const refused of [piglet, pkg, 'npm/v0.1.0']) if (re.test(refused)) problems.push(`${KAGENT_IMAGE_WORKFLOW}: tag filter ${pattern} also matches ${refused}, so that tag would start this workflow`);
    }
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

/**
 * discord-notify.yml's own rules, on top of hardeningProblems. It announces merged pull requests and published releases
 * in Discord. A webhook URL is a write credential for one channel, so every secret it reads must be named
 * DISCORD_WEBHOOK_*, must appear only as the value of a step env, and that step must be in a job of the `notifications`
 * environment (its secret and branch policy are the owner's, see OWNER-ACTIONS.md), whose step posts with curl. It runs on
 * nothing but a pushed merge into main and a published release, so a pull request, a fork and a workflow_run never reach it.
 */
export function notifyProblems(name, text) {
  if (name !== NOTIFY_WORKFLOW) return [];
  const problems = [];
  const workflow = parse(text) ?? {};
  const on = workflow.on ?? {};
  const triggers = Object.keys(on);
  if (!triggers.length || triggers.some((trigger) => !['push', 'release'].includes(trigger))) {
    problems.push(`${name}: must run only on a push to main and a published release (on.push, on.release), nothing else`);
  }
  if ('push' in on && (Object.keys(on.push ?? {}).join() !== 'branches' || (on.push?.branches ?? []).join() !== 'main')) {
    problems.push(`${name}: on.push must name branches: [main] and nothing else`);
  }
  if ('release' in on && (Object.keys(on.release ?? {}).join() !== 'types' || (on.release?.types ?? []).join() !== 'published')) {
    problems.push(`${name}: on.release must name types: [published] and nothing else`);
  }
  const found = [...text.matchAll(secretRef)].map((m) => m[1]);
  for (const other of new Set(found.filter((secret) => !WEBHOOK_SECRET.test(secret)))) {
    problems.push(`${name}: uses secrets.${other}; the only secrets are DISCORD_WEBHOOK_* webhook URLs`);
  }
  let legitimate = 0;
  for (const [id, job] of Object.entries(workflow.jobs ?? {})) {
    const environment = typeof job.environment === 'string' ? job.environment : job.environment?.name;
    for (const step of job.steps ?? []) {
      for (const [key, value] of Object.entries(step.env ?? {})) {
        const reference = new RegExp(`^\\$\\{\\{\\s*secrets\\.${key}\\s*\\}\\}$`);
        if (!WEBHOOK_SECRET.test(key) || !reference.test(String(value).trim())) continue;
        legitimate += 1;
        if (environment !== 'notifications') problems.push(`${name}: job ${id} reads secrets.${key} outside environment: notifications`);
        if (!/\bcurl\s/.test(String(step.run ?? ''))) problems.push(`${name}: job ${id}: the step that reads secrets.${key} does not post with curl`);
      }
    }
  }
  if (found.length !== legitimate) {
    problems.push(`${name}: a DISCORD_WEBHOOK_* secret may appear only in the env of a step (as NAME: \${{ secrets.NAME }}), nowhere else`);
  }
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
    problems.push(...hardeningProblems(file, text), ...releaseProblems(file, text), ...archonReleaseProblems(file, text), ...notifyProblems(file, text), ...npmPublishProblems(file, text), ...goToolchainProblems(file, text), ...kagentImageProblems(file, text, runners));
    const requirement = JSON.parse(readFileSync(join(root, 'scripts/pig-requirement.json'), 'utf8'));
    if (file !== 'ci.yml' && /MichaelKinsy\/PiG/.test(text)) problems.push(...pinProblems(parsed[file], requirement, file));
    if (file === 'ci.yml') {
      const workflow = parse(text);
      const declared = [...new Set(readManifests().flatMap(({ manifest }) => manifest.build?.targets ?? []))].sort();
      problems.push(...pinProblems(workflow, requirement), ...targetProblems(workflow, declared, runners), ...vulnerabilityProblems(workflow), ...listingsProblems(workflow));
    }
  }
  problems.push(...tagProblems(parsed));
  const grypeConfig = join(root, GRYPE_CONFIG);
  if (existsSync(grypeConfig)) problems.push(...grypeIgnoreProblems(readFileSync(grypeConfig, 'utf8')));
  const texts = Object.fromEntries(readdirSync(dir).filter((f) => /\.ya?ml$/.test(f)).map((f) => [f, readFileSync(join(dir, f), 'utf8')]));
  problems.push(...cacheProblems({ ...texts, parsed }));
  for (const problem of problems) console.error(`check-workflows: ${problem}`);
  if (problems.length) process.exitCode = 1;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) await main();
