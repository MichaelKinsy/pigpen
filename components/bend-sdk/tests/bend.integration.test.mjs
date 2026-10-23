// Runs a real `pig` with the Bend example extensions loaded as exact standalones and checks, over the real wire:
//   1. a tool call round trip (the faux model calls echo_bridge, the Bend extension answers),
//   2. a host call whose answer only the host can know (getSessionID: the result carries the id of the session
//      the host printed, so an SDK that never calls the host cannot pass),
//   3. a loud gap failure (an unsupported event stops the extension at load with the SDK's own message).
// PiG's hermetic test-faux provider, a temporary HOME, PIG_HOME, PI_HOME, PIG_CODING_AGENT_DIR and PI_CODING_AGENT_DIR:
// nothing under ~/.pig is read or written, no network, no credentials.
//
//   PIG_BIN=/path/to/pig npm run test:bend-sdk     (needs bend 2.0.38 on PATH or in $BEND_HOME/bin)
//
// Not run in CI: the CI images do not carry bend 2.0.38 (see README.md).
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import { delimiter, dirname, join, resolve } from 'node:path';
import { after, before, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';

const component = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const PINNED = '2.0.38';
const pig = process.env.PIG_BIN;
if (!pig) throw new Error('PIG_BIN is not set: point it at a pig (0.5.2 or later)');
const bendHome = process.env.BEND_HOME ?? join(homedir(), '.bend');
const path = [join(bendHome, 'bin'), process.env.PATH].filter(Boolean).join(delimiter);

let dir = '';
let sdk = '';

// The SDK under test is a copy in the temp dir, so the mutation check in README.md can edit it.
const sdkRoot = () => process.env.BEND_SDK_UNDER_TEST ?? component;

function runPig(prompt, extension) {
  const work = join(dir, 'work');
  const env = {
    HOME: join(dir, 'home'), PIG_HOME: join(dir, 'pighome'), PI_HOME: join(dir, 'pighome'),
    PIG_CODING_AGENT_DIR: join(dir, 'agent'), PI_CODING_AGENT_DIR: join(dir, 'agent'),
    PIG_TEST_FAUX: '1', PIG_TEST_FAUX_SCENARIO: 'parity-basic', BEND_NO_TELEMETRY: '1', PATH: path,
  };
  return spawnSync(pig, ['-p', prompt, '--model', 'test-faux/faux-1', '--no-session', '--mode', 'json', '-e', join(sdk, 'examples', extension)],
    { cwd: work, env, encoding: 'utf8', timeout: 120_000 });
}

describe('Bend SDK against a real pig', () => {
  before(() => {
    const version = spawnSync('bend', ['version'], { env: { PATH: path, BEND_NO_TELEMETRY: '1' }, encoding: 'utf8' });
    assert.equal(version.status, 0, `bend is required: ${version.error?.message ?? version.stderr}`);
    assert.match(version.stdout, new RegExp(`\\b${PINNED.replaceAll('.', '\\.')}\\b`), `bend ${PINNED} is required, got: ${version.stdout}`);
    dir = mkdtempSync(join(tmpdir(), 'bend-sdk-'));
    for (const sub of ['home', 'agent', 'pighome', 'work']) mkdirSync(join(dir, sub));
    sdk = join(dir, 'sdk');
    cpSync(sdkRoot(), sdk, { recursive: true, filter: (p) => !p.includes('node_modules') });
  });
  after(() => rmSync(dir, { recursive: true, force: true }));

  it('answers a tool call and a host call over the wire', () => {
    const run = runPig('Run: extension echo hello', 'bend-hello.bend');
    const events = (run.stdout ?? '').split('\n').filter((l) => l.startsWith('{')).map((l) => JSON.parse(l));
    const session = events.find((e) => e.type === 'session');
    assert.ok(session?.id, `no session event\n${run.stdout}\n${run.stderr}`);
    const end = events.find((e) => e.type === 'tool_execution_end' && e.toolName === 'echo_bridge');
    assert.ok(end, `the tool did not run\n${run.stdout}\n${run.stderr}`);
    assert.ok(!end.isError, JSON.stringify(end));
    const text = end.result.content.map((c) => c.text).join('');
    // "hello" came from the model, the session id only from the host's answer to getSessionID.
    assert.equal(text, `bend-echo: hello session=${session.id}`);
  });

  it('fails loudly at load for a capability the SDK does not implement', () => {
    const run = runPig('hi', 'bend-gap.bend');
    const output = `${run.stdout}\n${run.stderr}`;
    assert.match(output, /event before_agent_start is not supported by the Bend SDK/);
    assert.match(output, /Failed to load extension/);
  });
});
