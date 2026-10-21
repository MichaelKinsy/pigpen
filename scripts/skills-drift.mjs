// Drift check for Skills Packages that carry an original: runs every components/<name>/port/skills-drift.mjs.
// Each exports check({ root, upstream }) -> list of problems; no problems, no drift. `--upstream <name>=<git checkout>`
// also compares that Package's vendored original with the pinned upstream commit (network or a local clone; not run in CI).
import { existsSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';

export async function run({ root, upstream = {} }) {
  const results = [];
  const base = join(root, 'components');
  for (const name of readdirSync(base).sort()) {
    const module = join(base, name, 'port', 'skills-drift.mjs');
    if (!existsSync(module)) continue;
    const { check } = await import(pathToFileURL(module).href);
    results.push({ name, problems: check({ root, upstream: upstream[name] }) });
  }
  return results;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const root = resolve(fileURLToPath(new URL('../', import.meta.url)));
  const upstream = {};
  const args = process.argv.slice(2);
  for (let i = 0; i < args.length; i += 1) {
    if (args[i] !== '--upstream') throw new Error(`unknown argument ${args[i]}`);
    const [name, dir] = String(args[(i += 1)]).split('=');
    upstream[name] = resolve(dir);
  }
  const results = await run({ root, upstream });
  let failed = 0;
  for (const { name, problems } of results) {
    for (const problem of problems) console.error(`skills drift: ${name}: ${problem}`);
    failed += problems.length;
  }
  if (failed) process.exit(1);
  console.log(`Skills drift check passed: ${results.map((r) => r.name).join(', ')}`);
}
