// Runs the drift test of every component that has one (components/<name>/port/skills-drift.test.mjs), so the
// root test list stays the same when a Package adds its own.
import { existsSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { run } from './skills-drift.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));

test('the repository has no Skills drift', async () => {
  const results = await run({ root });
  assert.ok(results.length > 0, 'at least one Package carries a drift check');
  for (const { name, problems } of results) assert.deepEqual(problems, [], name);
});

for (const name of readdirSync(join(root, 'components')).sort()) {
  const file = join(root, 'components', name, 'port', 'skills-drift.test.mjs');
  if (existsSync(file)) await import(pathToFileURL(file).href);
}
