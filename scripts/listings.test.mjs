// Third-party listings: the schema, the checks (unpinned ref, missing license, an id that clashes with a Pigpen component) and the
// index entry they generate. The tests build small trees; the committed index.json is checked by `npm run check`.
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, beforeEach, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { generateIndex } from './generate-index.mjs';
import { listingEntry, listingInstallCommand, listingProblems, listingSpec, readListings, validSpdx } from './listings.mjs';

const here = fileURLToPath(new URL('.', import.meta.url));
const repository = 'MichaelKinsy/pigpen';
const fixture = JSON.parse(readFileSync(join(here, 'fixtures/listings/example-hello.json'), 'utf8'));
const piglet = { ...fixture, id: 'example-piglet', kind: 'piglet', name: 'Example piglet', subdirectory: 'piglets/review', commit: 'a'.repeat(40) };
delete piglet.version;
delete piglet.resources;

const write = (dir, file, value) => { mkdirSync(join(dir, file, '..'), { recursive: true }); writeFileSync(join(dir, file), typeof value === 'string' ? value : JSON.stringify(value, null, 2)); };

/** A tree with one Pigpen Piglet (herdr) and one component (herdr), as generateIndex reads them. */
function tree() {
  const dir = mkdtempSync(join(tmpdir(), 'pigpen-listings-'));
  write(dir, 'piglets/herdr/piglet.yaml', 'name: herdr\ndescription: "PiG with herdr"\nrelease:\n  version: 0.1.0\n');
  write(dir, 'piglets/herdr/catalog.json', { featured: false, updatedAt: '2026-09-30', languages: ['go'], notes: [] });
  write(dir, 'components/herdr/package.json', { name: 'pigpen-herdr', version: '0.1.0', description: 'Reports state', license: 'MIT', pi: { extensions: ['extensions/herdr'] } });
  return dir;
}

const problemsOf = (dir) => {
  const found = readListings(join(dir, 'listings'));
  return listingProblems(found, { reserved: new Set(['herdr', 'pigpen-herdr', '@pi-in-go/pigpen-herdr']), repository });
};

describe('the fixture listing', () => {
  it('is valid and becomes an unofficial entry with the real maintainer', () => {
    const entry = listingEntry(fixture);
    assert.equal(entry.official, false);
    assert.equal(entry.featured, false);
    assert.equal(entry.maintainer, 'Example Maintainer');
    assert.equal(entry.kind, 'package');
    assert.equal(entry.status, 'available');
    assert.equal(entry.license, 'MIT');
    assert.equal(entry.piCompatibility, 'not-tested');
    assert.equal(entry.repository, 'https://github.com/example-maintainer/pig-hello');
    assert.equal(entry.source.spec, `git:https://github.com/example-maintainer/pig-hello.git@${fixture.commit}`);
    assert.equal(entry.source.url, `https://github.com/example-maintainer/pig-hello/tree/${fixture.commit}`);
    assert.equal(entry.installCommand, `pig install 'git:https://github.com/example-maintainer/pig-hello.git@${fixture.commit}'`);
    assert.match(entry.notes[0], /^Third-party listing: Example hello is maintained by Example Maintainer, not by Pigpen\. Pigpen pins commit 0123456789abcdef/);
    assert.match(entry.notes[1], /^Verified with PiG 0\.5\.2 \(07a8631fb54a\) on 2026-10-22: installed from the pinned commit/);
  });

  it('is accepted by the schema in a listings directory', () => {
    const dir = tree();
    try {
      write(dir, 'listings/example-hello.json', fixture);
      assert.deepEqual(problemsOf(dir), []);
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });
});

describe('a Piglet listing', () => {
  it('is planned with no releases, and its add command pins the commit and the subdirectory', () => {
    const entry = listingEntry(piglet);
    assert.equal(entry.kind, 'piglet');
    assert.equal(entry.status, 'planned');
    assert.deepEqual(entry.releases, []);
    assert.equal(entry.addCommand, `pig piglet add 'git:https://github.com/example-maintainer/pig-hello.git@${'a'.repeat(40)}#subdirectory=piglets%2Freview'`);
    assert.equal(entry.source.url, `https://github.com/example-maintainer/pig-hello/tree/${'a'.repeat(40)}/piglets/review`);
    assert.equal(listingInstallCommand(piglet), entry.addCommand);
    assert.equal(listingSpec({ ...piglet, subdirectory: undefined }), `git:https://github.com/example-maintainer/pig-hello.git@${'a'.repeat(40)}`);
    assert.ok(entry.notes.some((note) => /publishes no Binary/.test(note)));
    assert.equal('installCommand' in entry, false);
  });

  it('may not carry a Package version or Resources', () => {
    const dir = tree();
    try {
      write(dir, 'listings/example-piglet.json', { ...piglet, version: '1.0.0' });
      assert.match(problemsOf(dir).join('\n'), /version/);
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });
});

describe('the checks', () => {
  let dir;
  beforeEach(() => { dir = tree(); });
  afterEach(() => rmSync(dir, { recursive: true, force: true }));
  const problemsFor = (changes, omit = []) => {
    const listing = { ...fixture, ...changes };
    for (const key of omit) delete listing[key];
    write(dir, 'listings/example-hello.json', listing);
    return problemsOf(dir).join('\n');
  };

  it('rejects an unpinned ref', () => {
    for (const commit of ['main', 'HEAD', 'v1.2.3', '0123456', 'A'.repeat(40), '0'.repeat(39)]) {
      assert.match(problemsFor({ commit }), /commit must be a full 40-character lowercase commit hash/, commit);
    }
    assert.match(problemsFor({}, ['commit']), /commit is missing: pin the listing to a full 40-character commit hash/);
    assert.match(problemsFor({ ref: 'main' }), /ref is not a field: pin with "commit"/);
    assert.match(problemsFor({ branch: 'main' }), /branch is not a field: pin with "commit"/);
  });

  it('rejects a missing or non-SPDX license', () => {
    assert.match(problemsFor({}, ['license']), /license is missing/);
    for (const license of ['', 'UNLICENSED', 'NONE', 'NOASSERTION', 'SEE LICENSE IN LICENSE.txt', 'LicenseRef-custom', 'MIT OR', 'AND MIT', '(MIT', 'MIT AND', 'GPL-2.0+ WITH', 'MIT/Apache-2.0']) {
      assert.match(problemsFor({ license }), /license/, JSON.stringify(license));
    }
    assert.equal(problemsFor({ license: 'Apache-2.0 OR MIT' }), '');
    assert.equal(problemsFor({ license: '(MIT AND BSD-3-Clause) OR GPL-2.0-or-later WITH Classpath-exception-2.0' }), '');
  });

  it('rejects an id that is a Pigpen component or Piglet', () => {
    write(dir, 'listings/herdr.json', { ...fixture, id: 'herdr', name: 'Third-party herdr' });
    write(dir, 'listings/pigpen-herdr.json', { ...fixture, id: 'pigpen-herdr', name: 'Other' });
    write(dir, 'listings/other.json', { ...fixture, id: 'other', name: 'Herdr' });
    const problems = problemsOf(dir).join('\n');
    assert.match(problems, /listings\/herdr\.json: herdr is the id or name of a Pigpen Piglet or component/);
    assert.match(problems, /listings\/pigpen-herdr\.json: pigpen-herdr is the id or name of a Pigpen Piglet or component/);
    assert.match(problems, /listings\/other\.json: Herdr is the id or name of a Pigpen Piglet or component/);
  });

  it('rejects a file that is not <id>.json, a bad id and Pigpen\'s own repository', () => {
    write(dir, 'listings/wrong-name.json', fixture);
    assert.match(problemsOf(dir).join('\n'), /id example-hello must equal the file name \(wrong-name\)/);
    rmSync(join(dir, 'listings/wrong-name.json'));
    write(dir, 'listings/Notes.txt', 'x');
    assert.match(problemsOf(dir).join('\n'), /listings\/Notes\.txt: listings holds only <id>\.json files/);
    rmSync(join(dir, 'listings/Notes.txt'));
    assert.match(problemsFor({ repository: 'https://github.com/MichaelKinsy/pigpen' }), /is Pigpen's own repository/);
    assert.match(problemsFor({ repository: 'https://github.com/example-maintainer/pig-hello.git' }), /repository must be https:\/\/github\.com\/<owner>\/<repo>/);
    assert.match(problemsFor({ repository: 'https://gitlab.com/a/b' }), /repository must be/);
    assert.match(problemsFor({ subdirectory: '../x' }), /subdirectory/);
    assert.match(problemsFor({ subdirectory: '/abs' }), /subdirectory/);
  });

  it('reports invalid JSON by file name', () => {
    write(dir, 'listings/example-hello.json', '{ not json');
    assert.match(problemsOf(dir).join('\n'), /listings\/example-hello\.json: not valid JSON/);
  });

  it('requires the verification record and the Package fields', () => {
    assert.match(problemsFor({}, ['verified']), /verified is missing/);
    assert.match(problemsFor({}, ['version']), /version is missing/);
    assert.match(problemsFor({}, ['resources']), /resources is missing/);
    assert.match(problemsFor({ verified: { pigVersion: '0.5', pigCommit: 'abc', date: 'yesterday' } }), /verified\.pigVersion[^\n]*\nlistings[^\n]*verified\.pigCommit[^\n]*\nlistings[^\n]*verified\.date/);
  });
});

describe('generateIndex with listings', () => {
  let dir;
  beforeEach(() => { dir = tree(); });
  afterEach(() => rmSync(dir, { recursive: true, force: true }));

  it('appends the listings after Pigpen\'s own entries, sorted by id, and moves updatedAt to a newer verification date', () => {
    write(dir, 'listings/example-piglet.json', piglet);
    write(dir, 'listings/example-hello.json', { ...fixture, verified: { ...fixture.verified, date: '2026-11-02' } });
    const index = generateIndex(dir, repository);
    assert.deepEqual(index.entries.map((entry) => [entry.id, entry.official]), [['herdr', false], ['example-hello', false], ['example-piglet', false]]);
    assert.equal(index.updatedAt, '2026-11-02');
    assert.equal(JSON.stringify(generateIndex(dir, repository)), JSON.stringify(index));
  });

  it('fails the generator, naming the file, on a listing that breaks a rule', () => {
    write(dir, 'listings/herdr.json', { ...fixture, id: 'herdr', name: 'Third-party herdr' });
    assert.throws(() => generateIndex(dir, repository), /Invalid third-party listings:\nlistings\/herdr\.json: herdr is the id or name of a Pigpen Piglet or component/);
    rmSync(join(dir, 'listings/herdr.json'));
    write(dir, 'listings/example-hello.json', { ...fixture, commit: 'main' });
    assert.throws(() => generateIndex(dir, repository), /listings\/example-hello\.json: commit must be a full 40-character/);
  });

  it('has no listings by default', () => {
    assert.deepEqual(generateIndex(dir, repository).entries.map((entry) => entry.id), ['herdr']);
  });
});

describe('validSpdx', () => {
  it('accepts expressions of license ids', () => {
    for (const text of ['MIT', 'Apache-2.0', 'GPL-2.0+', 'MIT OR Apache-2.0', '(MIT)', 'GPL-3.0-only WITH GCC-exception-3.1']) assert.equal(validSpdx(text), true, text);
  });
  it('refuses everything else', () => {
    for (const text of ['', ' MIT', 'MIT ', 'mit license', 'MIT,Apache-2.0', 'WITH', 'MIT WITH', 'NONE', 'MIT WITH Foo+']) assert.equal(validSpdx(text), false, JSON.stringify(text));
  });
});
