import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { GOVULNCHECK_VERSION, acceptedProblems, allGoModules, classify, judge, loadAccepted, moduleProblems, parseMessages } from './govulncheck.mjs';
import { goModules, root } from './go-modules.mjs';

const script = fileURLToPath(new URL('./govulncheck.mjs', import.meta.url));

// govulncheck's -format json: pretty-printed objects back to back, with braces inside strings.
const stream = (...objects) => objects.map((o) => JSON.stringify(o, null, 2)).join('\n');
const config = { config: { protocol_version: 'v1.0.0', scanner_name: 'govulncheck', scan_level: 'symbol', scan_mode: 'source' } };
const osv = (id) => ({ osv: { id, summary: `summary of ${id} with a } brace and a \\" quote` } });
const called = (id) => ({ finding: { osv: id, fixed_version: 'v1.2.3', trace: [{ module: 'm', package: 'm/p', function: 'F', position: { filename: 'x.go', line: 1 } }] } });
const imported = (id) => ({ finding: { osv: id, trace: [{ module: 'm', package: 'm/p' }] } });

describe('govulncheck gate', () => {
  it('pins a release, not a moving ref', () => {
    assert.match(GOVULNCHECK_VERSION, /^v\d+\.\d+\.\d+$/);
  });

  it('splits the concatenated JSON objects, ignoring braces and quotes inside strings', () => {
    const messages = parseMessages(stream(config, osv('GO-1'), called('GO-1')));
    assert.equal(messages.length, 3);
    assert.equal(messages[1].osv.summary, 'summary of GO-1 with a } brace and a \\" quote');
    assert.throws(() => parseMessages('{"config": {'), /ends inside an object/);
    assert.throws(() => parseMessages('}'), /unbalanced/);
  });

  it('fails a module on a finding with a called function and only reports the imported ones', () => {
    const verdict = classify(parseMessages(stream(config, osv('GO-1'), osv('GO-2'), called('GO-1'), imported('GO-1'), imported('GO-2'))));
    assert.deepEqual(verdict.called.map((v) => [v.id, v.fixed, v.symbols]), [['GO-1', 'v1.2.3', ['m/p.F']]]);
    assert.deepEqual(verdict.imported, ['GO-2']);
    assert.equal(judge('/m', { code: 0, stdout: stream(config, osv('GO-1'), called('GO-1')), stderr: '' }).status, 'called');
    assert.equal(judge('/m', { code: 0, stdout: stream(config, osv('GO-2'), imported('GO-2')), stderr: '' }).status, 'ok');
    assert.equal(judge('/m', { code: 0, stdout: stream(config), stderr: '' }).status, 'ok');
  });

  describe('recorded exceptions', () => {
    const ollama = (id, extra = {}) => ({ finding: { osv: id, trace: [{ module: 'github.com/ollama/ollama', version: 'v0.40.13', package: 'github.com/ollama/ollama/api', function: 'init', ...extra }] } });
    const adapter = join(root, 'components/kagent/harness/adapter');
    const record = { directory: 'components/kagent/harness/adapter', module: 'github.com/ollama/ollama', version: 'v0.40.13', ids: ['GO-2025-0001', 'GO-2025-0002'], reason: 'the reason is written down here in full, with the evidence', traces: ['m.main calls p.init'] };
    const run = (dir, ...objects) => judge(dir, { code: 0, stdout: stream(config, osv('GO-2025-0001'), ...objects), stderr: '' }, [record]);

    it('excuses an init-only finding of the recorded module, version, id and directory, and says so', () => {
      const verdict = run(adapter, ollama('GO-2025-0001'));
      assert.equal(verdict.status, 'ok');
      assert.deepEqual(verdict.excused.map((v) => v.id), ['GO-2025-0001']);
    });
    it('does not excuse a call into the module, another version, another id, another module, another directory or a finding with a fix', () => {
      assert.equal(run(adapter, ollama('GO-2025-0001'), ollama('GO-2025-0001', { function: 'Generate' })).status, 'called');
      assert.equal(run(adapter, ollama('GO-2025-0001', { version: 'v0.40.15' })).status, 'called');
      assert.equal(run(adapter, ollama('GO-2025-0009')).status, 'called');
      assert.equal(run(join(root, 'components/kagent/harness'), ollama('GO-2025-0001')).status, 'called');
      assert.equal(run(adapter, ollama('GO-2025-0001', { module: 'github.com/other/ollama' })).status, 'called');
      const fixed = ollama('GO-2025-0001');
      fixed.finding.fixed_version = 'v0.41.0';
      assert.equal(run(adapter, fixed).status, 'called');
    });
    it('keeps the reachable findings of a module that also has an excused one', () => {
      const verdict = run(adapter, ollama('GO-2025-0001'), called('GO-2025-0003'));
      assert.equal(verdict.status, 'called');
      assert.deepEqual(verdict.called.map((v) => v.id), ['GO-2025-0003']);
      assert.deepEqual(verdict.excused.map((v) => v.id), ['GO-2025-0001']);
    });
    it('refuses a record that is vague, and the repository record is complete and names a module that exists', () => {
      assert.deepEqual(acceptedProblems([record]), []);
      assert.equal(acceptedProblems([{ ...record, version: 'latest' }, { ...record, ids: [] }, { ...record, reason: 'ok' }, { ...record, traces: [] }, { ...record, directory: 'components/nope' }]).length, 5);
      assert.deepEqual(acceptedProblems(loadAccepted()), []);
    });
  });

  it('treats a module that did not load as a failure, not as a pass', () => {
    const load = judge('/m', { code: 1, stdout: '', stderr: 'govulncheck: loading packages: reading go.mod at revision components/typesafe/v0.0.0: unknown revision' });
    assert.equal(load.status, 'failed');
    assert.match(load.detail, /unknown revision/);
    assert.equal(judge('/m', { code: 0, stdout: '', stderr: '' }).status, 'failed', 'no config message: nothing was scanned');
    assert.equal(judge('/m', { code: 0, stdout: '{"config": {', stderr: '' }).status, 'failed');
  });

  it('lists every go.mod of the repository, which includes every module the tooling lists', () => {
    const all = allGoModules();
    assert.ok(all.length >= goModules().length);
    assert.deepEqual(moduleProblems(all, goModules()), []);
    assert.deepEqual(moduleProblems(['/a'], ['/a', join(root, 'components/x')]).length, 1);
  });

  it('finds a go.mod anywhere below the root, nested ones and fixtures included, and skips node_modules', () => {
    const dir = mkdtempSync(join(tmpdir(), 'govulncheck-test-'));
    try {
      for (const sub of ['a', 'a/b/testdata/c', 'node_modules/x']) {
        mkdirSync(join(dir, sub), { recursive: true });
        writeFileSync(join(dir, sub, 'go.mod'), 'module m\n');
      }
      assert.deepEqual(allGoModules(dir), [join(dir, 'a'), join(dir, 'a/b/testdata/c')]);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it('refuses an argument that is not an os/arch target before touching Go', () => {
    const result = spawnSync(process.execPath, [script, 'android'], { encoding: 'utf8' });
    assert.equal(result.status, 2);
    assert.match(result.stderr, /not an os\/arch target: android/);
  });
});
