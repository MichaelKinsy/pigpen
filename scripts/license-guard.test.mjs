// The licence guard on fixture trees: a GPL Package fused into an MIT Piglet is refused, GPL into GPL and MIT into GPL are
// allowed, and the refusal reaches the stage, the release plan, the catalogue and the README.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { fusionProblems, readmeProblems } from './license-guard.mjs';
import { listingEntry } from './listings.mjs';
import { LICENSES_BEGIN, LICENSES_END, renderLicensesBlock, withLicensesBlock } from './licenses.mjs';

const scripts = fileURLToPath(new URL('.', import.meta.url));
const temp = mkdtempSync(join(tmpdir(), 'pigpen-licguard-'));
after(() => rmSync(temp, { recursive: true, force: true }));
let counter = 0;

/** A minimal repository: a LICENSE, components {name: licence-or-undefined} and Piglets {name: {license, packages}}. */
function repository({ components, piglets }) {
  const dir = join(temp, `repo${counter++}`);
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'LICENSE'), 'MIT License\n');
  for (const [name, license] of Object.entries(components)) {
    const base = join(dir, 'components', name);
    mkdirSync(base, { recursive: true });
    writeFileSync(join(base, 'package.json'), JSON.stringify({ name, version: '0.1.0', ...(license ? { license } : {}) }));
    writeFileSync(join(base, 'README.md'), `# ${name}\n\nLicence: ${license ?? 'none'}.\n`);
  }
  for (const [name, { license, packages }] of Object.entries(piglets)) {
    const base = join(dir, 'piglets', name);
    mkdirSync(base, { recursive: true });
    const list = Object.entries(packages).map(([alias, source]) => `  ${alias}: ${source}`).join('\n');
    writeFileSync(join(base, 'piglet.yaml'), `name: ${name}\ndescription: fixture\nrelease:\n  version: 0.1.0\nbuild:\n  targets: [linux/amd64]\npackages:\n${list}\nextensions: []\n`);
    writeFileSync(join(base, 'catalog.json'), JSON.stringify({ featured: false, updatedAt: '2026-11-07', languages: ['go'], notes: [] }));
    writeFileSync(join(base, 'provenance.json'), JSON.stringify({ origin: 'original', authors: ['x'], license, licenseFile: '../../LICENSE', upstreams: [] }));
  }
  return dir;
}
const piglet = (license, ...aliases) => ({ license, packages: Object.fromEntries(aliases.map((alias) => [alias, `local:../../components/${alias}`])) });

describe('fusionProblems', () => {
  it('refuses a GPL-3.0-or-later Package fused into an MIT Piglet, naming both licences', () => {
    const dir = repository({ components: { 'pi-opencode-free': 'GPL-3.0-or-later' }, piglets: { batteries: piglet('MIT', 'pi-opencode-free') } });
    const problems = fusionProblems(dir);
    assert.equal(problems.length, 1);
    assert.match(problems[0], /^refused: Package "pi-opencode-free" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "batteries" \(MIT\): GPL-3\.0-or-later \(copyleft\) would force the whole work, Piglet and Binary, to GPL-3\.0-or-later, not MIT\. Keep it a standalone, opt-in Package/);
  });
  it('allows GPL into the same GPL and MIT into a GPL Piglet', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later', mit: 'MIT' }, piglets: { copyleft: piglet('GPL-3.0-or-later', 'gpl', 'mit'), plain: piglet('MIT', 'mit') } });
    assert.deepEqual(fusionProblems(dir), []);
  });
  it('refuses GPL into a GPL-2.0-only Piglet (no relicensing path)', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: { old: piglet('GPL-2.0-only', 'gpl') } });
    assert.match(fusionProblems(dir)[0], /cannot be relicensed as GPL-2\.0-only/);
  });
  it('refuses a Package with no licence, an unknown one, and a Piglet with no licence', () => {
    const dir = repository({ components: { none: undefined, odd: 'Some-Custom-1.0', fine: 'MIT' }, piglets: { a: piglet('MIT', 'none'), b: piglet('MIT', 'odd'), c: piglet(undefined, 'fine') } });
    const problems = fusionProblems(dir);
    assert.match(problems.find((p) => p.includes('"none"')), /no licence is declared/);
    assert.match(problems.find((p) => p.includes('"odd"')), /not in Pigpen's licence compatibility table/);
    assert.ok(problems.some((p) => /Piglet "c" declares no licence/.test(p)));
    assert.ok(problems.some((p) => /Piglet "c"/.test(p) && /"fine"/.test(p)), 'the Piglet without a licence refuses every Package too');
  });
  it('refuses a Package from a source whose licence cannot be read, and skips the Piglet\'s own resources', () => {
    const dir = repository({ components: {}, piglets: { remote: { license: 'MIT', packages: { thing: 'npm:thing@1.0.0', own: 'local:./extensions/own' } } } });
    const problems = fusionProblems(dir);
    assert.equal(problems.length, 1);
    assert.match(problems[0], /Package "thing" from "npm:thing@1\.0\.0", whose licence cannot be read here/);
  });
  it('checks only the Piglets named', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-only' }, piglets: { bad: piglet('MIT', 'gpl'), good: piglet('GPL-3.0-only', 'gpl') } });
    assert.equal(fusionProblems(dir).length, 1);
    assert.deepEqual(fusionProblems(dir, ['good']), []);
    assert.equal(fusionProblems(dir, ['bad']).length, 1);
  });
  it('passes on this repository today', () => assert.deepEqual(fusionProblems(), []));
});

describe('the Package page and the README block', () => {
  it('requires a copyleft Package README to state its licence', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later', mit: 'MIT' }, piglets: {} });
    assert.deepEqual(readmeProblems(dir), []);
    writeFileSync(join(dir, 'components/gpl/README.md'), '# gpl\n');
    assert.match(readmeProblems(dir)[0], /components\/gpl\/README\.md must state the licence GPL-3\.0-or-later/);
  });
  it('lists non-MIT Packages from their own metadata and replaces the block in place', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later', mit: 'MIT', both: 'MIT AND Apache-2.0' }, piglets: {} });
    const block = renderLicensesBlock(dir);
    assert.match(block, /components\/both`\]\(components\/both\) \| MIT AND Apache-2\.0 \| permissive; may be bundled/);
    assert.match(block, /components\/gpl`\]\(components\/gpl\) \| GPL-3\.0-or-later \| standalone, opt-in; never bundled/);
    assert.doesNotMatch(block, /components\/mit/);
    const readme = `before\n${LICENSES_BEGIN}\nstale\n${LICENSES_END}\nafter\n`;
    const updated = withLicensesBlock(readme, dir);
    assert.ok(updated.startsWith('before\n') && updated.endsWith('\nafter\n') && !updated.includes('stale'));
    assert.throws(() => withLicensesBlock('no markers', dir), /needs the/);
  });
  it('puts the licence note in the catalogue entry of a non-MIT listing and none on an MIT one', () => {
    const fixture = JSON.parse(readFileSync(join(scripts, 'fixtures/listings/example-hello.json'), 'utf8'));
    assert.ok(listingEntry({ ...fixture, license: 'GPL-3.0-or-later' }).notes.some((note) => /^Licence: GPL-3\.0-or-later \(copyleft, not MIT\)\./.test(note)));
    assert.ok(!listingEntry({ ...fixture, license: 'MIT' }).notes.some((note) => note.startsWith('Licence:')));
  });
  it('keeps this repository\'s README block and index.json current', () => {
    const result = spawnSync(process.execPath, [join(scripts, 'generate-index.mjs'), '--check'], { encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
  });
});

describe('where the guard is wired', () => {
  const run = (script, args, env = {}) => spawnSync(process.execPath, [join(scripts, script), ...args], { encoding: 'utf8', env: { PATH: process.env.PATH, ...env } });
  const bad = () => repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: { batteries: piglet('MIT', 'gpl') } });

  it('the CLI exits 1 with the message on a refusal and 0 on a clean tree', () => {
    const refused = run('license-guard.mjs', ['--root', bad()]);
    assert.equal(refused.status, 1);
    assert.match(refused.stderr, /refused: Package "gpl" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "batteries" \(MIT\)/);
    const fine = repository({ components: { mit: 'MIT' }, piglets: { batteries: piglet('GPL-3.0-or-later', 'mit') } });
    const accepted = run('license-guard.mjs', ['--root', fine]);
    assert.equal(accepted.status, 0, accepted.stderr);
  });
  it('npm run stage (stage-piglets) refuses before staging anything', () => {
    const dir = bad();
    const result = run('stage-piglets.mjs', [dir]);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /refused: Package "gpl"/);
    assert.throws(() => readFileSync(join(dir, 'dist/staged/piglets/batteries/piglet.yaml')));
  });
  it('stages a compatible tree', () => {
    const dir = repository({ components: { gpl: 'GPL-3.0-or-later' }, piglets: { copyleft: piglet('GPL-3.0-or-later', 'gpl') } });
    const result = run('stage-piglets.mjs', [dir]);
    assert.equal(result.status, 0, result.stderr);
  });
  it('the release plan (build-matrix) refuses the tagged Piglet on this repository\'s tree when it is incompatible', () => {
    // build-matrix reads the repository it lives in, so run a copy of the scripts next to a fixture tree.
    const dir = bad();
    cpSync(scripts, join(dir, 'scripts'), { recursive: true, filter: (path) => !/node_modules|fixtures|e2e|perf/.test(path) });
    symlinkSync(join(scripts, '..', 'node_modules'), join(dir, 'node_modules'), 'dir');
    for (const file of ['index.schema.json', 'listing.schema.json']) cpSync(join(scripts, '..', file), join(dir, file));
    writeFileSync(join(dir, 'catalog.config.json'), JSON.stringify({ repository: 'MichaelKinsy/pigpen', ref: 'main' }));
    const result = spawnSync(process.execPath, [join(dir, 'scripts/build-matrix.mjs')], { encoding: 'utf8', env: { PATH: process.env.PATH, GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: 'batteries/v0.1.0', GITHUB_OUTPUT: join(dir, 'out') } });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /refused: Package "gpl" \(GPL-3\.0-or-later\) cannot be fused into the Piglet "batteries" \(MIT\)/);
  });
});
