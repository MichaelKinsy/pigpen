#!/usr/bin/env node
// govulncheck gate: every Go module of the repository must have no vulnerability that its code can reach.
//
//   node scripts/govulncheck.mjs [os/arch ...]      (default: every target a Piglet can build for, scripts/runners.mjs)
//
// Each module is scanned from one temporary go.work (scripts/go-modules.mjs): the PiG SDK is replaced with the staged SDK, as
// `pig` does when it builds an extension, and every local module is replaced by its directory, so a requirement at
// v0.0.0 resolves. Outside a workspace govulncheck cannot load those modules ("unknown revision components/typesafe/v0.0.0").
// Needs PIG_SDK_DIR or PIG_BIN (see go-modules.mjs) and Go.
//
// The scan is govulncheck's default source mode: it follows the call graph, so a vulnerable function the module's code can
// call is a finding that fails the gate, and a vulnerable module or package that is only imported is reported and does not.
// The gate also fails when a module is not scanned: any go.mod below the repository that govulncheck did not load, a failed
// load, or a run that reports no findings stream. It never skips a module. The only exceptions are the records in
// govulncheck-accepted.json: one module directory, one vulnerable module at one version, listed OSV ids, no fixed version, and
// findings that are only the init of a linked package (see `excuse`); a record the run no longer reports fails the gate.
//
// The version is pinned here: it is installed with `go install ...@<version>` (the module proxy verifies it against the
// checksum database) into a scratch directory, so the check never depends on a govulncheck found on PATH.
import { execFileSync, spawn } from 'node:child_process';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { availableParallelism } from 'node:os';
import { join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { goModules, goWorkEnv, root, scratchDir } from './go-modules.mjs';
import { runners } from './runners.mjs';

/** The govulncheck release the gate installs. Raise it deliberately: a new release can change what is reported. */
export const GOVULNCHECK_VERSION = 'v1.9.0';

const SKIPPED_DIRECTORIES = new Set(['node_modules', '.git', '.pig-src', 'dist']);

/** Every directory below `directory` that holds a go.mod, sorted. Nothing is left out: a module that is not scanned is a failure. */
export function allGoModules(directory = root) {
  const found = [];
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      if (entry.isFile() && entry.name === 'go.mod') found.push(dir);
      if (entry.isDirectory() && !SKIPPED_DIRECTORIES.has(entry.name)) walk(join(dir, entry.name));
    }
  };
  walk(directory);
  return found.sort();
}

/**
 * The JSON objects of `govulncheck -format json` output: a concatenation of pretty-printed objects, not one document per
 * line. Splits at the braces that close a top-level object, ignoring braces inside strings.
 */
export function parseMessages(text) {
  const messages = [];
  let depth = 0, start = -1, inString = false, escaped = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (inString) {
      if (escaped) escaped = false;
      else if (c === '\\') escaped = true;
      else if (c === '"') inString = false;
      continue;
    }
    if (c === '"') inString = true;
    else if (c === '{') { if (depth === 0) start = i; depth++; }
    else if (c === '}') {
      depth--;
      if (depth < 0) throw new Error('govulncheck output: unbalanced }');
      if (depth === 0) messages.push(JSON.parse(text.slice(start, i + 1)));
    }
  }
  if (depth !== 0 || inString) throw new Error('govulncheck output ends inside an object');
  return messages;
}

/**
 * What the stream says. A finding whose first trace frame names a function is reachable from the module's code (a call
 * stack ends in it); one with only a package or module is imported or required but not called, and is informational.
 */
export function classify(messages) {
  const osvs = new Map(messages.filter((m) => m.osv).map((m) => [m.osv.id, m.osv]));
  const called = new Map();
  const imported = new Set();
  for (const { finding } of messages) {
    if (!finding) continue;
    const frame = finding.trace?.[0] ?? {};
    if (frame.function) {
      const entry = called.get(finding.osv) ?? { id: finding.osv, fixed: finding.fixed_version ?? '', symbols: new Set(), modules: new Set(), initOnly: true };
      entry.symbols.add(`${frame.package ?? frame.module}.${frame.function}`);
      entry.modules.add(`${frame.module}@${frame.version ?? ''}`);
      entry.initOnly &&= frame.function === 'init';
      called.set(finding.osv, entry);
    } else {
      imported.add(finding.osv);
    }
  }
  for (const id of called.keys()) imported.delete(id);
  const summary = (id) => osvs.get(id)?.summary ?? '';
  return {
    config: messages.find((m) => m.config)?.config,
    called: [...called.values()].map((entry) => ({ ...entry, symbols: [...entry.symbols].sort(), modules: [...entry.modules].sort(), summary: summary(entry.id) })).sort((a, b) => a.id.localeCompare(b.id)),
    imported: [...imported].sort(),
  };
}

/** Install the pinned govulncheck into a scratch directory; returns its path. */
export function installGovulncheck(version = GOVULNCHECK_VERSION, env = process.env) {
  const bin = scratchDir('pigpen-govulncheck-');
  // Outside any module and workspace: `go install pkg@version` must not read a go.work.
  execFileSync('go', ['install', `golang.org/x/vuln/cmd/govulncheck@${version}`], {
    cwd: bin, stdio: ['ignore', 'inherit', 'inherit'], env: { ...env, GOBIN: bin, GOWORK: 'off', GOFLAGS: '' },
  });
  const tool = join(bin, process.platform === 'win32' ? 'govulncheck.exe' : 'govulncheck');
  if (!existsSync(tool)) throw new Error(`go install did not produce ${tool}`);
  const reported = execFileSync(tool, ['-version'], { encoding: 'utf8', env }).match(/govulncheck@(\S+)/)?.[1];
  if (reported !== version) throw new Error(`installed govulncheck reports ${reported}, not ${version}`);
  return tool;
}

/** Run one scan; resolves { code, stdout, stderr }. */
function scan(tool, directory, env) {
  return new Promise((done) => {
    const child = spawn(tool, ['-format', 'json', './...'], { cwd: directory, env });
    let stdout = '', stderr = '';
    child.stdout.on('data', (chunk) => { stdout += chunk; });
    child.stderr.on('data', (chunk) => { stderr += chunk; });
    child.on('error', (error) => done({ code: -1, stdout, stderr: `${stderr}${error.message}` }));
    child.on('close', (code) => done({ code, stdout, stderr }));
  });
}

/** Every recorded exception: scripts/govulncheck-accepted.json. See acceptedProblems for what a record must say. */
export function loadAccepted(file = fileURLToPath(new URL('./govulncheck-accepted.json', import.meta.url))) {
  return JSON.parse(readFileSync(file, 'utf8')).accepted ?? [];
}

/** What is wrong with the recorded exceptions themselves: each names a module directory that exists, one vulnerable module at one version, its OSV ids and why. */
export function acceptedProblems(accepted, directory = root) {
  const problems = [];
  accepted.forEach((record, index) => {
    const where = `govulncheck-accepted.json[${index}]`;
    if (typeof record.directory !== 'string' || !existsSync(join(directory, record.directory, 'go.mod'))) problems.push(`${where}: directory is not a Go module of the repository`);
    if (typeof record.module !== 'string' || !record.module) problems.push(`${where}: no vulnerable module`);
    if (!/^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(record.version ?? '')) problems.push(`${where}: version is not one exact module version`);
    if (!Array.isArray(record.ids) || !record.ids.length || !record.ids.every((id) => /^GO-\d{4}-\d+$/.test(id))) problems.push(`${where}: ids must list GO-YYYY-NNNN identifiers`);
    if (!Array.isArray(record.traces) || !record.traces.length) problems.push(`${where}: traces must list the call chains govulncheck reports`);
    if (typeof record.reason !== 'string' || record.reason.trim().length < 40) problems.push(`${where}: reason must say why the code does not use the vulnerable module`);
  });
  return problems;
}

/**
 * Splits a module's reachable findings into the ones a record excuses and the rest. A finding is excused only when ALL hold:
 * the record names this module directory, the vulnerable module at exactly the version found, and the OSV id; govulncheck
 * names no fixed version (a fix that appears turns the finding red again); and every frame it reports is a package `init`,
 * that is, the package is linked in but no function of it is called. A call into the vulnerable module, another version, another
 * id or another module directory is never excused.
 */
export function excuse(directory, called, accepted, base = root) {
  const name = relative(base, directory).split(/[\\/]/).join('/');
  const excused = [], reachable = [];
  for (const entry of called) {
    const record = accepted.find((r) => r.directory === name && r.ids.includes(entry.id) && entry.modules.length === 1 && entry.modules[0] === `${r.module}@${r.version}`);
    if (record && entry.initOnly && !entry.fixed) excused.push({ ...entry, record });
    else reachable.push(entry);
  }
  return { excused, reachable };
}

/** The scan of one module as { directory, status: 'ok' | 'called' | 'failed', ... }; `accepted` is loadAccepted(). */
export function judge(directory, result, accepted = []) {
  if (result.code !== 0) return { directory, status: 'failed', detail: (result.stderr || result.stdout).trim().split('\n').slice(-12).join('\n') };
  let messages;
  try { messages = parseMessages(result.stdout); } catch (error) { return { directory, status: 'failed', detail: error.message }; }
  const verdict = classify(messages);
  if (!verdict.config) return { directory, status: 'failed', detail: 'no config message: govulncheck did not report a scan' };
  const { excused, reachable } = excuse(directory, verdict.called, accepted);
  return { directory, status: reachable.length ? 'called' : 'ok', ...verdict, called: reachable, excused };
}

/**
 * The records the runs do not bear out, as messages (empty when every record id was excused by some scan). `results` are the
 * judged scans of every target. A scan that failed to run says nothing. An id that was reported but not excused (the version,
 * the module or the frames no longer match the record) is told apart from one that was not reported at all.
 */
export function staleRecords(accepted, results, base = root) {
  const excused = new Set(), reported = new Set();
  for (const result of results) {
    if (result.status === 'failed') continue;
    const name = relative(base, result.directory).split(/[\\/]/).join('/');
    for (const v of result.excused) excused.add(`${name} ${v.id}`);
    for (const v of result.called) reported.add(`${name} ${v.id}`);
  }
  const problems = [];
  for (const record of accepted) {
    for (const id of record.ids) {
      const key = `${record.directory} ${id}`;
      if (excused.has(key)) continue;
      problems.push(reported.has(key)
        ? `${id} in ${record.directory} is reported but not excused by its record (the module, version or frames differ from it); correct or remove the record`
        : `${id} in ${record.directory} was not reported by this run; remove it from the record`);
    }
  }
  return problems;
}

/** The modules go.work must hold: every go.mod, and a mismatch with the listed ones is itself a failure. */
export function moduleProblems(all, listed) {
  const problems = [];
  const scanned = new Set(all);
  for (const dir of listed) if (!scanned.has(dir)) problems.push(`${relative(root, dir)}: a module the repository tooling lists but the gate does not scan`);
  return problems;
}

async function main(targets) {
  const bad = targets.filter((t) => !/^[a-z0-9]+\/[a-z0-9]+$/.test(t));
  if (bad.length) {
    console.error(`govulncheck: not an os/arch target: ${bad.join(', ')}`);
    return 2;
  }
  const modules = allGoModules();
  const accepted = loadAccepted();
  const problems = [...moduleProblems(modules, goModules()), ...acceptedProblems(accepted)];
  if (problems.length) {
    for (const problem of problems) console.error(`govulncheck: ${problem}`);
    return 1;
  }
  const tool = installGovulncheck();
  const base = goWorkEnv(modules);
  const goVersion = execFileSync('go', ['version'], { encoding: 'utf8' }).trim();
  console.log(`${goVersion}; govulncheck ${GOVULNCHECK_VERSION}; ${modules.length} modules`);
  if (!targets.length) targets.push(...Object.keys(runners));
  let failed = false;
  const everything = [];
  for (const target of targets) {
    const [GOOS, GOARCH] = target.split('/');
    const env = { ...base, GOOS, GOARCH, CGO_ENABLED: '0' };
    const queue = [...modules];
    const results = [];
    const worker = async () => {
      for (let dir = queue.shift(); dir; dir = queue.shift()) results.push(judge(dir, await scan(tool, dir, env), accepted));
    };
    await Promise.all(Array.from({ length: Math.max(1, Math.min(8, availableParallelism() >> 1)) }, worker));
    results.sort((a, b) => a.directory.localeCompare(b.directory));
    everything.push(...results);
    for (const result of results) {
      const name = relative(root, result.directory);
      if (result.status === 'ok') {
        console.log(`ok     ${target} ${name}: 0 vulnerabilities affect the code${result.imported.length ? ` (imported, not called: ${result.imported.join(', ')})` : ''}`);
        if (result.excused.length) console.log(`         accepted (scripts/govulncheck-accepted.json): ${result.excused.map((v) => v.id).join(', ')} in ${result.excused[0].modules[0]}, reported only as the init of a package that is linked in; no function of it is called`);
      } else if (result.status === 'called') {
        failed = true;
        console.log(`FAIL   ${target} ${name}: reachable vulnerabilities`);
        for (const v of result.called) console.log(`         ${v.id}${v.fixed ? ` (fixed in ${v.fixed})` : ''}: ${v.summary}\n           ${v.symbols.slice(0, 5).join(', ')}`);
      } else {
        failed = true;
        console.log(`FAIL   ${target} ${name}: not scanned\n${result.detail.replace(/^/gm, '         ')}`);
      }
    }
    if (results.length !== modules.length) {
      failed = true;
      console.log(`FAIL   ${target}: scanned ${results.length} of ${modules.length} modules`);
    }
    console.log(`${results.every((r) => r.status === 'ok') ? 'ok  ' : 'FAIL'} ${target}: ${results.filter((r) => r.status === 'ok').length} of ${modules.length} modules have no reachable vulnerability`);
  }
  for (const message of staleRecords(accepted, everything)) {
    failed = true;
    console.log(`FAIL   govulncheck-accepted.json: ${message}`);
  }
  return failed ? 1 : 0;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) process.exitCode = await main(process.argv.slice(2));
