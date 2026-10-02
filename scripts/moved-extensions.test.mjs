// Runtime proof for the extensions moved from the owner's personal PiG configuration
// (session-ingest, context-info) and the dev-skills Package: install each Package into a scratch PiG home, then drive a
// real `pig` against a scripted local provider. Needs PIG_BIN (a PiG build with the public Go
// SDK) and a Go toolchain: the Packages build on first use. Never reads real credentials.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { requirePig } from './pig-bin.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const pig = process.env.PIG_BIN && requirePig();

const session = [
  { type: 'session', version: 3, id: 'moved-ext-session-1', timestamp: '2026-01-01T00:00:00.000Z', cwd: '/work' },
  { type: 'message', timestamp: '2026-01-01T00:00:01.000Z', message: { role: 'user', content: [{ type: 'text', text: 'Add a parser for the flurble format' }] } },
  { type: 'message', timestamp: '2026-01-01T00:00:02.000Z', message: { role: 'assistant', provider: 'acme', model: 'm-1', stopReason: 'toolUse',
    content: [{ type: 'text', text: 'Reading the spec.' }, { type: 'toolCall', id: 'c1', name: 'read', arguments: { path: 'SPEC.md' } }],
    usage: { input: 100, output: 20, totalTokens: 120, cost: { total: 0.01 } } } },
  { type: 'message', timestamp: '2026-01-01T00:00:03.000Z', message: { role: 'toolResult', toolCallId: 'c1', toolName: 'read', isError: false, content: [{ type: 'text', text: 'flurble = 1*DIGIT' }] } },
].map((entry) => JSON.stringify(entry)).join('\n') + '\n';

test('session-ingest, context-info and dev-skills run in a real pig', { skip: !pig && 'set PIG_BIN', timeout: 240000 }, async (t) => {
  const scratch = realpathSync(mkdtempSync(join(tmpdir(), 'pigpen-moved-')));
  t.after(() => rmSync(scratch, { recursive: true, force: true }));
  const home = join(scratch, 'home');
  const cwd = join(scratch, 'work');
  mkdirSync(join(home, 'agent'), { recursive: true });
  mkdirSync(cwd);
  const transcript = join(scratch, 'session.jsonl');
  writeFileSync(transcript, session);
  const env = { PATH: process.env.PATH, HOME: home, USERPROFILE: home, PIG_HOME: home, PIG_CODING_AGENT_DIR: join(home, 'agent'),
    PI_TELEMETRY: '0', PI_OFFLINE: '1', GIT_TERMINAL_PROMPT: '0', GOFLAGS: process.env.GOFLAGS ?? '', GOCACHE: process.env.GOCACHE ?? join(scratch, 'gocache'),
    GOPATH: process.env.GOPATH ?? join(scratch, 'gopath'), GOTOOLCHAIN: 'local' };

  // Scripted provider: the first request calls read_session, the second answers with text.
  const requests = [];
  const server = createServer(async (request, response) => {
    let raw = '';
    for await (const chunk of request) raw += chunk;
    requests.push(JSON.parse(raw));
    const first = requests.length === 1;
    const delta = first
      ? { role: 'assistant', tool_calls: [{ index: 0, id: 'call_1', type: 'function', function: { name: 'read_session', arguments: JSON.stringify({ path: transcript, mode: 'stats' }) } }] }
      : { role: 'assistant', content: 'session read' };
    response.writeHead(200, { 'Content-Type': 'text/event-stream' });
    for (const choice of [{ delta, finish_reason: null }, { delta: {}, finish_reason: first ? 'tool_calls' : 'stop' }]) {
      response.write(`data: ${JSON.stringify({ id: 'x', object: 'chat.completion.chunk', created: 1, model: 'fake', choices: [{ index: 0, ...choice }] })}\n\n`);
    }
    response.end('data: [DONE]\n\n');
  });
  await new Promise((accept) => server.listen(0, '127.0.0.1', accept));
  t.after(() => new Promise((accept) => { server.close(accept); server.closeAllConnections(); }));
  writeFileSync(join(home, 'agent/models.json'), JSON.stringify({ providers: { fake: {
    baseUrl: `http://127.0.0.1:${server.address().port}/v1`, api: 'openai-completions', apiKey: 'synthetic-test-key',
    models: [{ id: 'fake', name: 'Fake', reasoning: false, input: ['text'], contextWindow: 128000, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }],
  } } }));

  for (const name of ['session-ingest', 'context-info', 'dev-skills']) {
    const component = join(root, 'components', name);
    const validated = spawnSync(pig, ['package', 'validate', component], { cwd, env, encoding: 'utf8' });
    assert.equal(validated.status, 0, validated.stdout + validated.stderr);
    const installed = spawnSync(pig, ['install', component], { cwd, env, encoding: 'utf8' });
    assert.equal(installed.status, 0, installed.stdout + installed.stderr);
  }

  const flags = ['--no-session', '--offline', '--no-context-files', '--approve', '--model', 'fake/fake'];
  // Async: the scripted provider lives in this process, so it must not be blocked by spawnSync.
  const printed = await new Promise((accept, reject) => {
    const run = spawn(pig, ['--print', ...flags, 'read the session'], { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '', stderr = '';
    const timer = setTimeout(() => run.kill('SIGKILL'), 120000);
    run.stdout.on('data', (chunk) => { stdout += chunk; });
    run.stderr.on('data', (chunk) => { stderr += chunk; });
    run.on('error', reject);
    run.on('close', (status) => { clearTimeout(timer); accept({ status, stdout, stderr }); });
  });
  assert.equal(printed.status, 0, printed.stdout + printed.stderr);
  assert.match(printed.stdout, /session read/);
  assert.ok(requests[0].tools.some((tool) => tool.function.name === 'read_session'), 'read_session is offered to the model');
  const result = requests[1].messages.find((message) => message.role === 'tool');
  const text = typeof result.content === 'string' ? result.content : result.content.map((part) => part.text).join('');
  for (const expected of ['# Session stats', 'turns: 1', 'tokens: in 100, out 20', '$0.0100', 'acme/m-1']) assert.ok(text.includes(expected), `${expected} in:\n${text}`);

  // Slash commands answer through UI notifications, which print mode does not show: use RPC.
  const child = spawn(pig, ['--mode', 'rpc', ...flags], { cwd, env, stdio: ['pipe', 'pipe', 'ignore'] });
  t.after(() => child.kill('SIGKILL'));
  const events = [];
  let buffer = '';
  child.stdout.on('data', (chunk) => {
    buffer += chunk;
    for (let i = buffer.indexOf('\n'); i >= 0; i = buffer.indexOf('\n')) {
      try { events.push(JSON.parse(buffer.slice(0, i))); } catch { /* not a JSON line */ }
      buffer = buffer.slice(i + 1);
    }
  });
  const notifications = () => events.filter((event) => event.type === 'extension_ui_request' && event.method === 'notify').map((event) => event.message);
  const command = async (message, wanted) => {
    const before = notifications().length;
    child.stdin.write(`${JSON.stringify({ type: 'prompt', message })}\n`);
    for (let i = 0; i < 100 && notifications().length === before; i++) await new Promise((accept) => setTimeout(accept, 100));
    const got = notifications().slice(before).join('\n');
    for (const expected of wanted) assert.ok(got.includes(expected), `${message}: ${expected} in:\n${got}`);
    return got;
  };
  await new Promise((accept) => setTimeout(accept, 3000)); // session_start
  await command('/context', ['Context Window', 'System prompt', 'read_session']);
  await command('/tools', ['read_session']);
  await command('/cost', ['Session Cost']);
  await command('/prompts', ['Main Agent System Prompt']);
  await command('/context-footer on', ['Context footer on']);
  await command('/context-footer off', ['Context footer off']);
  assert.ok(!events.some((event) => event.type === 'extension_ui_request' && event.method === 'notify' && /prompt-edit/.test(event.message)), 'no /prompt-edit');
  child.kill('SIGKILL');

  // The Skills Package: /skill:<name> expands the complete Skill body into the prompt PiG sends.
  const before = requests.length;
  const skillRun = await new Promise((accept, reject) => {
    const run = spawn(pig, ['--print', ...flags, '/skill:pigpen-tdd add a parser'], { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '', stderr = '';
    const timer = setTimeout(() => run.kill('SIGKILL'), 60000);
    run.stdout.on('data', (chunk) => { stdout += chunk; });
    run.stderr.on('data', (chunk) => { stderr += chunk; });
    run.on('error', reject);
    run.on('close', (status) => { clearTimeout(timer); accept({ status, stdout, stderr }); });
  });
  assert.equal(skillRun.status, 0, skillRun.stdout + skillRun.stderr);
  const sent = requests.slice(before).flatMap((request) => request.messages).flatMap((message) => typeof message.content === 'string' ? [message.content] : (message.content ?? []).map((part) => part.text ?? '')).join('\n');
  assert.ok(sent.includes('Mutation gate.') && sent.includes('Adapted from Matt Pocock'), 'the pigpen-tdd Skill body reaches the model');
});
