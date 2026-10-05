// Owner rule: every extension a Piglet selects is a Go SDK extension, so the Piglet can build a
// Binary with all extensions fused. This gate reads the authored manifests and follows each
// extension origin to its source:
//   local:<path>            a directory holding go.mod
//   package:<alias>         the Package's pi.extensions members, each a directory holding go.mod
// A Piglet that extends another is checked through its base, whose own manifest is checked too.
// go-only-exceptions.json lists the Piglets that still select a non-Go extension, with the reason.
// An exception that is no longer needed is an error, so the list only shrinks.
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseDocument } from 'yaml';

const goSources = (dir) => existsSync(join(dir, 'go.mod'));

function packageMembers(packageRoot) {
  const manifest = JSON.parse(readFileSync(join(packageRoot, 'package.json'), 'utf8'));
  const entries = manifest.pi?.extensions ?? [];
  const members = [];
  for (const entry of entries) {
    if (entry.endsWith('/*')) {
      const parent = join(packageRoot, entry.slice(0, -2));
      if (existsSync(parent)) for (const name of readdirSync(parent)) members.push(join(parent, name));
    } else members.push(join(packageRoot, entry));
  }
  return members;
}

/** Returns [{piglet, extension, source}] for every selected extension that is not Go. */
export function nonGoExtensions(directory) {
  const violations = [];
  const base = join(directory, 'piglets');
  if (!existsSync(base)) return violations;
  for (const name of readdirSync(base).sort()) {
    const path = join(base, name, 'piglet.yaml');
    if (!existsSync(path)) continue;
    const document = parseDocument(readFileSync(path, 'utf8'), { uniqueKeys: true });
    const data = document.toJS({ maxAliasCount: 0 });
    if (!data || typeof data !== 'object') continue;
    const owner = join(base, name);
    for (const extension of Array.isArray(data.extensions) ? data.extensions : []) {
      const origin = (extension.origins ?? [])[0];
      const check = (dir, label) => {
        if (!existsSync(dir) || !statSync(dir).isDirectory() || !goSources(dir)) violations.push({ piglet: name, extension: extension.name, source: label });
      };
      if (typeof origin !== 'string') continue;
      if (origin.startsWith('local:')) check(resolve(owner, origin.slice('local:'.length)), origin);
      else if (origin.startsWith('package:')) {
        const source = (data.packages ?? {})[origin.slice('package:'.length)];
        if (typeof source !== 'string' || !source.startsWith('local:')) { violations.push({ piglet: name, extension: extension.name, source: origin }); continue; }
        const members = packageMembers(resolve(owner, source.slice('local:'.length)));
        const member = members.find((m) => basename(m) === extension.name) ?? members[0];
        if (!member) violations.push({ piglet: name, extension: extension.name, source: origin });
        else check(member, `${origin} -> ${member.slice(directory.length + 1)}`);
      } else violations.push({ piglet: name, extension: extension.name, source: `${origin} (not verifiable here)` });
    }
  }
  return violations;
}

export function checkGoOnly(directory) {
  const exceptionsPath = join(directory, 'go-only-exceptions.json');
  const exceptions = existsSync(exceptionsPath) ? JSON.parse(readFileSync(exceptionsPath, 'utf8')) : {};
  const violations = nonGoExtensions(directory);
  const errors = [];
  for (const v of violations) {
    if (!exceptions[v.piglet]) errors.push(`${v.piglet}: extension ${v.extension} (${v.source}) is not a Go extension; Piglets must be Go only`);
  }
  const offenders = new Set(violations.map((v) => v.piglet));
  for (const piglet of Object.keys(exceptions)) {
    if (!offenders.has(piglet)) errors.push(`go-only-exceptions.json: ${piglet} no longer selects a non-Go extension; remove the exception`);
  }
  return { errors, excepted: violations.filter((v) => exceptions[v.piglet]) };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const directory = resolve(process.argv[2] || fileURLToPath(new URL('../', import.meta.url)));
  const { errors, excepted } = checkGoOnly(directory);
  for (const v of excepted) console.log(`excepted: ${v.piglet} selects ${v.extension} (${v.source})`);
  if (errors.length) {
    console.error(errors.join('\n'));
    process.exitCode = 1;
  } else console.log('Go-only gate passed');
}
