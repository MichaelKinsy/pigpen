#!/usr/bin/env node
// Verify every third-party listing (listings/<id>.json) against the pig Pigpen targets: the CI job and the same check anyone can run.
//
//   node scripts/verify-listings.mjs [--listings dir] [--only id,id]        PIG_BIN; PIG_SOURCE_ROOT for a Piglet
//
// For each listing, in a throwaway HOME with no credentials and PIG_TEST_FAUX (no model, no provider):
//   pin       the pinned commit is fetched from the repository (and a `tag`, if named, still points at it);
//   layout    the subdirectory exists, a license file is there, and the license the listing names is the one a Package's
//             package.json names;
//   Package:  `pig install` of the exact command the index shows, the installed version and Resources equal the listing's,
//             `pig package validate`, `pig install <root> --validate-only` (build, start, register), and a faux-model
//             session with the Package installed: it answers and prints no `Failed to load extension` diagnostic;
//   Piglet:   `pig piglet add` of the command the index shows, `pig piglet validate`, a native build of the Binary and the
//             platform smoke (version, faux turn) on it;
//   verified  the PiG the listing says it was verified with is the pig this run uses.
// Every listing is tried; the exit status is 1 if any failed, and the output names each failed listing and step.
// A listing's code runs here (it is built and loaded), so run this where nothing secret is reachable: CI has a read-only token.
import { spawnSync } from 'node:child_process';
import { appendFileSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseDocument } from 'yaml';
import { goCaches } from './go-modules.mjs';
import { hostTarget } from './host-target.mjs';
import { listingInstallCommand, listingSpec, readListings, root, validSpdx } from './listings.mjs';
import { packageResources } from './packages.mjs';
import { parsePigVersion, requirePig } from './pig-bin.mjs';
import { build, FAUX_PROMPT, loadErrors, loadFailure, smokeBinary, smokeEnv } from './platform-smoke.mjs';

const LIMIT_MS = { fetch: 300_000, install: 900_000, session: 120_000, build: 1_800_000 };
const firstLine = (text) => (text ?? '').trim().split(/\r?\n/)[0] ?? '';
const tail = (text, lines = 20) => (text ?? '').trim().split(/\r?\n/).slice(-lines).join('\n');

function spawn(command, args, { cwd, env, timeout }) {
  const result = spawnSync(command, args, { cwd, env, encoding: 'utf8', timeout, maxBuffer: 64 << 20 });
  const output = `${result.stdout ?? ''}${result.stderr ?? ''}`;
  if (result.error?.code === 'ETIMEDOUT') throw new Error(`${command} ${args.join(' ')} did not finish in ${timeout / 1000} s\n${tail(output)}`);
  if (result.error) throw new Error(`${command} ${args.join(' ')}: ${result.error.message}`);
  if (result.status !== 0) throw new Error(`${command} ${args.join(' ')} exited ${result.status}\n${tail(output)}`);
  return output;
}

/**
 * The environment one listing runs in: nothing of the caller's but PATH and the Go caches, everything else under `home`.
 * `extra` is for tests (a git config that serves the repository from a local clone).
 */
export function listingEnv(home, extra = {}) {
  const env = smokeEnv(home);
  delete env.PIG_OFFLINE; // pig install and piglet add fetch the pinned source; the faux session runs with --offline
  let caches = {};
  try { caches = goCaches(); } catch { /* no Go toolchain: a Go extension fails to build and says so */ }
  return { ...env, ...caches, GOTOOLCHAIN: 'local', ...extra };
}

/** Whether the listing's tag, if any, still points at its commit. Throws with what the repository says otherwise. */
function checkTag(listing, { cwd, env }) {
  if (!listing.tag) return;
  const refs = spawn('git', ['ls-remote', 'origin', `refs/tags/${listing.tag}`, `refs/tags/${listing.tag}^{}`], { cwd, env, timeout: LIMIT_MS.fetch }).trim().split('\n').filter(Boolean);
  const peeled = refs.find((line) => line.endsWith('^{}')) ?? refs[0];
  if (!peeled) throw new Error(`tag ${listing.tag} does not exist in ${listing.repository}`);
  const [commit] = peeled.split(/\s+/);
  if (commit !== listing.commit) throw new Error(`tag ${listing.tag} points at ${commit}, not the pinned ${listing.commit}`);
}

/** Fetch the pinned commit into `checkout`; the commit git got must be the one the listing names. */
function fetchPin(listing, checkout, env) {
  mkdirSync(checkout, { recursive: true });
  const git = (...args) => spawn('git', args, { cwd: checkout, env, timeout: LIMIT_MS.fetch });
  git('init', '-q');
  git('remote', 'add', 'origin', `${listing.repository}.git`);
  git('fetch', '-q', '--depth', '1', 'origin', listing.commit);
  const found = git('rev-parse', 'FETCH_HEAD^{commit}').trim();
  if (found !== listing.commit) throw new Error(`fetched ${found}, not the pinned ${listing.commit}`);
  git('-c', 'advice.detachedHead=false', 'checkout', '-q', 'FETCH_HEAD');
  checkTag(listing, { cwd: checkout, env });
}

/** The directory the listing points at inside the checkout, which must not leave it through a symlink. */
function listedRoot(listing, checkout) {
  const real = realpathSync(checkout);
  const path = listing.subdirectory ? resolve(real, listing.subdirectory) : real;
  if (!existsSync(path)) throw new Error(`subdirectory ${listing.subdirectory} does not exist at the pinned commit`);
  const found = realpathSync(path);
  if (found !== real && !found.startsWith(real + sep)) throw new Error(`subdirectory ${listing.subdirectory} resolves outside the repository`);
  if (!lstatSync(found).isDirectory()) throw new Error(`subdirectory ${listing.subdirectory} is not a directory`);
  return found;
}

const LICENSE_FILE = /^(?:licen[sc]e|copying|unlicense)(?:[-_.].*)?$/i;

/** A license file in the listed directory or the repository root, and a Package's own license field agreeing with the listing. */
function checkLicense(listing, dir, checkout) {
  const has = (where) => readdirSync(where, { withFileTypes: true }).some((entry) => entry.isFile() && LICENSE_FILE.test(entry.name));
  if (!has(dir) && !has(realpathSync(checkout))) throw new Error('no license file (LICENSE, LICENCE or COPYING) in the listed directory or the repository root at the pinned commit');
  if (listing.kind !== 'package') return;
  const manifest = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8'));
  if (manifest.license !== undefined && manifest.license !== listing.license) throw new Error(`package.json says license ${JSON.stringify(manifest.license)}, the listing says ${JSON.stringify(listing.license)}`);
  if (!validSpdx(listing.license)) throw new Error(`${listing.license} is not an SPDX license expression`);
}

/** The root `pig package list` reports for a spec. */
function installedRoot(pig, env, cwd, spec) {
  const lines = spawn(pig, ['package', 'list'], { cwd, env, timeout: LIMIT_MS.install }).split('\n');
  const at = lines.findIndex((line) => line.trim() === spec);
  if (at < 0) throw new Error(`${spec} is not listed after install:\n${lines.join('\n')}`);
  const path = lines[at + 1].trim();
  if (!existsSync(join(path, 'package.json'))) throw new Error(`${path} has no package.json`);
  return path;
}

const sameSet = (a, b) => JSON.stringify([...a].sort()) === JSON.stringify([...b].sort());

/** The faux-model session every kind ends with: it answers and no extension failed to load. */
function fauxSession(pig, env, cwd, extra = []) {
  const result = spawnSync(pig, [...extra, '-p', FAUX_PROMPT, '--model', 'test-faux/faux-1', '--no-session', '--offline'], { cwd, env, encoding: 'utf8', timeout: LIMIT_MS.session, maxBuffer: 64 << 20 });
  if (result.error) throw new Error(`faux-model session: ${result.error.message}`);
  const output = `${result.stdout ?? ''}\n${result.stderr ?? ''}`;
  if (loadErrors(output).length) throw new Error(`faux-model session: ${loadFailure(output)}`);
  if (result.status !== 0 || (result.stdout ?? '').trim() !== 'ok') throw new Error(`faux-model session: exit ${result.status}, stdout ${JSON.stringify(firstLine(result.stdout))}, stderr ${JSON.stringify(firstLine(result.stderr))}`);
}

function verifyPackage(listing, { pig, env, cwd, dir }) {
  const steps = [];
  const step = (name, fn) => steps.push({ name, run: fn });
  const spec = listingSpec(listing);
  let installed;
  step('install', () => { spawn(pig, ['install', spec], { cwd, env, timeout: LIMIT_MS.install }); installed = installedRoot(pig, env, cwd, spec); });
  step('contents', () => {
    const manifest = JSON.parse(readFileSync(join(installed, 'package.json'), 'utf8'));
    if (manifest.version !== listing.version) throw new Error(`the installed Package is version ${manifest.version}, the listing says ${listing.version}`);
    const found = packageResources(manifest);
    if (!sameSet(found, listing.resources)) throw new Error(`the installed Package declares resources [${found.join(', ')}], the listing says [${listing.resources.join(', ')}]`);
  });
  step('validate', () => { spawn(pig, ['package', 'validate', installed], { cwd, env, timeout: LIMIT_MS.install }); });
  step('build and register', () => {
    const manifest = JSON.parse(readFileSync(join(installed, 'package.json'), 'utf8'));
    if (manifest.pi?.extensions?.length) spawn(pig, ['install', installed, '--validate-only'], { cwd, env, timeout: LIMIT_MS.install });
  });
  step('faux session', () => {
    fauxSession(pig, env, cwd);
    // The session loaded what the Package declares: pig reports the installed Resources enabled and healthy.
    const status = JSON.parse(spawn(pig, ['status', '--json'], { cwd, env, timeout: LIMIT_MS.session }));
    const items = status.resources?.items ?? [];
    if (!status.healthy || status.errors?.length) throw new Error(`pig status reports the installed Package unhealthy: ${JSON.stringify(status.errors)}`);
    for (const kind of listing.resources) {
      const wanted = { extension: 'extensions', skill: 'skills', prompt: 'prompts', theme: 'themes' }[kind];
      if (wanted && !items.some((item) => item.kind === wanted && item.enabled && !item.health)) throw new Error(`pig status shows no enabled, healthy ${kind} from the installed Package`);
    }
  });
  return steps;
}

function pigletName(dir) {
  const manifest = existsSync(join(dir, 'package.json')) ? JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8')) : {};
  const file = join(dir, manifest.pig?.piglet ?? 'piglet.yaml');
  if (!existsSync(file)) throw new Error(`no Piglet file: ${manifest.pig?.piglet ?? 'piglet.yaml'} is not in the listed directory`);
  const document = parseDocument(readFileSync(file, 'utf8'), { uniqueKeys: true });
  if (document.errors.length) throw document.errors[0];
  return { file, name: document.toJS().name };
}

function verifyPiglet(listing, { pig, env, cwd, dir, home }) {
  const steps = [];
  const step = (name, fn) => steps.push({ name, run: fn });
  let found;
  step('add', () => { found = pigletName(dir); spawn(pig, ['piglet', 'add', listingSpec(listing)], { cwd, env, timeout: LIMIT_MS.install }); });
  step('validate', () => { spawn(pig, ['piglet', 'validate', found.name], { cwd, env, timeout: LIMIT_MS.install }); });
  step('build and smoke', () => {
    const out = join(home, 'bin');
    mkdirSync(out, { recursive: true });
    const host = hostTarget();
    const built = build(pig, found.name, out, host, found.file);
    if (built.error) throw new Error(`build: ${built.error}`);
    const bad = smokeBinary(built.target).filter((check) => !check.ok);
    if (bad.length) throw new Error(bad.map((check) => `${check.name}: ${check.detail}`).join('\n'));
  });
  return steps;
}

/** The PiG the listing says it was verified with is the pig this run uses. */
function checkVerifiedWith(listing, pig, requirement) {
  const result = spawnSync(pig, ['--version'], { encoding: 'utf8', env: { PATH: process.env.PATH ?? '', PIG_OFFLINE: '1', PI_SKIP_VERSION_CHECK: '1' } });
  const found = parsePigVersion(result.stdout ?? '')?.join('.');
  if (found !== listing.verified.pigVersion) throw new Error(`the listing says it was verified with PiG ${listing.verified.pigVersion}, this run uses PiG ${found}: re-verify it and update verified (pigVersion, pigCommit, date)`);
  if (requirement && listing.verified.pigCommit !== requirement.commit) throw new Error(`the listing says it was verified with PiG commit ${listing.verified.pigCommit}, this run uses ${requirement.commit} (scripts/pig-requirement.json): update verified.pigCommit`);
}

/**
 * Verify one listing. Returns {id, ok, steps: [{name, ok, detail}]}; a step that fails stops that listing, never the others.
 * `requirement` is the release in scripts/pig-requirement.json (omit to skip the commit comparison); `extraEnv` is for tests.
 */
export function verifyListing(listing, { pig, requirement, extraEnv = {} }) {
  const scratch = realpathSync(mkdtempSync(join(tmpdir(), `pigpen-listing-${listing.id}-`)));
  const results = [];
  try {
    const home = join(scratch, 'home');
    const cwd = join(scratch, 'work');
    for (const where of [home, cwd, join(home, 'tmp')]) mkdirSync(where, { recursive: true });
    const env = listingEnv(home, extraEnv);
    const checkout = join(scratch, 'checkout');
    const state = {};
    const plan = [
      ['pin', () => fetchPin(listing, checkout, env)],
      ['layout', () => { state.dir = listedRoot(listing, checkout); checkLicense(listing, state.dir, checkout); }],
      ['verified with', () => checkVerifiedWith(listing, pig, requirement)],
    ];
    const kindSteps = () => (listing.kind === 'package' ? verifyPackage : verifyPiglet)(listing, { pig, env, cwd, dir: state.dir, home });
    for (const [name, run] of plan) {
      if (!attempt(results, name, run)) return { id: listing.id, ok: false, steps: results };
    }
    for (const { name, run } of kindSteps()) {
      if (!attempt(results, name, run)) break;
    }
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
  return { id: listing.id, ok: results.every((step) => step.ok), steps: results };
}

function attempt(results, name, run) {
  try {
    run();
    results.push({ name, ok: true, detail: '' });
    return true;
  } catch (error) {
    results.push({ name, ok: false, detail: error.message });
    return false;
  }
}

const mark = (ok) => (ok ? 'pass' : '**FAIL**');

function markdown(rows) {
  const lines = ['### Third-party listings', '', '| Listing | Result | Failed step |', '| --- | --- | --- |'];
  for (const row of rows) lines.push(`| ${row.id} | ${mark(row.ok)} | ${row.steps.find((step) => !step.ok)?.name ?? '-'} |`);
  for (const row of rows) for (const step of row.steps.filter((s) => !s.ok)) lines.push('', `**${row.id}: ${step.name}**`, '', '```', step.detail, '```');
  return `${lines.join('\n')}\n`;
}

function main() {
  const args = process.argv.slice(2);
  const value = (flag) => { const i = args.indexOf(flag); return i === -1 ? undefined : args[i + 1]; };
  const directory = resolve(value('--listings') ?? join(root, 'listings'));
  const pig = requirePig();
  const requirement = JSON.parse(readFileSync(join(root, 'scripts/pig-requirement.json'), 'utf8')).release;
  const only = value('--only')?.split(',').filter(Boolean);
  const found = readListings(directory);
  const invalid = found.filter(({ problems }) => problems.length);
  if (invalid.length) throw new Error(invalid.map(({ file, problems }) => `listings/${file}: ${problems.join('; ')}`).join('\n'));
  const listings = found.map(({ listing }) => listing);
  const unknown = (only ?? []).filter((id) => !listings.some((listing) => listing.id === id));
  if (unknown.length) throw new Error(`no listing named ${unknown.join(', ')} (listed: ${listings.map((l) => l.id).join(', ') || 'none'})`);
  const chosen = only ? listings.filter((listing) => only.includes(listing.id)) : listings;
  console.log(`verify-listings: ${chosen.length} listing(s) with ${pig}`);
  const rows = [];
  for (const listing of chosen) {
    console.log(`::group::${listing.id}: ${listingInstallCommand(listing)}`);
    const row = verifyListing(listing, { pig, requirement });
    console.log('::endgroup::');
    console.log(`  ${row.ok ? 'ok  ' : 'FAIL'} ${row.id}${row.steps.filter((s) => !s.ok).map((s) => `\n       ${s.name}: ${s.detail.replaceAll('\n', '\n         ')}`).join('')}`);
    rows.push(row);
  }
  if (process.env.GITHUB_STEP_SUMMARY && rows.length) appendFileSync(process.env.GITHUB_STEP_SUMMARY, markdown(rows));
  const failed = rows.filter((row) => !row.ok);
  console.log(failed.length ? `verify-listings: ${failed.length} of ${rows.length} failed: ${failed.map((row) => row.id).join(', ')}` : `verify-listings: all ${rows.length} listing(s) passed`);
  process.exitCode = failed.length ? 1 : 0;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  try { main(); } catch (error) { console.error(`verify-listings: ${error.message}`); process.exitCode = 1; }
}
