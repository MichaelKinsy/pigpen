import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { GOVULNCHECK_VERSION, allGoModules, classify, judge, moduleProblems, parseMessages } from './govulncheck.mjs';
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
