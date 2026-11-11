// Faithfulness gates for the REA payload (design gates 1, 2 and 4), run against the payload payload.mjs wrote for this machine:
//   1. Catalog equality: REA from the payload and REA as `npx rea-agents@<pin>` installs it (a fresh npm install), both on the
//      payload's Node, list the same tools and prompts, byte for byte after sorting, under both input-schema profiles.
//   2. Registration equality: `rea setup --client pi` writes {type: stdio, command: <rea.mjs>, args: [mcp]} with nothing else,
//      which is what the rea member registers with the embedded Node in front (extensions/rea TestPrepareRegistersTheUnpackedPayload).
//   4. Skill equality (scripts/skill.mjs): payload.mjs enforces it on every build; this reruns it on the extracted payload.
// Needs network (npm) and zstd. Run: npm run test:rea
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { after, before, test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { shippedSkill, skillDrift, upstreamSkill } from '../scripts/skill.mjs';

const component = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repository = resolve(component, '../..');
const lock = JSON.parse(readFileSync(join(component, 'rea.lock.json'), 'utf8'));
const target = `${{ win32: 'windows' }[process.platform] ?? process.platform}/${{ x64: 'amd64' }[process.arch] ?? process.arch}`;
const stem = join(component, 'extensions/rea/payload', target.replace('/', '_'));

let work;
let payloadRoot;
let reference;
let node;

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { encoding: 'utf8', maxBuffer: 64 << 20, ...options });
  assert.equal(result.status, 0, `${command} ${args.join(' ')}: ${result.error?.message ?? ''}${result.stderr}`);
  return result.stdout;
}

/** tools/list (all pages) and prompts/list of `rea.mjs mcp`, sorted by name, as canonical JSON. */
async function catalog(entry, env) {
  const child = spawn(node, [entry, 'mcp'], { env: { ...process.env, ...env }, stdio: ['pipe', 'pipe', 'inherit'] });
  let buffer = '';
  const waiting = new Map();
  child.stdout.setEncoding('utf8');
  child.stdout.on('data', (chunk) => {
    buffer += chunk;
    for (let i; (i = buffer.indexOf('\n')) >= 0;) {
      const line = buffer.slice(0, i);
      buffer = buffer.slice(i + 1);
      if (!line.trim()) continue;
      const message = JSON.parse(line);
      waiting.get(message.id)?.(message);
    }
  });
  let id = 0;
  const rpc = (method, params) => new Promise((resolveCall, reject) => {
    const n = ++id;
    waiting.set(n, (message) => (message.error ? reject(new Error(`${method}: ${JSON.stringify(message.error)}`)) : resolveCall(message.result)));
    child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', id: n, method, params })}\n`);
  });
  try {
    const init = await rpc('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'pigpen-rea-gate', version: '0' } });
    child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' })}\n`);
    const list = async (method, key) => {
      const items = [];
      let cursor;
      do {
        const page = await rpc(method, cursor ? { cursor } : {});
        items.push(...page[key]);
        cursor = page.nextCursor;
      } while (cursor);
      return items.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
    };
    const tools = await list('tools/list', 'tools');
    const prompts = await list('prompts/list', 'prompts');
    const canonical = JSON.stringify({ tools, prompts });
    return { server: init.serverInfo, tools: tools.length, prompts: prompts.length, sha256: createHash('sha256').update(canonical).digest('hex') };
  } finally {
    child.kill();
  }
}

before(() => {
  assert.ok(existsSync(`${stem}.tar.zst`), `no payload for ${target}: run node components/rea/scripts/payload.mjs first`);
  work = mkdtempSync(join(tmpdir(), 'rea-gates-'));
  payloadRoot = join(work, 'payload');
  mkdirSync(payloadRoot);
  run('zstd', ['-d', '--long=27', '-q', '-f', '-o', join(work, 'payload.tar'), `${stem}.tar.zst`]);
  run('tar', ['-xf', join(work, 'payload.tar'), '-C', payloadRoot]);
  const info = JSON.parse(readFileSync(join(payloadRoot, 'payload.json'), 'utf8'));
  node = join(payloadRoot, info.node.executable);
  assert.equal(run(node, ['--version']).trim(), `v${lock.node.version}`);
  // What `npx rea-agents@<pin>` runs: a fresh npm install, with npm's own resolution instead of REA's tagged lock.
  reference = join(work, 'npx');
  mkdirSync(reference);
  run('npm', ['install', `${lock.rea.package}@${lock.rea.version}`, '--no-audit', '--no-fund', '--ignore-scripts', '--loglevel=error', '--prefix', reference], { shell: process.platform === 'win32' });
});

after(() => {
  if (work) rmSync(work, { recursive: true, force: true });
});

for (const profile of ['compact', 'full']) {
  test(`gate 1: the payload's REA lists the same tools and prompts as npx's, ${profile} input schemas`, async () => {
    const env = profile === 'full' ? { REA_MCP_INPUT_SCHEMA_PROFILE: 'full' } : {};
    const payload = await catalog(join(payloadRoot, 'rea/scripts/rea.mjs'), env);
    const npx = await catalog(join(reference, 'node_modules', lock.rea.package, 'scripts/rea.mjs'), env);
    assert.equal(payload.server.version, lock.rea.version);
    assert.ok(payload.tools > 100, `only ${payload.tools} tools`);
    assert.deepEqual(payload, npx);
  });
}

test('gate 2: rea setup --client pi registers exactly the stdio command the rea member mirrors', () => {
  const home = join(work, 'home');
  mkdirSync(home);
  const entry = join(payloadRoot, 'rea/scripts/rea.mjs');
  const env = { ...process.env, HOME: home, USERPROFILE: home };
  delete env.PI_CODING_AGENT_DIR;
  run(node, [entry, 'setup', '--client', 'pi', '--yes'], { env });
  const written = jsonFiles(home).filter((path) => readFileSync(path, 'utf8').includes('"mcpServers"'));
  assert.equal(written.length, 1, `rea setup wrote mcpServers to ${written.length} files: ${written.join(', ')}`);
  const server = JSON.parse(readFileSync(written[0], 'utf8')).mcpServers.rea;
  assert.deepEqual(Object.keys(server).sort(), ['args', 'command', 'type']);
  assert.equal(server.type, 'stdio');
  assert.deepEqual(server.args, ['mcp']);
  assert.equal(server.command, entry);
});

test("gate 4: the Piglet's REA skill is the payload's, byte for byte apart from the prefixed name", () => {
  const drift = skillDrift(join(payloadRoot, 'rea/skills', upstreamSkill), join(component, 'skills', shippedSkill));
  assert.equal(drift, undefined);
});

function jsonFiles(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return jsonFiles(path);
    return entry.isFile() && entry.name.endsWith('.json') ? [path] : [];
  });
}
