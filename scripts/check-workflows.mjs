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
// This checks hardening and drift, not behaviour: what the workflow does is shown by running it.
import { readdirSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';

const shaPinned = /^[^@\s]+@[0-9a-f]{40}$/;

// The only write permissions a job of release.yml may hold: attestations in the build, the release itself in publish.
const releaseWrites = { build: ['id-token: write', 'attestations: write'], publish: ['contents: write'] };

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
    const allowed = name === 'release.yml' ? releaseWrites[id] ?? [] : [];
    for (const grant of writes(job.permissions)) if (!allowed.includes(grant)) problems.push(`${name}: job ${id}: ${grant}`);
    if (job.uses && !job.uses.startsWith('./') && !shaPinned.test(job.uses)) problems.push(`${name}: ${job.uses} is not pinned to a full commit SHA`);
    if (!job.uses && !(job['timeout-minutes'] > 0)) problems.push(`${name}: job ${id} has no timeout-minutes`);
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
  if (found.filter((secret) => secret === SIGNING_KEY).length !== legitimate) problems.push(`${name}: ${SIGNING_KEY} may appear only in the env of a step (as ${SIGNING_KEY}: \${{ secrets.${SIGNING_KEY} }}), nowhere else`);
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
  for (const [file, [wants, refuses, kind, other]] of Object.entries({ 'release.yml': [piglet, pkg, 'Piglet', 'Package'], 'release-package.yml': [pkg, piglet, 'Package', 'Piglet'] })) {
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
    problems.push(...hardeningProblems(file, text), ...releaseProblems(file, text));
    const requirement = JSON.parse(readFileSync(join(root, 'scripts/pig-requirement.json'), 'utf8'));
    if (file !== 'ci.yml' && /MichaelKinsy\/PiG/.test(text)) problems.push(...pinProblems(parsed[file], requirement, file));
    if (file === 'ci.yml') {
      const workflow = parse(text);
      const declared = [...new Set(readManifests().flatMap(({ manifest }) => manifest.build?.targets ?? []))].sort();
      problems.push(...pinProblems(workflow, requirement), ...targetProblems(workflow, declared, runners));
    }
  }
  problems.push(...tagProblems(parsed));
  for (const problem of problems) console.error(`check-workflows: ${problem}`);
  if (problems.length) process.exitCode = 1;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) await main();
