#!/usr/bin/env node
// Build the REA payload for one target from the pins in rea.lock.json, and check REA's skill against the copy in skills/.
//
//   node components/rea/scripts/payload.mjs [<os>/<arch>]        default: this machine
//   REA_SOURCE=/path/to/rea node components/rea/scripts/payload.mjs   pack a local REA checkout instead (dev, never released)
//   node components/rea/scripts/payload.mjs --sync-skill         write the pinned REA skill into skills/ (scripts/skill.mjs) and exit
//
// Output, read by the rea member's go:embed (extensions/rea/payload/, gitignored):
//   <os>_<arch>.tar.zst  deterministic tar (sorted, uid/gid 0, mtime 0) of node/ (the pinned Node), rea/ (the published package
//                        with its production dependencies installed from REA's tagged lockfile) and payload.json
//   <os>_<arch>.json     what the member reads at startup: digests, versions, file count
// Payloads for other targets are removed, so a Binary embeds only its own.
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { cpSync, lstatSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, relative, resolve, sep } from 'node:path';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { fileURLToPath } from 'node:url';
import { adaptSkillFile, shippedSkill, skillDrift, skillFiles, upstreamSkill } from './skill.mjs';

const component = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repository = resolve(component, '../..');
const lock = JSON.parse(readFileSync(join(component, 'rea.lock.json'), 'utf8'));
const payloadDir = join(component, 'extensions/rea/payload');
// Downloaded Node archives, checked against rea.lock.json on every use. Under dist/, which staging never copies.
const cacheDir = join(repository, 'dist/cache/rea');
const pigletSkill = join(component, 'skills', shippedSkill);

const goTargets = { 'linux/amd64': ['linux', 'x64'], 'linux/arm64': ['linux', 'arm64'], 'darwin/arm64': ['darwin', 'arm64'], 'darwin/amd64': ['darwin', 'x64'], 'windows/amd64': ['win32', 'x64'] };

function fail(message) {
  console.error(`payload: ${message}`);
  process.exit(2);
}

function run(command, args, options = {}) {
  // npm is npm.cmd on Windows, which Node runs only through a shell.
  const shell = command === 'npm' && process.platform === 'win32';
  const result = spawnSync(command, args, { encoding: 'utf8', maxBuffer: 64 << 20, shell, ...options });
  if (result.error) fail(`cannot run ${command}: ${result.error.message}`);
  if (result.status !== 0) fail(`${command} ${args.join(' ')} exited ${result.status}:\n${(result.stderr || result.stdout || '').trim().slice(-4000)}`);
  return result.stdout;
}

const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex');

function hostTarget() {
  const os = { linux: 'linux', darwin: 'darwin', win32: 'windows' }[process.platform];
  const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
  return os && arch ? `${os}/${arch}` : undefined;
}

/** Regular files and directories under root, sorted by their slash-separated relative path. Symlinks are an error. */
function walk(root) {
  const entries = [];
  const visit = (dir) => {
    for (const name of readdirSync(dir)) {
      const path = join(dir, name);
      const rel = relative(root, path).split(sep).join('/');
      const stat = lstatSync(path);
      if (stat.isSymbolicLink()) fail(`${rel}: symlinks are not packed (npm ci runs with --no-bin-links)`);
      if (stat.isDirectory()) {
        entries.push({ rel, path, dir: true });
        visit(path);
      } else if (stat.isFile()) entries.push({ rel, path, dir: false, mode: stat.mode & 0o111 ? 0o755 : 0o644 });
      else fail(`${rel}: not a regular file or directory`);
    }
  };
  visit(root);
  return entries.sort((a, b) => (a.rel < b.rel ? -1 : a.rel > b.rel ? 1 : 0));
}

/** The tool_timeout_sec `rea setup --client codex` writes with REA's default environment. */
function recommendedToolTimeoutSec(node, entry, work) {
  const home = join(work, 'setup-home');
  mkdirSync(join(home, '.codex'), { recursive: true });
  const env = { PATH: `${dirname(node)}${process.platform === 'win32' ? ';' : ':'}${process.env.PATH ?? ''}`, HOME: home, USERPROFILE: home, CODEX_HOME: join(home, '.codex') };
  if (process.env.SystemRoot) env.SystemRoot = process.env.SystemRoot;
  run(node, [entry, 'setup', '--client', 'codex', '--yes'], { cwd: home, env });
  const config = readFileSync(join(home, '.codex', 'config.toml'), 'utf8');
  const seconds = Number(/^\s*tool_timeout_sec\s*=\s*(\d+)\s*$/m.exec(config)?.[1]);
  if (!(seconds > 0)) fail(`rea setup --client codex wrote no tool_timeout_sec:\n${config}`);
  return seconds;
}

// ---- deterministic tar ----

function octal(value, width) {
  return value.toString(8).padStart(width - 1, '0') + '\0';
}

function header(name, { size = 0, mode = 0o644, type = '0' }) {
  const block = Buffer.alloc(512);
  block.write(name, 0, 100, 'utf8');
  block.write(octal(mode, 8), 100, 'ascii');
  block.write(octal(0, 8), 108, 'ascii');
  block.write(octal(0, 8), 116, 'ascii');
  block.write(octal(size, 12), 124, 'ascii');
  block.write(octal(0, 12), 136, 'ascii');
  block.fill(' ', 148, 156);
  block.write(type, 156, 'ascii');
  block.write('ustar\0', 257, 'ascii');
  block.write('00', 263, 'ascii');
  let sum = 0;
  for (const byte of block) sum += byte;
  block.write(octal(sum, 7) + ' ', 148, 'ascii');
  return block;
}

const padding = (size) => Buffer.alloc((512 - (size % 512)) % 512);

/** Header blocks for name: a PAX path record first when the name does not fit ustar's 100 bytes. */
function headers(name, options) {
  if (Buffer.byteLength(name) <= 100) return [header(name, options)];
  const body = (length) => `${length} path=${name}\n`;
  let length = Buffer.byteLength(body(0));
  while (Buffer.byteLength(body(length)) !== length) length = Buffer.byteLength(body(length));
  const record = Buffer.from(body(length));
  return [header('././@PaxHeader', { size: record.length, type: 'x' }), record, padding(record.length), header(name.slice(0, 100), options)];
}

async function* tarStream(entries) {
  for (const entry of entries) {
    if (entry.dir) {
      yield* headers(`${entry.rel}/`, { mode: 0o755, type: '5' });
      continue;
    }
    const bytes = entry.bytes ?? readFileSync(entry.path);
    yield* headers(entry.rel, { size: bytes.length, mode: entry.mode });
    yield bytes;
    yield padding(bytes.length);
  }
  yield Buffer.alloc(1024);
}

// ---- steps ----

async function download(url, file, digest) {
  if (existsSync(file) && sha256(readFileSync(file)) === digest) return;
  const response = await fetch(url, { redirect: 'follow' });
  if (!response.ok) fail(`${url}: HTTP ${response.status}`);
  const bytes = Buffer.from(await response.arrayBuffer());
  const got = sha256(bytes);
  if (got !== digest) fail(`${url}: sha256 ${got}, pinned ${digest}`);
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, bytes);
}

/** The tarball name `npm pack --json` reports: an array before npm 12, an object keyed by package name since. */
function packedName(output) {
  const reported = JSON.parse(output);
  const first = Array.isArray(reported) ? reported[0] : Object.values(reported)[0];
  if (!first?.filename) fail(`npm pack reported no tarball: ${output.slice(0, 300)}`);
  return first.filename;
}

function packRea(work) {
  const source = process.env.REA_SOURCE;
  if (source) {
    const root = resolve(source);
    if (!existsSync(join(root, 'dist'))) fail(`REA_SOURCE=${root} has no dist/; run npm ci && npm run build:cached there first`);
    const commit = run('git', ['-C', root, 'rev-parse', 'HEAD']).trim();
    const name = packedName(run('npm', ['pack', '--json', '--ignore-scripts', '--pack-destination', work], { cwd: root }));
    return { tarball: join(work, name), lockfile: readFileSync(join(root, 'package-lock.json')), dev: commit };
  }
  const name = packedName(run('npm', ['pack', `${lock.rea.package}@${lock.rea.version}`, '--json', '--pack-destination', work]));
  const tarball = join(work, name);
  const integrity = `sha512-${createHash('sha512').update(readFileSync(tarball)).digest('base64')}`;
  if (integrity !== lock.rea.integrity) fail(`${name}: integrity ${integrity}, pinned ${lock.rea.integrity}`);
  const lockfile = readFileSync(join(component, lock.rea.lockfile));
  if (sha256(lockfile) !== lock.rea.lockfileSha256) fail(`${lock.rea.lockfile}: sha256 does not match rea.lock.json`);
  return { tarball, lockfile };
}

function checkLockRoot(pkg, lockfile) {
  const root = JSON.parse(lockfile).packages?.[''];
  const same = (a = {}, b = {}) => JSON.stringify(Object.entries(a).sort()) === JSON.stringify(Object.entries(b).sort());
  if (!root || root.version !== pkg.version || !same(root.dependencies, pkg.dependencies) || !same(root.optionalDependencies, pkg.optionalDependencies)) {
    fail(`the lockfile's root package does not match ${pkg.name}@${pkg.version}'s dependencies`);
  }
}

async function main() {
  const args = process.argv.slice(2);
  if (args.includes('--sync-skill')) {
    const work = mkdtempSync(join(tmpdir(), 'rea-skill-'));
    try {
      const { tarball } = packRea(work);
      run('tar', ['-xzf', tarball, '-C', work]);
      rmSync(pigletSkill, { recursive: true, force: true });
      const published = join(work, 'package/skills', upstreamSkill);
      for (const rel of skillFiles(published)) {
        mkdirSync(dirname(join(pigletSkill, rel)), { recursive: true });
        writeFileSync(join(pigletSkill, rel), adaptSkillFile(rel, readFileSync(join(published, rel))));
      }
      console.error(`payload: wrote ${shippedSkill} from ${lock.rea.package}@${lock.rea.version}'s ${upstreamSkill} into ${relative(repository, pigletSkill)}`);
    } finally {
      rmSync(work, { recursive: true, force: true });
    }
    return;
  }
  const target = args[0] ?? hostTarget();
  if (!target || !goTargets[target]) fail(`usage: payload.mjs [<os>/<arch>], one of ${Object.keys(goTargets).join(', ')}`);
  const [npmOS, npmCPU] = goTargets[target];
  const archive = lock.node.archives[target];
  const started = Date.now();
  const work = mkdtempSync(join(tmpdir(), 'rea-payload-'));
  try {
    const stage = join(work, 'stage');
    const reaDir = join(stage, 'rea');
    const nodeDir = join(stage, 'node');
    mkdirSync(reaDir, { recursive: true });
    mkdirSync(join(nodeDir, 'bin'), { recursive: true });

    // 1-3: REA from the pinned tarball, its tagged lockfile, production dependencies for the target.
    const { tarball, lockfile, dev } = packRea(work);
    run('tar', ['-xzf', tarball, '-C', reaDir, '--strip-components=1']);
    const pkg = JSON.parse(readFileSync(join(reaDir, 'package.json'), 'utf8'));
    checkLockRoot(pkg, lockfile);
    writeFileSync(join(reaDir, 'package-lock.json'), lockfile);
    const platformFlags = [`--os=${npmOS}`, `--cpu=${npmCPU}`, ...(npmOS === 'linux' ? ['--libc=glibc'] : [])];
    run('npm', ['ci', '--omit=dev', '--ignore-scripts', '--no-bin-links', '--no-audit', '--no-fund', '--engine-strict=false', '--loglevel=error', ...platformFlags], { cwd: reaDir });
    run('npm', ['audit', 'signatures', '--omit=dev'], { cwd: reaDir });
    rmSync(join(reaDir, 'node_modules/.package-lock.json'), { force: true });

    // 4: the pinned Node's executable and license.
    const nodeArchive = join(cacheDir, archive.file);
    await download(`https://nodejs.org/dist/v${lock.node.version}/${archive.file}`, nodeArchive, archive.sha256);
    const base = archive.file.replace(/\.(tar\.xz|zip)$/, '');
    const exe = target.startsWith('windows/') ? 'node.exe' : 'node';
    const member = target.startsWith('windows/') ? `${base}/node.exe` : `${base}/bin/node`;
    const unpack = join(work, 'node-archive');
    mkdirSync(unpack);
    run('tar', ['-xf', nodeArchive, '-C', unpack, member, `${base}/LICENSE`]);
    cpSync(join(unpack, member), join(nodeDir, 'bin', exe));
    cpSync(join(unpack, base, 'LICENSE'), join(nodeDir, 'LICENSE'));

    // 4b: the MCP tool deadline REA recommends for its server. `rea setup` writes it only for clients whose config has the
    // field (Codex: tool_timeout_sec); Pi's has none, and PiG's default of 60 s cancels REA's longer analyses. Ask REA itself,
    // with the default environment, in a throwaway HOME. The staged Node runs it when it is this machine's.
    const setupNode = target === hostTarget() ? join(nodeDir, 'bin', exe) : process.execPath;
    const toolTimeoutSec = recommendedToolTimeoutSec(setupNode, join(reaDir, 'scripts/rea.mjs'), work);

    // Gate 4: REA's skill in the Piglet is the published one, byte for byte apart from the prefixed name.
    const drift = skillDrift(join(reaDir, 'skills', upstreamSkill), pigletSkill);
    if (drift && !dev) fail(`${relative(repository, pigletSkill)} is not ${lock.rea.package}@${lock.rea.version}'s skill (${drift}). Run payload.mjs --sync-skill.`);

    // 5: digests, payload.json, deterministic tar, zstd.
    const entries = walk(stage);
    const tree = createHash('sha256');
    let files = 0;
    let bytes = 0;
    for (const entry of entries) {
      if (entry.dir) continue;
      const content = readFileSync(entry.path);
      tree.update(`${entry.mode.toString(8)} ${sha256(content)} ${entry.rel}\n`);
      files++;
      bytes += content.length;
    }
    const info = {
      target,
      rea: { package: pkg.name, version: pkg.version, ...(dev ? { dev } : { tag: lock.rea.tag, commit: lock.rea.commit, integrity: lock.rea.integrity }) },
      node: { version: lock.node.version, archive: archive.file, sha256: archive.sha256, executable: `node/bin/${exe}` },
      entry: 'rea/scripts/rea.mjs',
      toolTimeoutSec,
      treeSha256: tree.digest('hex'),
      files,
      bytes,
    };
    const infoBytes = Buffer.from(`${JSON.stringify(info, null, 2)}\n`);
    entries.push({ rel: 'payload.json', bytes: infoBytes, dir: false, mode: 0o644 });
    entries.sort((a, b) => (a.rel < b.rel ? -1 : a.rel > b.rel ? 1 : 0));

    mkdirSync(payloadDir, { recursive: true });
    const stem = target.replace('/', '_');
    const out = join(payloadDir, `${stem}.tar.zst`);
    const zstd = spawn('zstd', ['-19', '--long=27', '-T0', '-q', '-f', '-o', out], { stdio: ['pipe', 'inherit', 'inherit'] });
    const exited = new Promise((resolveExit, reject) => {
      zstd.on('error', (error) => reject(new Error(`cannot run zstd (install it): ${error.message}`)));
      zstd.on('close', (code) => (code === 0 ? resolveExit() : reject(new Error(`zstd exited ${code}`))));
    });
    await pipeline(Readable.from(tarStream(entries)), zstd.stdin);
    await exited;

    const compressed = readFileSync(out);
    const sidecar = { ...info, archiveSha256: sha256(compressed), archiveBytes: compressed.length };
    writeFileSync(join(payloadDir, `${stem}.json`), `${JSON.stringify(sidecar, null, 2)}\n`);
    for (const name of readdirSync(payloadDir)) {
      if (/\.(tar\.zst|json)$/.test(name) && !name.startsWith(`${stem}.`)) rmSync(join(payloadDir, name));
    }
    const mb = (n) => `${(n / 1e6).toFixed(1)} MB`;
    console.error(`payload: ${relative(repository, out)} ${mb(compressed.length)} (${files} files, ${mb(bytes)} unpacked), REA ${pkg.version}${dev ? ` dev ${dev.slice(0, 12)}` : ''}, Node ${lock.node.version}, ${((Date.now() - started) / 1000).toFixed(1)} s`);
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
}

await main().catch((error) => fail(error.message));
