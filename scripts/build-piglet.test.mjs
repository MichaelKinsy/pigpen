// One documented command builds a Piglet Binary from a checkout. PiG rejects a Package that
// lives outside the Piglet's directory, so the command must stage first and hand `pig piglet
// build` the staged manifest. A fake `pig` records what it was asked, so no toolchain is needed.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, describe, it } from 'node:test';
import { parse } from 'yaml';
import { root } from './go-modules.mjs';

const scratch = realpathSync(mkdtempSync(join(tmpdir(), 'pigpen-build-')));
after(() => rmSync(scratch, { recursive: true, force: true }));
const fake = join(scratch, 'pig');
const record = join(scratch, 'args.json');
const required = JSON.parse(readFileSync(new URL('./pig-requirement.json', import.meta.url), 'utf8')).pig;
writeFileSync(fake, `#!/bin/sh\nif [ "$1" = --version ]; then echo ${required}; exit 0; fi\nnode -e 'require("fs").writeFileSync(process.argv[1], JSON.stringify({argv: process.argv.slice(2), cwd: process.cwd(), root: process.env.PIG_SOURCE_ROOT || ""}))' "${record}" "$@"\nexit "\${FAKE_PIG_EXIT:-0}"\n`);
chmodSync(fake, 0o755);

const run = (args, env = {}) => spawnSync(process.execPath, [join(root, 'scripts/build-piglet.mjs'), ...args], {
  cwd: scratch, encoding: 'utf8',
  env: { PATH: process.env.PATH, HOME: scratch, PIG_HOME: scratch, PIG_CODING_AGENT_DIR: scratch, PIG_BIN: fake, PIG_SOURCE_ROOT: scratch, ...env },
});

// The manifests name every platform a release ships, but pig's native builder builds only the machine it runs
// on (`native builder supports only host target`), so a local build asks for the host target unless told otherwise.
const hostTarget = `${{ linux: 'linux', darwin: 'darwin', win32: 'windows' }[process.platform]}/${{ x64: 'amd64', arm64: 'arm64' }[process.arch]}`;
const argvOut = (file) => JSON.parse(readFileSync(file, 'utf8')).argv.indexOf('--out');

describe('build-piglet', () => {
  it('builds for the host target, and leaves a --targets the user gave alone', () => {
    let result = run(['pig-extension-porter', '--out', join(scratch, 'out', 'host')]);
    assert.equal(result.status, 0, result.stdout + result.stderr);
    let argv = JSON.parse(readFileSync(record, 'utf8')).argv;
    assert.deepEqual(argv.slice(-2), ['--targets', hostTarget]);
    result = run(['pig-extension-porter', '--out', join(scratch, 'out', 'given'), '--', '--targets', 'linux/arm64']);
    assert.equal(result.status, 0, result.stdout + result.stderr);
    argv = JSON.parse(readFileSync(record, 'utf8')).argv;
    assert.deepEqual(argv.filter((a) => a === '--targets'), ['--targets'], 'one --targets only');
    assert.deepEqual(argv.slice(-2), ['--targets', 'linux/arm64']);
  });

  it('stages, then builds the staged manifest as a Binary', () => {
    const result = run(['pig-extension-porter', '--out', join(scratch, 'out', 'porter')]);
    assert.equal(result.status, 0, result.stdout + result.stderr);
    const call = JSON.parse(readFileSync(record, 'utf8'));
    const manifest = call.argv[2];
    assert.deepEqual(call.argv.slice(0, 2), ['piglet', 'build']);
    assert.equal(manifest, join(root, 'dist/staged/piglets/pig-extension-porter/piglet.yaml'));
    assert.deepEqual(call.argv.slice(3), ['--format', 'binary', '--out', join(scratch, 'out', 'porter'), '--targets', hostTarget]);
    assert.equal(call.root, scratch, 'PIG_SOURCE_ROOT is passed through');
    const staged = parse(readFileSync(manifest, 'utf8'));
    for (const source of Object.values(staged.packages)) assert.match(source, /^local:\.\/packages\//, 'Packages must sit inside the Piglet directory');
    assert.ok(existsSync(join(root, 'dist/staged/piglets/pig-extension-porter/packages/extension-equivalence')));
  });

  it('defaults the output to dist/bin/<name>', () => {
    const result = run(['pig-extension-porter']);
    assert.equal(result.status, 0, result.stdout + result.stderr);
    assert.equal(JSON.parse(readFileSync(record, 'utf8')).argv[argvOut(record) + 1], join(root, 'dist/bin/pig-extension-porter'));
  });

  it('rejects an unknown Piglet and lists the known ones', () => {
    const result = run(['nope']);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /no Piglet named "nope"/);
    assert.match(result.stderr, /pig-extension-porter/);
  });

  it('rejects a manifest path and says why staging is needed', () => {
    const result = run(['piglets/pig-extension-porter/piglet.yaml']);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /Piglet name/);
    assert.match(result.stderr, /escapes the Piglet anchor|stage/);
  });

  it('keeps pig\'s exit status and adds the two usual remedies', () => {
    const result = run(['pig-extension-porter'], { FAKE_PIG_EXIT: '3', PIG_SOURCE_ROOT: '' });
    assert.equal(result.status, 3);
    assert.match(result.stderr, /PIG_SOURCE_ROOT/);
    assert.match(result.stderr, /git checkout/);
  });

  it('refuses to overwrite a binary unless --replace, which keeps the old one as .prev', () => {
    const target = join(scratch, 'kept', 'porter');
    mkdirSync(join(scratch, 'kept'), { recursive: true });
    writeFileSync(target, 'old build');
    rmSync(record, { force: true });
    const refused = run(['pig-extension-porter', '--out', target]);
    assert.equal(refused.status, 2);
    assert.match(refused.stderr, /already exists/);
    assert.match(refused.stderr, /--replace/);
    assert.ok(!existsSync(record), 'pig must not run when the output exists');
    assert.equal(readFileSync(target, 'utf8'), 'old build');

    const replaced = run(['pig-extension-porter', '--out', target, '--replace']);
    assert.equal(replaced.status, 0, replaced.stderr);
    assert.equal(readFileSync(`${target}.prev`, 'utf8'), 'old build');
    assert.ok(!existsSync(target), 'the fake pig writes nothing, so the old file moved aside');
  });

  it('puts the previous binary back when the replacing build fails', () => {
    const target = join(scratch, 'kept2', 'porter');
    mkdirSync(join(scratch, 'kept2'), { recursive: true });
    writeFileSync(target, 'old build');
    const result = run(['pig-extension-porter', '--out', target, '--replace'], { FAKE_PIG_EXIT: '1' });
    assert.equal(result.status, 1);
    assert.equal(readFileSync(target, 'utf8'), 'old build');
    assert.ok(!existsSync(`${target}.prev`));
  });
});
