// scripts/verify-listings.mjs on a fixture: a one-extension Package served from a local repository for the URL the listing names
// (url.<file>.insteadOf), so the exact production commands run without the network. Needs PIG_BIN (pig 0.5.2 or later, see
// scripts/pig-bin.mjs) and git. The cases that fail check that the listing is named and the step that broke is said.
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { parsePigVersion, requirePig } from './pig-bin.mjs';
import { verifyListing } from './verify-listings.mjs';

const here = fileURLToPath(new URL('.', import.meta.url));
const pig = process.env.PIG_BIN && requirePig();
const fixtureListing = JSON.parse(readFileSync(join(here, 'fixtures/listings/example-hello.json'), 'utf8'));
const gitEnv = (scratch) => ({ ...process.env, GIT_CONFIG_GLOBAL: join(scratch, 'gitconfig'), GIT_CONFIG_SYSTEM: '/dev/null', GIT_TERMINAL_PROMPT: '0',
  GIT_AUTHOR_NAME: 'T', GIT_AUTHOR_EMAIL: 't@example.com', GIT_COMMITTER_NAME: 'T', GIT_COMMITTER_EMAIL: 't@example.com' });
const git = (scratch, cwd, ...args) => execFileSync('git', ['-c', 'commit.gpgsign=false', ...args], { cwd, encoding: 'utf8', env: gitEnv(scratch) }).trim();

test('verify-listings on a fixture Package', { skip: !pig && 'set PIG_BIN', timeout: 1800000 }, async (t) => {
  const scratch = realpathSync(mkdtempSync(join(tmpdir(), 'pigpen-listing-test-')));
  t.after(() => rmSync(scratch, { recursive: true, force: true }));
  const work = join(scratch, 'work');
  const bare = join(scratch, 'hello.git');
  cpSync(join(here, 'fixtures/listing-extension'), work, { recursive: true });
  git(scratch, work, 'init', '-q', '-b', 'main');
  git(scratch, work, 'add', '-A');
  git(scratch, work, 'commit', '-q', '-m', 'one');
  const first = git(scratch, work, 'rev-parse', 'HEAD');
  writeFileSync(join(work, 'README.md'), 'second\n');
  git(scratch, work, 'add', '-A');
  git(scratch, work, 'commit', '-q', '-m', 'two');
  git(scratch, work, 'tag', 'v1.0.0', first);
  git(scratch, work, 'tag', 'v1.0.1', 'HEAD');
  const second = git(scratch, work, 'rev-parse', 'HEAD');
  git(scratch, scratch, 'clone', '-q', '--bare', work, bare);
  writeFileSync(join(scratch, 'gitconfig'), `[url "file://${bare}"]\n\tinsteadOf = ${fixtureListing.repository}.git\n[protocol "file"]\n\tallow = always\n`);
  const [major, minor, patch] = parsePigVersion(execFileSync(pig, ['--version'], { encoding: 'utf8', env: { ...process.env, PIG_OFFLINE: '1' } }));
  const listing = (changes = {}) => ({ ...structuredClone(fixtureListing), commit: second, verified: { ...fixtureListing.verified, pigVersion: `${major}.${minor}.${patch}` }, ...changes });
  const verify = (changes) => verifyListing(listing(changes), { pig, extraEnv: { GIT_CONFIG_GLOBAL: join(scratch, 'gitconfig'), GIT_CONFIG_SYSTEM: '/dev/null' } });
  const failedStep = (result) => result.steps.find((step) => !step.ok);

  await t.test('a Package pinned to a commit installs, validates, builds and loads in a faux-model session', () => {
    const result = verify({});
    assert.deepEqual(result.steps.filter((step) => !step.ok), [], JSON.stringify(result.steps, null, 2));
    assert.equal(result.ok, true);
    assert.deepEqual(result.steps.map((step) => step.name), ['pin', 'layout', 'verified with', 'install', 'contents', 'validate', 'build and register', 'faux session']);
  });

  await t.test('a tag that points at the pinned commit passes, and one that moved fails at the pin', () => {
    assert.equal(verify({ tag: 'v1.0.1' }).ok, true);
    const moved = verify({ tag: 'v1.0.0' });
    assert.equal(moved.id, 'example-hello');
    assert.equal(failedStep(moved).name, 'pin');
    assert.match(failedStep(moved).detail, new RegExp(`tag v1\\.0\\.0 points at ${first}, not the pinned ${second}`));
    assert.match(failedStep(verify({ tag: 'v9.9.9' })).detail, /tag v9\.9\.9 does not exist/);
  });

  await t.test('a commit the repository does not have fails at the pin', () => {
    const result = verify({ commit: 'f'.repeat(40) });
    assert.equal(result.ok, false);
    assert.equal(failedStep(result).name, 'pin');
  });

  await t.test('a listing that disagrees with the pinned Package fails at the step that shows it', () => {
    assert.equal(failedStep(verify({ version: '2.0.0' })).name, 'contents');
    assert.match(failedStep(verify({ version: '2.0.0' })).detail, /installed Package is version 1\.0\.0, the listing says 2\.0\.0/);
    assert.match(failedStep(verify({ resources: ['extension', 'skill'] })).detail, /declares resources \[extension\], the listing says \[extension, skill\]/);
    assert.match(failedStep(verify({ license: 'Apache-2.0' })).detail, /package\.json says license "MIT", the listing says "Apache-2\.0"/);
    assert.equal(failedStep(verify({ subdirectory: 'missing' })).name, 'layout');
  });

  await t.test('a listing verified with another PiG fails and says to re-verify', () => {
    const result = verify({ verified: { ...fixtureListing.verified, pigVersion: '0.0.1' } });
    assert.equal(failedStep(result).name, 'verified with');
    assert.match(failedStep(result).detail, /verified with PiG 0\.0\.1, this run uses PiG \d+\.\d+\.\d+: re-verify it/);
  });

  // A variant of the fixture served for the same URL: `edit(dir)` changes the tree, the result is a listing pinned to that commit.
  const variant = (name, edit) => {
    const clone = join(scratch, name);
    cpSync(work, clone, { recursive: true });
    edit(clone);
    git(scratch, clone, 'add', '-A');
    git(scratch, clone, 'commit', '-q', '-m', name);
    const served = join(scratch, `${name}.git`);
    git(scratch, scratch, 'clone', '-q', '--bare', clone, served);
    writeFileSync(join(scratch, `gitconfig-${name}`), `[url "file://${served}"]\n\tinsteadOf = ${fixtureListing.repository}.git\n[protocol "file"]\n\tallow = always\n`);
    return verifyListing(listing({ commit: git(scratch, clone, 'rev-parse', 'HEAD') }), { pig, extraEnv: { GIT_CONFIG_GLOBAL: join(scratch, `gitconfig-${name}`), GIT_CONFIG_SYSTEM: '/dev/null' } });
  };

  await t.test('a pinned tree without a license file fails at the layout', () => {
    const result = variant('nolicense', (dir) => rmSync(join(dir, 'LICENSE')));
    assert.equal(result.id, 'example-hello');
    assert.equal(failedStep(result).name, 'layout');
    assert.match(failedStep(result).detail, /no license file/);
  });

  await t.test('an extension that throws when it loads fails the listing at build and register', () => {
    const result = variant('throws', (dir) => writeFileSync(join(dir, 'extensions/hello.ts'), 'export default function (pi: any) { throw new Error("listing fixture boom"); }\n'));
    assert.equal(result.ok, false);
    assert.equal(failedStep(result).name, 'build and register');
    assert.match(failedStep(result).detail, /listing fixture boom/);
  });

  await t.test('a Package whose manifest names a missing extension fails validation', () => {
    const result = variant('missing', (dir) => rmSync(join(dir, 'extensions'), { recursive: true }));
    assert.equal(result.ok, false);
    assert.ok(['install', 'validate', 'build and register'].includes(failedStep(result).name), JSON.stringify(result.steps));
  });

  await t.test('the command line exits 1 and names each failed listing', () => {
    const listings = join(scratch, 'listings');
    mkdirSync(listings);
    // No network and no local repository are served to this process, so the pin cannot be fetched: that fails, by name.
    writeFileSync(join(listings, 'example-hello.json'), JSON.stringify({ ...listing({}), commit: '0'.repeat(40) }));
    const run = spawnSync(process.execPath, [join(here, 'verify-listings.mjs'), '--listings', listings], { encoding: 'utf8', env: { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_SYSTEM: '/dev/null' } });
    assert.equal(run.status, 1, run.stdout + run.stderr);
    assert.match(run.stdout, /FAIL example-hello\n\s+pin:/);
    assert.match(run.stdout, /verify-listings: 1 of 1 failed: example-hello/);
  });
});

test('verify-listings on a fixture Piglet', { skip: !(pig && process.env.PIG_SOURCE_ROOT) && 'set PIG_BIN and PIG_SOURCE_ROOT', timeout: 1800000 }, async (t) => {
  const scratch = realpathSync(mkdtempSync(join(tmpdir(), 'pigpen-listing-piglet-')));
  t.after(() => rmSync(scratch, { recursive: true, force: true }));
  const work = join(scratch, 'work');
  const bare = join(scratch, 'lean.git');
  cpSync(join(here, 'fixtures/listing-piglet'), join(work, 'piglets/lean'), { recursive: true });
  cpSync(join(here, 'fixtures/listing-piglet/LICENSE'), join(work, 'LICENSE'));
  git(scratch, scratch, 'init', '-q', '-b', 'main', work);
  git(scratch, work, 'add', '-A');
  git(scratch, work, 'commit', '-q', '-m', 'one');
  git(scratch, scratch, 'clone', '-q', '--bare', work, bare);
  writeFileSync(join(scratch, 'gitconfig'), `[url "file://${bare}"]\n\tinsteadOf = ${fixtureListing.repository}.git\n[protocol "file"]\n\tallow = always\n`);
  const [major, minor, patch] = parsePigVersion(execFileSync(pig, ['--version'], { encoding: 'utf8', env: { ...process.env, PIG_OFFLINE: '1' } }));
  const piglet = { ...structuredClone(fixtureListing), id: 'example-piglet', kind: 'piglet', name: 'listing-fixture-lean', subdirectory: 'piglets/lean', commit: git(scratch, work, 'rev-parse', 'HEAD'), verified: { ...fixtureListing.verified, pigVersion: `${major}.${minor}.${patch}` } };
  delete piglet.version;
  delete piglet.resources;
  const verify = (listing) => verifyListing(listing, { pig, extraEnv: { GIT_CONFIG_GLOBAL: join(scratch, 'gitconfig'), GIT_CONFIG_SYSTEM: '/dev/null' } });

  await t.test('a Piglet pinned to a commit is added, validated, built and run through a faux-model turn', () => {
    const result = verify(piglet);
    assert.deepEqual(result.steps.filter((step) => !step.ok), [], JSON.stringify(result.steps, null, 2));
    assert.deepEqual(result.steps.map((step) => step.name), ['pin', 'layout', 'verified with', 'add', 'validate', 'build and smoke']);
  });

  await t.test('a subdirectory without a Piglet file fails at add, naming the listing', () => {
    const result = verify({ ...piglet, subdirectory: 'piglets' });
    assert.equal(result.id, 'example-piglet');
    assert.equal(result.ok, false);
    assert.equal(result.steps.find((step) => !step.ok).name, 'add');
  });
});
