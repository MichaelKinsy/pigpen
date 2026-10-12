// First-run cost of an aot bench Piglet Binary (bench-ext10-aot, bench-ext10-split): what the aot runtime spends before the TypeScript set runs
// compiled, and what the compiled host weighs. The compiled host is a slot image built on the user's machine; it is not part of the Binary.
//   start 1  the aot-runtime extension installs its manifest and the pinned Pi-Bolt compiler (/aot-runtime install); /bun-runtime install gives the
//            first builds a Bun to run on. Reported: bunInstallSeconds, compilerInstallSeconds.
//   start 2  the TypeScript set runs on Bun, PiG records the build request and the Binary's own worker compiles the set. Reported: compileSeconds
//            (start to compiled host), hostBytes and imageDirBytes.
//   start 3  the set runs on the compiled host (checked from the process list). Reported: runsCompiled.
// Needs network for the two downloads (pinned and signature-checked) and no PIG_OFFLINE.
// usage: node scripts/bench-aot-cost.mjs --binary <path> [--timeout <seconds>] [--json <file>]
import { spawn } from 'node:child_process';
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';

const sleep = (ms) => new Promise((ok) => setTimeout(ok, ms));

function findFiles(dir, name, out = []) {
  let entries = [];
  try { entries = readdirSync(dir, { withFileTypes: true }); } catch { return out; }
  for (const entry of entries) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) findFiles(path, name, out);
    else if (entry.name === name) out.push(path);
  }
  return out;
}

function treeBytes(dir) {
  let total = 0;
  let entries = [];
  try { entries = readdirSync(dir, { withFileTypes: true }); } catch { return 0; }
  for (const entry of entries) {
    const path = join(dir, entry.name);
    total += entry.isDirectory() ? treeBytes(path) : statSync(path).size;
  }
  return total;
}

function processesRunning(name, home) {
  let count = 0;
  for (const pid of readdirSync('/proc').filter((p) => /^\d+$/.test(p))) {
    try {
      const cmdline = readFileSync(`/proc/${pid}/cmdline`, 'utf8');
      if (cmdline.includes(name) && cmdline.includes(home)) count += 1;
    } catch { /* the process ended */ }
  }
  return count;
}

export async function measure(binary, timeoutSeconds = 900) {
  const home = mkdtempSync(join(tmpdir(), 'bench-aot-cost-'));
  const agent = join(home, 'agent');
  const slot = join(agent, 'runtimes', 'typescript');
  mkdirSync(agent, { recursive: true });
  mkdirSync(join(home, 'tmp'), { recursive: true });
  writeFileSync(join(agent, 'settings.json'), JSON.stringify({ lastChangelogVersion: '9999.0.0' }));
  writeFileSync(join(agent, 'auth.json'), '{}');
  const env = { PATH: process.env.PATH, HOME: home, PIG_HOME: join(home, '.pig'), PIG_CODING_AGENT_DIR: agent, PI_CODING_AGENT_DIR: agent, PIG_TELEMETRY: '0', PIG_SKIP_VERSION_CHECK: '1', TERM: 'dumb', TMPDIR: join(home, 'tmp'), PIG_TS_RUNTIME: 'aot', PIG_TS_RUNTIMES_DIR: slot };
  const result = { binary };
  const start = () => {
    const child = spawn(binary, ['--mode', 'rpc', '--no-session'], { env, cwd: home, stdio: ['pipe', 'pipe', 'pipe'] });
    const run = { child, lines: [], stderr: '', at: performance.now(), next: 1 };
    child.stderr.on('data', (d) => { run.stderr += d; });
    createInterface({ input: child.stdout }).on('line', (line) => { try { run.lines.push(JSON.parse(line)); } catch { /* not an event */ } });
    run.seconds = () => (performance.now() - run.at) / 1000;
    run.fail = (what) => new Error(`${what} after ${timeoutSeconds} s; stderr tail: ${run.stderr.slice(-600)}`);
    run.until = async (done, what) => {
      while (!done()) {
        if (run.seconds() > timeoutSeconds) throw run.fail(what);
        if (child.exitCode !== null) throw new Error(`the Binary exited ${child.exitCode} while waiting for ${what}; stderr tail: ${run.stderr.slice(-600)}`);
        await sleep(250);
      }
    };
    run.ask = async (message) => {
      const id = `r${run.next++}`;
      child.stdin.write(`${JSON.stringify(message.startsWith('{') ? { ...JSON.parse(message), id } : { type: 'prompt', message, id })}\n`);
      await run.until(() => run.lines.some((e) => e.type === 'response' && e.id === id), `the response to ${message}`);
    };
    return run;
  };
  let run;
  try {
    run = start();
    await run.ask('{"type":"get_commands"}');
    await run.ask('/bun-runtime install');
    await run.until(() => findFiles(slot, 'bun').length > 0, 'a Bun in the slot');
    result.bunInstallSeconds = Number(run.seconds().toFixed(1));
    const compilerFrom = run.seconds();
    await run.ask('/aot-runtime install');
    let last = -1;
    let stableFor = 0;
    await run.until(() => { // the command answers at once; the download is done when the slot's aot directory stops growing
      const size = treeBytes(join(slot, 'aot'));
      stableFor = size === last && size > 20e6 ? stableFor + 1 : 0;
      last = size;
      return stableFor >= 12;
    }, 'the compiler install');
    result.compilerInstallSeconds = Number((run.seconds() - compilerFrom).toFixed(1));
    run.child.kill('SIGKILL');

    run = start();
    await run.ask('{"type":"get_commands"}');
    await run.until(() => findFiles(join(slot, 'aot'), 'pig-ts-host').length > 0, 'a compiled host');
    const host = findFiles(join(slot, 'aot'), 'pig-ts-host')[0];
    await sleep(500);
    result.compileSeconds = Number(run.seconds().toFixed(1));
    result.hostBytes = statSync(host).size;
    result.imageDirBytes = treeBytes(join(host, '..'));
    run.child.kill('SIGKILL');

    run = start();
    await run.ask('{"type":"get_commands"}');
    await sleep(1500);
    result.runsCompiled = processesRunning('pig-ts-host', home) > 0;
  } finally {
    run?.child.kill('SIGKILL');
    rmSync(home, { recursive: true, force: true });
  }
  return result;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const at = (flag) => { const i = process.argv.indexOf(flag); return i < 0 ? undefined : process.argv[i + 1]; };
  if (!at('--binary')) { console.error('usage: node scripts/bench-aot-cost.mjs --binary <path> [--timeout <seconds>] [--json <file>]'); process.exit(2); }
  const result = await measure(resolve(at('--binary')), Number(at('--timeout') ?? 900));
  if (at('--json')) writeFileSync(at('--json'), `${JSON.stringify(result, null, 1)}\n`);
  console.log(JSON.stringify(result, null, 1));
}
