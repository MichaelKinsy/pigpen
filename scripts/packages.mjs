// Component Packages: the tag scheme, the install command and the committed release record behind an `available`
// index entry. Nothing here reads a secret or the network; the only external program is git.
//
// A Package `components/<name>` is released by pushing the tag `components/<name>/v<version>`. The tag has two
// slashes, so it can never match a Piglet tag (`<name>/v<version>`, one slash) or the Piglet release workflow's
// `*/v*` filter. It installs from the tag with the form PiG documents for a monorepo Package:
//   pig install 'git:https://github.com/<owner>/<repo>.git@components/<name>/v<version>#subdirectory=components%2F<name>'
// `releases/packages/<name>.json` records {tag, commit, date} once the tag exists (npm run record -- package <name> <version>);
// generate-index.mjs re-verifies each record against the git objects and builds the entry from the tagged tree.
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const root = fileURLToPath(new URL('../', import.meta.url));
const NAME = /^[a-z0-9][a-z0-9.-]*$/;
const SEMVER = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?$/;
const REPOSITORY = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;
const SHA = /^[0-9a-f]{40}$/;

export const packageTag = (name, version) => {
  if (!NAME.test(name)) throw new Error('Invalid component name: ' + name);
  if (!SEMVER.test(version)) throw new Error('Invalid version: ' + version);
  return `components/${name}/v${version}`;
};

/** {name, version} of a well-formed Package tag, else undefined. */
export function parsePackageTag(tag) {
  const m = /^components\/([a-z0-9][a-z0-9.-]*)\/v(.+)$/.exec(String(tag));
  return m && SEMVER.test(m[2]) ? { name: m[1], version: m[2] } : undefined;
}

export function packageSpec(repository, name, version) {
  if (!REPOSITORY.test(repository)) throw new Error('Invalid repository: ' + repository);
  return `git:https://github.com/${repository}.git@${packageTag(name, version)}#subdirectory=components%2F${name}`;
}

export const packageInstallCommand = (repository, name, version) => `pig install '${packageSpec(repository, name, version)}'`;

/** Every component with a package.json: [{dir, path, manifest}] sorted by directory. */
export function readPackages(directory = root) {
  return readdirSync(join(directory, 'components'), { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && existsSync(join(directory, 'components', entry.name, 'package.json')))
    .sort((a, b) => a.name.localeCompare(b.name, 'en'))
    .map(({ name }) => {
      const path = join(directory, 'components', name, 'package.json');
      return { dir: name, path, manifest: JSON.parse(readFileSync(path, 'utf8')) };
    });
}

/** {name, version} when `tag` is a Package tag whose component's manifest (in the working tree) has that version. */
export function checkPackageTag(tag, { directory = root } = {}) {
  const parsed = parsePackageTag(tag);
  if (!parsed) throw new Error(`${tag} is not a Package tag (components/<name>/v<version>)`);
  component(parsed.name, directory);
  const found = JSON.parse(readFileSync(join(directory, 'components', parsed.name, 'package.json'), 'utf8')).version;
  if (found !== parsed.version) throw new Error(`Tag ${tag} says ${parsed.version}, but the manifest has version ${found}`);
  return parsed;
}

const git = (directory, ...args) => execFileSync('git', ['-C', directory, ...args], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
const tryGit = (directory, ...args) => { try { return git(directory, ...args); } catch { return undefined; } };

function component(name, directory) {
  if (!NAME.test(String(name))) throw new Error('Invalid component name: ' + name);
  if (!existsSync(join(directory, 'components', name, 'package.json'))) throw new Error('There is no component ' + name);
}

const manifestAt = (directory, commit, name) => JSON.parse(git(directory, 'show', `${commit}:components/${name}/package.json`));

/** The commit a tag names, or undefined when the clone has no such tag. */
const tagCommit = (directory, tag) => tryGit(directory, 'rev-parse', '--verify', '--quiet', `refs/tags/${tag}^{commit}`);

/** Record that tag components/<name>/v<version> exists: {tag, commit, date}. Refuses a tag the manifest at that commit contradicts. */
export function recordPackage(name, version, { directory = root } = {}) {
  component(name, directory);
  const tag = packageTag(name, version);
  const commit = tagCommit(directory, tag);
  if (!commit) throw new Error(`The clone has no tag ${tag}. Create it and fetch it first (OWNER-ACTIONS.md).`);
  const found = manifestAt(directory, commit, name).version;
  if (found !== version) throw new Error(`Tag ${tag} points at a tree whose manifest has version ${found}, not ${version}`);
  return { tag, commit, date: git(directory, 'log', '-1', '--format=%cs', commit) };
}

/** Throws unless `record` (the contents of releases/packages/<name>.json) is exactly what the git objects confirm; returns it with name and version. */
export function verifyPackageRecord(name, record, { directory = root } = {}) {
  const keys = Object.keys(record ?? {}).sort().join(',');
  if (keys !== 'commit,date,tag') throw new Error(`Package record ${name} has unexpected or missing fields (${keys}; need commit, date, tag) for its tag`);
  const parsed = parsePackageTag(record.tag);
  if (!parsed) throw new Error(`Package record ${name}: ${record.tag} is not a Package tag`);
  if (parsed.name !== name) throw new Error(`Package record file ${name} names ${parsed.name}`);
  if (!SHA.test(record.commit)) throw new Error(`Package record ${name}: commit must be 40 hex digits`);
  if (!/^\d{4}-\d{2}-\d{2}$/.test(record.date)) throw new Error(`Package record ${name}: date must be YYYY-MM-DD`);
  if (tryGit(directory, 'cat-file', '-e', `${record.commit}^{commit}`) === undefined) {
    throw new Error(`Package record ${name}: commit ${record.commit} is not in this clone (CI must check out the full history and tags)`);
  }
  const tagged = tagCommit(directory, record.tag);
  if (!tagged) throw new Error(`Package record ${name}: this clone has no tag ${record.tag} (fetch tags)`);
  if (tagged !== record.commit) throw new Error(`Package record ${name}: tag ${record.tag} points at ${tagged}, not ${record.commit}`);
  const found = manifestAt(directory, record.commit, name).version;
  if (found !== parsed.version) throw new Error(`Package record ${name}: the tagged manifest has version ${found}, not ${parsed.version}`);
  return { ...record, name, version: parsed.version };
}

/** committed releases/packages/*.json -> Map(name -> record); empty when the tree has none. */
export function readPackageRecords(directory = root) {
  const dir = join(directory, 'releases', 'packages');
  const records = new Map();
  if (!existsSync(dir)) return records;
  for (const file of readdirSync(dir).sort()) {
    const m = /^([a-z0-9][a-z0-9.-]*)\.json$/.exec(file);
    if (!m) throw new Error('Unexpected file in releases/packages: ' + file);
    records.set(m[1], JSON.parse(readFileSync(join(dir, file), 'utf8')));
  }
  return records;
}

const RESOURCE_KEYS = [['pi', 'extensions', 'extension'], ['pi', 'skills', 'skill'], ['pi', 'prompts', 'prompt'], ['pi', 'themes', 'theme'],
  ['pig', 'hooks', 'hook'], ['pig', 'mcpServers', 'mcp'], ['pig', 'agentEnvironments', 'agent-environment']];

export function packageResources(manifest) {
  return RESOURCE_KEYS.filter(([block, key]) => Array.isArray(manifest[block]?.[key]) && manifest[block][key].length > 0).map(([, , resource]) => resource);
}

/** The `available` index entry of one released Package, built from the tree at the tagged commit. */
export function packageEntry(name, record, { directory = root, repository }) {
  const verified = verifyPackageRecord(name, record, { directory });
  const manifest = manifestAt(directory, verified.commit, name);
  const tree = git(directory, 'ls-tree', '-r', '--name-only', verified.commit, `components/${name}/`).split('\n');
  const languages = tree.some((file) => file.endsWith('/go.mod')) ? ['go'] : [];
  const resources = packageResources(manifest);
  const notes = [`Installs from the tag ${verified.tag} of the Pigpen repository with the command above; \`pig update\` keeps that tag. A later release is a new tag.`];
  if (resources.includes('extension') && languages.includes('go')) notes.push('The extension is Go and builds from source on first use, so it needs a Go toolchain; nothing is downloaded as a prebuilt Binary.');
  if (resources.length === 0) notes.push('A library: a Go module that other Packages and Piglets build against. Installing it adds no Resource.');
  return {
    id: manifest.name, kind: 'package', name: manifest.name, description: manifest.description,
    maintainer: repository.split('/')[0], official: false, featured: false, status: 'available', languages,
    repository: 'https://github.com/' + repository,
    source: { type: 'git', spec: packageSpec(repository, name, verified.version),
      url: `https://github.com/${repository}/tree/${verified.tag}/components/${name}` },
    version: verified.version, license: manifest.license, resources,
    installCommand: packageInstallCommand(repository, name, verified.version),
    piCompatibility: resources.includes('extension') ? 'not-applicable' : 'not-tested', notes,
  };
}
