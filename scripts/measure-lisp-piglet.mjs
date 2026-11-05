#!/usr/bin/env node
// Measures a Piglet whose extension is Common Lisp: the cached SBCL image PiG builds at the first load, and the time of that
// first load (a cold run: empty PIG_HOME and cache) against a warm run (image cached). PiG 0.7.0 builds a Piglet Binary with
// a Lisp member but that Binary stops at startup (its baked closure expects the Lisp cell inside it), so the runs use the
// Piglet source with PIG_BIN; --binary reports the size of a Binary built from the same manifest, checks its --version and
// records whether it starts (never a failure: the stop is PiG's, and this line shows when a newer PiG fixes it).
//
//   PIG_BIN=<pig> node scripts/measure-lisp-piglet.mjs --piglet <piglet.yaml> [--without <extension>] [--binary <built Binary>] [--runs 5] [--json]
//
// --without leaves one extension out of the measured manifest (a copy written beside it and removed after). The Piglet's empty
// Go member exists only so a Binary can build; run from source it would add a Go cell build to every cold run.
//
// Each run is the one-turn faux print-mode run platform-smoke uses (no network, no model, no credentials) in a throwaway
// HOME, so a cold run is "start, build the image, load the extension, answer". SBCL comes from PIG_SBCL / SBCL_HOME or PATH.
// The numbers are wall-clock milliseconds on the machine that runs this; report the machine with them.
import { spawnSync } from 'node:child_process';
import { parseDocument } from 'yaml';
import { appendFileSync, writeFileSync, chmodSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { FAUX_PROMPT, loadErrors, smokeEnv } from './platform-smoke.mjs';
import { requirePig } from './pig-bin.mjs';

const RUN_LIMIT_MS = 300_000;

export function median(values) {
  const sorted = [...values].sort((a, b) => a - b);
  const mid = sorted.length >> 1;
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
}

/** Every regular file under `dir` with its size, sorted largest first. */
export function filesBySize(dir) {
  const found = [];
  const walk = (current) => {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const path = join(current, entry.name);
      if (entry.isDirectory()) walk(path);
      else if (entry.isFile()) found.push({ path, bytes: statSync(path).size });
    }
  };
  walk(dir);
  return found.sort((a, b) => b.bytes - a.bytes);
}

/** Whether a built Binary starts: its --version, then the faux turn. Reports, never throws; the stop is PiG 0.7.0's, not the Piglet's. */
export function binaryStartup(binary) {
  const home = mkdtempSync(join(tmpdir(), 'pigpen-lisp-binary-'));
  try {
    for (const dir of ['tmp', 'work']) mkdirSync(join(home, dir), { recursive: true });
    const env = { ...smokeEnv(home), XDG_CACHE_HOME: join(home, 'cache') };
    for (const name of ['PIG_SBCL', 'SBCL_HOME']) if (process.env[name]) env[name] = process.env[name];
    const run = (args) => spawnSync(binary, args, { cwd: join(home, 'work'), env, encoding: 'utf8', timeout: RUN_LIMIT_MS, maxBuffer: 64 << 20 });
    const version = run(['--version']);
    const turn = run(['-p', FAUX_PROMPT, '--model', 'test-faux/faux-1', '--no-session']);
    const first = (text) => (text ?? '').trim().split('\n')[0];
    return {
      version: version.status === 0 ? first(version.stdout) : `exit ${version.status}`,
      startsAndAnswers: turn.status === 0 && (turn.stdout ?? '').trim() === 'ok',
      detail: turn.status === 0 ? first(turn.stdout) : first(turn.stderr) || `exit ${turn.status}`,
    };
  } finally {
    removeTree(home);
  }
}

/** Remove a run's home. The Go module cache a Go cell fills is read-only, so make the directories writable first. */
function removeTree(dir) {
  const walk = (current) => {
    chmodSync(current, 0o700);
    for (const entry of readdirSync(current, { withFileTypes: true })) if (entry.isDirectory()) walk(join(current, entry.name));
  };
  try { walk(dir); } catch { /* already gone */ }
  rmSync(dir, { recursive: true, force: true });
}

function runOnce(pig, manifest, home) {
  const env = { ...smokeEnv(home), XDG_CACHE_HOME: join(home, 'cache') };
  for (const name of ['PIG_SBCL', 'SBCL_HOME']) if (process.env[name]) env[name] = process.env[name];
  const started = process.hrtime.bigint();
  const turn = spawnSync(pig, ['--piglet', manifest, '-p', FAUX_PROMPT, '--model', 'test-faux/faux-1', '--no-session'], { cwd: join(home, 'work'), env, encoding: 'utf8', timeout: RUN_LIMIT_MS, maxBuffer: 64 << 20 });
  const ms = Number(process.hrtime.bigint() - started) / 1e6;
  const output = `${turn.stdout ?? ''}\n${turn.stderr ?? ''}`;
  if (turn.error || turn.status !== 0 || (turn.stdout ?? '').trim() !== 'ok' || loadErrors(output).length) {
    throw new Error(`faux turn failed (exit ${turn.status}): ${turn.error?.message ?? output.trim().split('\n').slice(0, 8).join('\n')}`);
  }
  return ms;
}

export function measure(pig, manifest, runs, binary) {
  const base = mkdtempSync(join(tmpdir(), 'pigpen-lisp-measure-'));
  try {
    const cold = [];
    const warm = [];
    let image;
    for (let i = 0; i < runs; i++) {
      const home = join(base, `cold-${i}`);
      for (const dir of ['tmp', 'work']) mkdirSync(join(home, dir), { recursive: true });
      cold.push(runOnce(pig, manifest, home));
      if (i === 0) {
        // The image is the Lisp cell's runner (an SBCL core with the extension and the SDK in it); the cache is the Lisp cells' whole directory.
        const files = filesBySize(home).filter((f) => f.path.split(sep).join('/').includes('/cells/lisp/'));
        image = files.length ? { path: relative(home, files[0].path), bytes: files[0].bytes, cacheBytes: files.reduce((sum, f) => sum + f.bytes, 0) } : undefined;
        for (let w = 0; w < runs; w++) warm.push(runOnce(pig, manifest, home));
      } else removeTree(home);
    }
    return { binary: binary ? { path: binary, bytes: statSync(binary).size, ...binaryStartup(binary) } : undefined, runs, image, coldMs: cold, warmMs: warm, coldMedianMs: median(cold), warmMedianMs: median(warm) };
  } finally {
    removeTree(base);
  }
}

const mib = (bytes) => `${(bytes / 1048576).toFixed(1)} MiB (${bytes.toLocaleString('en-US')} bytes)`;

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  const args = process.argv.slice(2);
  const value = (flag) => { const i = args.indexOf(flag); return i === -1 ? undefined : args[i + 1]; };
  const manifest = value('--piglet');
  const binary = value('--binary');
  const runs = Number(value('--runs') ?? 5);
  if (!manifest || !Number.isInteger(runs) || runs < 1) { console.error('usage: PIG_BIN=<pig> node scripts/measure-lisp-piglet.mjs --piglet <piglet.yaml> [--binary <built Binary>] [--runs N] [--json]'); process.exit(2); }
  try {
    const without = value('--without');
    let measured = resolve(manifest);
    let copy;
    if (without) {
      const document = parseDocument(readFileSync(measured, 'utf8'));
      const kept = (document.toJS().extensions ?? []).filter((entry) => entry.name !== without);
      if (kept.length === (document.toJS().extensions ?? []).length) throw new Error(`${manifest} has no extension named ${without}`);
      document.set('extensions', kept);
      copy = join(dirname(measured), '.measure-piglet.yaml');
      writeFileSync(copy, String(document));
      measured = copy;
    }
    let result;
    try { result = measure(requirePig(), measured, runs, binary && resolve(binary)); } finally { if (copy) rmSync(copy, { force: true }); }
    if (args.includes('--json')) console.log(JSON.stringify(result, null, 2));
    const lines = [
      ...(result.binary ? [`Binary: ${mib(result.binary.bytes)}; --version ${JSON.stringify(result.binary.version)}; faux turn ${result.binary.startsAndAnswers ? 'answers ok' : `does not start: ${result.binary.detail}`}`] : []),
      result.image ? `Image built at first load: ${mib(result.image.bytes)} (${result.image.path}); cache written: ${mib(result.image.cacheBytes)}` : 'no cache file written',
      `First load, cold (median of ${runs}): ${(result.coldMedianMs / 1000).toFixed(2)} s; runs ${result.coldMs.map((ms) => (ms / 1000).toFixed(2)).join(', ')} s`,
      `Load with the image cached (median of ${result.warmMs.length}): ${(result.warmMedianMs / 1000).toFixed(3)} s; runs ${result.warmMs.map((ms) => (ms / 1000).toFixed(3)).join(', ')} s`,
    ];
    console.log(lines.join('\n'));
    if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, `### Lisp Piglet measurement\n\n${lines.map((line) => `- ${line}`).join('\n')}\n`);
  } catch (error) {
    console.error(`measure-lisp-piglet: ${error.message}`);
    process.exitCode = 1;
  }
}
