// Tests of scripts/dev-skills-drift.mjs: the real Package passes, and each kind of drift is caught.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, describe, it } from 'node:test';
import { checkDevSkills, checkOnline, gitBlobId, renderTable, sha256, writeCredits } from './dev-skills-drift.mjs';
import { root } from './go-modules.mjs';

const scratch = [];
after(() => { for (const dir of scratch) rmSync(dir, { recursive: true, force: true }); });

/** A copy of the dev-skills Package in a scratch root, as the checker expects to find it. */
function fixture() {
  const dir = mkdtempSync(join(tmpdir(), 'dev-skills-drift-test-'));
  scratch.push(dir);
  mkdirSync(join(dir, 'components'), { recursive: true });
  cpSync(join(root, 'components/dev-skills'), join(dir, 'components/dev-skills'), { recursive: true });
  return dir;
}
const mapOf = (dir) => JSON.parse(readFileSync(join(dir, 'components/dev-skills/port/upstream-map.json'), 'utf8'));
const writeMap = (dir, map) => writeFileSync(join(dir, 'components/dev-skills/port/upstream-map.json'), JSON.stringify(map, null, 2));
const edit = (dir, rel, fn) => { const path = join(dir, 'components/dev-skills', rel); writeFileSync(path, fn(readFileSync(path, 'utf8'))); };
const mentions = (errors, text) => errors.some((e) => e.includes(text));

describe('git blob ids and hashes', () => {
  it('computes the id git gives a blob', () => {
    assert.equal(gitBlobId(Buffer.from('hello\n')), 'ce013625030ba8dba906f756967f9e9ca394464a');
    assert.equal(gitBlobId(Buffer.alloc(0)), 'e69de29bb2d1d6434b8b29ae775ad8c2e48c5391');
    assert.equal(sha256(Buffer.from('abc')), 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad');
  });
});

describe('dev-skills Package', () => {
  it('matches its upstream map', () => {
    assert.deepEqual(checkDevSkills(root), []);
  });

  it('maps each shipped file to a path and the pinned commit, with adaptations', () => {
    const map = mapOf(root);
    assert.equal(map.skills.length, 10);
    for (const row of map.skills) {
      assert.ok(map.sources[row.source], row.shipped);
      assert.ok(row.path && row.vendored && row.adaptations.length, row.shipped);
    }
    assert.equal(map.sources['mattpocock/skills'].commit, 'd81f3a183412e71a5b1e84ca21bc1a35eea03a60');
    assert.equal(map.sources['mitsuhiko/agent-stuff'].commit, '0865c849befd2021490679f96a8dee58c84ac857');
  });
});

describe('offline drift', () => {
  it('catches an edited Skill whose row was not revisited', () => {
    const dir = fixture();
    edit(dir, 'skills/pigpen-tdd/SKILL.md', (t) => `${t}\nOne more rule.\n`);
    assert.ok(mentions(checkDevSkills(dir), 'skills/pigpen-tdd/SKILL.md: edited since its row was written'));
  });

  it('catches an edited vendored upstream copy', () => {
    const dir = fixture();
    edit(dir, 'upstream/mattpocock-skills/tdd.md', (t) => t.replace('Red before green', 'Green before red'));
    assert.ok(mentions(checkDevSkills(dir), 'is not the upstream blob'));
  });

  it('catches an edited license copy', () => {
    const dir = fixture();
    edit(dir, 'upstream/mitsuhiko-agent-stuff/LICENSE', (t) => `${t} `);
    assert.ok(mentions(checkDevSkills(dir), 'license mitsuhiko/agent-stuff'));
  });

  it('catches a shipped file that has no row', () => {
    const dir = fixture();
    writeFileSync(join(dir, 'components/dev-skills/skills/pigpen-tdd/EXTRA.md'), 'extra\n');
    assert.ok(mentions(checkDevSkills(dir), 'skills/pigpen-tdd/EXTRA.md: shipped but not in upstream-map.json'));
  });

  it('catches a row whose shipped file is gone', () => {
    const dir = fixture();
    rmSync(join(dir, 'components/dev-skills/skills/pigpen-handoff'), { recursive: true });
    assert.ok(mentions(checkDevSkills(dir), 'skills/pigpen-handoff/SKILL.md: the shipped file does not exist'));
  });

  it('catches a row without adaptations', () => {
    const dir = fixture();
    const map = mapOf(dir);
    map.skills[0].adaptations = [];
    writeMap(dir, map);
    assert.ok(mentions(checkDevSkills(dir), 'list its adaptations'));
  });

  it('catches a pin that differs from provenance.json', () => {
    const dir = fixture();
    const map = mapOf(dir);
    map.sources['mattpocock/skills'].commit = '5beb35b040e3de97e54fa9e386d7e42d15c295c2';
    writeMap(dir, map);
    assert.ok(mentions(checkDevSkills(dir), 'map pins 5beb35b, provenance.json pins d81f3a1'));
  });

  it('catches a commit that is not a full id', () => {
    const dir = fixture();
    const map = mapOf(dir);
    map.sources['mattpocock/skills'].commit = 'd81f3a1';
    writeMap(dir, map);
    assert.ok(mentions(checkDevSkills(dir), 'commit must be a full 40-hex id'));
  });

  it('catches a Skill whose opening note does not name the source and the pin', () => {
    const dir = fixture();
    const map = mapOf(dir);
    edit(dir, 'skills/pigpen-handoff/SKILL.md', (t) => t.replace('d81f3a1', 'abcdef0'));
    map.skills.find((r) => r.shipped.includes('handoff')).adaptedSha256 = sha256(readFileSync(join(dir, 'components/dev-skills/skills/pigpen-handoff/SKILL.md')));
    writeMap(dir, map);
    assert.ok(mentions(checkDevSkills(dir), 'opening note must name mattpocock/skills and the pinned commit d81f3a1'));
  });

  it('catches an upstream in provenance.json that the map does not know', () => {
    const dir = fixture();
    const path = join(dir, 'components/dev-skills/provenance.json');
    const provenance = JSON.parse(readFileSync(path, 'utf8'));
    provenance.upstreams.push({ ...provenance.upstreams[0], name: 'someone/else' });
    writeFileSync(path, JSON.stringify(provenance));
    assert.ok(mentions(checkDevSkills(dir), 'upstream someone/else has no source in the map'));
  });

  it('catches a stale CREDITS table and rewrites it', () => {
    const dir = fixture();
    edit(dir, 'CREDITS.md', (t) => t.replace('| `skills/pigpen-tdd/SKILL.md`', '| `skills/pigpen-tdd/OLD.md`'));
    assert.ok(mentions(checkDevSkills(dir), 'CREDITS.md map table is out of date'));
    writeCredits(dir);
    assert.deepEqual(checkDevSkills(dir), []);
  });

  it('catches a CREDITS.md without the generated block', () => {
    const dir = fixture();
    edit(dir, 'CREDITS.md', (t) => t.replace(/<!-- dev-skills-map:begin[^>]*-->/, ''));
    assert.ok(mentions(checkDevSkills(dir), 'no generated map block'));
  });

  it('renders every shipped file, its upstream path, the short pin and each adaptation', () => {
    const map = mapOf(root);
    const table = renderTable(map);
    for (const row of map.skills) {
      assert.ok(table.includes(row.shipped) && table.includes(row.path), row.shipped);
      for (const adaptation of row.adaptations) assert.ok(table.includes(adaptation.replaceAll('|', '\\|')), adaptation);
    }
    assert.ok(table.includes('`d81f3a1`') && table.includes('`0865c84`'));
  });
});

describe('online check against an upstream repository', () => {
  const git = (cwd, ...args) => {
    const r = spawnSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@example.com', '-c', 'commit.gpgsign=false', ...args], { cwd, encoding: 'utf8' });
    assert.equal(r.status, 0, r.stderr);
    return r.stdout.trim();
  };

  /** A local upstream with one Skill at one commit, and a fixture whose map points at it. */
  function upstream() {
    const repo = mkdtempSync(join(tmpdir(), 'dev-skills-upstream-'));
    scratch.push(repo);
    git(repo, 'init', '-q', '-b', 'main');
    git(repo, 'config', 'uploadpack.allowAnySHA1InWant', 'true');
    mkdirSync(join(repo, 'skills/engineering/tdd'), { recursive: true });
    writeFileSync(join(repo, 'skills/engineering/tdd/SKILL.md'), 'tdd v1\n');
    writeFileSync(join(repo, 'LICENSE'), 'MIT\n');
    git(repo, 'add', '-A');
    git(repo, 'commit', '-q', '-m', 'one');
    const pin = git(repo, 'rev-parse', 'HEAD');
    const dir = fixture();
    const map = mapOf(dir);
    map.sources = { 'mattpocock/skills': { url: `file://${repo}`, commit: pin } };
    map.skills = [{ ...map.skills[0], source: 'mattpocock/skills', path: 'skills/engineering/tdd/SKILL.md', upstreamBlob: gitBlobId(Buffer.from('tdd v1\n')) }];
    map.licenses = { 'mattpocock/skills': { path: 'LICENSE', vendored: 'x', upstreamBlob: gitBlobId(Buffer.from('MIT\n')) } };
    writeMap(dir, map);
    return { repo, dir, pin };
  }

  it('accepts a pin whose blobs are what the map records', () => {
    const { dir } = upstream();
    assert.deepEqual(checkOnline(dir), { errors: [], drift: [] });
  });

  it('rejects a recorded blob the upstream does not have at the pin', () => {
    const { dir } = upstream();
    const map = mapOf(dir);
    map.skills[0].upstreamBlob = gitBlobId(Buffer.from('tdd v2\n'));
    writeMap(dir, map);
    const { errors } = checkOnline(dir);
    assert.ok(mentions(errors, 'skills/engineering/tdd/SKILL.md at'), errors.join('\n'));
  });

  it('rejects a path that does not exist at the pin', () => {
    const { dir } = upstream();
    const map = mapOf(dir);
    map.skills[0].path = 'skills/engineering/tdd/MISSING.md';
    writeMap(dir, map);
    assert.ok(mentions(checkOnline(dir).errors, 'does not exist at'));
  });

  it('rejects a commit the upstream cannot serve', () => {
    const { dir } = upstream();
    const map = mapOf(dir);
    map.sources['mattpocock/skills'].commit = '0123456789012345678901234567890123456789';
    writeMap(dir, map);
    assert.ok(mentions(checkOnline(dir).errors, 'cannot fetch'));
  });

  it('lists a path that changed upstream after the pin, with --latest only', () => {
    const { repo, dir } = upstream();
    writeFileSync(join(repo, 'skills/engineering/tdd/SKILL.md'), 'tdd v2\n');
    git(repo, 'add', '-A');
    git(repo, 'commit', '-q', '-m', 'two');
    assert.deepEqual(checkOnline(dir), { errors: [], drift: [] });
    const { errors, drift } = checkOnline(dir, { latest: true });
    assert.deepEqual(errors, []);
    assert.equal(drift.length, 1);
    assert.ok(drift[0].includes('skills/engineering/tdd/SKILL.md: changed upstream since'), drift[0]);
  });

  it('reports no drift while upstream is unchanged', () => {
    const { dir } = upstream();
    assert.deepEqual(checkOnline(dir, { latest: true }), { errors: [], drift: [] });
  });
});
