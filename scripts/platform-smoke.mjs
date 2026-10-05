#!/usr/bin/env node
// Build every Piglet for this machine and smoke each Binary: the per-OS job of the CI platform matrix, and the same
// check anyone can run locally.
//
//   node scripts/platform-smoke.mjs [--only name,name] [--out dir] [--no-stage]
//
// PiG's native builder builds only the machine it runs on, so each OS builds its own target (scripts/host-target.mjs),
// with the PiG that scripts/pig-bin.mjs selects (PIG_BIN) and the PiG source checkout it needs (PIG_SOURCE_ROOT).
// Each Binary is run twice, in a throwaway HOME, with PIG_TEST_FAUX (no network, no model, no credentials):
//   1. `--version` reports the pig version these scripts target;
//   2. a one-turn print-mode run (`-p "reply with exactly: ok" --model test-faux/faux-1 --no-session`) exits 0, answers
//      `ok`, and prints no `Failed to load extension` diagnostic (pig exits 1 when an extension fails to load).
// This proves a Binary starts on the platform, loads every fused extension and completes a turn. What each extension
// does is the job of the Go tests; this script does not claim it.
// Exit status is 1 if any Piglet fails to build or fails a check; every Piglet is still tried and reported.
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { appendFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { root } from './go-modules.mjs';
import { hostTarget } from './host-target.mjs';
import { parsePigVersion, pigVersionMatches, requirePig, requiredPigVersion } from './pig-bin.mjs';

export const FAUX_PROMPT = 'reply with exactly: ok';
const RUN_LIMIT_MS = 120_000; // a hung Binary must fail the job, not hold the runner for six hours

/** Lines of pig output that report an extension that did not load. */
export function loadErrors(text) {
  return text.split(/\r?\n/).filter((line) => /Failed to load extension/.test(line));
}

/** A clean environment for one run: nothing of the caller's PIG_*, PI_* or credentials, everything under `home`. */
export function smokeEnv(home) {
  const env = {
    PATH: process.env.PATH ?? '', TERM: 'dumb', LANG: 'C.UTF-8',
    HOME: home, USERPROFILE: home, PIG_HOME: join(home, 'pig'), PIG_CODING_AGENT_DIR: join(home, 'agent'), PI_CODING_AGENT_DIR: join(home, 'pi'),
    TMPDIR: join(home, 'tmp'), TEMP: join(home, 'tmp'), TMP: join(home, 'tmp'),
    PIG_TEST_FAUX: '1', PIG_OFFLINE: '1', PI_SKIP_VERSION_CHECK: '1', GIT_TERMINAL_PROMPT: '0',
  };
  // Windows programs need these to find the system, and Go to find its caches; unset ones must not become "undefined".
  for (const name of ['SystemRoot', 'SYSTEMROOT', 'WINDIR', 'ComSpec', 'GOCACHE', 'GOMODCACHE']) if (process.env[name]) env[name] = process.env[name];
  return env;
}

const firstLine = (text) => (text ?? '').trim().split(/\r?\n/)[0] ?? '';

/** Run the two smoke checks on one Binary. Returns [{ name, ok, detail }]. */
export function smokeBinary(bin, { required = parsePigVersion(requiredPigVersion()) } = {}) {
  const home = mkdtempSync(join(tmpdir(), 'pigpen-smoke-'));
  try {
    for (const dir of ['tmp', 'work']) mkdirSync(join(home, dir), { recursive: true });
    const env = smokeEnv(home);
    const run = (args) => spawnSync(bin, args, { cwd: join(home, 'work'), env, encoding: 'utf8', timeout: RUN_LIMIT_MS });
    const checks = [];

    const version = run(['--version']);
    const found = parsePigVersion(version.stdout ?? '');
    checks.push({
      name: '--version',
      ok: version.status === 0 && !!found && pigVersionMatches(found, required),
      detail: version.error ? version.error.message : `exit ${version.status}, ${JSON.stringify(firstLine(version.stdout))}`,
    });

    const turn = run(['-p', FAUX_PROMPT, '--model', 'test-faux/faux-1', '--no-session']);
    const output = `${turn.stdout ?? ''}\n${turn.stderr ?? ''}`;
    const failed = loadErrors(output);
    const answered = (turn.stdout ?? '').trim() === 'ok';
    checks.push({
      name: 'faux turn',
      ok: turn.status === 0 && answered && failed.length === 0,
      detail: turn.error ? turn.error.message
        : failed.length ? failed[0]
          : `exit ${turn.status}, stdout ${JSON.stringify(firstLine(turn.stdout))}${turn.status === 0 ? '' : `, stderr ${JSON.stringify(firstLine(turn.stderr))}`}`,
    });
    return checks;
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
}

/**
 * Load one extension in a pig the way a run does: `pig -e <extension>` with the test model. A load failure is reported on
 * the output and exits nonzero, which `--list-models` does not do. Used for an extension that registers a native
 * Provider, which `pig install --validate-only` cannot load (its host binds no native provider registry).
 */
export function loadsInPig(pig, extension) {
  const home = mkdtempSync(join(tmpdir(), 'pigpen-load-'));
  try {
    for (const dir of ['tmp', 'work']) mkdirSync(join(home, dir), { recursive: true });
    const turn = spawnSync(pig, ['-e', extension, '-p', FAUX_PROMPT, '--model', 'test-faux/faux-1', '--no-session'], { cwd: join(home, 'work'), env: smokeEnv(home), encoding: 'utf8', timeout: RUN_LIMIT_MS });
    const failed = loadErrors(`${turn.stdout ?? ''}\n${turn.stderr ?? ''}`);
    const ok = !turn.error && turn.status === 0 && (turn.stdout ?? '').trim() === 'ok' && failed.length === 0;
    return { ok, detail: turn.error ? turn.error.message : failed[0] ?? `exit ${turn.status}, stdout ${JSON.stringify(firstLine(turn.stdout))}, stderr ${JSON.stringify(firstLine(turn.stderr))}` };
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
}

/** The Piglets in the staged tree, by name. */
export function stagedPiglets(dir = join(root, 'dist/staged/piglets')) {
  return existsSync(dir) ? readdirSync(dir).filter((name) => existsSync(join(dir, name, 'piglet.yaml'))).sort() : [];
}

function build(pig, name, out, host) {
  const target = join(out, `${name}${host.exe}`);
  rmSync(target, { force: true });
  const started = Date.now();
  const home = mkdtempSync(join(tmpdir(), 'pigpen-build-'));
  try {
    // pig keeps its Package cache and trust state under PIG_HOME; a build must not touch the runner's own.
    const env = { ...process.env, PIG_HOME: join(home, 'pig'), PIG_CODING_AGENT_DIR: join(home, 'agent') };
    const manifest = join(root, 'dist/staged/piglets', name, 'piglet.yaml');
    const result = spawnSync(pig, ['piglet', 'build', manifest, '--format', 'binary', '--out', target, '--targets', host.target], { cwd: root, env, encoding: 'utf8', maxBuffer: 64 << 20 });
    const seconds = Math.round((Date.now() - started) / 100) / 10;
    if (result.error || result.status !== 0 || !existsSync(target)) {
      return { target, seconds, error: result.error?.message ?? `pig piglet build exited ${result.status}\n${(result.stderr || result.stdout || '').trim().split('\n').slice(-25).join('\n')}` };
    }
    return { target, seconds };
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
}

const sha256 = (file) => createHash('sha256').update(readFileSync(file)).digest('hex');

function markdown(host, rows) {
  const lines = [`### Piglet Binaries for ${host.target}`, '', '| Piglet | Build | --version | Faux turn | Size | SHA-256 |', '| --- | --- | --- | --- | --- | --- |'];
  for (const row of rows) {
    const mark = (ok) => (ok ? 'pass' : '**FAIL**');
    const [version, turn] = row.checks ?? [];
    lines.push(`| ${row.piglet} | ${row.error ? '**FAIL**' : `${row.seconds}s`} | ${version ? mark(version.ok) : '-'} | ${turn ? mark(turn.ok) : '-'} | ${row.bytes ? `${(row.bytes / 1048576).toFixed(1)} MiB` : '-'} | ${row.sha256 ? `\`${row.sha256.slice(0, 12)}\`` : '-'} |`);
  }
  for (const row of rows) {
    for (const detail of [row.error, ...(row.checks ?? []).filter((c) => !c.ok).map((c) => `${c.name}: ${c.detail}`)].filter(Boolean)) {
      lines.push('', `**${row.piglet}**`, '', '```', detail, '```');
    }
  }
  return `${lines.join('\n')}\n`;
}

async function main() {
  const args = process.argv.slice(2);
  const value = (flag) => { const i = args.indexOf(flag); return i === -1 ? undefined : args[i + 1]; };
  const host = hostTarget();
  const out = resolve(value('--out') ?? join(root, 'dist/bin'));
  const pig = requirePig();
  if (!process.env.PIG_SOURCE_ROOT) throw new Error('PIG_SOURCE_ROOT is not set: a native Binary build needs a git checkout of the PiG source this pig was built from');

  if (!args.includes('--no-stage')) {
    const staged = spawnSync(process.execPath, [join(root, 'scripts/stage-piglets.mjs')], { cwd: root, stdio: 'inherit' });
    if (staged.status !== 0) throw new Error('staging failed; fix the diagnostic above');
  }
  const all = stagedPiglets();
  const only = value('--only')?.split(',').filter(Boolean);
  const unknown = (only ?? []).filter((name) => !all.includes(name));
  if (unknown.length) throw new Error(`no staged Piglet named ${unknown.join(', ')} (staged: ${all.join(', ') || 'none; run npm run stage'})`);
  const names = only ?? all;
  if (!names.length) throw new Error('no staged Piglets; run npm run stage');

  mkdirSync(out, { recursive: true });
  console.log(`platform-smoke: ${names.length} Piglet(s) for ${host.target} with ${pig}`);
  const rows = [];
  for (const name of names) {
    const built = build(pig, name, out, host);
    const row = { piglet: name, target: host.target, seconds: built.seconds };
    if (built.error) {
      row.error = built.error;
      console.log(`  FAIL ${name}: build\n${built.error}`);
    } else {
      row.bytes = statSync(built.target).size;
      row.sha256 = sha256(built.target);
      row.checks = smokeBinary(built.target);
      const bad = row.checks.filter((check) => !check.ok);
      console.log(`  ${bad.length ? 'FAIL' : 'ok  '} ${name} (${built.seconds}s, ${(row.bytes / 1048576).toFixed(1)} MiB)${bad.map((c) => `\n       ${c.name}: ${c.detail}`).join('')}`);
    }
    rows.push(row);
  }
  writeFileSync(join(out, 'smoke-summary.json'), `${JSON.stringify({ target: host.target, pig: requiredPigVersion(), piglets: rows }, null, 2)}\n`);
  if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, markdown(host, rows));
  const failed = rows.filter((row) => row.error || row.checks.some((check) => !check.ok));
  console.log(failed.length ? `platform-smoke: ${failed.length} of ${rows.length} failed: ${failed.map((r) => r.piglet).join(', ')}` : `platform-smoke: all ${rows.length} Piglets built and passed for ${host.target}`);
  process.exitCode = failed.length ? 1 : 0;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  main().catch((error) => { console.error(`platform-smoke: ${error.message}`); process.exitCode = 1; });
}
