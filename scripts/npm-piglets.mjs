// The npm form of a Piglet: a publishable source package `@pi-in-go/pigpen-piglet-<name>` (keyword `pig-piglet`) that
// `pig piglet add npm:<package>` registers. PiG reads the Piglet from `pig.piglet` in package.json, and refuses local Resource
// sources and `extends` of a local file in a remote source, so the generator (never a hand edit) makes dist/npm/piglets/<name>
// from the authored piglets/<name>:
//   - each `local:../../components/<dir>` Package becomes `npm:@pi-in-go/pigpen-<dir>@^<version of that Package>`;
//   - `extends` is flattened into one manifest;
//   - an extension that only exists as a local fixture (`local:./extensions/seed-check`, an empty build-pipeline fixture the
//     Binary needs and the source does not) is dropped.
// The signed Binaries stay on GitHub Releases (`pig piglet pull github:<repo>/<name>@<version>`); the README says so.
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { isMap, parseDocument, stringify } from 'yaml';
import { readManifests, root } from './generate-index.mjs';
import { npmName } from './npm-packages.mjs';
import { readPackages } from './packages.mjs';

export const pigletNpmName = (name) => `@pi-in-go/pigpen-piglet-${name}`;
const LOCAL_PACKAGE = /^local:(?:\.\.\/){2}components\/([a-z0-9][a-z0-9.-]*)\/?$/;

/** The registered-source manifest of one Piglet as plain data, plus what was changed and why. */
export function npmPigletSource(name, { directory = root } = {}) {
  const manifests = readManifests(directory);
  const found = manifests.find(({ manifest }) => manifest.name === name);
  if (!found) throw new Error('There is no Piglet ' + name);
  const versions = new Map(readPackages(directory).map(({ dir, manifest }) => [dir, manifest.version]));
  const notes = [];
  let data = found.manifest;
  if (data.extends) {
    const baseName = /^local:\.\.\/([a-z0-9][a-z0-9.-]*)\/piglet\.yaml$/.exec(data.extends.source)?.[1];
    const base = manifests.find(({ manifest }) => manifest.name === baseName)?.manifest;
    if (!base) throw new Error(`${name}: cannot flatten extends ${data.extends.source}`);
    const { extends: _extends, ...own } = data;
    data = { ...base, ...own };
    notes.push(`extends ${baseName} is flattened into this manifest`);
  }
  data = structuredClone(data);
  for (const [alias, source] of Object.entries(data.packages ?? {})) {
    const m = LOCAL_PACKAGE.exec(source);
    if (m) {
      if (!versions.has(m[1])) throw new Error(`${name}: ${source} is not a component`);
      data.packages[alias] = `npm:${npmName(m[1])}@^${versions.get(m[1])}`;
    } else if (!/^npm:/.test(source)) throw new Error(`${name}: Package ${alias} (${source}) has no npm form`);
  }
  if (Array.isArray(data.extensions)) {
    const keep = data.extensions.filter((extension) => !(extension.origins ?? []).every((origin) => origin.startsWith('local:')));
    if (keep.length !== data.extensions.length) notes.push('local fixture extensions are dropped (a remote source takes no local Resource)');
    data.extensions = keep;
  }
  for (const kind of ['extensions', 'skills', 'prompts']) {
    for (const entry of data[kind] ?? []) for (const origin of entry.origins ?? []) {
      if (origin.startsWith('local:')) throw new Error(`${name}: ${kind} ${entry.name} has the local origin ${origin}, which a remote source cannot take`);
    }
  }
  return { data, notes, metadata: found.metadata };
}

/** package.json of the npm Piglet source. */
export function pigletPackageJson(name, { directory = root, repository } = {}) {
  const { data, metadata } = npmPigletSource(name, { directory });
  return {
    name: pigletNpmName(name), version: data.release.version, description: data.description,
    keywords: ['pig-piglet', 'pigpen', ...(metadata.languages ?? [])], license: 'MIT', author: 'Michael Kinsy',
    repository: { type: 'git', url: `git+https://github.com/${repository}.git`, directory: `piglets/${name}` },
    homepage: `https://github.com/${repository}/tree/main/piglets/${name}#readme`,
    bugs: { url: `https://github.com/${repository}/issues` },
    publishConfig: { access: 'public' },
    files: ['piglet.yaml', 'README.md', 'LICENSE', 'provenance.json'],
    pig: { piglet: 'piglet.yaml' },
  };
}

const binaryNote = (name, version, repository) => `\n## Signed Binary\n\nThis npm package is the Piglet's **source**: \`pig piglet add npm:${pigletNpmName(name)}\` registers it, and its Packages install from npm. The signed per-platform **Binary** is published separately on GitHub Releases (release \`${name}/v${version}\`):\n\n\`\`\`\npig piglet pull 'github:${repository}/${name}@${version}'\n\`\`\`\n`;

/** Generate dist/npm/piglets/<name>; returns the directory. */
export function buildPigletTree(name, outRoot, { directory = root, repository = JSON.parse(readFileSync(join(directory, 'catalog.config.json'), 'utf8')).repository } = {}) {
  const { data, notes } = npmPigletSource(name, { directory });
  const target = join(outRoot, name);
  rmSync(target, { recursive: true, force: true });
  mkdirSync(target, { recursive: true });
  const header = `# GENERATED by scripts/npm-piglets.mjs from piglets/${name}/piglet.yaml. Do not edit.${notes.map((n) => `\n# ${n}.`).join('')}\n`;
  writeFileSync(join(target, 'piglet.yaml'), header + stringify(data, { lineWidth: 0 }));
  const readme = join(directory, 'piglets', name, 'README.md');
  writeFileSync(join(target, 'README.md'), (existsSync(readme) ? readFileSync(readme, 'utf8') : `# ${name}\n`).trimEnd() + '\n' + binaryNote(name, data.release.version, repository));
  cpSync(join(directory, 'LICENSE'), join(target, 'LICENSE'));
  const provenance = join(directory, 'piglets', name, 'provenance.json');
  writeFileSync(join(target, 'provenance.json'), existsSync(provenance) ? readFileSync(provenance) : '{}\n');
  writeFileSync(join(target, 'package.json'), JSON.stringify(pigletPackageJson(name, { directory, repository }), null, 2) + '\n');
  return target;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  const config = JSON.parse(readFileSync(join(root, 'catalog.config.json'), 'utf8'));
  const out = join(root, 'dist/npm/piglets');
  for (const { manifest } of readManifests()) console.log(buildPigletTree(manifest.name, out, { repository: config.repository }));
}
