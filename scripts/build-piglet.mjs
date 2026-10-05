import { spawnSync } from 'node:child_process';
import { existsSync, readdirSync, renameSync } from 'node:fs';
import { isAbsolute, join, resolve } from 'node:path';
import { root } from './go-modules.mjs';
import { targetOf } from './host-target.mjs';
import { requirePig } from './pig-bin.mjs';

// One command from a checkout to a Piglet Binary:
//   npm run build:piglet -- <piglet-name> [--out <file>] [--replace] [-- <extra pig piglet build flags>]
// pig never overwrites a binary; --replace moves an existing one to <out>.prev first.
// PiG anchors a Piglet's local Packages inside the Piglet's own directory, so the authored
// piglets/<name>/piglet.yaml (which selects ../../components/<pkg>) cannot be built directly:
// PiG says `escapes the Piglet anchor`. `npm run stage` copies the shared Packages next to the
// manifest; this script stages and builds that staged copy.

const names = () => readdirSync(join(root, 'piglets')).filter((name) => existsSync(join(root, 'piglets', name, 'piglet.yaml'))).sort();

function fail(message) {
  console.error(`build-piglet: ${message}`);
  process.exit(2);
}

const args = process.argv.slice(2);
const separator = args.indexOf('--');
const extra = separator === -1 ? [] : args.slice(separator + 1);
const own = separator === -1 ? args : args.slice(0, separator);
let name;
let out;
let replace = false;
for (let i = 0; i < own.length; i++) {
  if (own[i] === '--replace') replace = true;
  else if (own[i] === '--out') out = own[++i] ?? fail('--out needs a path');
  else if (own[i].startsWith('-')) fail(`unknown flag ${own[i]}`);
  else if (name === undefined) name = own[i];
  else fail(`unexpected argument ${own[i]}`);
}
if (name === undefined) fail(`usage: npm run build:piglet -- <piglet-name> [--out <file>]\nPiglets: ${names().join(', ')}`);
if (/[\\/]/.test(name) || name.endsWith('.yaml')) {
  fail(`give the Piglet name (one of ${names().join(', ')}), not the manifest path ${name}.\n`
    + 'The authored manifest selects Packages outside its directory, which PiG rejects ("escapes the Piglet anchor"); this command stages a copy first (npm run stage) and builds that.');
}
if (!names().includes(name)) fail(`no Piglet named "${name}". Piglets: ${names().join(', ')}`);

const step = (command, commandArgs, options = {}) => spawnSync(command, commandArgs, { cwd: root, stdio: 'inherit', ...options });
const staged = spawnSync(process.execPath, [join(root, 'scripts/quality-gates.mjs')], { cwd: root, stdio: 'inherit' }).status === 0
  && step(process.execPath, [join(root, 'scripts/go-only.mjs')]).status === 0
  && step(process.execPath, [join(root, 'scripts/stage-piglets.mjs')]).status === 0;
if (!staged) fail('staging failed; fix the diagnostic above');

const manifest = join(root, 'dist/staged/piglets', name, 'piglet.yaml');
const target = out ? resolve(out) : join(root, 'dist/bin', name);
let pig;
try { pig = requirePig(); } catch (error) { fail(error.message); }
if (existsSync(target)) {
  if (!replace) fail(`${target} already exists (pig never overwrites a binary). Choose another --out, or pass --replace to keep the old one as ${target}.prev.`);
}
if (!process.env.PIG_SOURCE_ROOT) {
  console.error('build-piglet: PIG_SOURCE_ROOT is not set; pig will look for its source checkout from the working directory.');
}
const moved = existsSync(target);
if (moved) renameSync(target, `${target}.prev`);
console.error(`build-piglet: ${pig} piglet build ${manifest} --format binary --out ${target} (host target unless --targets is given)`);
// The manifests name every platform a release ships, but pig's native builder builds only the machine it runs on
// ("native builder supports only host target"), so build for this host unless the caller chose targets.
const host = targetOf();
const targets = extra.some((flag) => flag === '--targets' || flag.startsWith('--targets=')) || !host ? [] : ['--targets', host.target];
const result = spawnSync(pig, ['piglet', 'build', manifest, '--format', 'binary', '--out', target, ...extra, ...targets], { cwd: root, stdio: 'inherit' });
if (result.error) fail(`cannot run ${pig}: ${result.error.message} (set PIG_BIN to a pig executable)`);
if (result.status !== 0) {
  if (moved && !existsSync(target)) renameSync(`${target}.prev`, target); // a failed build must not cost the previous binary
  console.error('build-piglet: the build failed. Read pig\'s diagnostic above. A native Binary build also needs a git checkout of the '
    + 'PiG source that this pig was built from: PIG_SOURCE_ROOT=/path/to/that/git checkout.');
}
process.exit(result.status ?? 1);
