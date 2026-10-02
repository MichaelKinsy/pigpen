// The ports list: ports/ports.json (schema: ports/ports.schema.json) is the dispatch list for every
// Pigpen port; ports/README.md is generated from it. scripts/ports.mjs validates, renders and updates it.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { checkPorts, pinFetchArgs, provenanceSkeleton, renderReadme, setStatus, validatePorts } from './ports.mjs';

const repo = join(dirname(fileURLToPath(import.meta.url)), '..');
const SHA = 'a'.repeat(40);
const SHA2 = 'b'.repeat(40);

const upstream = (over = {}) => ({
  name: 'pi-thing', url: 'https://github.com/example/pi-thing', commit: SHA, license: 'MIT', author: 'Ada Author', ...over,
});
const row = (over = {}) => ({
  id: 'thing', name: 'Thing', kind: 'port', summary: 'Does a thing.', upstream: upstream(),
  targetPackage: 'components/thing', lane: 'pigpen-thing', status: 'queued',
  credit: 'Ported from pi-thing by Ada Author (MIT), https://github.com/example/pi-thing.', ...over,
});
const list = (...rows) => ({ schemaVersion: 1, updatedAt: '2026-09-30', ports: rows });
const errorsOf = (data, options) => validatePorts(data, options);
const rejects = (data, pattern, options) => {
  const errors = errorsOf(data, options);
  assert.ok(errors.some((e) => pattern.test(e)), `expected an error matching ${pattern}, got ${JSON.stringify(errors)}`);
};

function tree(files) {
  const dir = mkdtempSync(join(tmpdir(), 'pigpen-ports-'));
  for (const [path, text] of Object.entries(files)) {
    mkdirSync(dirname(join(dir, path)), { recursive: true });
    writeFileSync(join(dir, path), typeof text === 'string' ? text : JSON.stringify(text, null, 2) + '\n');
  }
  return dir;
}
const cli = (root, ...args) => spawnSync(process.execPath, [join(repo, 'scripts/ports.mjs'), '--root', root, ...args], { encoding: 'utf8' });

test('schema and semantic rules', async (t) => {
  await t.test('accepts a complete row, an original row, and an original that credits an asset', () => {
    assert.deepEqual(errorsOf(list(row())), []);
    assert.deepEqual(errorsOf(list(row({ id: 'own', kind: 'original', upstream: null, targetPackage: 'components/own', credit: 'Original work by Michael Kinsy (MIT).' }))), []);
    assert.deepEqual(errorsOf(list(row({ kind: 'original', additionalUpstreams: [upstream({ name: 'sprites', url: 'https://github.com/example/sprites', commit: SHA2, author: 'Bo' })], credit: 'pi-thing by Ada Author https://github.com/example/pi-thing; sprites by Bo https://github.com/example/sprites.' }))), []);
  });
  await t.test('every required field is required, unknown fields are rejected', () => {
    for (const key of ['id', 'name', 'kind', 'summary', 'targetPackage', 'lane', 'status', 'credit']) {
      const r = row();
      delete r[key];
      rejects(list(r), new RegExp(key));
    }
    rejects(list(row({ surprise: true })), /additional/i);
    rejects({ ...list(row()), extra: 1 }, /additional/i);
  });
  await t.test('id, status, kind and lane are constrained', () => {
    rejects(list(row({ id: 'Not Kebab' })), /id/);
    rejects(list(row({ status: 'finished' })), /status/);
    rejects(list(row({ kind: 'fork' })), /kind/);
    rejects(list(row({ lane: 'has space' })), /lane/);
    rejects(list(row(), row({ targetPackage: 'components/other' })), /duplicate id "thing"/);
  });
  await t.test('targets stay inside components/ or piglets/ and are unique', () => {
    for (const bad of ['../x', 'components/../x', '/abs/x', 'src/thing', 'components/a/b', 'components/']) rejects(list(row({ targetPackage: bad })), /targetPackage/);
    rejects(list(row(), row({ id: 'two', lane: 'pigpen-two' })), /target "components\/thing" is owned by both/);
    rejects(list(row(), row({ id: 'two', lane: 'pigpen-two', targetPackage: 'components/two', otherTargets: ['components/thing'] })), /target "components\/thing" is owned by both/);
    assert.deepEqual(errorsOf(list(row({ otherTargets: ['piglets/thing'] }))), []);
  });
  await t.test('upstream identity: full lowercase commit, https without credentials, real license', () => {
    for (const commit of ['abc1234', 'A'.repeat(40), 'g'.repeat(40), SHA + 'a']) rejects(list(row({ upstream: upstream({ commit }) })), /commit/);
    rejects(list(row({ upstream: upstream({ url: 'http://github.com/example/pi-thing' }) })), /url/);
    rejects(list(row({ upstream: upstream({ url: 'https://tok:en@github.com/example/pi-thing' }), credit: 'pi-thing Ada Author https://tok:en@github.com/example/pi-thing' })), /url/);
    for (const license of ['NOASSERTION', 'UNKNOWN', 'UNLICENSED', '']) rejects(list(row({ upstream: upstream({ license }) })), /license/);
    rejects(list(row({ upstream: upstream({ author: ' ' }) })), /author/);
  });
  await t.test('port, adapter and move rows need an upstream; original rows may have none', () => {
    for (const kind of ['port', 'adapter', 'move']) rejects(list(row({ kind, upstream: null })), /needs an upstream/);
    assert.deepEqual(errorsOf(list(row({ kind: 'original', upstream: null, credit: 'Original.' }))), []);
  });
  await t.test('the credit text names the upstream, its URL and its author (as the provenance gate requires)', () => {
    rejects(list(row({ credit: 'Ported from something.' })), /credit must include "pi-thing"/);
    rejects(list(row({ credit: 'pi-thing https://github.com/example/pi-thing' })), /credit must include "Ada Author"/);
    rejects(list(row({ credit: 'pi-thing Ada Author' })), /credit must include "https:\/\/github.com\/example\/pi-thing"/);
    rejects(list(row({ additionalUpstreams: [upstream({ name: 'second', url: 'https://github.com/example/second', author: 'Cy' })] })), /credit must include "second"/);
  });
  await t.test('priority is for queued rows only', () => {
    assert.deepEqual(errorsOf(list(row({ priority: 2 }))), []);
    rejects(list(row({ status: 'porting', priority: 2 })), /priority/);
    rejects(list(row({ priority: 0 })), /priority/);
  });
});

test('cross-checks against the repository tree', async (t) => {
  const prov = (upstreams, extra = {}) => ({ origin: 'ported', authors: ['x'], license: 'MIT', licenseFile: 'LICENSE', upstreams, ...extra });
  const pu = (over = {}) => ({ name: 'pi-thing', authors: ['Ada Author'], url: 'https://github.com/example/pi-thing', revision: SHA, path: 'p', license: 'MIT', licenseFile: 'L', attributionFile: 'C', ...over });
  await t.test('a matching provenance record passes; a status that is done needs the target to exist', () => {
    const root = tree({ 'components/thing/provenance.json': prov([pu()]) });
    try {
      assert.deepEqual(errorsOf(list(row({ status: 'review' })), { root }), []);
      assert.deepEqual(errorsOf(list(row({ status: 'done' })), { root }), []);
      rejects(list(row({ status: 'done', targetPackage: 'components/missing' })), /done.*components\/missing.*does not exist/, { root });
      assert.deepEqual(errorsOf(list(row({ status: 'porting', targetPackage: 'components/missing' })), { root }), [], 'unstarted targets need not exist');
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
  await t.test('a provenance upstream missing from the row, at another commit, or at another license is an error', () => {
    const root = tree({
      'components/thing/provenance.json': prov([pu({ revision: SHA2 })]),
      'components/lic/provenance.json': prov([pu({ license: 'Apache-2.0' })]),
      'components/extra/provenance.json': prov([pu(), pu({ name: 'other', url: 'https://github.com/example/other' })]),
    });
    try {
      rejects(list(row()), /provenance pins .*pi-thing.* at bbbb.*row pins aaaa/, { root });
      rejects(list(row({ targetPackage: 'components/lic' })), /license "Apache-2.0".*row says "MIT"/, { root });
      rejects(list(row({ targetPackage: 'components/extra' })), /provenance upstream https:\/\/github.com\/example\/other is not in the row/, { root });
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
  await t.test('a done row whose Package has no provenance.json is an error', () => {
    const root = tree({ 'components/thing/README.md': '# thing\n' });
    try { rejects(list(row({ status: 'done' })), /done but components\/thing\/provenance\.json is missing/, { root }); } finally { rmSync(root, { recursive: true, force: true }); }
  });
  await t.test('upstream URLs match ignoring case, a .git suffix and a trailing slash', () => {
    const root = tree({ 'components/thing/provenance.json': prov([pu({ url: 'https://GitHub.com/example/pi-thing.git' })]), 'components/slash/provenance.json': prov([pu({ url: 'https://github.com/example/pi-thing/' })]) });
    try {
      assert.deepEqual(errorsOf(list(row()), { root }), []);
      assert.deepEqual(errorsOf(list(row({ targetPackage: 'components/slash' })), { root }), []);
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
  await t.test('a port or move whose Package provenance does not record the row\'s upstream is an error', () => {
    const root = tree({ 'components/thing/provenance.json': prov([]), 'components/moved/provenance.json': prov([]), 'components/own/provenance.json': prov([], { origin: 'original' }) });
    try {
      rejects(list(row()), /thing: components\/thing\/provenance\.json does not record the row's upstream https:\/\/github\.com\/example\/pi-thing/, { root });
      rejects(list(row({ kind: 'move', targetPackage: 'components/moved' })), /does not record the row's upstream/, { root });
      // An adapter credits its SDK in the row and CREDITS.md; the provenance schema has no slot for it yet.
      assert.deepEqual(errorsOf(list(row({ kind: 'adapter', targetPackage: 'components/own' })), { root }), []);
      assert.deepEqual(errorsOf(list(row({ kind: 'original', upstream: null, targetPackage: 'components/own', credit: 'Original.' })), { root }), []);
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
  await t.test('provenance dependencies (an SDK the package builds on) are checked the same way', () => {
    const root = tree({ 'components/thing/provenance.json': prov([], { origin: 'original', dependencies: [pu({ revision: SHA2 })] }) });
    try { rejects(list(row({ kind: 'adapter' })), /provenance pins .*at bbbb/, { root }); } finally { rmSync(root, { recursive: true, force: true }); }
  });
});

test('README rendering', async (t) => {
  const data = list(
    row({ id: 'b-second', targetPackage: 'components/b', lane: 'pigpen-b', status: 'porting', summary: 'pipe | in summary' }),
    row({ id: 'a-first', targetPackage: 'components/a', lane: 'pigpen-a', status: 'queued', priority: 1, kind: 'original', upstream: null, credit: 'Original by Michael Kinsy.' }),
  );
  await t.test('one table row per port, in file order, with every dispatch column', () => {
    const text = renderReadme(data);
    assert.match(text, /^# Pigpen ports\n/);
    assert.match(text, /generated from \[`ports\.json`\]\(ports\.json\)/i);
    const rows = text.split('\n').filter((l) => /^\| `(a-first|b-second)` /.test(l));
    assert.equal(rows.length, 2);
    assert.ok(text.indexOf('`b-second`') < text.indexOf('`a-first`'));
    assert.match(text, /\| ID \| Upstream \| Pinned commit \| License \| Upstream author \| Target Package \| Lane \| Status \| Credit \|/);
    assert.match(rows[0], /\[pi-thing\]\(https:\/\/github\.com\/example\/pi-thing\)/);
    assert.match(rows[0], /\[`aaaaaaaaaaaa`\]\(https:\/\/github\.com\/example\/pi-thing\/commit\/a{40}\)/);
    assert.match(rows[0], /\| MIT \| Ada Author \| `components\/b` \| `pigpen-b` \| porting \|/);
    assert.match(rows[1], /\| — \| — \| MIT \(Pigpen\) \| Michael Kinsy \|/);
  });
  await t.test('pipes in text cannot break the table; output is deterministic and ends with a newline', () => {
    const text = renderReadme(data);
    assert.ok(text.includes('pipe \\| in summary'));
    for (const l of text.split('\n').filter((l) => /^\| `/.test(l))) assert.equal(l.replace(/\\\|/g, '').split('|').length, 11, l);
    assert.equal(text, renderReadme(JSON.parse(JSON.stringify(data))));
    assert.ok(text.endsWith('\n') && !text.endsWith('\n\n'));
  });
  await t.test('status counts and the queue order are stated', () => {
    const text = renderReadme(data);
    assert.match(text, /queued: 1/);
    assert.match(text, /porting: 1/);
    assert.match(text, /review: 0/);
    assert.match(text, /done: 0/);
    assert.match(text, /Queue order.*\n[\s\S]*1\. `a-first`/);
  });
  await t.test('checkPorts fails on a stale README, a missing file and invalid data', () => {
    const files = { 'ports/ports.json': data, 'ports/ports.schema.json': readFileSync(join(repo, 'ports/ports.schema.json'), 'utf8'), 'ports/README.md': renderReadme(data) };
    const root = tree(files);
    try {
      assert.deepEqual(checkPorts(root).errors, []);
      writeFileSync(join(root, 'ports/README.md'), renderReadme(data).replace('porting', 'done'));
      assert.match(checkPorts(root).errors.join('\n'), /ports\/README\.md is stale: run npm run generate/);
      rmSync(join(root, 'ports/README.md'));
      assert.match(checkPorts(root).errors.join('\n'), /ports\/README\.md is missing/);
      writeFileSync(join(root, 'ports/ports.json'), JSON.stringify(list(row({ status: 'nope' }))));
      assert.match(checkPorts(root).errors.join('\n'), /status/);
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
});

test('status updates', async (t) => {
  const base = list(row({ status: 'queued' }));
  await t.test('follows queued, porting, review, done, and allows rework and abandon', () => {
    const at = (from, to) => setStatus(list(row({ status: from })), 'thing', to).ports[0].status;
    assert.equal(at('queued', 'porting'), 'porting');
    assert.equal(at('porting', 'review'), 'review');
    assert.equal(at('review', 'done'), 'done');
    assert.equal(at('review', 'porting'), 'porting');
    assert.equal(at('porting', 'queued'), 'queued');
    assert.equal(at('done', 'review'), 'review');
  });
  await t.test('refuses to skip a stage, an unknown id or an unknown status; setting the same status is a no-op', () => {
    assert.throws(() => setStatus(base, 'thing', 'review'), /queued to review/);
    assert.throws(() => setStatus(base, 'thing', 'done'), /queued to done/);
    assert.throws(() => setStatus(base, 'nope', 'porting'), /no port row "nope"/);
    assert.throws(() => setStatus(base, 'thing', 'shipped'), /status/);
    assert.equal(setStatus(base, 'thing', 'queued').ports[0].status, 'queued');
  });
  await t.test('is pure, drops the queue priority once work starts, and can record a note', () => {
    const queued = list(row({ priority: 3 }));
    const next = setStatus(queued, 'thing', 'porting', 'started by lane pigpen-thing');
    assert.equal(queued.ports[0].status, 'queued');
    assert.equal(next.ports[0].priority, undefined);
    assert.equal(next.ports[0].notes, 'started by lane pigpen-thing');
    assert.equal(setStatus(next, 'thing', 'review', 'second').ports[0].notes, 'started by lane pigpen-thing; second');
    assert.deepEqual(errorsOf(next), []);
  });
  await t.test('a note after a sentence reads as a sentence, not ".;"', () => {
    const next = setStatus(list(row({ notes: 'Port from the spec.' })), 'thing', 'porting', 'lane started');
    assert.equal(next.ports[0].notes, 'Port from the spec. lane started.');
    assert.equal(setStatus(list(row({ notes: 'No stop' })), 'thing', 'porting', 'lane started.').ports[0].notes, 'No stop; lane started.');
  });
});

test('provenance skeleton for the porting Skill', () => {
  const sk = provenanceSkeleton(row({ additionalUpstreams: [upstream({ name: 'second', url: 'https://github.com/example/second', commit: SHA2, license: 'Apache-2.0', author: 'Cy' })] }));
  assert.equal(sk.origin, 'ported');
  assert.deepEqual(sk.upstreams.map((u) => [u.name, u.url, u.revision, u.license, u.authors]), [
    ['pi-thing', 'https://github.com/example/pi-thing', SHA, 'MIT', ['Ada Author']],
    ['second', 'https://github.com/example/second', SHA2, 'Apache-2.0', ['Cy']],
  ]);
  assert.equal(sk.upstreams[0].attributionFile, 'CREDITS.md');
  assert.equal(provenanceSkeleton(row({ kind: 'original', upstream: null })).origin, 'original');
  assert.deepEqual(provenanceSkeleton(row({ kind: 'original', upstream: null })).upstreams, []);
});

test('verify-pins never hands credentials to an upstream host', () => {
  const saved = { GIT_ASKPASS: process.env.GIT_ASKPASS, SSH_ASKPASS: process.env.SSH_ASKPASS };
  process.env.GIT_ASKPASS = '/usr/bin/some-askpass';
  process.env.SSH_ASKPASS = '/usr/bin/some-askpass';
  let args, env;
  try { ({ args, env } = pinFetchArgs('/tmp/x', 'https://github.com/example/pi-thing', SHA)); } finally {
    for (const [k, v] of Object.entries(saved)) if (v === undefined) delete process.env[k]; else process.env[k] = v;
  }
  const fetchAt = args.indexOf('fetch');
  assert.ok(fetchAt > 0, JSON.stringify(args));
  const before = args.slice(0, fetchAt);
  assert.ok(before.join(' ').includes('-c credential.helper='), 'credential helpers are disabled');
  assert.ok(before.join(' ').includes('-c core.askPass='), 'no askpass program');
  assert.equal(env.GIT_TERMINAL_PROMPT, '0');
  assert.equal(env.GCM_INTERACTIVE, 'never');
  assert.equal(env.GIT_ASKPASS, undefined);
  assert.equal(env.SSH_ASKPASS, undefined);
  assert.deepEqual(args.slice(-2), ['https://github.com/example/pi-thing', SHA]);
});

test('command line', async (t) => {
  const files = () => ({ 'ports/ports.json': list(row({ priority: 1 })), 'ports/ports.schema.json': readFileSync(join(repo, 'ports/ports.schema.json'), 'utf8'), 'ports/README.md': renderReadme(list(row({ priority: 1 }))) });
  await t.test('check passes on a fresh tree and fails, non-zero, on drift', () => {
    const root = tree(files());
    try {
      const ok = cli(root, 'check');
      assert.equal(ok.status, 0, ok.stderr);
      assert.match(ok.stdout, /Ports list passed: 1 ports/);
      writeFileSync(join(root, 'ports/README.md'), 'old\n');
      const bad = cli(root, 'check');
      assert.equal(bad.status, 1);
      assert.match(bad.stderr, /stale/);
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
  await t.test('generate rewrites the README; --check-like drift then passes', () => {
    const root = tree({ ...files(), 'ports/README.md': 'old\n' });
    try {
      assert.equal(cli(root, 'generate').status, 0);
      assert.equal(readFileSync(join(root, 'ports/README.md'), 'utf8'), renderReadme(list(row({ priority: 1 }))));
      assert.equal(cli(root, 'check').status, 0);
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
  await t.test('show prints one row as JSON (the porting Skill input); unknown ids fail', () => {
    const root = tree(files());
    try {
      const shown = cli(root, 'show', 'thing');
      assert.equal(shown.status, 0, shown.stderr);
      assert.equal(JSON.parse(shown.stdout).upstream.commit, SHA);
      const missing = cli(root, 'show', 'nope');
      assert.equal(missing.status, 1);
      assert.match(missing.stderr, /no port row "nope"/);
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
  await t.test('set-status updates ports.json and regenerates the README in one step', () => {
    const root = tree(files());
    try {
      const done = cli(root, 'set-status', 'thing', 'porting', '--note', 'lane started');
      assert.equal(done.status, 0, done.stderr);
      const data = JSON.parse(readFileSync(join(root, 'ports/ports.json'), 'utf8'));
      assert.equal(data.ports[0].status, 'porting');
      assert.equal(data.ports[0].notes, 'lane started');
      assert.equal(cli(root, 'check').status, 0);
      const skip = cli(root, 'set-status', 'thing', 'done');
      assert.equal(skip.status, 1);
      assert.match(skip.stderr, /porting to done/);
      assert.equal(JSON.parse(readFileSync(join(root, 'ports/ports.json'), 'utf8')).ports[0].status, 'porting');
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
  await t.test('list filters by status and provenance prints the skeleton', () => {
    const root = tree(files());
    try {
      assert.match(cli(root, 'list', '--status', 'queued').stdout, /^thing\tqueued\tcomponents\/thing\tpigpen-thing$/m);
      assert.equal(cli(root, 'list', '--status', 'done').stdout.trim(), '');
      assert.equal(JSON.parse(cli(root, 'provenance', 'thing').stdout).upstreams[0].revision, SHA);
      assert.equal(cli(root, 'bogus').status, 2);
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
});

const upstreams = (p) => [...(p.upstream ? [p.upstream] : []), ...(p.additionalUpstreams ?? [])];

test('the real ports list', async (t) => {
  const data = JSON.parse(readFileSync(join(repo, 'ports/ports.json'), 'utf8'));
  const ids = new Set(data.ports.map((p) => p.id));
  await t.test('validates against the schema and the repository tree, and its README is current', () => {
    const { errors } = checkPorts(repo);
    assert.deepEqual(errors, []);
  });
  await t.test('seeds every current port and move and the queued ports', () => {
    const seeded = ['herdr', 'extension-porter', 'dirty-repo-guard', 'web-search', 'acp', 'ahp', 'a2a', 'jev', 'typesafe-client', 'pi-typesafe', 'pi-warden',
      'angry-pigs', 'pig-runner', 'pig-play', 'pig-snake', 'session-ingest', 'context-info', 'skills',
      'rpiv-ask-user-question', 'rpiv-todo', 'plannotator', 'ponytail', 'langfuse-observability', 'pi-subagents'];
    for (const id of seeded) assert.ok(ids.has(id), `missing row ${id}`);
    assert.ok(!ids.has('pig-login'), 'the sprite login and /sprite are built into PiG 0.4.0');
    assert.equal(ids.size, data.ports.length);
  });
  await t.test('the queued rows follow the top-installs order; MCP is not listed', () => {
    const queued = data.ports.filter((p) => p.status === 'queued' && p.priority).sort((a, b) => a.priority - b.priority).map((p) => p.id);
    assert.deepEqual(queued, ['rpiv-todo', 'rpiv-ask-user-question', 'plannotator', 'ponytail', 'langfuse-observability']);
    assert.ok(![...ids].some((id) => /mcp/.test(id)), 'MCP is built into PiG');
  });
  await t.test('pins the pieces the running lanes already fixed', () => {
    const by = Object.fromEntries(data.ports.map((p) => [p.id, p]));
    assert.equal(by['web-search'].upstream.commit, '9a734ed195da2f4cccc2fb5e7128f6774a380f47');
    assert.equal(by.acp.upstream.commit, 'b0581c9c1d675e634234674484247008b03d69b4');
    assert.equal(by.jev.upstream.commit, '88e5fb3888948e7065110d47cdf6ac57abb71ba4');
    assert.equal(by.a2a.upstream.commit, 'ebf17c56ef7e63c72883a45454a538bbc0df66b8');
    assert.equal(by['typesafe-client'].additionalUpstreams[0].name, 'system-one-adapter-python');
  });
  await t.test('credits the copyright holder the upstream LICENSE names', () => {
    const by = Object.fromEntries(data.ports.map((p) => [p.id, p]));
    // pi-typesafe and pi-warden: LICENSE says "Copyright (c) 2026 Ryan Gapac"; DevMortimer is the GitHub account.
    for (const id of ['pi-typesafe', 'pi-warden']) {
      assert.match(by[id].upstream.author, /Ryan Gapac/, id);
      assert.match(by[id].credit, /Ryan Gapac/, id);
    }
  });
  await t.test('targets are the Packages the running lanes build', () => {
    const by = Object.fromEntries(data.ports.map((p) => [p.id, p]));
    assert.equal(by['pi-warden'].targetPackage, 'components/warden');
    const skills = by.skills;
    assert.equal(skills.targetPackage, 'components/dev-skills');
    assert.equal(skills.kind, 'port', 'the dev Skills are adaptations of public Skills');
    assert.deepEqual(upstreams(skills).map((u) => [u.url, u.commit, u.license]), [
      ['https://github.com/mattpocock/skills', 'd81f3a183412e71a5b1e84ca21bc1a35eea03a60', 'MIT'],
      ['https://github.com/mitsuhiko/agent-stuff', '0865c849befd2021490679f96a8dee58c84ac857', 'Apache-2.0'],
    ]);
  });
  await t.test('names no lane-local path, private source or private organisation', () => {
    const text = JSON.stringify(data);
    for (const bad of [/(^|[\s"(])tasks\//, /pig-lanes/, /~\//, /\/tmp\//, /\/home\//, /private pig-stuff/i, /mainst/i]) assert.doesNotMatch(text, bad);
  });
});
