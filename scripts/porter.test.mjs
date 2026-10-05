import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { chmodSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, renameSync, rmSync, statSync, symlinkSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse, stringify } from 'yaml';
import test from 'node:test';
import { requirePig } from './pig-bin.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const pig = requirePig();

// The provider records what PiG actually sends after Package discovery and Skill expansion.
test('porter stages one shared Package for two Piglets without changing authored resources or settings', { timeout: 90000 }, async t => {
  const scratch = realpathSync(mkdtempSync(join(tmpdir(), 'pigpen-porter-')));
  t.after(() => rmSync(scratch, { recursive: true, force: true }));
  const home = join(scratch, 'home');
  const cwd = join(scratch, 'work');
  mkdirSync(join(home, 'agent'), { recursive: true });
  mkdirSync(cwd);
  const env = { PATH: process.env.PATH, HOME: home, USERPROFILE: home, PIG_HOME: home,
    PIG_CODING_AGENT_DIR: join(home, 'agent'), PI_TELEMETRY: '0', PI_OFFLINE: '1' };
  const run = (args, status = 0) => new Promise((accept, reject) => {
    const child = spawn(pig, args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '', stderr = '';
    const timer = setTimeout(() => child.kill('SIGKILL'), 20000);
    child.stdout.on('data', chunk => { stdout += chunk; });
    child.stderr.on('data', chunk => { stderr += chunk; });
    child.on('error', error => { clearTimeout(timer); reject(error); });
    child.on('close', (code, signal) => {
      clearTimeout(timer);
      try {
        assert.equal(code, status, `${args.join(' ')}: signal=${signal}\n${stdout}\n${stderr}`);
        accept({ stdout, stderr });
      } catch (error) { reject(error); }
    });
  });

  const index = JSON.parse(readFileSync(join(root, 'index.json')));
  assert.ok(index.entries.every(entry => entry.official === false), 'catalog must not claim official status');
  const source = join(scratch, 'source');
  const component = join(source, 'components/extension-port');
  const manifest = join(source, 'piglets/pig-porter/piglet.yaml');
  cpSync(join(root, 'components/extension-port'), component, { recursive: true });
  cpSync(join(root, 'LICENSE'), join(source, 'LICENSE'));
  // The runtime scenario is about one shared Skill Package selected by two Piglets. Take the
  // focused porter's composition without its Go extension (which builds on first use), so the
  // scenario needs no toolchain: it is the same Skill selection, staged and run.
  const focused = parse(readFileSync(join(root, 'piglets/pig-extension-porter/piglet.yaml'), 'utf8'));
  focused.name = 'pig-porter';
  delete focused.packages['extension-equivalence'];
  delete focused.build;
  focused.extensions = [];
  mkdirSync(join(source, 'piglets/pig-porter'), { recursive: true });
  writeFileSync(manifest, stringify(focused));
  cpSync(join(root, 'piglets/pig-extension-porter/catalog.json'), join(source, 'piglets/pig-porter/catalog.json'));
  const peer = join(source, 'piglets/porter-peer');
  mkdirSync(peer);
  const authored = readFileSync(manifest, 'utf8');
  const peerManifest = authored.replace('name: pig-porter\n', 'name: porter-peer\n') + 'tools: []\n';
  writeFileSync(join(peer, 'piglet.yaml'), peerManifest);
  cpSync(join(root, 'piglets/pig-extension-porter/catalog.json'), join(peer, 'catalog.json'));
  const stage = (status = 0) => {
    const result = spawnSync(process.execPath, [join(root, 'scripts/stage-piglets.mjs'), source], { cwd, env, encoding: 'utf8' });
    assert.ifError(result.error);
    assert.equal(result.status, status, result.stdout + result.stderr);
    return result.stdout + result.stderr;
  };
  const stagedRoot = join(source, 'dist/staged');
  const skillPath = 'skills/pigpen-pi-extension-port/SKILL.md';
  const skill = readFileSync(join(component, skillPath), 'utf8');
  const body = skill.replace(/^---\n[\s\S]*?\n---\n/, '').trim();
  assert.ok(body.length > 0);

  const requests = [];
  const server = createServer(async (request, response) => {
    let raw = '';
    for await (const chunk of request) raw += chunk;
    try {
      const payload = JSON.parse(raw);
      requests.push(payload);
      response.writeHead(200, { 'Content-Type': 'text/event-stream' });
      for (const choice of [{ delta: { role: 'assistant', content: 'porter transport verified' }, finish_reason: null },
        { delta: {}, finish_reason: 'stop' }]) {
        response.write(`data: ${JSON.stringify({ id: 'porter-test', object: 'chat.completion.chunk', created: 1,
          model: 'porter-test', choices: [{ index: 0, ...choice }] })}\n\n`);
      }
      response.end('data: [DONE]\n\n');
    } catch (error) { response.writeHead(400); response.end(String(error)); }
  });
  await new Promise(accept => server.listen(0, '127.0.0.1', accept));
  t.after(() => new Promise(accept => { server.close(accept); server.closeAllConnections(); }));
  const { port } = server.address();
  writeFileSync(join(home, 'agent/models.json'), JSON.stringify({ providers: { 'porter-test': {
    baseUrl: `http://127.0.0.1:${port}/v1`, api: 'openai-completions', apiKey: 'synthetic-test-key',
    models: [{ id: 'porter-test', name: 'Porter test', reasoning: false, input: ['text'],
      contextWindow: 128000, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }],
  } } }));
  const prompt = ['--print', '--no-session', '--offline', '--no-extensions', '--no-context-files', '--approve',
    '--model', 'porter-test/porter-test', '/skill:pigpen-pi-extension-port inventory the supplied extension'];

  await run(['package', 'validate', component]);
  await run(['install', component]);
  const direct = await run(prompt);
  assert.match(direct.stdout, /porter transport verified/);
  const directText = requests[0].messages.flatMap(message => typeof message.content === 'string'
    ? [message.content] : message.content.map(part => part.text || '')).join('\n');
  assert.ok(directText.includes(body), 'direct Package install must expand the complete Skill body');
  await run(['remove', component]);
  const settings = readFileSync(join(home, 'agent/settings.json'), 'utf8');
  console.log('Direct Package validation, installation, Skill expansion, and removal passed');

  const unselected = 'AMBIENT_SKILL_MUST_NOT_LOAD';
  for (const directory of [join(home, '.agents/skills/unselected'), join(cwd, '.pig/skills/unselected'), join(component, 'skills/unselected')]) {
    mkdirSync(directory, { recursive: true });
    writeFileSync(join(directory, 'SKILL.md'), `---\nname: unselected\ndescription: ${unselected}\n---\nUnrelated procedure.\n`);
  }
  const packagePath = join(component, 'package.json');
  const packageData = JSON.parse(readFileSync(packagePath));
  packageData.pi.skills.push('skills/unselected');
  writeFileSync(packagePath, JSON.stringify(packageData));
  mkdirSync(join(source, 'components/unused'));
  writeFileSync(join(source, 'components/unused/unused.txt'), 'not selected');
  writeFileSync(join(component, 'executable.sh'), '#!/bin/sh\nexit 0\n');
  chmodSync(join(component, 'executable.sh'), 0o755);

  assert.match(stage(), /Staged 2 Piglets/);
  const stagedManifests = ['pig-porter', 'porter-peer'].map(name => join(stagedRoot, 'piglets', name, 'piglet.yaml'));
  let emptyScopeTools;
  for (const [index, stagedManifest] of stagedManifests.entries()) {
    const name = ['pig-porter', 'porter-peer'][index];
    const copiedPackage = join(stagedRoot, 'piglets', name, 'packages/extension-port');
    const rewritten = parse(readFileSync(stagedManifest, 'utf8'));
    const expected = parse(index === 0 ? authored : peerManifest);
    expected.packages['extension-port'] = 'local:./packages/extension-port';
    assert.deepEqual(rewritten, expected, 'staging must change only the local Package source');
    assert.equal(readFileSync(join(copiedPackage, skillPath), 'utf8'), skill);
    assert.equal(readFileSync(join(copiedPackage, 'LICENSE'), 'utf8'), readFileSync(join(component, 'LICENSE'), 'utf8'));
    assert.equal(readFileSync(join(copiedPackage, 'CREDITS.md'), 'utf8'), readFileSync(join(component, 'CREDITS.md'), 'utf8'));
    assert.equal(statSync(join(copiedPackage, 'executable.sh')).mode & 0o777, 0o755);
    assert.deepEqual(readdirSync(join(stagedRoot, 'piglets', name, 'packages')), ['extension-port']);
    await run(['package', 'validate', copiedPackage]);
    await run(['piglet', 'validate', stagedManifest]);
    const selected = await run(['--piglet', stagedManifest, ...prompt]);
    assert.match(selected.stdout, /porter transport verified/);
    const request = requests.at(-1);
    const selectedText = request.messages.flatMap(message => typeof message.content === 'string'
      ? [message.content] : message.content.map(part => part.text || '')).join('\n');
    assert.ok(selectedText.includes(body), 'each Piglet must expand the complete shared Skill without a global installation');
    assert.ok(!selectedText.includes(unselected), 'unselected Package and ambient Skills must not load');
    if (index === 1) emptyScopeTools = request.tools?.map(tool => tool.function.name) || [];
    assert.equal(readFileSync(join(home, 'agent/settings.json'), 'utf8'), settings);
  }
  assert.equal(requests.length, 3);
  assert.equal(readFileSync(manifest, 'utf8'), authored);
  assert.equal(readFileSync(join(peer, 'piglet.yaml'), 'utf8'), peerManifest);
  assert.equal(readFileSync(join(component, skillPath), 'utf8'), skill);

  const stagedSnapshot = readFileSync(stagedManifests[0], 'utf8');
  writeFileSync(join(stagedRoot, 'stale.txt'), 'must disappear after restaging');
  const updatedSkill = skill + '\nSource edit must reach both staged Piglets.\n';
  writeFileSync(join(component, skillPath), updatedSkill);
  stage();
  assert.ok(!existsSync(join(stagedRoot, 'stale.txt')));
  for (const name of ['pig-porter', 'porter-peer']) {
    assert.equal(readFileSync(join(stagedRoot, 'piglets', name, 'packages/extension-port', skillPath), 'utf8'), updatedSkill);
  }
  assert.equal(readFileSync(stagedManifests[0], 'utf8'), stagedSnapshot, 'unchanged manifests must stage deterministically');
  // A staged composition must not depend on the authored directories remaining at their original paths.
  renameSync(join(source, 'components'), join(source, 'components-hidden'));
  renameSync(join(source, 'piglets'), join(source, 'piglets-hidden'));
  await run(['piglet', 'validate', stagedManifests[0]]);
  const relocated = await run(['--piglet', stagedManifests[0], ...prompt]);
  assert.match(relocated.stdout, /porter transport verified/);
  assert.ok(JSON.stringify(requests.at(-1)).includes('Source edit must reach both staged Piglets.'));
  renameSync(join(source, 'components-hidden'), join(source, 'components'));
  renameSync(join(source, 'piglets-hidden'), join(source, 'piglets'));
  assert.equal(requests.length, 4);

  const sibling = parse(authored).packages['extension-port'];
  for (const [value, diagnostic] of [
    ['local:../../../outside', /components/],
    [`local:${component}`, /relative/],
    ['local:../../components/missing', /ENOENT|missing/],
  ]) {
    writeFileSync(manifest, authored.replace(sibling, value));
    assert.match(stage(1), diagnostic);
    assert.equal(readFileSync(stagedManifests[0], 'utf8'), stagedSnapshot, 'invalid input must not destroy the last stage');
  }
  writeFileSync(manifest, authored.replace('packages:', 'packages: {}\npackages:'));
  assert.match(stage(1), /YAML|unique|duplicate/i);
  writeFileSync(manifest, authored);
  symlinkSync(join(source, 'LICENSE'), join(component, 'linked-license'));
  assert.match(stage(1), /symlink/i);
  rmSync(join(component, 'linked-license'));
  mkdirSync(join(source, 'piglets/pig-porter/packages/extension-port'), { recursive: true });
  assert.match(stage(1), /collision|reserved/i);
  rmSync(join(source, 'piglets/pig-porter/packages'), { recursive: true });
  rmSync(join(source, 'dist'), { recursive: true });
  const outside = join(scratch, 'outside');
  mkdirSync(outside);
  writeFileSync(join(outside, 'sentinel'), 'untouched');
  symlinkSync(outside, join(source, 'dist'));
  assert.match(stage(1), /symlink/i);
  assert.equal(readFileSync(join(outside, 'sentinel'), 'utf8'), 'untouched');
  rmSync(join(source, 'dist'));
  stage();
  assert.equal(readFileSync(join(home, 'agent/settings.json'), 'utf8'), settings);

  const missing = join(scratch, 'missing.yaml');
  writeFileSync(missing, 'name: missing\npackages: {}\nskills:\n  - name: pigpen-pi-extension-port\n    origins: [package:missing]\n');
  const failed = await run(['piglet', 'validate', missing], 1);
  assert.match(failed.stdout + failed.stderr, /missing/i);
  assert.equal(requests.length, 4, 'invalid selection must not reach the model');

  const version = await run(['version']);
  const digest = value => createHash('sha256').update(value).digest('hex');
  console.log(JSON.stringify({ runtime: version.stdout.trim(), node: process.version,
    stageScriptSha256: digest(readFileSync(join(root, 'scripts/stage-piglets.mjs'))),
    skillSha256: digest(updatedSkill), sourceManifestSha256: digest(readFileSync(manifest)),
    stagedManifestSha256: digest(readFileSync(stagedManifests[0])),
    requestsSha256: digest(JSON.stringify(requests)), requests: requests.length,
    staging: 'passed', emptyScopeTools, result: emptyScopeTools.length ? 'failed' : 'passed' }));
  // Report staging and path-safety results before checking the separate runtime scope contract.
  await t.test('PiG honors the staged explicit empty tool scope', () => {
    assert.deepEqual(emptyScopeTools, [], 'tools: [] must expose no model tools');
  });
});
