import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import test from 'node:test';
import { checkGoOnly } from './go-only.mjs';

const put = (root, path, text) => {
  mkdirSync(dirname(join(root, path)), { recursive: true });
  writeFileSync(join(root, path), text);
};

function fixture(build) {
  const root = mkdtempSync(join(tmpdir(), 'pigpen-goonly-'));
  try {
    build(root);
    return checkGoOnly(root);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

test('go-only gate', async (t) => {
  await t.test('accepts local Go and Go Packages, ignores Skill-only and derived Piglets', () => {
    const { errors } = fixture((root) => {
      put(root, 'components/tool/package.json', JSON.stringify({ pi: { extensions: ['extensions/tool'] } }));
      put(root, 'components/tool/extensions/tool/go.mod', 'module x\n');
      put(root, 'piglets/a/piglet.yaml', 'name: a\npackages:\n  tool: local:../../components/tool\nextensions:\n  - name: tool\n    origins: [package:tool]\n  - name: own\n    origins: [local:./extensions/own]\n');
      put(root, 'piglets/a/extensions/own/go.mod', 'module y\n');
      put(root, 'piglets/b/piglet.yaml', 'name: b\nextends:\n  source: local:../a/piglet.yaml\n');
      put(root, 'piglets/c/piglet.yaml', 'name: c\nskills: []\n');
    });
    assert.deepEqual(errors, []);
  });
  await t.test('rejects a Node extension, in a Package and locally', () => {
    const { errors } = fixture((root) => {
      put(root, 'components/n/package.json', JSON.stringify({ pi: { extensions: ['extensions/n'] } }));
      put(root, 'components/n/extensions/n/extension.ts', 'export default () => {}');
      put(root, 'piglets/a/piglet.yaml', 'name: a\npackages:\n  n: local:../../components/n\nextensions:\n  - name: n\n    origins: [package:n]\n  - name: l\n    origins: [local:./extensions/l]\n');
      put(root, 'piglets/a/extensions/l/index.js', '');
    });
    assert.equal(errors.length, 2);
    assert.match(errors[0], /a: extension n .* not a Go extension/);
  });
  await t.test('an exception excuses a Piglet and a stale exception is an error', () => {
    const build = (exceptions) => (root) => {
      put(root, 'components/n/package.json', JSON.stringify({ pi: { extensions: ['extensions/n'] } }));
      put(root, 'components/n/extensions/n/extension.ts', '');
      put(root, 'piglets/a/piglet.yaml', 'name: a\npackages:\n  n: local:../../components/n\nextensions:\n  - name: n\n    origins: [package:n]\n');
      put(root, 'piglets/g/piglet.yaml', 'name: g\nextensions: []\n');
      put(root, 'go-only-exceptions.json', JSON.stringify(exceptions));
    };
    assert.deepEqual(fixture(build({ a: 'temporary' })).errors, []);
    const stale = fixture(build({ a: 'temporary', g: 'old' })).errors;
    assert.equal(stale.length, 1);
    assert.match(stale[0], /g no longer selects a non-Go extension/);
  });
  await t.test('remote origins cannot be verified and fail', () => {
    const { errors } = fixture((root) => put(root, 'piglets/a/piglet.yaml', 'name: a\nextensions:\n  - name: r\n    origins: [npm:some-extension]\n'));
    assert.match(errors[0], /not verifiable/);
  });
});
