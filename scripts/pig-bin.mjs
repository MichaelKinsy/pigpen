// The one place that decides which pig a script or test runs. A test that drives a real pig (the ACP
// end-to-end scenarios, the Piglet builds, the tmux runs) passes or fails with that pig's behavior, so it must
// run the pig Pigpen targets, never whatever `pig` the shell finds first. PIG_BIN is therefore required, and its
// version must match scripts/pig-requirement.json: the same major.minor (a 0.x minor is a breaking release) at
// the required patch or later.
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const requirementFile = fileURLToPath(new URL('./pig-requirement.json', import.meta.url));

/** The pig version these scripts target, "major.minor.patch". */
export function requiredPigVersion() {
  return JSON.parse(readFileSync(requirementFile, 'utf8')).pig;
}

/** [major, minor, patch] from the first x.y.z in `pig --version` output ("0.3.1+0.99.2" is 0.3.1), or undefined. */
export function parsePigVersion(text) {
  const m = /(\d+)\.(\d+)\.(\d+)/.exec(String(text).split('\n')[0] ?? '');
  return m ? [Number(m[1]), Number(m[2]), Number(m[3])] : undefined;
}

/** Whether a pig at `found` runs the pig `required` targets. */
export function pigVersionMatches(found, required) {
  return found[0] === required[0] && found[1] === required[1] && found[2] >= required[2];
}

const checked = new Map();

/**
 * PIG_BIN, verified: set, runnable, and at the required version. Returns it as given (a path or a command
 * name), or throws an Error that says what is wrong and how to get the right pig.
 */
export function requirePig(env = process.env) {
  const pig = env.PIG_BIN;
  const required = requiredPigVersion();
  const build = `Build the pig from the PiG source Pigpen targets (go build -o /path/to/pig ./cmd/pig in a PiG checkout at ${required} or later) and set PIG_BIN to it. Pigpen never falls back to the pig on PATH.`;
  if (!pig) throw new Error(`PIG_BIN is not set. These tests need pig ${required.split('.').slice(0, 2).join('.')}.x (>= ${required}). ${build}`);
  if (checked.has(pig)) return checked.get(pig);
  const result = spawnSync(pig, ['--version'], { encoding: 'utf8', env: { ...env, PIG_OFFLINE: '1', PI_SKIP_VERSION_CHECK: '1' } });
  if (result.error) throw new Error(`PIG_BIN=${pig} cannot be run: ${result.error.message}. ${build}`);
  const found = parsePigVersion(result.stdout);
  if (result.status !== 0 || !found) {
    throw new Error(`PIG_BIN=${pig} did not report a version for --version (exit ${result.status}: ${(result.stdout + result.stderr).trim().slice(0, 200)}). ${build}`);
  }
  if (!pigVersionMatches(found, parsePigVersion(required))) {
    throw new Error(`PIG_BIN=${pig} is pig ${found.join('.')}, but these tests target pig ${required.split('.').slice(0, 2).join('.')}.x (>= ${required}). ${build}`);
  }
  checked.set(pig, pig);
  return pig;
}
