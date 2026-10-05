#!/usr/bin/env node
// Download the pinned PiG release for a platform, check its checksum, unpack it and check that the binary reports the
// pinned version. CI jobs on every OS use this instead of building pig, so they run the pig users install.
//
//   node scripts/fetch-pig.mjs [--target linux/amd64] [--out .pig-bin] [--github-env]
//
// The pin is `release` in scripts/pig-requirement.json: the release tag, the source commit its Piglet builds need, and the
// SHA-256 of each archive (the release's SHA256SUMS, re-checked by hashing a download of every archive). Nothing is
// extracted or run unless the checksum matches. `--github-env` appends PIG_BIN=<path> to $GITHUB_ENV for later steps.
import { createHash } from 'node:crypto';
import { appendFileSync, chmodSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { hostTarget } from './host-target.mjs';
import { parsePigVersion, pigVersionMatches } from './pig-bin.mjs';

const requirementFile = fileURLToPath(new URL('./pig-requirement.json', import.meta.url));

export function readRequirement(file = requirementFile) {
  return JSON.parse(readFileSync(file, 'utf8'));
}

/** The archive's bytes, as downloaded, or an Error that names the URL and the status. */
async function download(url) {
  const response = await fetch(url, { redirect: 'follow' });
  if (!response.ok) throw new Error(`${url}: HTTP ${response.status}`);
  return Buffer.from(await response.arrayBuffer());
}

/** tar reads .tar.gz everywhere; a .zip needs Windows' own bsdtar, which a Git Bash PATH hides behind GNU tar. */
function extract(archive, into) {
  const windowsTar = process.platform === 'win32' && process.env.SystemRoot ? join(process.env.SystemRoot, 'System32', 'tar.exe') : 'tar';
  const result = spawnSync(archive.endsWith('.zip') ? windowsTar : 'tar', ['-xf', archive, '-C', into], { encoding: 'utf8' });
  if (result.error || result.status !== 0) throw new Error(`cannot unpack ${archive}: ${result.error?.message ?? result.stderr}`);
}

/**
 * Fetch, verify and unpack the pinned release for `target`. Returns the absolute path of the pig executable.
 * `baseUrl` replaces the release download URL (tests serve an archive locally).
 */
export async function fetchPig({ target = hostTarget().target, out = '.pig-bin', baseUrl, requirement = readRequirement() } = {}) {
  const release = requirement.release;
  const asset = release?.assets?.[target];
  if (!asset) throw new Error(`no pinned PiG release asset for ${target} (pinned: ${Object.keys(release?.assets ?? {}).join(', ') || 'none'})`);
  const url = `${baseUrl ?? `https://github.com/${release.repository}/releases/download/${release.tag}`}/${asset.file}`;
  const bytes = await download(url);
  const sha256 = createHash('sha256').update(bytes).digest('hex');
  if (sha256 !== asset.sha256) throw new Error(`${asset.file}: sha256 is ${sha256}, the pin says ${asset.sha256}; refusing to unpack it`);
  const dir = resolve(out);
  mkdirSync(dir, { recursive: true });
  const archive = join(dir, asset.file);
  writeFileSync(archive, bytes);
  extract(archive, dir);
  rmSync(archive);
  const folder = asset.file.replace(/\.(tar\.gz|zip)$/, '');
  const exe = join(dir, folder, target.startsWith('windows/') ? 'pig.exe' : 'pig');
  if (!existsSync(exe)) throw new Error(`${asset.file} has no ${exe.slice(dir.length + 1)}`);
  if (!target.startsWith('windows/')) chmodSync(exe, 0o755);
  // Run it only when it is this machine's target: the pin is the version the release was made as.
  if (target === hostTarget().target) {
    const run = spawnSync(exe, ['--version'], { encoding: 'utf8', env: { ...process.env, PIG_OFFLINE: '1', PI_SKIP_VERSION_CHECK: '1' } });
    const found = parsePigVersion(run.stdout);
    if (run.status !== 0 || !found || !pigVersionMatches(found, parsePigVersion(requirement.pig))) {
      throw new Error(`${exe} reports ${JSON.stringify((run.stdout ?? '').trim().split('\n')[0])}, the pin is ${requirement.pig}`);
    }
  }
  return exe;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  const args = process.argv.slice(2);
  const value = (flag) => { const i = args.indexOf(flag); return i === -1 ? undefined : args[i + 1]; };
  try {
    const exe = await fetchPig({ target: value('--target'), out: value('--out') });
    console.log(exe);
    if (args.includes('--github-env')) {
      if (!process.env.GITHUB_ENV) throw new Error('--github-env needs $GITHUB_ENV');
      appendFileSync(process.env.GITHUB_ENV, `PIG_BIN=${exe}\n`);
    }
  } catch (error) {
    console.error(`fetch-pig: ${error.message}`);
    process.exitCode = 1;
  }
}
