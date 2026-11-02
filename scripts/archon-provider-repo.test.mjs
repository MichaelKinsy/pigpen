// The standalone repository Archon installs archon-provider-pig from (scripts/archon-provider-repo.mjs): its release tag, its six asset
// names and checksums.txt, its layout, and the descriptor check, each against what Archon itself requires.
//
// Archon's contract is read from its source. Set ARCHON_CHECKOUT to a checkout of coleam00/Archon (release-archon-provider.yml checks out the commit
// in its ARCHON_COMMIT and sets it) and the real `releaseAsset` of packages/paths/src/release-asset.ts is called and the real TAG regex of
// packages/cli/src/commands/plugin-source.ts is read. Without it those two comparisons are skipped and the expectations written out below, copied
// from that source, still hold.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, before, describe, it } from 'node:test';
import { pathToFileURL } from 'node:url';
import {
  ARCHON_TAG, BUN_TARGETS, checkArchonManifest, checksumsText, generateRepo, handshake, installLine, planRelease, releaseAsset, releaseAssets, releaseTag, standaloneRepository, writeChecksums,
} from './archon-provider-repo.mjs';
import { root } from './packages.mjs';

const COMPONENT_TAG = 'components/archon-provider-pig/v0.1.0';
const component = join(root, 'components', 'archon-provider-pig');
const archon = process.env.ARCHON_CHECKOUT;
// What packages/paths/src/release-asset.ts returns for each target, for the executable archon-provider-pig.
const EXPECTED_ASSETS = [
  'archon-provider-pig-linux-x64',
  'archon-provider-pig-linux-arm64',
  'archon-provider-pig-darwin-x64',
  'archon-provider-pig-darwin-arm64',
  'archon-provider-pig-windows-x64.exe',
  'archon-provider-pig-windows-arm64.exe',
];
// plugin-binary.ts expectedDigest: how Archon reads a line of checksums.txt.
const archonDigestLine = /^([0-9a-f]{64}) [ *](.+?)\r?$/;

let dir;
before(() => { dir = mkdtempSync(join(tmpdir(), 'archon-provider-repo-')); });
after(() => rmSync(dir, { recursive: true, force: true }));

describe('the release tag', () => {
  it('maps a component tag to a plain tag Archon accepts', () => {
    const tag = releaseTag(COMPONENT_TAG);
    assert.equal(tag, 'v0.1.0');
    assert.match(tag, ARCHON_TAG);
  });
  it('the component tag itself is not one Archon accepts (why a separate repository)', () => {
    assert.doesNotMatch(COMPONENT_TAG, ARCHON_TAG);
  });
  it('every version pigpen accepts in a component tag maps to a tag Archon accepts', () => {
    for (const version of ['0.1.0', '1.2.3', '10.20.30', '0.1.0-rc.1', '1.0.0-beta-2']) {
      const tag = releaseTag(`components/archon-provider-pig/v${version}`);
      assert.equal(tag, `v${version}`);
      assert.match(tag, ARCHON_TAG, tag);
    }
  });
  it('plans the real component: its tag, the standalone repository and the executable', () => {
    assert.deepEqual(planRelease(COMPONENT_TAG), { name: 'archon-provider-pig', version: '0.1.0', tag: 'v0.1.0', repository: 'MichaelKinsy/archon-provider-pig', executable: 'archon-provider-pig' });
  });
  it('refuses a tag whose version the plugin does not answer, and a non-component tag', () => {
    assert.throws(() => planRelease('components/archon-provider-pig/v9.9.9'), /answers version/);
    assert.throws(() => planRelease('archon-provider-pig/v0.1.0'), /not a component tag/);
    assert.throws(() => standaloneRepository('herdr'), /no standalone repository/);
  });
  it('is the TAG regex of the Archon source', { skip: archon ? false : 'ARCHON_CHECKOUT is not set' }, () => {
    const source = readFileSync(join(archon, 'packages/cli/src/commands/plugin-source.ts'), 'utf8');
    const literal = /^const TAG = (\/.*\/);$/m.exec(source)?.[1];
    assert.ok(literal, 'plugin-source.ts has no `const TAG = /.../;`');
    assert.equal(literal, String(ARCHON_TAG));
    assert.equal(new RegExp(literal.slice(1, -1)).test(COMPONENT_TAG), false);
  });
});

describe('release assets and checksums.txt', () => {
  it('names the six assets the way Archon does', () => {
    assert.deepEqual(BUN_TARGETS.map((target) => releaseAsset('archon-provider-pig', target)), EXPECTED_ASSETS);
    assert.deepEqual(releaseAssets('archon-provider-pig'), EXPECTED_ASSETS);
    assert.throws(() => releaseAsset('archon-provider-pig', 'bun-linux-x64-baseline'), /Unsupported release target/);
    assert.throws(() => releaseAsset('archon-provider-pig', 'linux-x64'), /Unsupported release target/);
  });
  it('is the real releaseAsset of Archon, for every target', { skip: archon ? false : 'ARCHON_CHECKOUT is not set' }, async () => {
    const real = await import(pathToFileURL(join(archon, 'packages/paths/src/release-asset.ts')).href);
    for (const target of BUN_TARGETS) assert.equal(releaseAsset('archon-provider-pig', target), real.releaseAsset('archon-provider-pig', target), target);
    assert.equal(releaseAssets('archon-provider-pig').length, 6);
  });
  it('writes a sha256sum checksums.txt Archon can read for every asset', () => {
    const out = join(dir, 'assets');
    mkdirSync(out);
    for (const asset of EXPECTED_ASSETS) writeFileSync(join(out, asset), `binary of ${asset}`);
    writeFileSync(join(out, 'not-an-asset'), 'ignored');
    writeChecksums(out, 'archon-provider-pig');
    const text = readFileSync(join(out, 'checksums.txt'), 'utf8');
    assert.ok(text.endsWith('\n'));
    const digests = new Map();
    for (const line of text.split('\n')) {
      const match = archonDigestLine.exec(line);
      if (match) digests.set(match[2], match[1]);
    }
    assert.deepEqual([...digests.keys()], EXPECTED_ASSETS);
    for (const asset of EXPECTED_ASSETS) assert.equal(digests.get(asset), createHash('sha256').update(`binary of ${asset}`).digest('hex'));
    assert.equal(text.split('\n').filter(Boolean).length, 6);
  });
  it('refuses a release that lacks an asset', () => {
    const out = join(dir, 'partial');
    mkdirSync(out);
    for (const asset of EXPECTED_ASSETS.slice(1)) writeFileSync(join(out, asset), 'x');
    assert.throws(() => checksumsText(out, 'archon-provider-pig'), /archon-provider-pig-linux-x64 is missing/);
  });
});

describe('the generated repository', () => {
  const line = 'archon plugin add MichaelKinsy/archon-provider-pig';
  it('the install line is the one that works, in the component README and in the generated one', () => {
    assert.equal(installLine('MichaelKinsy/archon-provider-pig'), line);
    const readme = readFileSync(join(component, 'README.md'), 'utf8');
    assert.ok(readme.includes(`\`\`\`bash\n${line}\n\`\`\``), 'the component README names the install line');
    assert.doesNotMatch(readme, /archon plugin add [^\n]*pigpen/);
    assert.doesNotMatch(readme, /@components\//);
  });
  it('has the README, LICENSE and manifest at the root, and the sources', () => {
    const out = join(dir, 'repo');
    generateRepo(component, out);
    const files = readdirSync(out).sort();
    for (const file of ['README.md', 'LICENSE', 'archon-plugin.json', 'src', 'test']) assert.ok(files.includes(file), `${file} in ${files.join(', ')}`);
    assert.equal(readFileSync(join(out, 'LICENSE'), 'utf8'), readFileSync(join(component, 'LICENSE'), 'utf8'));
    const readme = readFileSync(join(out, 'README.md'), 'utf8');
    assert.ok(readme.startsWith('# archon-provider-pig\n\n> This repository is generated from'));
    assert.ok(readme.includes(line));
    const manifest = JSON.parse(readFileSync(join(out, 'archon-plugin.json'), 'utf8'));
    checkArchonManifest(manifest);
    assert.equal(manifest.executable, 'archon-provider-pig');
    assert.ok(readFileSync(join(out, 'src', 'main.ts'), 'utf8').includes("const VERSION = '0.1.0';"));
  });
  it('carries no em dash into the public repository', () => {
    const out = join(dir, 'repo');
    const walk = (directory) => readdirSync(directory, { withFileTypes: true }).flatMap((entry) => (entry.isDirectory() ? walk(join(directory, entry.name)) : [join(directory, entry.name)]));
    for (const file of walk(out)) assert.ok(!readFileSync(file, 'utf8').includes('\u2014'), `${file} has an em dash`);
  });
  it('refuses a component whose README lacks the install line', () => {
    const copy = join(dir, 'archon-provider-pig');
    mkdirSync(copy);
    for (const file of ['LICENSE', 'archon-plugin.json', 'provenance.json']) writeFileSync(join(copy, file), readFileSync(join(component, file)));
    for (const entry of ['src', 'test', 'fixtures']) mkdirSync(join(copy, entry));
    writeFileSync(join(copy, 'README.md'), '# archon-provider-pig\n\narchon plugin add MichaelKinsy/pigpen/components/archon-provider-pig@components/archon-provider-pig/v0.1.0\n');
    assert.throws(() => generateRepo(copy, join(dir, 'refused')), /must carry the install line/);
  });
  it('checkArchonManifest holds the manifest to Archon\'s schema', () => {
    const good = { schemaVersion: 1, kind: 'provider', name: 'provider-pig', description: 'x', executable: 'archon-provider-pig' };
    checkArchonManifest(good);
    assert.throws(() => checkArchonManifest({ ...good, kind: 'forge' }), /kind provider/);
    assert.throws(() => checkArchonManifest({ ...good, name: 'Provider.Pig' }), /name must be/);
    assert.throws(() => checkArchonManifest({ ...good, executable: 'pig' }), /executable must be archon-provider-/);
  });
});

describe('the descriptor check', () => {
  const answer = (descriptor) => `#!/usr/bin/env node
process.stdin.once('data', (data) => {
  const { id } = JSON.parse(String(data));
  process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id, result: { protocolVersion: 1, agentCapabilities: { _meta: { archon: ${JSON.stringify(descriptor)} } }, authMethods: [] } }) + '\\n');
});
setTimeout(() => {}, 5000);
`;
  const descriptor = { protocol: 3, id: 'pig', displayName: 'PiG', version: '0.1.0', capabilities: {}, credentials: { kind: 'static', specs: [] }, configSchema: {} };
  const fixture = (name, source) => {
    const file = join(dir, name);
    writeFileSync(file, source);
    chmodSync(file, 0o755);
    return file;
  };
  const expected = { executable: 'archon-provider-pig', version: '0.1.0' };
  it('accepts a binary that answers a descriptor for the executable and version', async () => {
    assert.equal((await handshake(fixture('ok', answer(descriptor)), expected)).id, 'pig');
  });
  it('rejects a protocol other than 3, an id that does not make the executable, and a stale version', async () => {
    await assert.rejects(handshake(fixture('p2', answer({ ...descriptor, protocol: 2 })), expected), /protocol is 2/);
    await assert.rejects(handshake(fixture('id', answer({ ...descriptor, id: 'other' })), expected), /does not make the executable/);
    await assert.rejects(handshake(fixture('ver', answer({ ...descriptor, version: '0.0.9' })), expected), /version 0\.0\.9/);
  });
  it('rejects a binary that exits without answering', async () => {
    await assert.rejects(handshake(fixture('dead', '#!/bin/sh\nexit 0\n'), expected), /exited before/);
  });
});
