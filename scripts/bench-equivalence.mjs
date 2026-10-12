// Equivalence of the bench Piglet Binaries with the ten TypeScript originals (PiG's bench/evals/fixtures/extensions/bench-ext-NN.ts, which load
// unchanged in Pi). Each arm runs the same scripted session through `--mode rpc`: get_commands, the tools the first model request declares, one
// prompt per extension whose scripted model calls bench_tool_NN (tool result and details), every /bench-NN command, and a last round of calls that
// reads each handler's cumulative event count back. Every section is canonicalised to JSON and hashed; an arm is equivalent when each section's hash
// equals the reference arm's. The reference arm runs the originals with `-e` under Stock PiG's Node cell.
// usage: node scripts/bench-equivalence.mjs --pig <pig> --exts <dir of bench-ext-NN.ts> --binary <name>=<path> [--binary ...] [--json <file>]
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';

const N = 10;
const nn = (i) => String(i).padStart(2, '0');

export function canonical(value) {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value && typeof value === 'object') return `{${Object.keys(value).sort().map((k) => `${JSON.stringify(k)}:${canonical(value[k])}`).join(',')}}`;
  return JSON.stringify(value);
}
export const digest = (value) => createHash('sha256').update(canonical(value)).digest('hex').slice(0, 16);

/** An OpenAI-compatible scripted model: a prompt "call NN" answers with a bench_tool_NN call, anything else with text. */
function fakeModel() {
  const requests = [];
  const server = createServer((req, res) => {
    if (req.method === 'GET') {
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ object: 'list', data: [{ id: 'fake-model', object: 'model' }] }));
      return;
    }
    let body = '';
    req.on('data', (chunk) => { body += chunk; });
    req.on('end', () => {
      const request = JSON.parse(body);
      requests.push(request);
      const last = request.messages.at(-1);
      const text = typeof last.content === 'string' ? last.content : (last.content ?? []).map((p) => p.text ?? '').join('');
      res.writeHead(200, { 'content-type': 'text/event-stream' });
      const chunk = (delta, finish) => res.write(`data: ${JSON.stringify({ id: 'c', object: 'chat.completion.chunk', created: 0, model: 'fake-model', choices: [{ index: 0, delta, finish_reason: finish ?? null }] })}\n\n`);
      if (last.role === 'user' && text.startsWith('call ')) {
        const id = text.split(' ')[1];
        chunk({ role: 'assistant', tool_calls: [{ index: 0, id: `call_${id}`, type: 'function', function: { name: `bench_tool_${id}`, arguments: '{"value":"x"}' } }] });
        chunk({}, 'tool_calls');
      } else {
        chunk({ role: 'assistant', content: 'Done.' });
        chunk({}, 'stop');
      }
      res.end('data: [DONE]\n\n');
    });
  });
  return new Promise((ok) => server.listen(0, '127.0.0.1', () => ok({ server, requests, port: server.address().port })));
}

class Rpc {
  constructor(argv, env, cwd) {
    this.child = spawn(argv[0], argv.slice(1), { env, cwd, stdio: ['pipe', 'pipe', 'pipe'] });
    this.events = [];
    this.waiters = [];
    this.next = 1;
    this.stderr = '';
    this.child.stderr.on('data', (d) => { this.stderr += d; });
    createInterface({ input: this.child.stdout }).on('line', (line) => {
      let event;
      try { event = JSON.parse(line); } catch { return; }
      this.events.push(event);
      for (const waiter of [...this.waiters]) waiter();
    });
  }
  send(message) {
    const id = `r${this.next++}`;
    this.child.stdin.write(`${JSON.stringify({ ...message, id })}\n`);
    return id;
  }
  /** Resolves with the events up to and including the first that satisfies predicate, consuming them. */
  until(predicate, seconds = 120) {
    return new Promise((ok, fail) => {
      const timer = setTimeout(() => fail(new Error(`timed out waiting for an event; stderr: ${this.stderr.slice(-400)}`)), seconds * 1000);
      const check = () => {
        const at = this.events.findIndex(predicate);
        if (at < 0) return;
        clearTimeout(timer);
        this.waiters.splice(this.waiters.indexOf(check), 1);
        ok(this.events.splice(0, at + 1));
      };
      this.waiters.push(check);
      check();
    });
  }
  close() { this.child.kill('SIGKILL'); }
}

async function session(arm, model, base) {
  const home = mkdtempSync(join(base, `${arm.name}-`));
  const agent = join(home, 'agent');
  mkdirSync(join(home, 'tmp'), { recursive: true });
  mkdirSync(agent, { recursive: true });
  writeFileSync(join(agent, 'models.json'), JSON.stringify({ providers: { fake: { baseUrl: `http://127.0.0.1:${model.port}/v1`, api: 'openai-completions', apiKey: 'fake', models: [{ id: 'fake-model', name: 'fake', contextWindow: 200000, maxTokens: 8192, input: ['text'], reasoning: false, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }] } } }));
  writeFileSync(join(agent, 'settings.json'), JSON.stringify({ lastChangelogVersion: '9999.0.0' }));
  writeFileSync(join(agent, 'auth.json'), '{}');
  const env = { PATH: process.env.PATH, HOME: home, PIG_HOME: join(home, '.pig'), PIG_CODING_AGENT_DIR: agent, PI_CODING_AGENT_DIR: agent, PIG_OFFLINE: '1', PI_OFFLINE: '1', PIG_TELEMETRY: '0', PIG_SKIP_VERSION_CHECK: '1', TERM: 'dumb', TMPDIR: join(home, 'tmp'), PIG_TS_RUNTIME: 'node' };
  const rpc = new Rpc([arm.exe, '--mode', 'rpc', '--no-session', '--offline', ...arm.args, '--provider', 'fake', '--model', 'fake-model'], env, home);
  const first = model.requests.length;
  const observed = {};
  try {
    const id = rpc.send({ type: 'get_commands' });
    const [response] = (await rpc.until((e) => e.type === 'response' && e.id === id)).slice(-1);
    const commands = (response.data?.commands ?? []).filter((c) => String(c.name ?? '').startsWith('bench-'));
    observed.commands = commands.map((c) => ({ name: c.name, description: c.description })).sort((a, b) => a.name.localeCompare(b.name));
    if (observed.commands.length !== N) throw new Error(`${observed.commands.length} of ${N} bench commands listed`);
    const call = async (i) => {
      rpc.send({ type: 'prompt', message: `call ${nn(i)}` });
      const ends = (await rpc.until((e) => e.type === 'agent_end')).filter((e) => e.type === 'tool_execution_end');
      if (ends.length !== 1) throw new Error(`call ${nn(i)}: ${ends.length} tool executions`);
      return { toolName: ends[0].toolName, isError: ends[0].isError, result: ends[0].result };
    };
    const calls = [];
    for (let i = 1; i <= N; i++) calls.push(await call(i));
    for (let i = 1; i <= N; i++) {
      const rid = rpc.send({ type: 'prompt', message: `/bench-${nn(i)}` });
      const [reply] = (await rpc.until((e) => e.type === 'response' && e.id === rid)).slice(-1);
      calls.push({ command: `bench-${nn(i)}`, success: reply.success, error: reply.error });
    }
    for (let i = 1; i <= N; i++) calls.push(await call(i));
    observed.calls = calls;
    const tools = (model.requests[first]?.tools ?? []).map((t) => t.function ?? {}).filter((f) => String(f.name ?? '').startsWith('bench_tool_'));
    observed.first_request_tools = tools.sort((a, b) => a.name.localeCompare(b.name));
    if (tools.length !== N) throw new Error(`the first request declared ${tools.length} of ${N} bench tools`);
    observed.tool_messages_to_the_model = model.requests.slice(first).flatMap((r) => r.messages.slice(-1)).filter((m) => m.role === 'tool').map((m) => m.content);
  } finally {
    rpc.close();
  }
  return observed;
}

/** Runs each arm in order and reports, per arm, the section hashes and whether they equal the first arm's. */
export async function compare(arms) {
  const base = mkdtempSync(join(tmpdir(), 'bench-equivalence-'));
  const model = await fakeModel();
  const report = { arms: {}, reference: arms[0].name };
  try {
    for (const arm of arms) {
      try {
        const observed = await session(arm, model, base);
        report.arms[arm.name] = { sections: Object.fromEntries(Object.entries(observed).map(([k, v]) => [k, digest(v)])), observed };
      } catch (error) {
        report.arms[arm.name] = { error: String(error.message ?? error).slice(0, 2000) };
      }
    }
  } finally {
    model.server.close();
    rmSync(base, { recursive: true, force: true });
  }
  const reference = report.arms[report.reference];
  report.equivalent = Object.fromEntries(arms.map((a) => [a.name, Boolean(reference.sections && report.arms[a.name].sections && canonical(report.arms[a.name].sections) === canonical(reference.sections))]));
  return report;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const option = (name) => { const v = []; process.argv.forEach((a, i) => { if (a === name) v.push(process.argv[i + 1]); }); return v; };
  const [pig] = option('--pig');
  const [exts] = option('--exts');
  const binaries = option('--binary');
  const [jsonOut] = option('--json');
  if (!pig || !exts || binaries.length === 0) { console.error('usage: node scripts/bench-equivalence.mjs --pig <pig> --exts <dir> --binary <name>=<path> [--binary ...] [--json <file>]'); process.exit(2); }
  const files = readdirSync(resolve(exts)).filter((f) => /^bench-ext-\d\d\.ts$/.test(f)).sort();
  if (files.length !== N) { console.error(`${exts}: expected ${N} bench-ext-NN.ts files, found ${files.length}`); process.exit(2); }
  const arms = [{ name: 'originals', exe: resolve(pig), args: ['-ne', ...files.flatMap((f) => ['-e', join(resolve(exts), f)])] },
    ...binaries.map((b) => { const [name, path] = b.split('='); return { name, exe: resolve(path), args: [] }; })];
  const report = await compare(arms);
  if (jsonOut) writeFileSync(jsonOut, `${JSON.stringify(report, null, 1)}\n`);
  const sections = Object.keys(report.arms[report.reference].sections ?? {});
  console.log(`| section | ${arms.map((a) => a.name).join(' | ')} |\n|---|${arms.map(() => '---|').join('')}`);
  for (const s of sections) console.log(`| ${s} | ${arms.map((a) => report.arms[a.name].sections?.[s] ?? 'FAIL').join(' | ')} |`);
  console.log(`| equals ${report.reference} | ${arms.map((a) => report.equivalent[a.name]).join(' | ')} |`);
  for (const a of arms) if (report.arms[a.name].error) console.error(`${a.name}: ${report.arms[a.name].error}`);
  process.exit(Object.values(report.equivalent).every(Boolean) ? 0 : 1);
}
