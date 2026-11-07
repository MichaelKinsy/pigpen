#!/usr/bin/env node
// Builds, packages, checksums and verifies the prebuilt `pig-acp` executables that release-pig-acp.yml publishes. pig-acp is the
// ACP companion of the `acp` Package (components/acp): an editor starts it and it starts `pig --mode rpc` per session. It is a
// plain Go program with no dependency, so a release is one archive per platform of PiG's own release list (PiG
// .github/workflows/release-candidate.yml: android/arm64, linux and darwin on amd64 and arm64, windows on amd64 and arm64), a
// SHA256SUMS file, an Ed25519 signature of that file by the Pigpen release key (release-keys/pigpen-piglets.pub, the key that signs
// Piglet Binaries and release indexes), and a Sigstore build-provenance bundle beside every archive (made by the workflow, as
// release.yml does for Piglet Binaries).
//
//   plan                                      the tag is components/acp/v<version>; writes the build matrix (GitHub Actions outputs)
//   build <os/arch> <out-dir> [--go-env K=V]  one archive, built reproducibly: CGO off, -trimpath, -buildvcs=false, no build id,
//                                             the version in -X main.version, mtimes at SOURCE_DATE_EPOCH, sorted entries
//   checksums <dir>                           SHA256SUMS over the archives (sorted, `<sha256>  <name>`)
//   verify <dir> <public-key.pub>             SHA256SUMS matches every asset, its signature verifies, every archive holds what it
//                                             should; exit 1 and say what is wrong otherwise
//   smoke <archive>                           extract a native archive, run --version and the initialize handshake
//   snapshot <out-dir> [--go-version V]       everything above for every target with a throwaway key, the way the workflow does it
//
// The signature is `openssl pkeyutl -sign -rawin` over SHA256SUMS (PKCS#8 Ed25519 key, the format `pig piglet keygen` writes), made
// by the workflow's publish job and by `snapshot` with the same command (SIGN_ARGS), and checked by `verify` with node's crypto.
import { spawnSync } from 'node:child_process';
import { createHash, createPublicKey, verify as edVerify } from 'node:crypto';
import { appendFileSync, chmodSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { crc32, deflateRawSync, gunzipSync, gzipSync, inflateRawSync } from 'node:zlib';

export const root = fileURLToPath(new URL('../', import.meta.url));
export const COMPONENT = 'components/acp';
export const GO_MODULE = 'components/acp/extensions/acp/cmd/pig-acp';
export const PACKAGE_NAME = '@pi-in-go/pigpen-acp';
export const EXECUTABLE = 'pig-acp';
/** The Package's documents that the archive carries beside the executable. */
export const DOCS = ['LICENSE', 'CREDITS.md', 'README.md'];

/** PiG's release platforms. `runner` is where the archive is smoke-tested natively (scripts/runners.mjs); none for the rest. */
export const TARGETS = [
  { target: 'linux/amd64', runner: 'ubuntu-24.04' },
  { target: 'linux/arm64', runner: 'ubuntu-24.04-arm' },
  { target: 'darwin/arm64', runner: 'macos-15' },
  { target: 'darwin/amd64', runner: 'macos-15-intel' },
  { target: 'windows/amd64', runner: 'windows-2025' },
  { target: 'windows/arm64', runner: null },
  { target: 'android/arm64', runner: null },
].map((entry) => {
  const [goos, goarch] = entry.target.split('/');
  return { ...entry, goos, goarch, format: goos === 'windows' ? 'zip' : 'tar.gz', exe: goos === 'windows' ? `${EXECUTABLE}.exe` : EXECUTABLE };
});

/** The command that signs SHA256SUMS; the workflow's publish step runs exactly this (a test keeps them equal). */
export const SIGN_ARGS = (key, sums, sig) => ['pkeyutl', '-sign', '-rawin', '-inkey', key, '-in', sums, '-out', sig];

const fail = (message) => { throw new Error(message); };
const sha256 = (data) => createHash('sha256').update(data).digest('hex');

/** The release's version, taken from the Package manifest, and the two places the executable carries it (checked equal). */
export function readRelease(dir = root) {
  const manifest = JSON.parse(readFileSync(join(dir, COMPONENT, 'package.json'), 'utf8'));
  if (manifest.name !== PACKAGE_NAME) fail(`${COMPONENT}/package.json is ${manifest.name}, not ${PACKAGE_NAME}`);
  const version = manifest.version;
  if (!/^\d+\.\d+\.\d+$/.test(version)) fail(`version ${version} is not x.y.z`);
  const literal = (file, pattern) => readFileSync(join(dir, GO_MODULE, file), 'utf8').match(pattern)?.[1];
  const main = literal('main.go', /^var version = "([^"]+)"$/m);
  const agent = literal('internal/acp/agent.go', /^var Version = "([^"]+)"$/m);
  if (main !== version) fail(`main.go says version ${main}, the Package says ${version}`);
  if (agent !== version) fail(`internal/acp/agent.go says version ${agent}, the Package says ${version}`);
  return { version, tag: `components/acp/v${version}` };
}

export const archiveName = (version, { goos, goarch, format }) => `${EXECUTABLE}-${version}-${goos}-${goarch}.${format}`;
export const archiveDir = (version, { goos, goarch }) => `${EXECUTABLE}-${version}-${goos}-${goarch}`;

/** The plan: the tag must name this Package at its manifest version. Without a tag (a local run) it plans the release as it stands. */
export function plan(env = process.env, dir = root) {
  const { version, tag } = readRelease(dir);
  const given = env.GITHUB_REF_TYPE === 'tag' ? env.GITHUB_REF_NAME : '';
  if (given && given !== tag) fail(`tag ${given} is not ${tag}`);
  const include = TARGETS.map(({ target, goos, goarch, format }) => ({ target, goos, goarch, format, name: archiveName(version, { goos, goarch, format }) }));
  return { version, tag, matrix: { include }, smoke: { include: TARGETS.filter((t) => t.runner).map(({ target, goos, goarch, format, runner }) => ({ target, goos, goarch, format, runner, name: archiveName(version, { goos, goarch, format }) })) } };
}

// ---- reproducible archives -------------------------------------------------------------------------------------------------

/** ustar, no PAX: every name here is under 100 bytes. Sorted by the caller. */
function ustar(entries, mtime) {
  const blocks = [];
  const octal = (value, width) => value.toString(8).padStart(width - 1, '0') + '\0';
  for (const { name, data, mode, dir } of entries) {
    if (Buffer.byteLength(name) > 100) fail(`${name} is too long for a ustar name`);
    const header = Buffer.alloc(512);
    header.write(name, 0, 'utf8');
    header.write(octal(mode, 8), 100);
    header.write(octal(0, 8), 108); // uid
    header.write(octal(0, 8), 116); // gid
    header.write(octal(dir ? 0 : data.length, 12), 124);
    header.write(octal(mtime, 12), 136);
    header.write('        ', 148); // checksum placeholder: eight spaces
    header.write(dir ? '5' : '0', 156);
    header.write('ustar\0', 257);
    header.write('00', 263);
    const sum = header.reduce((total, byte) => total + byte, 0);
    header.write(sum.toString(8).padStart(6, '0') + '\0 ', 148);
    blocks.push(header);
    if (!dir) blocks.push(data, Buffer.alloc((512 - (data.length % 512)) % 512));
  }
  blocks.push(Buffer.alloc(1024));
  return Buffer.concat(blocks);
}

/** gzip with the header zlib would otherwise fill from the host: no name, mtime 0, OS byte 3 (Unix), level 9. */
export function gzipReproducible(buffer) {
  const out = gzipSync(buffer, { level: 9 });
  out[4] = out[5] = out[6] = out[7] = 0;
  out[8] = 2; // maximum compression
  out[9] = 3;
  return out;
}

/** A zip with deflated entries, DOS time from the epoch, Unix modes in the external attributes. */
function zip(entries, mtime) {
  const when = new Date(Math.max(mtime, 315532800) * 1000);
  const dosTime = (when.getUTCHours() << 11) | (when.getUTCMinutes() << 5) | (when.getUTCSeconds() >> 1);
  const dosDate = ((when.getUTCFullYear() - 1980) << 9) | ((when.getUTCMonth() + 1) << 5) | when.getUTCDate();
  const locals = [];
  const centrals = [];
  let offset = 0;
  for (const { name, data, mode, dir } of entries) {
    const body = dir ? Buffer.alloc(0) : deflateRawSync(data, { level: 9 });
    const nameBytes = Buffer.from(name + (dir ? '/' : ''), 'utf8');
    const crc = dir ? 0 : crc32(data);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0x0800, 6); // UTF-8 names
    local.writeUInt16LE(dir ? 0 : 8, 8);
    local.writeUInt16LE(dosTime, 10);
    local.writeUInt16LE(dosDate, 12);
    local.writeUInt32LE(crc, 14);
    local.writeUInt32LE(body.length, 18);
    local.writeUInt32LE(dir ? 0 : data.length, 22);
    local.writeUInt16LE(nameBytes.length, 26);
    locals.push(local, nameBytes, body);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE((3 << 8) | 20, 4); // made by Unix, 2.0
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(0x0800, 8);
    central.writeUInt16LE(dir ? 0 : 8, 10);
    central.writeUInt16LE(dosTime, 12);
    central.writeUInt16LE(dosDate, 14);
    central.writeUInt32LE(crc, 16);
    central.writeUInt32LE(body.length, 20);
    central.writeUInt32LE(dir ? 0 : data.length, 24);
    central.writeUInt16LE(nameBytes.length, 28);
    central.writeUInt32LE(((dir ? 0o040000 : 0o100000) | mode) * 0x10000 + (dir ? 0x10 : 0), 38);
    central.writeUInt32LE(offset, 42);
    centrals.push(central, nameBytes);
    offset += local.length + nameBytes.length + body.length;
  }
  const directory = Buffer.concat(centrals);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(entries.length, 8);
  end.writeUInt16LE(entries.length, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, directory, end]);
}

/** The archive's entries: the directory, the executable, then the Package's documents, in name order. */
export function archiveEntries(version, target, binary, dir = root) {
  const top = archiveDir(version, target);
  const files = [
    { name: `${top}/${target.exe}`, data: binary, mode: target.goos === 'windows' ? 0o644 : 0o755 },
    ...DOCS.map((doc) => ({ name: `${top}/${doc}`, data: readFileSync(join(dir, COMPONENT, doc)), mode: 0o644 })),
  ].sort((a, b) => (a.name < b.name ? -1 : 1));
  return [{ name: top, mode: 0o755, dir: true }, ...files];
}

export function makeArchive(version, target, binary, epoch, dir = root) {
  const entries = archiveEntries(version, target, binary, dir);
  return target.format === 'zip' ? zip(entries, epoch) : gzipReproducible(ustar(entries.map((e) => (e.dir ? { ...e, name: e.name + '/' } : e)), epoch));
}

/** The files of an archive: `{ name, mode, data }`, read back with no tool but node (the verifier must not trust what built it). */
export function readArchive(file) {
  const bytes = readFileSync(file);
  const out = [];
  if (file.endsWith('.zip')) {
    const end = bytes.lastIndexOf(Buffer.from([0x50, 0x4b, 0x05, 0x06]));
    if (end < 0) fail(`${basename(file)} is not a zip`);
    const count = bytes.readUInt16LE(end + 10);
    let at = bytes.readUInt32LE(end + 16);
    for (let i = 0; i < count; i += 1) {
      const method = bytes.readUInt16LE(at + 10);
      const crc = bytes.readUInt32LE(at + 16);
      const compressed = bytes.readUInt32LE(at + 20);
      const nameLength = bytes.readUInt16LE(at + 28);
      const extraLength = bytes.readUInt16LE(at + 30);
      const commentLength = bytes.readUInt16LE(at + 32);
      const attributes = bytes.readUInt32LE(at + 38);
      const local = bytes.readUInt32LE(at + 42);
      const name = bytes.subarray(at + 46, at + 46 + nameLength).toString('utf8');
      const start = local + 30 + bytes.readUInt16LE(local + 26) + bytes.readUInt16LE(local + 28);
      const raw = bytes.subarray(start, start + compressed);
      const data = method === 8 ? inflateRawSync(raw) : raw;
      if (!name.endsWith('/') && crc32(data) !== crc) fail(`${basename(file)}: ${name} fails its CRC`);
      out.push({ name: name.replace(/\/$/, ''), dir: name.endsWith('/'), mode: (attributes >>> 16) & 0o7777, data });
      at += 46 + nameLength + extraLength + commentLength;
    }
    return out;
  }
  const tar = gunzipSync(bytes);
  for (let at = 0; at + 512 <= tar.length && tar[at] !== 0; ) {
    const field = (from, to) => tar.subarray(at + from, at + to).toString('utf8').replace(/\0.*$/s, '');
    const size = parseInt(field(124, 136).trim() || '0', 8);
    const typeflag = field(156, 157);
    const checksum = parseInt(field(148, 156), 8);
    const header = Buffer.from(tar.subarray(at, at + 512));
    header.fill(0x20, 148, 156);
    if (header.reduce((total, byte) => total + byte, 0) !== checksum) fail(`${basename(file)}: ${field(0, 100)} has a bad header checksum`);
    out.push({ name: field(0, 100).replace(/\/$/, ''), dir: typeflag === '5', mode: parseInt(field(100, 108).trim(), 8), mtime: parseInt(field(136, 148).trim(), 8), data: tar.subarray(at + 512, at + 512 + size) });
    at += 512 + Math.ceil(size / 512) * 512;
  }
  return out;
}

// ---- build ----------------------------------------------------------------------------------------------------------------

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) fail(`${command} ${args.join(' ')} exited ${result.status}\n${result.stdout ?? ''}${result.stderr ?? ''}`);
  return result.stdout;
}

/** The commit time every mtime is set to: SOURCE_DATE_EPOCH, else the HEAD commit's author time. */
export function sourceDateEpoch(env = process.env, dir = root) {
  if (env.SOURCE_DATE_EPOCH) return Number(env.SOURCE_DATE_EPOCH);
  return Number(run('git', ['-C', dir, 'log', '-1', '--format=%ct']).trim());
}

/** Compiles the executable for `target` into `file`. Android is linked with the NDK by PiG's own script, which also checks the ELF. */
export function compile(target, version, file, { env = process.env, dir = root, androidScript = env.PIG_ANDROID_SCRIPT } = {}) {
  const flags = ['-trimpath', '-buildvcs=false', '-ldflags', `-s -w -buildid= -X main.version=${version}`, '-o', file, './'];
  const cwd = join(dir, GO_MODULE);
  const buildEnv = { ...env, GOOS: target.goos, GOARCH: target.goarch, CGO_ENABLED: '0', GOFLAGS: '-mod=readonly', GOWORK: 'off' };
  if (target.goos === 'android') {
    if (!androidScript) fail('android/arm64 needs PIG_ANDROID_SCRIPT, PiG\'s automation/release/android-binary.sh (and an NDK: ANDROID_NDK_LATEST_HOME)');
    // pig-acp imports no cgo package, so without -linkmode=external Go links it statically and without bionic, which Termux's
    // loader refuses; the external link makes the NDK's clang produce the dynamic PIE that android-binary.sh checks.
    const android = flags.map((flag) => (flag.startsWith('-s -w') ? `${flag} -linkmode=external` : flag));
    run('bash', [androidScript, 'build', ...android], { cwd, env: { ...buildEnv, CGO_ENABLED: '1' } });
    run('bash', [androidScript, 'check', file], { cwd, env: buildEnv });
  } else {
    run('go', ['build', ...flags], { cwd, env: buildEnv });
  }
}

/** Builds and packs one archive into `outDir`; returns its path. */
export function build(targetName, outDir, options = {}) {
  const target = TARGETS.find((t) => t.target === targetName) ?? fail(`unknown target ${targetName}`);
  const dir = options.dir ?? root;
  const { version } = readRelease(dir);
  const scratch = mkdtempSync(join(tmpdir(), 'pig-acp-'));
  try {
    const file = join(scratch, target.exe);
    compile(target, version, file, options);
    mkdirSync(outDir, { recursive: true });
    const out = join(outDir, archiveName(version, target));
    writeFileSync(out, makeArchive(version, target, readFileSync(file), sourceDateEpoch(options.env, dir), dir));
    return out;
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}

// ---- checksums, signature, verification -------------------------------------------------------------------------------------

export const SUMS = 'SHA256SUMS';
export const SIGNATURE = 'SHA256SUMS.sig';
const isAsset = (name) => /^pig-acp-\d+\.\d+\.\d+-(linux|darwin|windows|android)-(amd64|arm64)\.(tar\.gz|zip)$/.test(name);

export function checksums(dir) {
  const names = readdirSync(dir).filter(isAsset).sort();
  if (!names.length) fail(`no pig-acp archive in ${dir}`);
  const text = names.map((name) => `${sha256(readFileSync(join(dir, name)))}  ${name}\n`).join('');
  writeFileSync(join(dir, SUMS), text);
  return text;
}

/** Everything `verify` finds wrong with a release directory; empty when it is sound. */
export function verifyProblems(dir, publicKeyFile, { version } = readRelease()) {
  const problems = [];
  const names = readdirSync(dir).sort();
  const archives = names.filter(isAsset);
  const expected = TARGETS.map((t) => archiveName(version, t));
  for (const name of expected) if (!archives.includes(name)) problems.push(`missing archive ${name}`);
  for (const name of archives) if (!expected.includes(name)) problems.push(`unexpected archive ${name}`);
  for (const name of names) {
    if (isAsset(name) || [SUMS, SIGNATURE].includes(name) || (name.endsWith('.sigstore.json') && isAsset(name.slice(0, -'.sigstore.json'.length)))) continue;
    problems.push(`unexpected file ${name}`);
  }
  if (!names.includes(SUMS) || !names.includes(SIGNATURE)) return [...problems, `${SUMS} and ${SIGNATURE} are both required`];
  const sums = readFileSync(join(dir, SUMS));
  const listed = new Map();
  for (const line of sums.toString('utf8').split('\n').filter(Boolean)) {
    const match = /^([0-9a-f]{64}) {2}(\S+)$/.exec(line);
    if (!match) { problems.push(`${SUMS}: malformed line ${JSON.stringify(line)}`); continue; }
    listed.set(match[2], match[1]);
  }
  for (const name of archives) {
    if (!listed.has(name)) problems.push(`${SUMS} does not list ${name}`);
    else if (listed.get(name) !== sha256(readFileSync(join(dir, name)))) problems.push(`${name} does not match its ${SUMS} entry`);
  }
  for (const name of listed.keys()) if (!archives.includes(name)) problems.push(`${SUMS} lists ${name}, which is not an archive here`);
  const key = createPublicKey(readFileSync(publicKeyFile));
  if (key.asymmetricKeyType !== 'ed25519') problems.push(`${publicKeyFile} is not an Ed25519 key`);
  else if (!edVerify(null, sums, key, readFileSync(join(dir, SIGNATURE)))) problems.push(`${SIGNATURE} is not a signature of ${SUMS} by ${basename(publicKeyFile)}`);
  for (const name of archives) {
    const target = TARGETS.find((t) => archiveName(version, t) === name);
    if (!target) continue;
    try {
      const entries = readArchive(join(dir, name));
      const top = archiveDir(version, target);
      const want = [top, `${top}/${target.exe}`, ...DOCS.map((doc) => `${top}/${doc}`)].sort();
      const got = entries.map((e) => e.name).sort();
      if (JSON.stringify(got) !== JSON.stringify(want)) problems.push(`${name} holds ${got.join(', ')}; expected ${want.join(', ')}`);
      const binary = entries.find((e) => e.name === `${top}/${target.exe}`);
      if (binary && target.goos !== 'windows' && (binary.mode & 0o111) !== 0o111) problems.push(`${name}: ${target.exe} is not executable (mode ${binary.mode.toString(8)})`);
      if (binary && !binaryMatches(binary.data, target)) problems.push(`${name}: ${target.exe} is not a ${target.target} executable`);
      if (binary && !binary.data.includes(Buffer.from(`${version}`))) problems.push(`${name}: ${target.exe} does not carry version ${version}`);
    } catch (error) {
      problems.push(`${name}: ${error.message}`);
    }
  }
  return problems;
}

/** The executable's own header says the platform the archive's name claims (ELF, Mach-O or PE, and the machine). */
export function binaryMatches(data, { goos, goarch }) {
  const magic = data.readUInt32BE(0);
  if (goos === 'linux' || goos === 'android') {
    if (magic !== 0x7f454c46) return false;
    return data.readUInt16LE(18) === (goarch === 'amd64' ? 62 : 183);
  }
  if (goos === 'darwin') {
    if (data.readUInt32LE(0) !== 0xfeedfacf) return false;
    return data.readUInt32LE(4) === (goarch === 'amd64' ? 0x01000007 : 0x0100000c);
  }
  if (data.subarray(0, 2).toString('latin1') !== 'MZ') return false;
  const pe = data.readUInt32LE(0x3c);
  if (data.subarray(pe, pe + 4).toString('latin1') !== 'PE\0\0') return false;
  return data.readUInt16LE(pe + 4) === (goarch === 'amd64' ? 0x8664 : 0xaa64);
}

// ---- smoke ----------------------------------------------------------------------------------------------------------------

/** Extracts a native archive and drives the executable: --version, then initialize with no capabilities and with auth.terminal. */
export function smoke(archive, { version } = readRelease()) {
  const target = TARGETS.find((t) => archiveName(version, t) === basename(archive)) ?? fail(`${basename(archive)} is not a ${version} archive`);
  const scratch = mkdtempSync(join(tmpdir(), 'pig-acp-smoke-'));
  try {
    const top = archiveDir(version, target);
    const entry = readArchive(archive).find((e) => e.name === `${top}/${target.exe}`) ?? fail(`${basename(archive)} has no ${target.exe}`);
    const file = join(scratch, target.exe);
    writeFileSync(file, entry.data);
    chmodSync(file, 0o755);
    const said = run(file, ['--version']).trim();
    if (said !== `pig-acp ${version} (ACP protocol version 1)`) fail(`--version said ${JSON.stringify(said)}`);
    const ask = (caps) => {
      const request = { jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: 1, ...(caps === undefined ? {} : { clientCapabilities: caps }) } };
      const out = run(file, [], { input: JSON.stringify(request) + '\n', stdio: ['pipe', 'pipe', 'pipe'] });
      return JSON.parse(out.trim().split('\n')[0]).result;
    };
    const bare = ask({});
    if (bare.protocolVersion !== 1 || bare.agentInfo?.version !== version || JSON.stringify(bare.authMethods) !== '[]') fail(`initialize without capabilities answered ${JSON.stringify(bare)}`);
    const terminal = ask({ auth: { terminal: true } });
    if (terminal.authMethods?.length !== 1 || terminal.authMethods[0].type !== 'terminal') fail(`initialize with auth.terminal answered ${JSON.stringify(terminal.authMethods)}`);
    return { said, bare: bare.authMethods, terminal: terminal.authMethods[0].id };
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}

// ---- snapshot ---------------------------------------------------------------------------------------------------------------

/** The release as the workflow makes it, locally: build every target twice (the bytes must be equal), checksum, sign with a throwaway key, verify. */
export function snapshot(outDir, { targets = TARGETS.map((t) => t.target), env = process.env, log = console.log, androidScript = env.PIG_ANDROID_SCRIPT } = {}) {
  const { version } = readRelease();
  const epoch = sourceDateEpoch(env);
  const first = join(outDir, 'a');
  const second = join(outDir, 'b');
  rmSync(outDir, { recursive: true, force: true });
  const assets = join(outDir, 'release');
  mkdirSync(assets, { recursive: true });
  const options = { env: { ...env, SOURCE_DATE_EPOCH: String(epoch) }, androidScript };
  for (const target of targets) {
    const one = build(target, first, options);
    const two = build(target, second, options);
    const same = sha256(readFileSync(one)) === sha256(readFileSync(two));
    log(`${same ? 'reproducible' : 'DIFFERENT'}  ${basename(one)}  ${statSync(one).size} bytes`);
    if (!same) fail(`${basename(one)} differs between two builds of the same tree`);
    writeFileSync(join(assets, basename(one)), readFileSync(one));
  }
  const keyDir = mkdtempSync(join(tmpdir(), 'pig-acp-key-'));
  try {
    const key = join(keyDir, 'snapshot.key');
    const pub = join(keyDir, 'snapshot.pub');
    run('openssl', ['genpkey', '-algorithm', 'ED25519', '-out', key]);
    run('openssl', ['pkey', '-in', key, '-pubout', '-out', pub]);
    checksums(assets);
    run('openssl', SIGN_ARGS(key, join(assets, SUMS), join(assets, SIGNATURE)));
    run('openssl', ['pkeyutl', '-verify', '-rawin', '-pubin', '-inkey', pub, '-in', join(assets, SUMS), '-sigfile', join(assets, SIGNATURE)]);
    log('openssl verified SHA256SUMS.sig');
    writeFileSync(join(outDir, 'snapshot.pub'), readFileSync(pub));
    const complete = targets.length === TARGETS.length;
    const problems = verifyProblems(assets, pub, { version }).filter((p) => complete || !/^missing archive/.test(p));
    if (problems.length) fail(`verify found:\n  ${problems.join('\n  ')}`);
    log(`verify: ${targets.length} archive(s), ${SUMS} and its signature are sound`);
  } finally {
    rmSync(keyDir, { recursive: true, force: true });
  }
  return assets;
}

// ---- command line -----------------------------------------------------------------------------------------------------------

function main(argv) {
  const [command, ...rest] = argv;
  const flag = (name) => { const i = rest.indexOf(name); return i < 0 ? undefined : rest[i + 1]; };
  switch (command) {
    case 'plan': {
      const { version, tag, matrix, smoke: smokeMatrix } = plan();
      const lines = [`version=${version}`, `tag=${tag}`, `matrix=${JSON.stringify(matrix)}`, `smoke=${JSON.stringify(smokeMatrix)}`];
      if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, lines.join('\n') + '\n');
      else console.log(lines.join('\n'));
      return;
    }
    case 'build': console.log(build(rest[0], resolve(rest[1]))); return;
    case 'checksums': process.stdout.write(checksums(resolve(rest[0]))); return;
    case 'verify': {
      const problems = verifyProblems(resolve(rest[0]), resolve(rest[1]));
      if (problems.length) { for (const problem of problems) console.error(`verify: ${problem}`); process.exitCode = 1; } else console.log('verify: ok');
      return;
    }
    case 'smoke': console.log(JSON.stringify(smoke(resolve(rest[0])))); return;
    case 'snapshot': {
      const targets = flag('--targets')?.split(',');
      snapshot(resolve(rest[0]), targets ? { targets } : {});
      return;
    }
    default:
      console.error('usage: pig-acp-release.mjs plan | build <os/arch> <dir> | checksums <dir> | verify <dir> <pub> | smoke <archive> | snapshot <dir> [--targets a,b]');
      process.exitCode = 2;
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) main(process.argv.slice(2));
