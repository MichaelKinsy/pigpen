// Lists every upstream test title per test file (the twin contract). Usage:
//   node scripts/list-upstream-tests.mjs <pi-web-access checkout> > port/upstream-tests.json
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
const root = process.argv[2];
const dir = join(root, 'test');
const out = {};
for (const file of readdirSync(dir).filter((f) => f.endsWith('.test.mjs')).sort()) {
  const src = readFileSync(join(dir, file), 'utf8');
  const titles = [];
  const re = /^\s*(?:test|it)(?:\.skip|\.todo)?\(\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|`((?:[^`\\]|\\.)*)`)/gm;
  for (let m; (m = re.exec(src)); ) {
    const raw = m[1] ?? m[2] ?? m[3];
    titles.push(raw.replace(/\\(["'`\\])/g, '$1'));
  }
  out[file.replace(/\.test\.mjs$/, '')] = titles;
}
process.stdout.write(JSON.stringify(out, null, 1) + '\n');
