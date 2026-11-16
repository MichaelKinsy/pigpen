// Runs the release workflow's own `run` steps end to end against a throwaway local repository, the way a hosted run
// would, with a stub `gh` and a throwaway signing key, so the first real release holds no surprise the rehearsal could
// have shown. It is not a GitHub Actions emulator: it understands only the handful of actions and expressions this
// workflow uses, and it fails on anything else instead of guessing.
//
//   PIG_BIN=<pig> node scripts/rehearse-release.mjs --pig-src <PiG checkout at PIG_COMMIT> [--piglet <name>, default herdr] [--keep]
//                 [--expect-tools a,b [--without <extension>]]   (a Lisp Piglet: also start the released Binary and check those tools)
//
// What it does: a bare repository stands in for GitHub (a clone of this tree plus one commit that limits the Piglet to the
// host target, because only the host can build here: run it on each runner OS the Piglet ships for); tags `<name>/v<version>` on that commit; then runs `plan`,
// `android-check`, `build` (one matrix entry) and `publish` from .github/workflows/release.yml, each in a fresh
// checkout and a fresh RUNNER_TEMP, honouring every step's `if:` (so the SBCL install and the REA payload step run for the
// Piglets that have them). Then it checks what the owner will rely on: the signed receipt verifies against the
// pinned key with scripts/receipts.mjs, the Binary verifies and runs, the key file is gone after every step and the key
// never reaches a log. Last, the refusals: a tag that is not the manifest version, a second publish of the same tag,
// and a Binary altered after it was built. Nothing is pushed, nothing leaves the temp directory, no real key is read.
import { spawnSync } from 'node:child_process';
import { appendFileSync, chmodSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';
import { readManifests } from './generate-index.mjs';
import { goCaches } from './go-modules.mjs';
import { parsePinnedKey, verifyReceipt } from './receipts.mjs';

const repoRoot = fileURLToPath(new URL('../', import.meta.url));
const args = process.argv.slice(2);
const option = (name) => (args.includes(name) ? args[args.indexOf(name) + 1] : undefined);
// Absolute: the checkouts below clone it from inside the rehearsal's scratch directory, where a relative path (CI passes
// `.pig-src`) would not resolve.
const pigSrc = option('--pig-src') && resolve(option('--pig-src'));
const pig = process.env.PIG_BIN;
if (!pigSrc || !pig) throw new Error('usage: PIG_BIN=<pig> node scripts/rehearse-release.mjs --pig-src <PiG checkout at the pinned commit> [--piglet <name>] [--expect-tools a,b [--without <extension>]] [--keep]');
const name = option('--piglet') ?? 'herdr';
const expectTools = option('--expect-tools');
const without = option('--without');

const R = realpathSync(mkdtempSync(join(tmpdir(), 'pigpen-rehearsal-')));
const repository = 'MichaelKinsy/pigpen';
const results = [];
const note = (ok, what) => { results.push({ ok, what }); console.log(`${ok ? 'ok  ' : 'FAIL'} ${what}`); if (!ok) process.exitCode = 1; };
const baseEnv = {
  PATH: `${join(R, 'bin')}:${process.env.PATH}`, HOME: join(R, 'home'), USERPROFILE: join(R, 'home'),
  GIT_CONFIG_GLOBAL: join(R, 'home', '.gitconfig'), GIT_CONFIG_SYSTEM: '/dev/null', GIT_TERMINAL_PROMPT: '0',
  GIT_AUTHOR_NAME: 'Rehearsal', GIT_AUTHOR_EMAIL: 'r@example.com', GIT_COMMITTER_NAME: 'Rehearsal', GIT_COMMITTER_EMAIL: 'r@example.com',
  // The caller's Go caches, resolved before HOME moves into the workspace: left to default they would land in R/home,
  // cost a full rebuild and module download per job, and leave read-only module files that stop R from being removed.
  ...goCaches(), GOFLAGS: process.env.GOFLAGS ?? '', GOTOOLCHAIN: 'local', TMPDIR: join(R, 'tmp'),
  PI_TELEMETRY: '0', PI_SKIP_VERSION_CHECK: '1', GH_RELEASES_DIR: join(R, 'releases'), STUB_GH_LOG: join(R, 'gh.log'),
};
for (const dir of ['bin', 'home', 'tmp', 'releases', 'artifacts', 'logs', 'keys']) mkdirSync(join(R, dir), { recursive: true });
const sh = (cwd, command, env = {}) => spawnSync('bash', ['--noprofile', '--norc', '-eo', 'pipefail', '-c', command], { cwd, env: { ...baseEnv, ...env }, encoding: 'utf8' });
const git = (cwd, ...a) => { const r = spawnSync('git', ['-c', 'commit.gpgsign=false', ...a], { cwd, env: baseEnv, encoding: 'utf8' }); if (r.status !== 0) throw new Error(`git ${a.join(' ')}: ${r.stderr}`); return r.stdout.trim(); };

// The stub gh: `release view` fails with "release not found" until `release create` stored the assets.
writeFileSync(join(R, 'bin', 'gh'), `#!/bin/sh
echo "gh $*" >> "$STUB_GH_LOG"
[ -n "$GH_TOKEN" ] || { echo "stub gh: GH_TOKEN is not set" >&2; exit 4; }
case "$1 $2" in
  "release view") [ -d "$GH_RELEASES_DIR/$3" ] || { echo "release not found" >&2; exit 1; }; echo '{"tagName":"'"$3"'"}';;
  "release create") tag="$3"; shift 3; mkdir -p "$GH_RELEASES_DIR/$tag"
    while [ $# -gt 0 ]; do case "$1" in --*) shift 2;; *) cp "$1" "$GH_RELEASES_DIR/$tag/"; shift;; esac; done;;
  *) echo "stub gh: unsupported: $*" >&2; exit 2;;
esac
`);
chmodSync(join(R, 'bin', 'gh'), 0o755);

// A throwaway key, made by the pig under rehearsal. The private key never leaves R/keys and is passed only as the secret.
const keyPath = join(R, 'keys', 'rehearsal.key');
const keygen = spawnSync(pig, ['piglet', 'keygen', keyPath], { env: baseEnv, encoding: 'utf8' });
if (keygen.status !== 0) throw new Error('keygen: ' + keygen.stderr);
const privateKey = readFileSync(keyPath, 'utf8');
const keyBody = privateKey.split('\n').filter((l) => !l.startsWith('-----') && l).join('');
const pinned = parsePinnedKey(readFileSync(keyPath + '.pub', 'utf8'));
const keys = new Map([[pinned.keyId, { ...pinned, file: 'rehearsal.pub' }]]);

// The "GitHub": a bare clone of this tree plus one commit that limits the Piglet to the host target.
const found = readManifests().find(({ manifest }) => manifest.name === name);
if (!found) throw new Error(`rehearsal: no Piglet named ${name}`);
const version = found.manifest.release.version;
const host = `${process.platform === 'darwin' ? 'darwin' : 'linux'}/${process.arch === 'arm64' ? 'arm64' : 'amd64'}`;
if (!found.manifest.build.targets.includes(host)) throw new Error(`rehearsal: ${name} does not list the host target ${host} (${found.manifest.build.targets.join(', ')})`);
const origin = join(R, 'origin.git');
git(R, 'clone', '-q', '--bare', repoRoot, origin);
const seed = join(R, 'seed');
git(R, 'clone', '-q', origin, seed);
const manifestFile = join(seed, `piglets/${name}/piglet.yaml`);
const manifestText = readFileSync(manifestFile, 'utf8');
const limited = manifestText.replace(/^build:\n  targets: \[.*\]$/m, `build:\n  targets: [${host}]`);
if (!limited.includes(`targets: [${host}]`)) throw new Error(`rehearsal: could not limit ${name} to ${host}`);
writeFileSync(manifestFile, limited);
git(seed, 'commit', '-qam', `rehearsal: ${name} builds the host target only`);
// Forced: the bare clone carries every local branch of this repository, so a checkout on a branch of its own finds a `main` it is not
// a descendant of; the bare repository is the rehearsal's, so replacing its main is the point.
git(seed, 'push', '-q', '-f', 'origin', 'HEAD:refs/heads/main');
const tagSha = git(seed, 'rev-parse', 'HEAD');
const tag = `${name}/v${version}`;
const wrongTag = `${name}/v9.9.9`;
// The clone may already carry the real tags (the repository has released); the rehearsal's own commit gets them, forced, in its own bare repository.
git(seed, 'tag', '-f', tag);
git(seed, 'tag', '-f', wrongTag);
git(seed, 'push', '-q', '-f', 'origin', tag, wrongTag);

const workflow = parse(readFileSync(join(repoRoot, '.github/workflows/release.yml'), 'utf8'));

function evaluate(text, ctx) {
  return String(text).replace(/\$\{\{\s*(.+?)\s*\}\}/g, (_, expression) => {
    const value = expression.split('.').reduce((o, k) => (o === undefined ? undefined : o[k]), ctx);
    if (value === undefined) throw new Error(`rehearsal: cannot evaluate \${{ ${expression} }}`);
    return String(value);
  });
}

/**
 * A step's `if:`, for the only forms release.yml uses: `==` and `!=` against a quoted string, joined by `&&` and `||` (`&&` binds tighter).
 * Anything else fails the rehearsal rather than being guessed, and a step with no `if` always runs.
 */
function holds(condition, ctx) {
  if (condition === undefined) return true;
  const text = String(condition).trim().replace(/^\$\{\{\s*([\s\S]*?)\s*\}\}$/, '$1');
  const term = (t) => {
    const m = /^([A-Za-z_][\w.]*)\s*(==|!=)\s*'([^']*)'$/.exec(t.trim());
    if (!m) throw new Error(`rehearsal: cannot evaluate the condition \`${condition}\``);
    const value = m[1].split('.').reduce((o, k) => (o === undefined ? undefined : o[k]), ctx);
    if (value === undefined) throw new Error(`rehearsal: cannot evaluate ${m[1]} in the condition \`${condition}\``);
    return m[2] === '==' ? String(value) === m[3] : String(value) !== m[3];
  };
  return text.split('||').some((or) => or.split('&&').every(term));
}

/** Run one job in a fresh workspace and RUNNER_TEMP; `ctx` carries needs and matrix. */
function runJob(id, ctx, { ref = tag, sha = tagSha, expectFail = false } = {}) {
  const job = workflow.jobs[id];
  const dir = mkdtempSync(join(R, `job-${id}-`));
  const workspace = join(dir, 'work');
  const temp = join(dir, 'temp');
  mkdirSync(workspace);
  mkdirSync(temp);
  const outputFile = join(dir, 'output');
  const envFile = join(dir, 'env');
  writeFileSync(outputFile, '');
  writeFileSync(envFile, '');
  const summaryFile = join(dir, 'summary'); // the job summary the cache step appends to; Actions provides this file to every step
  writeFileSync(summaryFile, '');
  const exported = {};
  const outputs = { steps: {} };
  const base = { ...ctx, runner: { temp }, github: { workspace, token: 'rehearsal-github-token', ref: `refs/tags/${ref}` }, env: { ...workflow.env, ...(job.env ?? {}) }, steps: {}, secrets: { PIGLET_SIGNING_KEY: privateKey.trim() } };
  const log = join(R, 'logs', `${basename(dir)}.log`);
  let n = 0;
  for (const step of job.steps) {
    n += 1;
    const label = `${id}#${n} ${step.name ?? step.uses ?? step.run.split('\n')[0].slice(0, 50)}`;
    if (!holds(step.if, base)) { console.log(`     skipped (if: ${String(step.if).trim()}) ${label}`); continue; }
    if (step.uses) {
      const action = step.uses.split('@')[0];
      const w = step.with ?? {};
      if (action === 'actions/checkout') {
        const target = join(workspace, w.path ?? '.');
        if (w.repository) {
          git(R, 'clone', '-q', '--no-checkout', pigSrc, target);
          git(target, 'checkout', '-q', evaluate(w.ref, base));
        } else {
          git(R, 'clone', '-q', origin, target);
          git(target, 'checkout', '-q', sha);
        }
      } else if (action === 'actions/setup-node' || action === 'actions/setup-go' || action === 'actions/attest-build-provenance') {
        // The host's node and go stand in; attestation needs GitHub.
      } else if (action === 'actions/cache/restore') {
        // The rehearsal has no cache service, so a restore is always a miss: the same thing a first run on a fresh runner
        // does, with the outputs the following steps read.
        if (step.id) base.steps[step.id] = { outputs: { 'cache-hit': 'false', 'cache-matched-key': '' } };
      } else if (action === 'actions/upload-artifact') {
        const from = join(workspace, dirname(w.path));
        const files = readdirSync(from).filter((f) => f.startsWith(basename(w.path).replace('*', '')));
        if (!files.length) throw new Error('rehearsal: upload-artifact found no files for ' + w.path);
        const to = join(R, 'artifacts', evaluate(w.name, base));
        mkdirSync(to, { recursive: true });
        for (const f of files) cpSync(join(from, f), join(to, f));
      } else if (action === 'actions/download-artifact') {
        const to = resolve(workspace, evaluate(w.path, base));
        mkdirSync(to, { recursive: true });
        for (const a of readdirSync(join(R, 'artifacts'))) for (const f of readdirSync(join(R, 'artifacts', a))) cpSync(join(R, 'artifacts', a, f), join(to, f));
      } else {
        throw new Error('rehearsal: unsupported action ' + step.uses);
      }
      console.log(`     ${label}`);
      continue;
    }
    const stepCtx = { ...base, env: { ...base.env, ...exported } };
    for (const [k, v] of Object.entries(step.env ?? {})) if (/secrets\./.test(v) && k !== 'PIGLET_SIGNING_KEY') throw new Error('rehearsal: unexpected secret in ' + k);
    const stepEnv = Object.fromEntries(Object.entries(step.env ?? {}).map(([k, v]) => [k, evaluate(v, stepCtx)]));
    const env = {
      ...Object.fromEntries(Object.entries(base.env).map(([k, v]) => [k, evaluate(v, stepCtx)])), ...exported, ...stepEnv,
      CI: 'true', GITHUB_ACTIONS: 'true', GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: ref, GITHUB_REF: `refs/tags/${ref}`, GITHUB_SHA: sha, GITHUB_REPOSITORY: repository,
      GITHUB_WORKSPACE: workspace, RUNNER_TEMP: temp, GITHUB_OUTPUT: outputFile, GITHUB_ENV: envFile, GITHUB_STEP_SUMMARY: summaryFile,
    };
    const result = sh(workspace, evaluate(step.run, stepCtx), env);
    appendFileSync(log, `## ${label}\n${result.stdout}${result.stderr}\n`);
    for (const line of readFileSync(envFile, 'utf8').split('\n').filter(Boolean)) { const i = line.indexOf('='); exported[line.slice(0, i)] = line.slice(i + 1); }
    writeFileSync(envFile, '');
    if (step.id) {
      const out = {};
      for (const line of readFileSync(outputFile, 'utf8').split('\n').filter(Boolean)) { const i = line.indexOf('='); out[line.slice(0, i)] = line.slice(i + 1); }
      base.steps[step.id] = { outputs: out };
      outputs.steps[step.id] = out;
    }
    if (existsSync(join(temp, 'piglet-signing.key'))) note(false, `${label}: the key file was left on the runner`);
    console.log(`${result.status === 0 ? '     ' : expectFail ? 'REFUSED ' : 'FAIL '}${label}`);
    if (result.status !== 0) return { ok: false, outputs, failedAt: label, stderr: result.stderr + result.stdout };
  }
  return { ok: true, outputs, workspace, exported };
}

// 1. Negative: a tag that is not the manifest version never reaches a runner.
const bad = runJob('plan', {}, { ref: wrongTag, sha: git(seed, 'rev-parse', wrongTag), expectFail: true });
note(!bad.ok && /matches 0 Piglets/.test(bad.stderr ?? ''), `plan refuses the tag ${wrongTag} (not the manifest version)`);

// 2. The release.
const plan = runJob('plan', {});
note(plan.ok, 'plan: tag matches the manifest, tagged commit is on main');
if (!plan.ok) process.exit(1);
const planOut = plan.outputs.steps.plan;
const needs = { plan: { outputs: planOut } };
note(planOut.name === name && JSON.parse(planOut.matrix).include.length === 1, `plan emits ${name} and its host target ${host}`);
const android = runJob('android-check', { needs });
note(android.ok, 'android-check: every module compiles and vets for android/arm64');
const matrix = JSON.parse(planOut.matrix).include[0];
const build = runJob('build', { needs, matrix });
note(build.ok, `build ${matrix.target}: signed Binary built on its native runner`);
if (!build.ok) { console.log(`${build.failedAt}\n${(build.stderr ?? '').split('\n').slice(-40).join('\n')}`); process.exit(1); }
const artifactDir = join(R, 'artifacts', `piglet-binary-${matrix.goos}-${matrix.goarch}`);
const produced = readdirSync(artifactDir);
note(produced.length === 1, `one Binary artifact: ${produced.join(', ')}`);

// A Binary altered after signing: publish must refuse it and create no release.
cpSync(artifactDir, join(R, 'artifacts-good'), { recursive: true });
const victim = join(artifactDir, produced[0]);
const bytes = readFileSync(victim);
bytes[Math.floor(bytes.length / 2)] ^= 0xff;
writeFileSync(victim, bytes);
const tampered = runJob('publish', { needs }, { expectFail: true });
note(!tampered.ok && /publish#7/.test(tampered.failedAt ?? '') && !existsSync(join(R, 'releases', tag)), 'publish (the step that signs the release) refuses a Binary altered after it was signed, and creates no release');
rmSync(artifactDir, { recursive: true });
cpSync(join(R, 'artifacts-good'), artifactDir, { recursive: true });

const publish = runJob('publish', { needs });
note(publish.ok, `publish: one release ${tag} created`);
const assets = existsSync(join(R, 'releases', tag)) ? readdirSync(join(R, 'releases', tag)).sort() : [];
const binaryName = `pig-${name}-${matrix.goos}-${matrix.goarch}`;
note(JSON.stringify(assets) === JSON.stringify(['SHA256SUMS', 'piglet-release.json', binaryName].sort()), `release assets: ${assets.join(', ')}`);

// 3. What the owner relies on.
try {
  const receipt = readFileSync(join(R, 'releases', tag, 'piglet-release.json'), 'utf8');
  const verified = verifyReceipt(receipt, { keys, repository, name, version });
  note(true, `the receipt verifies against the pinned key ${verified.keyId} (scripts/receipts.mjs), tag, name, version and URLs equal`);
  note(verified.sourceRef.length > 0, `sourceRef: ${verified.sourceRef}`);
  writeFileSync(join(R, 'receipt.json'), receipt);
  writeFileSync(join(R, 'rehearsal.pub'), readFileSync(keyPath + '.pub'));
} catch (error) {
  note(false, 'receipt verification: ' + error.message);
}
const binary = join(R, 'releases', tag, binaryName);
if (existsSync(binary)) {
  chmodSync(binary, 0o755);
  note(true, `released Binary ${binaryName}: ${statSync(binary).size} bytes`);
  const verify = spawnSync(pig, ['piglet', 'verify', binary], { env: baseEnv, encoding: 'utf8' });
  note(verify.status === 0 && /ed25519:/.test(verify.stdout + verify.stderr), 'pig piglet verify accepts the released Binary: ' + (verify.stdout + verify.stderr).trim().split('\n')[0]);
  const run = spawnSync(binary, ['--version'], { env: baseEnv, encoding: 'utf8' });
  note(run.status === 0, 'the Binary runs: ' + run.stdout.trim());
  if (expectTools) {
    // A Lisp Piglet: the released Binary must start, load its embedded cell and register its tools in a faux turn (the check ci.yml's
    // autolith-binary jobs make on an unsigned Binary), with the SBCL the build job's own install step put in the environment.
    const staged = join(build.workspace, 'dist/staged/piglets', name, 'piglet.yaml');
    const measure = spawnSync(process.execPath, [join(repoRoot, 'scripts/measure-lisp-piglet.mjs'), '--piglet', staged, ...(without ? ['--without', without] : []), '--binary', binary, '--expect-tools', expectTools, '--runs', '1'],
      { env: { ...process.env, ...build.exported, PIG_BIN: pig, PIG_HOME: join(R, 'measure-home'), PI_TELEMETRY: '0' }, encoding: 'utf8' });
    console.log((measure.stdout + measure.stderr).trim().split('\n').map((l) => '     ' + l).join('\n'));
    note(measure.status === 0, `the released Binary starts and registers ${expectTools} (measure-lisp-piglet)`);
  }
}
const again = runJob('publish', { needs }, { expectFail: true });
note(!again.ok && /exists|already/i.test(again.stderr ?? ''), 'a second publish of the same tag is refused');

// 4. The key reached no log.
const logs = readdirSync(join(R, 'logs')).map((f) => readFileSync(join(R, 'logs', f), 'utf8')).join('\n');
note(!logs.includes(keyBody) && !(existsSync(join(R, 'gh.log')) && readFileSync(join(R, 'gh.log'), 'utf8').includes(keyBody)), 'the private key appears in no step log and no gh call');

console.log(`\n${results.some((r) => !r.ok) ? 'REHEARSAL FAILED' : 'REHEARSAL PASSED'} (${results.length} checks); workspace ${R}`);
if (!args.includes('--keep') && !process.exitCode) rmSync(R, { recursive: true, force: true });
