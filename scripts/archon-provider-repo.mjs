// Publishes an Archon plugin component as the standalone repository Archon installs from.
//
// Archon installs `archon plugin add <owner>/<repo>[/<path>][@<tag>]`: a tag may not contain `/` (its TAG regex, packages/cli/src/commands/plugin-source.ts),
// so this monorepo's component tags (`components/<name>/v<version>`) can never be named there, and with no tag Archon takes the repository's latest
// release, which in a monorepo is some other component's. The component is therefore generated into its own repository (`MichaelKinsy/<name>`), whose
// release tag is the plain `v<version>`, and whose release carries what Archon downloads: `<executable>-<os>-<arch>[.exe]` assets for linux, darwin and
// windows on x64 and arm64 (packages/paths/src/release-asset.ts) and a sha256sum-format `checksums.txt`. Archon then runs the staged binary and
// validates the descriptor it answers `initialize` with.
//
// .github/workflows/release-archon-provider.yml runs these commands on a pushed component tag:
//   node scripts/archon-provider-repo.mjs plan <component tag>             prints name, version, tag, repository, executable (and to GITHUB_OUTPUT)
//   node scripts/archon-provider-repo.mjs generate <component dir> <out>   writes the standalone repository layout into <out>
//   node scripts/archon-provider-repo.mjs assets <executable>              prints "<bun target> <asset name>" for the six targets
//   node scripts/archon-provider-repo.mjs checksums <dir> <executable>     writes <dir>/checksums.txt over the six assets
//   node scripts/archon-provider-repo.mjs handshake <binary> <component dir>  the descriptor check, the way Archon runs it
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { appendFileSync, cpSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { basename, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { checkPluginTag, pluginVersion } from './archon-plugins.mjs';
import { parsePackageTag } from './packages.mjs';

/** The six targets Archon's release-asset.ts accepts (`bun-<os>-<arch>`), each built with `bun build --compile --target=<target>`. */
export const BUN_TARGETS = ['bun-linux-x64', 'bun-linux-arm64', 'bun-darwin-x64', 'bun-darwin-arm64', 'bun-windows-x64', 'bun-windows-arm64'];

/** Archon's tag grammar, copied from packages/cli/src/commands/plugin-source.ts (`const TAG`). The test compares it with that file. */
export const ARCHON_TAG = /^[A-Za-z0-9][A-Za-z0-9._+-]*$/;

/** The one source repository the standalone repositories are generated from, and where each component is published. */
export const SOURCE_REPOSITORY = 'MichaelKinsy/pigpen';
export const STANDALONE = { 'archon-provider-pig': 'MichaelKinsy/archon-provider-pig' };

/** What the generated repository carries, relative to the component directory (README.md is generated, from the component's own). */
const COPIED = ['LICENSE', 'archon-plugin.json', 'provenance.json', 'src', 'test', 'fixtures'];

export function installLine(repository) {
  return `archon plugin add ${repository}`;
}

/** The tag of the release in the standalone repository for a component tag: components/<name>/v<version> becomes v<version>. */
export function releaseTag(componentTag) {
  const parsed = parsePackageTag(componentTag);
  if (!parsed) throw new Error(`${componentTag} is not a component tag (components/<name>/v<version>)`);
  return `v${parsed.version}`;
}

/** `<executable>-<os>-<arch>[.exe]`, the name Archon's release-asset.ts gives for a bun target. */
export function releaseAsset(executable, bunTarget) {
  const match = /^bun-(darwin|linux|windows)-(x64|arm64)$/.exec(bunTarget);
  if (!match) throw new Error(`Unsupported release target: ${bunTarget}`);
  return `${executable}-${match[1]}-${match[2]}${match[1] === 'windows' ? '.exe' : ''}`;
}

export const releaseAssets = (executable) => BUN_TARGETS.map((target) => releaseAsset(executable, target));

/** The standalone repository of a component, or a thrown error naming what is missing. */
export function standaloneRepository(name) {
  const repository = STANDALONE[name];
  if (!repository) throw new Error(`${name} has no standalone repository (scripts/archon-provider-repo.mjs STANDALONE)`);
  return repository;
}

/**
 * {name, version, tag, repository, executable} for a component tag: the tag names an Archon plugin whose answered version is the tag's
 * version (checkPluginTag), and the release tag Archon will be asked for passes Archon's own tag grammar.
 */
export function planRelease(componentTag, { directory } = {}) {
  const { name, version } = checkPluginTag(componentTag, directory ? { directory } : undefined);
  const tag = releaseTag(componentTag);
  if (!ARCHON_TAG.test(tag)) throw new Error(`${tag} is not a tag Archon accepts (${ARCHON_TAG}); Archon refuses "owner/repo@${tag}"`);
  const manifest = JSON.parse(readFileSync(join(directory ?? fileURLToPath(new URL('../', import.meta.url)), 'components', name, 'archon-plugin.json'), 'utf8'));
  return { name, version, tag, repository: standaloneRepository(name), executable: manifest.executable };
}

// Archon's manifest schema (packages/plugin-manifest/src/index.ts) is stricter than checkDescriptor: a name is lowercase words joined by "-",
// and a provider's executable is archon-provider-<id> with the id from the descriptor the binary answers.
const MANIFEST_NAME = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const PROVIDER_EXECUTABLE = /^archon-provider-[a-z0-9]+(?:-[a-z0-9]+)*$/;

/** Throws unless `manifest` is one Archon would accept for a provider plugin. */
export function checkArchonManifest(manifest) {
  if (manifest?.schemaVersion !== 1 || manifest.kind !== 'provider') throw new Error('archon-plugin.json: schemaVersion 1 and kind provider are required');
  if (!MANIFEST_NAME.test(manifest.name ?? '')) throw new Error('archon-plugin.json: name must be lowercase words joined by "-"');
  if (!PROVIDER_EXECUTABLE.test(manifest.executable ?? '')) throw new Error('archon-plugin.json: executable must be archon-provider-<id>');
}

/**
 * The repository layout Archon installs from, written into `out`: archon-plugin.json at the root (Archon reads <repo>/archon-plugin.json at the
 * release tag's commit), LICENSE, the sources and tests, and a README whose install line is the one that works. Throws on a component whose README
 * does not carry that install line, so the line a user copies is the line that was tested.
 */
export function generateRepo(componentPath, out) {
  const name = basename(componentPath);
  const repository = standaloneRepository(name);
  const manifest = JSON.parse(readFileSync(join(componentPath, 'archon-plugin.json'), 'utf8'));
  checkArchonManifest(manifest);
  if (manifest.executable !== name) throw new Error(`${name}: executable ${manifest.executable} must equal the component name (it is also the repository name)`);
  const readme = readFileSync(join(componentPath, 'README.md'), 'utf8');
  const line = installLine(repository);
  if (!readme.includes(line)) throw new Error(`${name}/README.md must carry the install line \`${line}\``);
  mkdirSync(out, { recursive: true });
  for (const entry of COPIED) cpSync(join(componentPath, entry), join(out, entry), { recursive: true });
  const note = `> This repository is generated from [${SOURCE_REPOSITORY}](https://github.com/${SOURCE_REPOSITORY}/tree/main/components/${name}) by its release workflow, one commit for each release. Send changes there.\n`;
  writeFileSync(join(out, 'README.md'), readme.replace(/^(# [^\n]*\n)/, `$1\n${note}`));
}

/** The sha256sum-format text for the six assets of `executable` in `dir`; throws if any is missing. */
export function checksumsText(dir, executable) {
  return releaseAssets(executable)
    .map((asset) => {
      const file = join(dir, asset);
      if (!existsSync(file)) throw new Error(`${file} is missing: a release carries all six assets`);
      return `${createHash('sha256').update(readFileSync(file)).digest('hex')}  ${asset}\n`;
    })
    .join('');
}

export function writeChecksums(dir, executable) {
  writeFileSync(join(dir, 'checksums.txt'), checksumsText(dir, executable));
}

/**
 * The descriptor check Archon makes after it stages a provider (core/src/providers/inspect-provider-plugin.ts, connect.ts): run the binary with a
 * minimal environment, send `initialize` over newline-delimited JSON-RPC, and read the descriptor from agentCapabilities._meta.archon. Checks what
 * Archon's installer then requires of it: protocol 3, a descriptor id that makes the manifest's executable `archon-provider-<id>`, and the version the
 * component answers. Resolves to the descriptor; rejects with the reason otherwise.
 */
export function handshake(binary, { executable, version, protocol = 3, timeoutMs = 10_000 }) {
  return new Promise((resolve, reject) => {
    const child = spawn(binary, [], { stdio: ['pipe', 'pipe', 'ignore'], env: { PATH: process.env.PATH ?? '', HOME: process.env.HOME ?? '' }, windowsHide: true });
    let buffer = '';
    let done = false;
    const finish = (error, value) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      child.stdin.destroy();
      child.kill();
      error ? reject(error) : resolve(value);
    };
    const timer = setTimeout(() => finish(new Error('initialize timed out')), timeoutMs);
    child.on('error', (error) => finish(new Error(`cannot run ${binary}: ${error.message}`)));
    child.on('close', () => finish(new Error('the binary exited before it answered initialize')));
    child.stdin.on('error', () => undefined);
    child.stdout.on('data', (data) => {
      buffer += data;
      const newline = buffer.indexOf('\n');
      if (newline < 0) return;
      try {
        const message = JSON.parse(buffer.slice(0, newline));
        if (message.id !== 1 || message.error) throw new Error(`initialize failed: ${JSON.stringify(message.error ?? message)}`);
        const descriptor = message.result?.agentCapabilities?._meta?.archon;
        if (message.result?.protocolVersion !== 1 || typeof descriptor !== 'object' || descriptor === null) throw new Error('initialize answered no descriptor');
        if (descriptor.protocol !== protocol) throw new Error(`descriptor protocol is ${descriptor.protocol}, Archon speaks ${protocol}`);
        if (executable !== `archon-provider-${descriptor.id}`) throw new Error(`descriptor id ${descriptor.id} does not make the executable ${executable}`);
        if (version !== undefined && descriptor.version !== version) throw new Error(`descriptor version ${descriptor.version} is not ${version}`);
        for (const key of ['displayName', 'capabilities', 'credentials', 'configSchema']) if (descriptor[key] === undefined) throw new Error(`descriptor has no ${key}`);
        finish(undefined, descriptor);
      } catch (error) {
        finish(error);
      }
    });
    child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: 1, clientCapabilities: { fs: { readTextFile: false, writeTextFile: false }, terminal: false, _meta: { archon: { protocol } } } } })}\n`);
  });
}

async function main([command, ...args]) {
  if (command === 'plan') {
    const tag = process.env.GITHUB_REF_TYPE === 'tag' ? process.env.GITHUB_REF_NAME : args[0];
    if (!tag) throw new Error('Not a tag: pass components/<name>/v<version>');
    const plan = planRelease(tag);
    const lines = Object.entries(plan).map(([key, value]) => `${key}=${value}\n`).join('');
    if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, lines);
    else process.stdout.write(lines);
  } else if (command === 'generate' && args.length === 2) {
    generateRepo(args[0], args[1]);
  } else if (command === 'assets' && args.length === 1) {
    for (const target of BUN_TARGETS) console.log(`${target} ${releaseAsset(args[0], target)}`);
  } else if (command === 'checksums' && args.length === 2) {
    writeChecksums(args[0], args[1]);
  } else if (command === 'handshake' && args.length === 2) {
    const manifest = JSON.parse(readFileSync(join(args[1], 'archon-plugin.json'), 'utf8'));
    const descriptor = await handshake(args[0], { executable: manifest.executable, version: pluginVersion(args[1]) });
    console.log(`descriptor ok: ${descriptor.id} ${descriptor.version} (protocol ${descriptor.protocol})`);
  } else {
    throw new Error('Usage: archon-provider-repo.mjs plan <tag> | generate <component dir> <out> | assets <executable> | checksums <dir> <executable> | handshake <binary> <component dir>');
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) await main(process.argv.slice(2));
