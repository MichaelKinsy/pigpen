import { createHash } from 'node:crypto';
import { existsSync, lstatSync, readFileSync, readdirSync, realpathSync } from 'node:fs';
import { basename, dirname, isAbsolute, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import Ajv from 'ajv';
import { isMap, parseDocument } from 'yaml';

const root = realpathSync(resolve(process.argv[2] || fileURLToPath(new URL('../', import.meta.url))));
// .upstream is the untracked symlink every lane worktree carries to its upstream sources; a checkout has none.
const ignored = new Set(['.git', 'node_modules', '.pig-src', '.pig-bin', 'dist', '.upstream']);
const kinds = ['extensions', 'skills', 'prompts', 'themes', 'hooks', 'mcp', 'libraries', 'assets'];
const text = { type: 'string', pattern: '\\S' };
const authors = { type: 'array', minItems: 1, uniqueItems: true, items: text };
const license = { ...text, not: { pattern: '^(UNLICENSED|NOASSERTION|UNKNOWN)$' } };
const ajv = new Ajv({ allErrors: true });
const source = {
  type: 'object', additionalProperties: false,
  required: ['name', 'authors', 'url', 'revision', 'license', 'licenseFile', 'attributionFile'],
  properties: { name: text, authors, url: text, revision: { type: 'string', pattern: '^[a-f0-9]{40}$' },
    path: text, license, licenseFile: text, attributionFile: text },
};
const validProvenance = ajv.compile({
  type: 'object', additionalProperties: false,
  required: ['origin', 'authors', 'license', 'licenseFile', 'upstreams'],
  properties: {
    origin: { enum: ['original', 'ported'] }, authors, license, licenseFile: text,
    // Work this one is derived from: `path` (the vendored original inside the Package) is required.
    upstreams: { type: 'array', items: { ...source, required: [...source.required, 'path'] } },
    // Libraries the work links or redistributes without being derived from them (a Go SDK it builds on):
    // credited and licensed like an upstream, but they do not make the work a port.
    dependencies: { type: 'array', items: source },
  },
});

function walk(directory) {
  return readdirSync(directory).sort().flatMap(name => {
    if (ignored.has(name)) return [];
    const path = join(directory, name);
    const stat = lstatSync(path);
    if (stat.isSymbolicLink()) throw new Error(`${relative(root, path)}: symlinks are not distributable source`);
    if (stat.isDirectory()) return walk(path);
    if (!stat.isFile()) throw new Error(`${relative(root, path)}: expected a regular file`);
    return [path];
  });
}

function localPath(owner, value, boundary = root) {
  const path = resolve(owner, value);
  const rel = relative(boundary, path);
  if (isAbsolute(value) || rel === '..' || rel.startsWith('../')) throw new Error(`${value}: path escapes ${relative(root, boundary) || 'repository'}`);
  if (realpathSync(path) !== path) throw new Error(`${value}: metadata paths must not traverse symlinks`);
  return path;
}

function nonemptyFile(owner, value) {
  const path = localPath(owner, value);
  const content = readFileSync(path, 'utf8');
  if (!content.trim()) throw new Error(`${relative(root, path)}: file is empty`);
  return content;
}

function checkProvenance(owner) {
  const path = join(owner, 'provenance.json');
  const data = JSON.parse(readFileSync(path, 'utf8'));
  if (!validProvenance(data)) throw new Error(`${relative(root, path)}: ${ajv.errorsText(validProvenance.errors)}`);
  if ((data.origin === 'original') !== (data.upstreams.length === 0)) throw new Error(`${relative(root, path)}: original requires no upstreams; ported requires upstreams`);
  nonemptyFile(owner, data.licenseFile);
  for (const source of [...data.upstreams, ...(data.dependencies ?? [])]) {
    const url = new URL(source.url);
    if (url.protocol !== 'https:' || url.username || url.password) throw new Error(`${relative(root, path)}: upstream url must be HTTPS without credentials`);
    if (source.path !== undefined) localPath(owner, source.path, owner);
    nonemptyFile(owner, source.licenseFile);
    const credit = nonemptyFile(owner, source.attributionFile);
    for (const value of [source.name, source.url, ...source.authors]) {
      if (!credit.includes(value)) throw new Error(`${relative(root, path)}: attribution must include ${value}`);
    }
  }
}

try {
  const files = walk(root);
  const skills = new Map();
  for (const path of files.filter(path => basename(path) === 'SKILL.md')) {
    const label = relative(root, path);
    const raw = readFileSync(path, 'utf8').replace(/^\uFEFF/, '').replaceAll('\r\n', '\n');
    const match = /^---\n([\s\S]*?)\n---(?:\n|$)/.exec(raw);
    if (!match) throw new Error(`${label}: missing or unclosed YAML frontmatter`);
    const doc = parseDocument(match[1], { uniqueKeys: true });
    if (doc.errors.length || doc.warnings.length) throw new Error(`${label}: YAML: ${[...doc.errors, ...doc.warnings].map(e => e.message).join('; ')}`);
    if (!isMap(doc.contents)) throw new Error(`${label}: frontmatter must be a mapping`);
    const data = doc.toJS({ maxAliasCount: 0 });
    if (typeof data.name !== 'string' || !/^pigpen-[a-z0-9]+(?:-[a-z0-9]+)*$/.test(data.name) || data.name.length > 64) throw new Error(`${label}: name must use pigpen- and lowercase hyphenated words, at most 64 characters`);
    if (typeof data.description !== 'string' || !data.description.trim()) throw new Error(`${label}: description must be a nonempty string`);
    if (basename(dirname(path)) !== data.name) throw new Error(`${label}: directory must match skill name`);
    if (skills.has(data.name)) throw new Error(`Duplicate skill ${data.name}: ${skills.get(data.name)} and ${label}`);
    skills.set(data.name, label);
  }

  const owners = ['piglets', 'components'].flatMap(kind => {
    const directory = join(root, kind);
    if (!existsSync(directory)) return [];
    return readdirSync(directory, { withFileTypes: true }).filter(entry => entry.isDirectory()).map(entry => join(directory, entry.name));
  });
  for (const owner of owners) checkProvenance(owner);
  const names = new Map();
  const hashes = new Map();
  let components = 0;
  for (const owner of owners) {
    for (const kind of kinds) {
      const directory = join(owner, kind);
      if (!existsSync(directory)) continue;
      for (const entry of readdirSync(directory, { withFileTypes: true })) {
        if (/^(README|LICENSE|NOTICE|CREDITS)(\.|$)/i.test(entry.name)) continue;
        const path = join(directory, entry.name);
        const label = relative(root, path);
        const name = `${kind}/${entry.name}`;
        if (names.has(name)) throw new Error(`Duplicate component name ${name}: ${names.get(name)} and ${label}; reference one owner`);
        names.set(name, label);
        const members = entry.isDirectory() ? walk(path) : [path];
        const payload = members.filter(file => !/^(README|LICENSE|NOTICE|CREDITS)(\.|$)/i.test(basename(file)) &&
          !['provenance.json', 'package.json', 'package-lock.json', 'go.mod', 'go.sum', 'Cargo.toml', 'Cargo.lock'].includes(basename(file)));
        if (!payload.length) continue;
        const hash = createHash('sha256');
        for (const file of payload) {
          hash.update(JSON.stringify([entry.isDirectory() ? relative(path, file) : '', readFileSync(file).toString('base64')]));
        }
        const digest = hash.digest('hex');
        if (hashes.has(digest)) throw new Error(`Duplicate component content: ${hashes.get(digest)} and ${label}; reference one owner`);
        hashes.set(digest, label);
        components++;
      }
    }
  }
  console.log(`Quality gates passed: ${skills.size} skills, ${owners.length} provenance records, ${components} components`);
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
