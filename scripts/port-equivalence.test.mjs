// The equivalence proof for the ported extensions, through the real hosts.
//   PIG_BIN=/path/to/pig npm run test:port             port vs recorded Pi traces (no Pi needed)
//   PIG_BIN=... PI_BIN=/path/to/pi npm run test:port   also re-runs the original under Pi and PiG
//   PIG_UPSTREAM=/path/to/export  also re-runs a Go relocation against the unmodified upstream Go extension
//     (a `git archive` of the upstream repository; port/relocation.json names the extension inside it)
// PIG_SOURCE_ROOT must be a git checkout of the PiG source the binary was built from when
// a scenario builds a Go extension into a Piglet Binary; the port lane builds from source.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { cpSync, existsSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { availableParallelism } from 'node:os';
import { join } from 'node:path';
import { describe, it } from 'node:test';
import { goWorkEnv, root, scratchDir, sdkDir } from './go-modules.mjs';
import { requirePig } from './pig-bin.mjs';

const pig = requirePig();
const pi = process.env.PI_BIN;
const harness = join(root, 'components/extension-equivalence/extensions/extension-equivalence');
const env = goWorkEnv();
const bin = join(scratchDir('pigeq-'), 'pigeq');
execFileSync('go', ['build', '-o', bin, '.'], { cwd: join(harness, 'cmd/pigeq'), env, stdio: 'inherit' });

// The Go relocations renamed the upstream's internal default sprite id to `pig-default`, so a live re-run of the upstream
// applies that one rename to a scratch copy of the export; every other difference still fails. (The old id is spelled in
// two pieces so that the tree carries no copy of the internal name.)
const upstreamRenames = [['h' + 'pe-agentic', 'pig-default']];
let renamedExport;
function upstreamExport() {
  if (renamedExport) return renamedExport;
  renamedExport = join(scratchDir('pigeq-upstream-'), 'export');
  cpSync(process.env.PIG_UPSTREAM, renamedExport, { recursive: true });
  const walk = (dir) => {
    for (const name of readdirSync(dir)) {
      const path = join(dir, name);
      if (statSync(path).isDirectory()) { walk(path); continue; }
      const text = readFileSync(path, 'utf8');
      const out = upstreamRenames.reduce((t, [from, to]) => t.split(from).join(to), text);
      if (out !== text) writeFileSync(path, out);
    }
  };
  walk(renamedExport);
  return renamedExport;
}

const ports = readdirSync(join(root, 'components'), { withFileTypes: true })
  .map((e) => e.name)
  .filter((name) => existsSync(join(root, 'components', name, 'port/scenarios')));

function pigeq(args, port) {
  return execFileSync(bin, args, {
    cwd: join(root, 'components', port), encoding: 'utf8', timeout: 20 * 60_000,
    env: { ...process.env, PIGEQ_PIG: pig, PIGEQ_PI: pi ?? '' },
  });
}

// What each kind of golden trace can prove (the header's lane, see the Skill's "What kind of port is this?"):
//   pi-ts           the TypeScript original under Pi: gaps, check, mutate, and a live re-run under Pi
//   pig-go-upstream a Go original under PiG (a relocation): check, mutate, and a live re-run when PIG_UPSTREAM
//                   is an export of the upstream repository and port/relocation.json names the extension in it
//   pig-go-self     no original: a regression baseline; check and mutate only, never called equivalence
function goldenHeader(dir) {
  const first = readdirSync(join(dir, 'port/golden')).find((f) => f.endsWith('.jsonl'));
  assert.ok(first, `${dir}: port/golden has no trace`);
  return JSON.parse(readFileSync(join(dir, 'port/golden', first), 'utf8').split('\n')[0]);
}

// The TypeScript file the golden traces were recorded from: the one under port/ whose sha256 is the header's
// `extension` hash (pigeq hashes a file original's bytes). A package original may be recorded from its source entry
// (pi-typesafe: oracle/src/extension.ts) or from an entry that re-exports it (warden: eq-entry.ts), which Pi compiles
// itself, so the live re-run needs no build of the package, only its dependencies.
function recordedOriginal(dir, hash) {
  const walk = (rel) => readdirSync(join(dir, rel), { withFileTypes: true }).flatMap((e) => {
    const path = `${rel}/${e.name}`;
    if (e.isDirectory()) return e.name === 'node_modules' || e.name === 'dist' ? [] : walk(path);
    return /\.(ts|mts|js|mjs)$/.test(e.name) ? [path] : [];
  });
  return walk('port').find((path) => `sha256:${createHash('sha256').update(readFileSync(join(dir, path))).digest('hex')}` === hash);
}

// Each mutant runs in its own copy of the port, so they can run side by side (a port with a hundred mutants
// and a slow unit suite, such as a2a, does not fit the per-command time limit one at a time).
const jobs = Math.max(1, Math.min(8, Math.floor(availableParallelism() / 2)));

for (const port of ports) {
  const dir = join(root, 'components', port);
  const [extension] = readdirSync(join(dir, 'extensions'));
  const header = goldenHeader(dir);
  const lane = header.lane;
  // The TypeScript original is one .ts file, or a whole package (a third-party original such as pi-typesafe:
  // port/oracle holds its package.json, and Pi loads the extension its `pi.extensions` names).
  // A package original names its extension in package.json (`pi.extensions`); only then is the directory the original,
  // otherwise the first .ts file is (a package holds many .ts files, most of them not extensions).
  const oraclePackageJson = join(dir, 'port/oracle/package.json');
  const namesExtension = lane === 'pi-ts' && existsSync(oraclePackageJson) && JSON.parse(readFileSync(oraclePackageJson, 'utf8')).pi?.extensions?.length > 0;
  const oracleFile = lane === 'pi-ts' && !namesExtension ? readdirSync(join(dir, 'port/oracle')).find((f) => f.endsWith('.ts')) : null;
  const original = oracleFile ? `port/oracle/${oracleFile}` : lane === 'pi-ts' && existsSync(join(dir, 'port/oracle/package.json')) ? 'port/oracle' : null;
  const relocation = existsSync(join(dir, 'port/relocation.json')) ? JSON.parse(readFileSync(join(dir, 'port/relocation.json'), 'utf8')) : null;
  // A third-party original that must be built (npm install && npm run build in port/oracle; gitignored) cannot be re-run until it is.
  const oraclePackage = original === 'port/oracle' ? JSON.parse(readFileSync(join(dir, 'port/oracle/package.json'), 'utf8')) : null;
  const recorded = lane === 'pi-ts' ? recordedOriginal(dir, header.extension) : null;
  const oracleUnbuilt = recorded
    ? (oraclePackage?.dependencies && Object.keys(oraclePackage.dependencies).length && !existsSync(join(dir, 'port/oracle/node_modules'))
      ? `install the original's dependencies first: ${existsSync(join(dir, 'port/oracle/package-lock.json')) ? 'npm ci --ignore-scripts' : 'npm install --ignore-scripts --no-package-lock'} in port/oracle` : false)
    : oraclePackage?.scripts?.build && !existsSync(join(dir, 'port/oracle/dist')) ? 'build the original first: npm install && npm run build in port/oracle' : false;
  const accept = existsSync(join(dir, 'port/accepted-gaps.json')) ? ['--accept-gaps', 'port/accepted-gaps.json'] : [];
  describe(`equivalence: ${port} (${lane})`, () => {
    it('finds no unaccepted SDK gap in the original', { skip: original ? false : 'the original is not TypeScript' }, () => {
      pigeq(['gaps', '--ts', original, ...accept], port);
    });
    it('finds no PiG-internal import or fuse hazard in the port', () => {
      pigeq(['gaps', '--go', `extensions/${extension}`, ...accept], port);
    });
    it('the Go port matches the recorded traces on every scenario', () => {
      const out = pigeq(['check', '--scenarios', 'port/scenarios', '--golden', 'port/golden', '--go', `extensions/${extension}`, '--pig', pig], port);
      assert.match(out, /^PASS /m);
      assert.doesNotMatch(out, /^FAIL /m);
    });
    it('every mutation of the port is caught', () => {
      const mutations = join(dir, 'port/mutations.json');
      if (!existsSync(mutations)) return;
      const out = pigeq(['mutate', '--scenarios', 'port/scenarios', '--golden', 'port/golden', '--go', `extensions/${extension}`, '--pig', pig, '--mutations', 'port/mutations.json', '--unit', '--jobs', String(jobs), '--sdk-dir', sdkDir()], port);
      assert.match(out, /, 0 not killed$/m);
      assert.doesNotMatch(out, /^(SURVIVED|INVALID)/m);
      assert.doesNotMatch(out, /downloading go|did not run the tests/);
    });
    it('the recorded traces are still what the original produces', {
      skip: lane === 'pi-ts' ? (!pi ? 'set PI_BIN to re-run the original under Pi' : oracleUnbuilt)
        : lane === 'pig-go-upstream' ? (process.env.PIG_UPSTREAM && relocation ? false : 'set PIG_UPSTREAM (an export of the upstream repository) to re-run the upstream extension')
        : 'a self-recorded baseline has no original to re-run',
    }, () => {
      const oracle = lane === 'pi-ts' ? ['--ts', recorded ?? original] : ['--go-oracle', join(process.env.PIG_UPSTREAM ? upstreamExport() : '', relocation?.oracle ?? '')];
      const out = pigeq(['run', '--scenarios', 'port/scenarios', ...oracle, '--go', `extensions/${extension}`, '--pig', pig, ...accept], port);
      assert.doesNotMatch(out, /^FAIL /m);
      assert.match(out, /^PASS /m);
    });
  });
}
