// The one component kind that is not a PiG Package: an Archon plugin, a directory `components/<name>/` with an
// `archon-plugin.json` (Archon's plugin descriptor) and no `package.json`. Archon, not PiG, installs and runs it, so
// `pig package validate`, the npm contract, the index and the Package release record do not apply to it. Every script
// that walks components/ asks `componentKind` and treats each kind by its own rules; a directory that is neither is an
// error, never skipped.
//
//   node scripts/archon-plugins.mjs check components/<name>   descriptor rules, then the plugin's own `bun test`
import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parsePackageTag } from './packages.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const NAME = /^[a-z0-9][a-z0-9.-]*$/;
// Archon's provider plugins are the only kind this repository ships; a new kind is a deliberate addition here.
const KINDS = ['provider'];

/** 'package' (has package.json), 'archon-plugin' (has archon-plugin.json). Neither or both is an error. */
export function componentKind(directory) {
  const pkg = existsSync(join(directory, 'package.json'));
  const plugin = existsSync(join(directory, 'archon-plugin.json'));
  if (pkg && plugin) throw new Error(`${directory}: has both package.json and archon-plugin.json; a component is a Package or an Archon plugin`);
  if (pkg) return 'package';
  if (plugin) return 'archon-plugin';
  throw new Error(`${directory}: neither package.json (a PiG Package) nor archon-plugin.json (an Archon plugin)`);
}

/** Every Archon plugin component: [{dir, path, descriptor}] sorted by directory. */
export function readArchonPlugins(directory = root) {
  return readdirSync(join(directory, 'components'), { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && existsSync(join(directory, 'components', entry.name, 'archon-plugin.json')))
    .sort((a, b) => a.name.localeCompare(b.name, 'en'))
    .map(({ name }) => {
      const path = join(directory, 'components', name);
      return { dir: name, path, descriptor: JSON.parse(readFileSync(join(path, 'archon-plugin.json'), 'utf8')) };
    });
}

/** The descriptor rules: schemaVersion 1, a known kind, a nonempty name and description, a bare executable name. Throws the first problem. */
export function checkDescriptor(descriptor, label = 'archon-plugin.json') {
  const fail = (message) => { throw new Error(`${label}: ${message}`); };
  if (typeof descriptor !== 'object' || descriptor === null || Array.isArray(descriptor)) fail('must be an object');
  const allowed = ['schemaVersion', 'kind', 'name', 'description', 'executable'];
  for (const key of Object.keys(descriptor)) if (!allowed.includes(key)) fail(`unknown field ${key}`);
  if (descriptor.schemaVersion !== 1) fail('schemaVersion must be 1');
  if (!KINDS.includes(descriptor.kind)) fail(`kind must be one of ${KINDS.join(', ')}`);
  if (typeof descriptor.name !== 'string' || !NAME.test(descriptor.name)) fail('name must be lowercase words joined by - or .');
  if (typeof descriptor.description !== 'string' || !descriptor.description.trim()) fail('description must be a nonempty string');
  if (typeof descriptor.executable !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(descriptor.executable)) fail('executable must be a bare file name (no path)');
}

/** The version the plugin answers `initialize` with: `const VERSION = '<semver>'` in src/main.ts. */
export function pluginVersion(directory) {
  const source = readFileSync(join(directory, 'src', 'main.ts'), 'utf8');
  const match = /^const VERSION = '([^']+)';$/m.exec(source);
  if (!match) throw new Error(`${directory}/src/main.ts: no \`const VERSION = '<semver>';\``);
  return match[1];
}

/** {name, version} when `tag` is components/<name>/v<version> of an Archon plugin whose answered version is that one. */
export function checkPluginTag(tag, { directory = root } = {}) {
  const parsed = parsePackageTag(tag);
  if (!parsed) throw new Error(`${tag} is not a component tag (components/<name>/v<version>)`);
  const path = join(directory, 'components', parsed.name);
  if (!existsSync(join(path, 'archon-plugin.json'))) throw new Error(`There is no Archon plugin ${parsed.name}`);
  const found = pluginVersion(path);
  if (found !== parsed.version) throw new Error(`Tag ${tag} says ${parsed.version}, but the plugin answers version ${found}`);
  return parsed;
}

/** The descriptor rules, then the plugin's own tests with bun (required: a missing bun fails, it is never skipped). */
export function validatePlugin(path, { run = spawnSync } = {}) {
  checkDescriptor(JSON.parse(readFileSync(join(path, 'archon-plugin.json'), 'utf8')), `${path}/archon-plugin.json`);
  const result = run('bun', ['test'], { cwd: path, stdio: 'inherit', shell: false });
  if (result.error) throw new Error(`${path}: bun is required to test an Archon plugin (${result.error.message})`);
  if (result.status !== 0) throw new Error(`${path}: bun test failed`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [command, target] = process.argv.slice(2);
  if (command !== 'check' || !target) throw new Error('Usage: node scripts/archon-plugins.mjs check components/<name>');
  validatePlugin(join(process.cwd(), target));
  console.log(`Archon plugin ok: ${target}`);
}
