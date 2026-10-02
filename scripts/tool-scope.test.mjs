// A Piglet entry's `tools: []` must keep that extension's tools away from the model in every mode.
// pig-with-batteries relies on it to keep pig-doctor's read-only `pig_doctor` tool off (command only).
//   PIG_BIN=/path/to/pig npm run test:tool-scope
// Needs a Go toolchain (pig-doctor builds from source on first use). Temporary HOME, PIG_HOME and agent
// directory; a local scripted provider records the tools each request offers.
//
// PiG 0.3.0 (63c6ba456) honours the scope in interactive and RPC mode but not in print and JSON mode:
// those cases are marked todo and named in RELEASE-BLOCKERS.md (PiG core item 9). Drop the todo when a
// PiG build passes them.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { after, before, test } from 'node:test';
import { requirePig } from './pig-bin.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const pig = requirePig();

const scratch = realpathSync(mkdtempSync(join(tmpdir(), 'pigpen-tool-scope-')));
const home = join(scratch, 'home');
const piglet = join(scratch, 'piglet');
let server;
let offered = [];

before(async () => {
  mkdirSync(join(home, 'agent'), { recursive: true });
  mkdirSync(join(piglet, 'packages'), { recursive: true });
  cpSync(join(root, 'components/pig-doctor'), join(piglet, 'packages/pig-doctor'), { recursive: true });
  // The same selection as pig-with-batteries' pig-doctor entry, alone.
  writeFileSync(join(piglet, 'piglet.yaml'), [
    'name: tool-scope', 'description: "pig-doctor as a command only"', 'release:', '  version: 0.1.0',
    'packages:', '  pig-doctor: local:./packages/pig-doctor',
    'extensions:', '  - name: pig-doctor', '    origins: [package:pig-doctor]', '    tools: []',
    'skills: []', 'discovery:', '  extensions: []', '  skills: []', '',
  ].join('\n'));
  server = createServer(async (request, response) => {
    let raw = '';
    for await (const chunk of request) raw += chunk;
    offered.push((JSON.parse(raw).tools ?? []).map((tool) => tool.function.name));
    response.writeHead(200, { 'Content-Type': 'text/event-stream' });
    for (const choice of [{ delta: { role: 'assistant', content: 'ok' }, finish_reason: null }, { delta: {}, finish_reason: 'stop' }]) {
      response.write(`data: ${JSON.stringify({ id: 'x', object: 'chat.completion.chunk', created: 1, model: 'fake', choices: [{ index: 0, ...choice }] })}\n\n`);
    }
    response.end('data: [DONE]\n\n');
  });
  await new Promise((accept) => server.listen(0, '127.0.0.1', accept));
  writeFileSync(join(home, 'agent/models.json'), JSON.stringify({ providers: { fake: {
    baseUrl: `http://127.0.0.1:${server.address().port}/v1`, api: 'openai-completions', apiKey: 'synthetic-test-key',
    models: [{ id: 'fake', name: 'Fake', reasoning: false, input: ['text'], contextWindow: 128000, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }],
  } } }));
});

after(() => {
  server?.close();
  server?.closeAllConnections();
  rmSync(scratch, { recursive: true, force: true });
});

const env = () => ({
  PATH: process.env.PATH, HOME: home, USERPROFILE: home, PIG_HOME: join(home, '.pig'), PIG_CODING_AGENT_DIR: join(home, 'agent'),
  PI_TELEMETRY: '0', PI_OFFLINE: '1', GIT_TERMINAL_PROMPT: '0', GOTOOLCHAIN: 'local', GOFLAGS: process.env.GOFLAGS ?? '',
  GOCACHE: process.env.GOCACHE ?? join(scratch, 'gocache'), GOPATH: process.env.GOPATH ?? join(scratch, 'gopath'),
  ...(process.env.GOMODCACHE ? { GOMODCACHE: process.env.GOMODCACHE } : {}),
});
const flags = ['--piglet', join(piglet, 'piglet.yaml'), '--no-session', '--offline', '--no-context-files', '--model', 'fake/fake'];

// Runs one prompt and returns the tool names of the first model request.
function firstRequestTools(args, { rpc = false } = {}) {
  offered = [];
  return new Promise((accept, reject) => {
    const child = spawn(pig, [...flags, ...args], { cwd: scratch, env: env(), stdio: [rpc ? 'pipe' : 'ignore', 'pipe', 'pipe'] });
    let stderr = '';
    child.stderr.on('data', (chunk) => { stderr += chunk; });
    child.stdout.resume();
    const timer = setTimeout(() => child.kill('SIGKILL'), 180000);
    const poll = setInterval(() => { if (offered.length) child.kill('SIGTERM'); }, 200);
    if (rpc) setTimeout(() => child.stdin.write(`${JSON.stringify({ type: 'prompt', message: 'hi' })}\n`), 3000);
    child.on('error', reject);
    child.on('close', () => {
      clearTimeout(timer);
      clearInterval(poll);
      if (!offered.length) reject(new Error(`no model request\n${stderr}`));
      else accept(offered[0]);
    });
  });
}

const known = { todo: 'PiG 0.3.0 (63c6ba456) ignores a per-extension tools: [] in print and JSON mode; RELEASE-BLOCKERS.md PiG item 9' };

test('RPC mode: tools: [] keeps pig_doctor away from the model', { timeout: 200000 }, async () => {
  const tools = await firstRequestTools(['--mode', 'rpc'], { rpc: true });
  assert.ok(tools.includes('read'), `built-in tools stay: ${tools}`);
  assert.ok(!tools.includes('pig_doctor'), `offered: ${tools}`);
});

test('print mode: tools: [] keeps pig_doctor away from the model', { timeout: 200000, ...known }, async () => {
  const tools = await firstRequestTools(['--print', 'hi']);
  assert.ok(!tools.includes('pig_doctor'), `offered: ${tools}`);
});

test('JSON mode: tools: [] keeps pig_doctor away from the model', { timeout: 200000, ...known }, async () => {
  const tools = await firstRequestTools(['--mode', 'json', 'hi']);
  assert.ok(!tools.includes('pig_doctor'), `offered: ${tools}`);
});
