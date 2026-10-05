#!/usr/bin/env node
// Interop proof for pig-acp: the ACP reference client (@agentclientprotocol/sdk 0.26.0, ClientSideConnection)
// starts the real pig-acp executable, which starts a real pig (or a built Piglet Binary) against a scripted
// model. Every JSON-RPC message in both directions is then validated against the pinned schema
// (../schema/schema.json, schema 0.26.0, protocol version 1).
//
//   PIG_ACP=<pig-acp binary> PIG=<pig or Piglet Binary> PIGEQ=<pigeq> node run.mjs
//
// Rule 17: pig runs with a temporary HOME, PIG_HOME and agent directories; nothing outside the temp directory is read or written.
import { spawn } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { Readable, Writable } from 'node:stream';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import * as acp from '@agentclientprotocol/sdk';
import Ajv2020 from 'ajv/dist/2020.js';

const here = dirname(fileURLToPath(import.meta.url));
const need = (n) => {
  if (!process.env[n]) throw new Error(`set ${n}`);
  return process.env[n];
};
const ADAPTER = resolve(need('PIG_ACP'));
const PIG = resolve(need('PIG'));
const PIGEQ = need('PIGEQ');
const timeout = (ms, what) => new Promise((_, rej) => setTimeout(() => rej(new Error(`timed out: ${what}`)), ms).unref());
const within = (p, ms, what) => Promise.race([p, timeout(ms, what)]);

const root = mkdtempSync(join(tmpdir(), 'acp-interop-'));
const env = {
  PATH: process.env.PATH,
  HOME: join(root, 'home'),
  PIG_HOME: join(root, 'pighome'),
  PIG_CODING_AGENT_DIR: join(root, 'agent'),
  PI_CODING_AGENT_DIR: join(root, 'agent'),
  PIG_OFFLINE: '1',
  PI_SKIP_VERSION_CHECK: '1',
  PI_TELEMETRY: '0',
  NO_COLOR: '1',
};
const work = join(root, 'work');
for (const d of [env.HOME, env.PIG_HOME, env.PIG_CODING_AGENT_DIR, work]) mkdirSync(d, { recursive: true });

// 1. The scripted model (pigeq llm serves the harness's OpenAI-compatible model).
const turns = [{ text: 'Hello from the scripted model' }, { toolCalls: [{ name: 'bash', arguments: { command: 'echo interop-ok' } }] }, { text: 'all done' }];
writeFileSync(join(root, 'turns.json'), JSON.stringify(turns));
const llm = spawn(PIGEQ, ['llm', '--script', join(root, 'turns.json'), '--log', join(root, 'llm.log')], { stdio: ['pipe', 'pipe', 'inherit'] });
const baseURL = await within(new Promise((res) => llm.stdout.once('data', (d) => res(String(d).trim()))), 15000, 'pigeq llm address');
writeFileSync(
  join(env.PIG_CODING_AGENT_DIR, 'models.json'),
  JSON.stringify({ providers: { 'acp-llm': { baseUrl: baseURL, api: 'openai-completions', apiKey: 'acp-key', models: [{ id: 'acp-1', name: 'ACP One', reasoning: false, input: ['text'], contextWindow: 100000, maxTokens: 4096, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }] } } }),
);
writeFileSync(join(env.PIG_CODING_AGENT_DIR, 'settings.json'), JSON.stringify({ defaultProvider: 'acp-llm', defaultModel: 'acp-1', quietStartup: true }));

// 2. pig-acp, with a tap on both directions of the wire.
const wire = [];
const adapter = spawn(ADAPTER, ['--pig', PIG], { env, cwd: work, stdio: ['pipe', 'pipe', 'inherit'] });
const exited = new Promise((res) => adapter.on('exit', (code, signal) => res({ code, signal })));
function tap(direction) {
  let buf = '';
  return new TransformStream({
    transform(chunk, controller) {
      buf += new TextDecoder().decode(chunk, { stream: true });
      let i;
      while ((i = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, i);
        buf = buf.slice(i + 1);
        if (line.trim()) wire.push({ direction, message: JSON.parse(line) });
      }
      controller.enqueue(chunk);
    },
  });
}
const toAgent = Writable.toWeb(adapter.stdin);
const tappedOut = new TransformStream();
tappedOut.readable.pipeThrough(tap('client->agent')).pipeTo(toAgent);
const fromAgent = Readable.toWeb(adapter.stdout).pipeThrough(tap('agent->client'));

// 3. The reference client.
const updates = [];
const calls = [];
const client = {
  async requestPermission(params) {
    calls.push('session/request_permission');
    return { outcome: { outcome: 'cancelled' } };
  },
  async sessionUpdate(params) {
    updates.push(params.update);
  },
  async readTextFile() {
    calls.push('fs/read_text_file');
    throw new Error('the adapter must not delegate file reads');
  },
  async writeTextFile() {
    calls.push('fs/write_text_file');
    throw new Error('the adapter must not delegate file writes');
  },
};
const conn = new acp.ClientSideConnection(() => client, acp.ndJsonStream(tappedOut.writable, fromAgent));

const failures = [];
const check = (what, fn) => {
  try {
    fn();
    console.log(`ok   ${what}`);
  } catch (e) {
    failures.push(what);
    console.log(`FAIL ${what}: ${e.message}`);
  }
};

try {
  const init = await within(
    conn.initialize({ protocolVersion: acp.PROTOCOL_VERSION, clientCapabilities: { fs: { readTextFile: true, writeTextFile: true }, terminal: true } }),
    30000,
    'initialize',
  );
  check('initialize answers the pinned protocol version and only implemented capabilities', () => {
    assert.equal(acp.PROTOCOL_VERSION, 1);
    assert.equal(init.protocolVersion, 1);
    assert.equal(init.agentCapabilities.loadSession, true);
    assert.deepEqual(init.agentCapabilities.mcpCapabilities, { http: false, sse: false });
    assert.equal(init.agentCapabilities.promptCapabilities.audio, false);
    assert.equal(init.agentInfo.name, 'pig-acp');
  });

  const session = await within(conn.newSession({ cwd: work, mcpServers: [] }), 60000, 'session/new');
  check('session/new returns models, modes and config options from pig', () => {
    assert.ok(session.sessionId);
    assert.equal(session.models.currentModelId, 'acp-llm/acp-1');
    assert.ok(session.modes.availableModes.length > 0);
    assert.ok(session.configOptions.some((o) => o.id === 'model'));
  });

  const p1 = await within(conn.prompt({ sessionId: session.sessionId, prompt: [{ type: 'text', text: 'say hello' }] }), 60000, 'prompt 1');
  check('a prompt streams text and ends with end_turn', () => {
    assert.equal(p1.stopReason, 'end_turn');
    const text = updates.filter((u) => u.sessionUpdate === 'agent_message_chunk').map((u) => u.content.text).join('');
    assert.match(text, /Hello from the scripted model/);
  });

  const p2 = await within(conn.prompt({ sessionId: session.sessionId, prompt: [{ type: 'text', text: 'run a command' }] }), 60000, 'prompt 2');
  check('a bash tool call is reported as an execute call with terminal output', () => {
    assert.equal(p2.stopReason, 'end_turn');
    const call = updates.find((u) => u.sessionUpdate === 'tool_call');
    assert.equal(call.kind, 'execute');
    assert.equal(call.title, 'echo interop-ok');
    const output = updates.filter((u) => u.sessionUpdate === 'tool_call_update').map((u) => u._meta?.terminal_output?.data ?? '').join('');
    assert.match(output, /interop-ok/);
    const last = updates.filter((u) => u.sessionUpdate === 'tool_call_update').at(-1);
    assert.equal(last.status, 'completed');
  });

  const listed = await within(conn.listSessions({ cwd: work }), 30000, 'session/list');
  check('session/list finds the session', () => assert.ok(listed.sessions.some((s) => s.sessionId === session.sessionId)));

  const cfg = await within(conn.setSessionConfigOption({ sessionId: session.sessionId, configId: 'model', value: 'acp-llm/acp-1' }), 30000, 'set_config_option');
  check('session/set_config_option returns the config options', () => assert.ok(cfg.configOptions.length >= 2));

  check('nothing was delegated to the client (no fs/* or terminal/* calls)', () => assert.deepEqual(calls, []));
  await within(
    conn.extMethod('session/fork', { sessionId: session.sessionId }).then(
      () => failures.push('session/fork succeeded'),
      (e) => check('session/fork is rejected with -32601', () => assert.equal(e.code, -32601)),
    ),
    15000,
    'session/fork',
  );

  // Schema conformance of everything on the wire.
  const schema = JSON.parse(readFileSync(join(here, '..', 'schema', 'schema.json'), 'utf8'));
  const ajv = new Ajv2020({ strict: false, allErrors: true, validateFormats: false });
  ajv.addSchema(schema, 'acp');
  const def = (name) => {
    const v = ajv.getSchema(`acp#/$defs/${name}`);
    if (!v) throw new Error(`the schema has no ${name}`);
    return v;
  };
  const byMethod = {
    requests: { initialize: 'InitializeRequest', 'session/new': 'NewSessionRequest', 'session/prompt': 'PromptRequest', 'session/list': 'ListSessionsRequest', 'session/set_config_option': 'SetSessionConfigOptionRequest' },
    responses: { initialize: 'InitializeResponse', 'session/new': 'NewSessionResponse', 'session/prompt': 'PromptResponse', 'session/list': 'ListSessionsResponse', 'session/set_config_option': 'SetSessionConfigOptionResponse' },
  };
  const methodOfId = new Map();
  let validated = 0;
  const problems = [];
  const validate = (name, value, where) => {
    const v = def(name);
    validated++;
    if (!v(value)) problems.push(`${where}: ${name}: ${ajv.errorsText(v.errors, { dataVar: 'msg' }).slice(0, 300)}`);
  };
  for (const { direction, message: m } of wire) {
    if (m.jsonrpc !== '2.0') problems.push(`${direction}: not JSON-RPC 2.0: ${JSON.stringify(m).slice(0, 80)}`);
    if (direction === 'client->agent' && m.method && m.id !== undefined) {
      methodOfId.set(m.id, m.method);
      if (byMethod.requests[m.method]) validate(byMethod.requests[m.method], m.params, `client->agent ${m.method}`);
    } else if (direction === 'agent->client' && m.method === 'session/update') {
      validate('SessionNotification', m.params, 'agent->client session/update');
    } else if (direction === 'agent->client' && m.method === 'session/request_permission') {
      validate('RequestPermissionRequest', m.params, 'agent->client session/request_permission');
    } else if (direction === 'agent->client' && m.id !== undefined && 'result' in m) {
      const method = methodOfId.get(m.id);
      if (byMethod.responses[method]) validate(byMethod.responses[method], m.result, `agent->client result of ${method}`);
    }
  }
  check(`all ${validated} messages that have a schema definition validate against schema 0.26.0`, () => {
    assert.ok(validated >= 12, `only ${validated} messages were validated`);
    assert.deepEqual(problems, []);
  });
  check('nothing but JSON-RPC 2.0 messages came from the adapter', () => assert.ok(wire.filter((w) => w.direction === 'agent->client').every((w) => w.message.jsonrpc === '2.0')));

  // Process exit: closing the client's output ends the adapter with status 0.
  await tappedOut.writable.close();
  const exit = await within(exited, 20000, 'the adapter to exit');
  check('closing stdin ends pig-acp with exit status 0', () => assert.deepEqual(exit, { code: 0, signal: null }));
} catch (e) {
  failures.push(String(e));
  console.log(`FAIL ${e.stack || e}`);
} finally {
  adapter.kill();
  llm.kill();
  rmSync(root, { recursive: true, force: true });
}
if (failures.length) {
  console.log(`\n${failures.length} check(s) failed`);
  process.exit(1);
}
console.log('\nall interop checks passed');
