// Compile and link every Piglet Binary for the release targets this machine cannot run.
//
//   node scripts/cross-link-check.mjs [--only name,name] [--targets os/arch,...] [--out dir]
//
// PiG's native builder builds only the machine it runs on and its container builder only a Linux target, so a darwin or
// windows Binary needs a native runner (scripts/runners.mjs). A native build still compiles with whatever GOOS and GOARCH
// the environment names, because the fused extensions link into one `go build`; only its last step, running the new Binary
// once to verify it, cannot run on a foreign OS. This script sets GOOS, GOARCH and CGO_ENABLED=0 for the build, treats the
// verification step's "exec format error" after a successful link as "linked", and fails on every other error. It proves that
// every fused extension, the PiG source and the SDK compile and link for the target. It does not prove the Binary runs there:
// platform-smoke on a native runner does that. pig deletes the Binary when its verification fails, so no artifact is kept.
// Needs PIG_BIN and PIG_SOURCE_ROOT (see platform-smoke.mjs) and a staged tree (npm run stage).
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { root } from './go-modules.mjs';
import { hostTarget } from './host-target.mjs';
import { requirePig, requiredPigVersion } from './pig-bin.mjs';
import { stagedPiglets } from './platform-smoke.mjs';
import { runners } from './runners.mjs';

const TARGET = /^[a-z0-9]+\/[a-z0-9]+$/;

/** The targets to check: the ones asked for, else every runner target but the host. */
export function targetsFor(requested, host = hostTarget()) {
  const bad = requested.filter((target) => !TARGET.test(target));
  if (bad.length) throw new Error(`not an os/arch target: ${bad.join(', ')}`);
  if (requested.includes(host.target)) throw new Error(`${host.target} is the host target: platform-smoke builds and runs it`);
  return requested.length ? requested : Object.keys(runners).filter((target) => target !== host.target);
}

/** The environment of one build: the target for Go, an isolated PiG home, and no cgo. */
export function buildEnv(base, target, home) {
  const [GOOS, GOARCH] = target.split('/');
  return { ...base, GOOS, GOARCH, CGO_ENABLED: '0', PIG_HOME: join(home, 'pig'), PIG_CODING_AGENT_DIR: join(home, 'agent') };
}

/** Turn one `pig piglet build` result into a verdict. `built`: it also ran. `linked`: linked, but this host cannot run it. */
export function classify(result, target) {
  const output = `${result.stdout ?? ''}\n${result.stderr ?? ''}`;
  if (result.status === 0) return { ok: true, level: 'built', detail: 'built and verified' };
  const linked = /Checksumming and verifying binary/.test(output) && !/Compiling and linking Go binary failed/.test(output);
  if (linked && /basic artifact verification failed: .*exec format error/.test(output)) {
    return { ok: true, level: 'linked', detail: `compiled and linked; the host cannot run a ${target} Binary` };
  }
  return { ok: false, level: 'failed', detail: output.trim().split('\n').slice(-25).join('\n') };
}

function buildOne(pig, name, target) {
  const home = mkdtempSync(join(tmpdir(), 'pigpen-xlink-'));
  try {
    const exe = target.startsWith('windows/') ? '.exe' : '';
    const out = join(home, `${name}${exe}`);
    const manifest = join(root, 'dist/staged/piglets', name, 'piglet.yaml');
    const started = Date.now();
    // pig's native builder accepts only its host target, so name that; GOOS and GOARCH in the environment choose the real one.
    const host = hostTarget().target;
    const result = spawnSync(pig, ['piglet', 'build', manifest, '--format', 'binary', '--builder', 'native', '--targets', host, '--out', out], { cwd: root, env: buildEnv(process.env, target, home), encoding: 'utf8', maxBuffer: 64 << 20 });
    return { ...classify(result, target), seconds: Math.round((Date.now() - started) / 100) / 10 };
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
}

function main() {
  const args = process.argv.slice(2);
  const value = (flag) => { const i = args.indexOf(flag); return i === -1 ? undefined : args[i + 1]; };
  let targets;
  try { targets = targetsFor(value('--targets')?.split(',').filter(Boolean) ?? []); } catch (error) { console.error(`cross-link-check: ${error.message}`); return 2; }
  const pig = requirePig();
  if (!process.env.PIG_SOURCE_ROOT) throw new Error('PIG_SOURCE_ROOT is not set: a native Binary build needs a git checkout of the PiG source this pig was built from');
  const all = stagedPiglets();
  const only = value('--only')?.split(',').filter(Boolean);
  const unknown = (only ?? []).filter((name) => !all.includes(name));
  if (unknown.length) throw new Error(`no staged Piglet named ${unknown.join(', ')} (run npm run stage)`);
  const names = only ?? all;
  if (!names.length) throw new Error('no staged Piglets; run npm run stage');

  console.log(`cross-link-check: ${names.length} Piglet(s) x ${targets.length} target(s) with ${pig}`);
  const rows = [];
  for (const target of targets) {
    for (const piglet of names) {
      const row = { piglet, target, ...buildOne(pig, piglet, target) };
      rows.push(row);
      console.log(`  ${row.ok ? 'ok  ' : 'FAIL'} ${target} ${piglet} (${row.seconds}s, ${row.level})${row.ok ? '' : `\n${row.detail}`}`);
    }
  }
  const out = value('--out');
  if (out) {
    mkdirSync(resolve(out), { recursive: true });
    writeFileSync(join(resolve(out), 'cross-link-summary.json'), `${JSON.stringify({ pig: requiredPigVersion(), rows }, null, 2)}\n`);
  }
  const failed = rows.filter((row) => !row.ok);
  console.log(failed.length ? `cross-link-check: ${failed.length} of ${rows.length} failed` : `cross-link-check: all ${rows.length} Piglet/target builds compiled and linked`);
  return failed.length ? 1 : 0;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  try { process.exitCode = main(); } catch (error) { console.error(`cross-link-check: ${error.message}`); process.exitCode = 1; }
}
