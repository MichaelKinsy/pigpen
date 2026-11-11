// Harness gates (design gates 3 and 6): run the built pig-rea and pig-rea-sealed Binaries against a scripted local model and
// check what the provider request carries, then their size against components/rea/budget.json. Needs the Binaries for this
// machine:
//   PIG_REA_BIN=dist/bin/pig-rea PIG_REA_SEALED_BIN=dist/bin/pig-rea-sealed npm run test:rea-harness
// Each run gets its own PIG_HOME; the unpacked REA payload is shared through a symlinked agent/rea so it unpacks once.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { after, before, describe, test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { startStubModel, stubModels } from './stub-model.mjs';

const budget = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', 'budget.json'), 'utf8'));
const target = `${{ win32: 'windows' }[process.platform] ?? process.platform}/${{ x64: 'amd64' }[process.arch] ?? process.arch}`;

const binaries = { open: process.env.PIG_REA_BIN, sealed: process.env.PIG_REA_SEALED_BIN };
let work;
let sharedRea;

before(() => {
  for (const [kind, path] of Object.entries(binaries)) {
    assert.ok(path && existsSync(path), `set PIG_REA${kind === 'sealed' ? '_SEALED' : ''}_BIN to the built ${kind === 'sealed' ? 'pig-rea-sealed' : 'pig-rea'} Binary (npm run build:piglet)`);
  }
  work = mkdtempSync(join(tmpdir(), 'rea-harness-'));
  sharedRea = join(work, 'rea-cache');
  mkdirSync(sharedRea);
});

after(() => {
  if (work) rmSync(work, { recursive: true, force: true });
});

let runs = 0;

/** A fresh PIG_HOME and project directory; seed(home, project) writes extra files first. With shared false the home gets its
 * own empty agent/rea, so the run unpacks REA. */
function sandbox(seed = () => {}, { shared = true } = {}) {
  const dir = join(work, `run-${++runs}`);
  const home = join(dir, 'home');
  const project = join(dir, 'project');
  mkdirSync(join(home, 'agent'), { recursive: true });
  mkdirSync(project, { recursive: true });
  if (shared) symlinkSync(sharedRea, join(home, 'agent', 'rea'));
  seed(home, project);
  return { home, project };
}

/** Runs `<bin> -p --model stub/stub <args> go` and returns the output and the provider requests. */
async function launch(bin, args, { box = sandbox(), reply } = {}) {
  const stub = await startStubModel(reply);
  writeFileSync(join(box.home, 'agent', 'models.json'), JSON.stringify(stubModels(stub.url)));
  try {
    const child = spawn(resolve(bin), ['-p', '--model', 'stub/stub', ...args, 'go'], {
      cwd: box.project,
      env: { ...process.env, PIG_HOME: box.home, PIG_OFFLINE: '1', PI_SKIP_VERSION_CHECK: '1', PIG_PIGLET_PATH: '', PIG_PIGLET_NAME: '' },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let out = '';
    child.stdout.on('data', (c) => { out += c; });
    child.stderr.on('data', (c) => { out += c; });
    const code = await new Promise((r) => child.on('close', r));
    return { code, out, requests: stub.requests };
  } finally {
    await stub.close();
  }
}

const toolNames = (request) => (request?.tools ?? []).map((t) => t.function?.name ?? t.name);
const systemText = (request) => {
  const message = request?.messages.find((m) => m.role === 'system' || m.role === 'developer');
  return typeof message?.content === 'string' ? message.content : JSON.stringify(message?.content ?? '');
};
const skills = (request) => [...systemText(request).matchAll(/<name>([^<]+)<\/name>/g)].map((m) => m[1]);
const reaListed = (request) => /- mcp__rea \(codemode\)/.test(systemText(request));
const pigletSkills = ['pigpen-reverse-engineer-anything', 'pigpen-derive-a-harness'];

// A user skill and a user extension in the agent directory, which a plain run of the harness must not load.
function seedUserResources(home) {
  mkdirSync(join(home, 'agent', 'skills', 'seeded-skill'), { recursive: true });
  writeFileSync(join(home, 'agent', 'skills', 'seeded-skill', 'SKILL.md'), '---\nname: seeded-skill\ndescription: A seeded user skill.\n---\n\nSeeded.\n');
  mkdirSync(join(home, 'agent', 'extensions'), { recursive: true });
  writeFileSync(join(home, 'agent', 'extensions', 'seeded.ts'), `export default function (pi: any) {
  pi.registerTool({ name: "seeded_tool", label: "Seeded", description: "A seeded user tool.", parameters: { type: "object", properties: {} },
    async execute() { return { content: [{ type: "text", text: "seeded" }], details: {} }; } });
}
`);
}

describe('pig-rea (open)', () => {
  test('REA reaches the model through codemode, and one REA call runs end to end', async () => {
    const code = [
      'const names = ALL_TOOLS.map((t) => t.name).filter((n) => n.startsWith("mcp__rea__"));',
      'const status = await tools.mcp__rea__binary_session({});',
      'const body = JSON.parse(typeof status === "string" ? status : status.content[0].text);',
      'return JSON.stringify({ count: names.length, open: body.result.open, providers: body.result.capabilities.length });',
    ].join('\n');
    const result = await launch(binaries.open, [], { reply: (_, i) => (i === 0 ? { toolCall: { name: 'codemode', arguments: { code } } } : { text: 'done' }) });
    assert.equal(result.code, 0, result.out);
    const [first, second] = result.requests;
    assert.ok(toolNames(first).includes('codemode'), `tools: ${toolNames(first)}`);
    assert.ok(reaListed(first), 'the mcp_servers section does not list mcp__rea');
    assert.deepEqual(skills(first), pigletSkills);
    assert.match(systemText(first), /This is the REA harness/, 'the dashboard\'s identity section is missing');
    const toolMessage = second?.messages.find((m) => m.role === 'tool');
    const content = typeof toolMessage?.content === 'string' ? toolMessage.content : JSON.stringify(toolMessage?.content);
    assert.match(content, /Script completed/, content);
    const answer = JSON.parse(/\{"count".*\}/.exec(content)?.[0] ?? '{}');
    assert.ok(answer.count > 100, `codemode sees ${answer.count} REA tools: ${content.slice(0, 500)}`);
    assert.equal(answer.open, false, 'binary_session did not answer');
    assert.ok(answer.providers > 0, 'binary_session listed no providers');
  });

  test('a plain run loads no ambient extension or skill; the widening overlay brings them back', async () => {
    const plain = await launch(binaries.open, [], { box: sandbox((home) => seedUserResources(home)) });
    assert.equal(plain.code, 0, plain.out);
    assert.ok(!toolNames(plain.requests[0]).includes('seeded_tool'), 'the user extension loaded');
    assert.deepEqual(skills(plain.requests[0]), pigletSkills);

    const box = sandbox((home, project) => {
      seedUserResources(home);
      writeFileSync(join(project, 'overlay.yaml'), 'name: rea-mine\nextends:\n  allowWiden: true\ndiscovery:\n  extensions: [user, workspace]\n  skills: [user, workspace]\n');
    });
    const widened = await launch(binaries.open, ['--piglet', './overlay.yaml'], { box });
    assert.equal(widened.code, 0, widened.out);
    assert.ok(toolNames(widened.requests[0]).includes('seeded_tool'), `tools: ${toolNames(widened.requests[0])}\n${widened.out}`);
    assert.ok(skills(widened.requests[0]).includes('seeded-skill'), `skills: ${skills(widened.requests[0])}`);
    assert.ok(reaListed(widened.requests[0]), 'REA is gone under the overlay');
  });

  test('-ns keeps the piglet skills', async () => {
    const result = await launch(binaries.open, ['-ns']);
    assert.deepEqual(skills(result.requests[0]), pigletSkills);
    assert.ok(reaListed(result.requests[0]));
  });

  test('-ne and --no-mcp drop REA; -ne with the MCP built-ins named brings it back', async () => {
    for (const args of [['-ne'], ['--no-mcp']]) {
      const off = await launch(binaries.open, args);
      assert.equal(off.code, 0, off.out);
      assert.ok(!reaListed(off.requests[0]) && !toolNames(off.requests[0]).includes('codemode'), `${args}: REA still reachable`);
    }
    const back = await launch(binaries.open, ['-ne', '-e', 'builtin:mcp', '-e', 'builtin:codemode', '-e', 'builtin:tool-search']);
    assert.ok(reaListed(back.requests[0]) && toolNames(back.requests[0]).includes('codemode'), back.out);
  });

  test('an overlay that removes rea-dashboard keeps REA and drops the dashboard', async () => {
    const box = sandbox((_, project) => {
      writeFileSync(join(project, 'quiet.yaml'), 'name: rea-quiet\nextends:\n  remove:\n    extensions: [rea-dashboard]\n');
    });
    const result = await launch(binaries.open, ['--piglet', './quiet.yaml'], { box });
    assert.equal(result.code, 0, result.out);
    assert.ok(reaListed(result.requests[0]), result.out);
    assert.doesNotMatch(systemText(result.requests[0]), /REA harness/, 'the dashboard\'s identity paragraph is still there');
  });

  // PiG 0.7.0 (still on PiG main, coding/cli/cli_runtime_build.go:281): when the project has config that needs a trust
  // decision (here .pig/mcp.json), the pre-trust pass loads user-scope extensions with a fixed scope ["user"], ignoring the
  // Piglet's discovery. The user's extension factory runs although the harness says discovery.extensions: [].
  test('a project with its own config does not run the user\'s extensions', { todo: 'PiG pre-trust pass ignores Piglet discovery' }, async () => {
    const marker = join(work, `marker-${runs + 1}`);
    const box = sandbox((home, project) => {
      mkdirSync(join(home, 'agent', 'extensions'), { recursive: true });
      writeFileSync(join(home, 'agent', 'extensions', 'marker.ts'), `import { writeFileSync } from "node:fs";\nexport default function () { writeFileSync(${JSON.stringify(marker)}, "ran"); }\n`);
      mkdirSync(join(project, '.pig'), { recursive: true });
      writeFileSync(join(project, '.pig', 'mcp.json'), JSON.stringify({ mcpServers: {} }));
    });
    const result = await launch(binaries.open, [], { box });
    assert.equal(result.code, 0, result.out);
    assert.ok(!existsSync(marker), 'the user extension ran in the pre-trust pass');
  });
});

describe('pig-rea-sealed', () => {
  test('ambient MCP servers, settings extensions, -e and --piglet are refused, and REA still connects', async () => {
    const seeded = { mcpServers: { seeded: { command: 'node', args: ['-e', 'setInterval(()=>{},1e9)'] } } };
    const box = sandbox((home) => {
      writeFileSync(join(home, 'agent', 'mcp.json'), JSON.stringify(seeded));
      writeFileSync(join(home, 'agent', 'settings.json'), JSON.stringify({ extensions: ['./does-not-matter.ts'] }));
      seedUserResources(home);
    });
    const result = await launch(binaries.sealed, [], { box });
    assert.equal(result.code, 0, result.out);
    assert.match(result.out, /built with build\.closed: true/);
    const first = result.requests[0];
    assert.ok(reaListed(first), `REA is missing from the sealed Binary\n${result.out}`);
    assert.doesNotMatch(systemText(first), /mcp__seeded/);
    assert.ok(!toolNames(first).includes('seeded_tool'));
    assert.deepEqual(skills(first), pigletSkills);

    writeFileSync(join(box.project, 'overlay.yaml'), 'name: o\nextends:\n  allowWiden: true\n');
    for (const args of [['-e', './x.ts'], ['--piglet', './overlay.yaml']]) {
      const refused = await launch(binaries.sealed, args, { box });
      assert.notEqual(refused.code, 0, `${args.join(' ')} was accepted by the sealed Binary:\n${refused.out}`);
      assert.equal(refused.requests.length, 0);
    }
  });

  // Same PiG pre-trust bug as above; in a closed Binary without the Node runtime it stops startup.
  test('a project .pig/mcp.json is refused without loading user extensions', { todo: 'PiG pre-trust pass ignores closed scopes' }, async () => {
    const box = sandbox((home, project) => {
      seedUserResources(home);
      mkdirSync(join(project, '.pig'), { recursive: true });
      writeFileSync(join(project, '.pig', 'mcp.json'), JSON.stringify({ mcpServers: { project_seeded: { command: 'node', args: ['-v'] } } }));
    });
    const result = await launch(binaries.sealed, [], { box });
    assert.equal(result.code, 0, result.out);
    assert.ok(reaListed(result.requests[0]));
    assert.doesNotMatch(systemText(result.requests[0]), /mcp__project_seeded/);
  });
});

describe('budget (gate 6)', () => {
  test('each Binary stays within 10% of its size in budget.json; first and warm start times are reported', async (t) => {
    const baseline = budget[target] ?? {};
    for (const [kind, name] of [['open', 'pig-rea'], ['sealed', 'pig-rea-sealed']]) {
      const size = statSync(binaries[kind]).size;
      t.diagnostic(`${name} ${target}: ${size} bytes${baseline[name] ? ` (budget ${baseline[name]})` : ' (no baseline for this target)'}`);
      if (baseline[name]) assert.ok(size <= baseline[name] * 1.1, `${name} is ${size} bytes, more than 10% over its budget ${baseline[name]}`);
    }
    const box = sandbox(() => {}, { shared: false });
    for (const label of ['first start (unpacks REA)', 'warm start']) {
      const started = performance.now();
      const result = await launch(binaries.open, [], { box });
      assert.equal(result.code, 0, result.out);
      t.diagnostic(`${label}: ${((performance.now() - started) / 1000).toFixed(2)} s to a finished one-turn run`);
    }
  });
});
