// Install one Package from its tag the way a user does and prove it: the exact command the index shows, then
// `pig package validate` on the installed root, `--validate-only` (build, start, register) on each Go extension, and remove.
//   node scripts/package-smoke.mjs <name> <version>        PIG_BIN, GITHUB_REPOSITORY (owner/repo)
// release-package.yml runs it against the pushed tag; package-install.test.mjs runs it for every Package against a local
// clone that git serves for the same URL. Isolated: a scratch HOME and agent directory, never the user's.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { registersNativeProvider } from './go-modules.mjs';
import { checkPackageTag, packageSpec, packageTag } from './packages.mjs';
import { requirePig } from './pig-bin.mjs';

export function run(pig, env, cwd, ...args) {
  const result = spawnSync(pig, args, { cwd, env, encoding: 'utf8' });
  assert.equal(result.status, 0, `pig ${args.join(' ')}\n${result.stdout}${result.stderr}`);
  return result.stdout + result.stderr;
}

/** The directory `pig package list` reports for a spec. */
export function installedRoot(pig, env, cwd, spec) {
  const lines = run(pig, env, cwd, 'package', 'list').split('\n');
  const at = lines.findIndex((line) => line.trim() === spec);
  assert.ok(at >= 0, `${spec} is not listed:\n${lines.join('\n')}`);
  const path = lines[at + 1].trim();
  assert.ok(existsSync(join(path, 'package.json')), `${path} has no package.json`);
  return path;
}

/** Install `name` at `version` from `repository`'s tag into the isolated `env`, check it, remove it. Returns the installed root. */
export function smokePackage({ pig, env, cwd, repository, name, version, spec = packageSpec(repository, name, version) }) {
  run(pig, env, cwd, 'install', spec);
  const installed = installedRoot(pig, env, cwd, spec);
  const manifest = JSON.parse(readFileSync(join(installed, 'package.json'), 'utf8'));
  assert.equal(manifest.version, version, `${spec} installed version ${manifest.version}`);
  run(pig, env, cwd, 'package', 'validate', installed);
  // A Go extension must also build and register under pig, which `package validate` does not do.
  // An extension that registers a native Provider cannot load under --validate-only (PiG 0.4.x binds no native provider
  // registry there); it is load-checked in a real pig by `npm run validate` and the platform smoke instead.
  for (const entry of manifest.pi?.extensions ?? []) {
    if (!registersNativeProvider(join(installed, entry))) run(pig, env, cwd, 'install', join(installed, entry), '--validate-only');
  }
  run(pig, env, cwd, 'remove', spec);
  assert.ok(!run(pig, env, cwd, 'package', 'list').includes(spec), `${spec} is still listed after remove`);
  return installed;
}

/** A scratch HOME and agent directory for one pig; the caller removes `scratch`. */
export function isolatedEnv(scratch, name, extra = {}) {
  const home = join(scratch, 'home-' + name);
  mkdirSync(join(home, 'agent'), { recursive: true });
  return { ...process.env, HOME: home, USERPROFILE: home, PIG_HOME: home, PIG_CODING_AGENT_DIR: join(home, 'agent'), PI_CODING_AGENT_DIR: join(home, 'agent'),
    PI_TELEMETRY: '0', PI_SKIP_VERSION_CHECK: '1', GIT_TERMINAL_PROMPT: '0', GOTOOLCHAIN: 'local', ...extra };
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const [name, version] = process.argv.slice(2);
  const repository = process.env.GITHUB_REPOSITORY;
  checkPackageTag(packageTag(name, version));
  if (!repository) throw new Error('GITHUB_REPOSITORY (owner/repo) is not set');
  const scratch = realpathSync(mkdtempSync(join(tmpdir(), 'pigpen-smoke-')));
  try {
    smokePackage({ pig: requirePig(), env: isolatedEnv(scratch, name), cwd: scratch, repository, name, version });
    console.log(`${packageTag(name, version)} installs from ${repository}`);
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}
