import { chmodSync, copyFileSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, realpathSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, join, relative, resolve } from 'node:path';
import { isMap, parseDocument } from 'yaml';
import { readManifests, root } from './generate-index.mjs';

// Stage local source only. PiG remains responsible for validating and running the resulting manifests.
const excluded = new Set(['.git', 'node_modules', 'dist']);
function sourceFiles(path) {
  const stat = lstatSync(path);
  if (stat.isSymbolicLink()) throw new Error(`${path}: source symlinks cannot be staged`);
  if (stat.isFile()) return [{ path, mode: stat.mode & 0o777 }];
  if (!stat.isDirectory()) throw new Error(`${path}: expected a regular file or directory`);
  return readdirSync(path).sort().filter(name => !excluded.has(name)).flatMap(name => sourceFiles(join(path, name)));
}

// Optional selections: a Piglet may list extensions it does not select by default in piglets/<name>/optional.json
// ({"<selection>": {"description": "...", "extensions": [{"name", "origins", "tools"?}]}}). They are OFF unless the stage
// is asked for them with `--with <piglet>:<selection>` (repeatable) or PIGPEN_WITH=<piglet>:<selection>[,...]; the default
// staged manifest is the authored one, byte for byte apart from the Package rewrites.
function parseArguments(argv, env) {
  const withs = (env.PIGPEN_WITH || '').split(',').map((entry) => entry.trim()).filter(Boolean);
  const positional = [];
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === '--with') withs.push(argv[++i] ?? '');
    else if (argv[i].startsWith('-')) throw new Error(`unknown option ${argv[i]}`);
    else positional.push(argv[i]);
  }
  if (positional.length > 1) throw new Error('Usage: node scripts/stage-piglets.mjs [--with <piglet>:<selection>]... [repository-root]');
  for (const entry of withs) if (!/^[a-z0-9][a-z0-9.-]*:[a-z0-9][a-z0-9.-]*$/.test(entry)) throw new Error(`--with needs <piglet>:<selection>, got "${entry}"`);
  return { root: positional[0], withs };
}

try {
  const options = parseArguments(process.argv.slice(2), process.env);
  const used = new Set();
  const directory = realpathSync(resolve(options.root || root));
  const dist = join(directory, 'dist');
  const output = join(dist, 'staged');
  for (const path of [dist, output]) {
    const stat = lstatSync(path, { throwIfNoEntry: false });
    if (stat && (stat.isSymbolicLink() || !stat.isDirectory())) throw new Error(`${path}: staging output must be a directory, not a symlink`);
  }

  const copies = sourceFiles(join(directory, 'LICENSE')).map(file => ({ ...file, destination: 'LICENSE' }));
  const manifests = readManifests(directory).map(({ path, raw, manifest }) => {
    const owner = dirname(path);
    const destination = join('piglets', manifest.name);
    for (const file of sourceFiles(owner)) {
      copies.push({ ...file, destination: join(destination, relative(owner, file.path)) });
    }
    const document = parseDocument(raw, { uniqueKeys: true });
    if (document.errors.length || document.warnings.length) throw new Error(`${path}: invalid YAML: ${[...document.errors, ...document.warnings].join('; ')}`);
    const packages = document.get('packages');
    if (packages !== undefined && !isMap(packages)) throw new Error(`${path}: packages must be a mapping`);
    const data = document.toJS({ maxAliasCount: 0 });
    for (const [alias, source] of Object.entries(data.packages || {})) {
      if (!/^[a-z0-9][a-z0-9.-]*$/.test(alias)) throw new Error(`${path}: invalid Package alias ${alias}`);
      if (typeof source !== 'string') throw new Error(`${path}: Package ${alias} must have a string source`);
      if (/^(npm:|git:|https?:|ssh:)/.test(source)) continue;
      const locator = source.replace(/^local:/, '');
      if (isAbsolute(locator) || locator.includes('\\') || !/^\.{1,2}\//.test(locator)) {
        throw new Error(`${path}: local Package ${alias} must use a relative path`);
      }
      const packageRoot = resolve(owner, locator);
      const owned = relative(owner, packageRoot).split('\\').join('/');
      if (owned && owned !== '..' && !owned.startsWith('../')) {
        if (!lstatSync(packageRoot).isDirectory()) throw new Error(`${packageRoot}: local Package must be a directory`);
        continue;
      }
      const shared = relative(directory, packageRoot).split('\\').join('/');
      if (!/^components\/[a-z0-9][a-z0-9.-]*$/.test(shared)) {
        throw new Error(`${path}: shared Package ${alias} must select exactly components/<name> inside the repository`);
      }
      if (realpathSync(packageRoot) !== packageRoot) throw new Error(`${packageRoot}: shared Package path must not traverse symlinks`);
      if (!lstatSync(packageRoot).isDirectory()) throw new Error(`${packageRoot}: shared Package must be a directory`);
      const stagedPackage = join('packages', alias);
      if (existsSync(join(owner, stagedPackage))) throw new Error(`${path}: generated Package destination collision at ${stagedPackage}`);
      for (const file of sourceFiles(packageRoot)) {
        copies.push({ ...file, destination: join(destination, stagedPackage, relative(packageRoot, file.path)) });
      }
      document.setIn(['packages', alias], `local:./packages/${alias}`);
    }
    for (const entry of options.withs.filter((value) => value.startsWith(`${manifest.name}:`))) {
      const selection = entry.slice(manifest.name.length + 1);
      const optionalPath = join(owner, 'optional.json');
      const optional = existsSync(optionalPath) ? JSON.parse(readFileSync(optionalPath, 'utf8')) : {};
      if (!Object.hasOwn(optional, selection)) throw new Error(`${path}: no optional selection "${selection}" (declared: ${Object.keys(optional).join(', ') || 'none'})`);
      const existing = new Set((data.extensions || []).map((extension) => extension.name));
      for (const extension of optional[selection].extensions) {
        if (existing.has(extension.name)) throw new Error(`${path}: optional selection ${selection} adds extension ${extension.name}, which the manifest already selects`);
        for (const origin of extension.origins) {
          const alias = /^package:(.+)$/.exec(origin)?.[1];
          if (alias === undefined || !Object.hasOwn(data.packages || {}, alias)) throw new Error(`${path}: optional selection ${selection} names origin ${origin}, which the manifest does not declare`);
        }
        document.addIn(['extensions'], document.createNode(extension));
      }
      used.add(entry);
    }
    return { destination: join(destination, 'piglet.yaml'), text: '# Generated by npm run stage. Edit the authored composition, not this copy.\n' + document.toString() };
  });

  for (const entry of options.withs) if (!used.has(entry)) throw new Error(`--with ${entry}: no Piglet named ${entry.split(':')[0]}`);

  // Prepare the complete replacement before removing the previous generated tree.
  mkdirSync(dist, { recursive: true });
  const temporary = mkdtempSync(join(dist, '.stage-'));
  try {
    for (const file of copies) {
      const target = join(temporary, file.destination);
      mkdirSync(dirname(target), { recursive: true });
      copyFileSync(file.path, target);
      chmodSync(target, file.mode);
    }
    for (const manifest of manifests) writeFileSync(join(temporary, manifest.destination), manifest.text);
    rmSync(output, { recursive: true, force: true });
    renameSync(temporary, output);
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
  console.log(`Staged ${manifests.length} Piglets under ${output}${options.withs.length ? ` (optional: ${options.withs.join(', ')})` : ''}`);
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
