// bench/pins.json is what pi-arena reads: every pinned bench Piglet must exist with a fixed release version, list exactly its manifest's
// extensions as slots, and the PiG revision must be the one the copied sources come from.
import assert from 'node:assert/strict';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { describe, it } from 'node:test';
import { parse } from 'yaml';
import { root } from './go-modules.mjs';

const pins = JSON.parse(readFileSync(join(root, 'bench/pins.json'), 'utf8'));
const spec = JSON.parse(readFileSync(join(root, 'bench/sync-spec.json'), 'utf8'));

describe('bench/pins.json', () => {
  it('pins the PiG revision the copied sources come from', () => {
    assert.equal(pins.pigRevision, spec.pig.revision);
  });
  for (const [name, pin] of Object.entries(pins.piglets)) {
    it(`${name}: a fixed version, and the slots of its manifest`, () => {
      const manifest = parse(readFileSync(join(root, pin.manifest), 'utf8'));
      assert.equal(manifest.name, name);
      assert.match(manifest.release.version, /^\d+\.\d+\.\d+$/, 'a floating or pre-release version is not pinnable');
      assert.deepEqual((manifest.extensions ?? []).map((e) => e.name), pin.slots);
      for (const source of Object.values(manifest.packages ?? {})) assert.match(source, /^local:\.\.\/\.\.\/components\//, `${name}: a package outside Pigpen cannot be pinned by commit`);
    });
  }
});

// Owner rule (2026-10-12): bench Piglets use exact frozen dependencies, never ranges. Go modules are locked by exact go.mod versions and
// go.sum hashes; a binary a Piglet fetches is pinned by version and SHA-256 in its source; no bench component declares an npm dependency.
describe('bench Piglet dependency pins', () => {
  const walk = (dir, out = []) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) walk(path, out);
      else out.push(path);
    }
    return out;
  };
  const benchComponents = () => {
    const names = new Set(Object.keys(spec.components));
    for (const pin of Object.values(pins.piglets)) {
      const manifest = parse(readFileSync(join(root, pin.manifest), 'utf8'));
      for (const source of Object.values(manifest.packages ?? {})) names.add(source.replace(/^local:\.\.\/\.\.\/components\//, ''));
    }
    return [...names].sort();
  };
  const localModule = /^(example\.com\/benchext10\/|github\.com\/MichaelKinsy\/pigpen\/)/;

  it('every go.mod requires exact versions and every external requirement has its go.sum hashes', () => {
    let modules = 0;
    for (const name of benchComponents()) {
      for (const file of walk(join(root, 'components', name)).filter((f) => f.endsWith('/go.mod'))) {
        modules += 1;
        const text = readFileSync(file, 'utf8');
        const sums = existsSync(join(dirname(file), 'go.sum')) ? readFileSync(join(dirname(file), 'go.sum'), 'utf8') : '';
        assert.doesNotMatch(text, /^replace /m, `${file}: a replace directive moves a dependency off its pinned version`);
        for (const [, path, version] of text.matchAll(/^\s*(?:require\s+)?([\w.-]+\.[\w.-]+\/[\w./-]+)\s+(v\S+)/gm)) {
          assert.match(version, /^v\d+\.\d+\.\d+$/, `${file}: ${path} ${version} is not an exact release version`);
          if (localModule.test(path)) continue;
          assert.ok(sums.includes(`${path} ${version} h1:`), `${file}: go.sum lacks the ${path} ${version} hash`);
          assert.ok(sums.includes(`${path} ${version}/go.mod h1:`), `${file}: go.sum lacks the ${path} ${version}/go.mod hash`);
        }
      }
    }
    assert.ok(modules >= 5, 'the walk must reach the bench modules');
  });

  it('no bench component declares an npm dependency', () => {
    for (const name of benchComponents()) {
      const manifest = JSON.parse(readFileSync(join(root, 'components', name, 'package.json'), 'utf8'));
      for (const key of ['dependencies', 'devDependencies', 'optionalDependencies', 'peerDependencies', 'bundledDependencies']) {
        assert.equal(manifest[key], undefined, `${name}: ${key} would resolve a range at install time`);
      }
    }
  });

  it('a binary a bench Piglet fetches is pinned by exact version and SHA-256 in its source', () => {
    const pinned = {
      'bun-runtime': { dir: 'extensions/bunruntime', version: /(?:bunVersion|Version)\s*=\s*"(\d+\.\d+\.\d+)"/ },
      'aot-runtime': { dir: 'extensions/aot-runtime', version: /toolchainVersion\s*=\s*"(\d+\.\d+\.\d+)"/ },
    };
    for (const [name, { dir, version }] of Object.entries(pinned)) {
      const source = walk(join(root, 'components', name, dir)).filter((f) => f.endsWith('.go') && !f.endsWith('_test.go')).map((f) => readFileSync(f, 'utf8')).join('\n');
      assert.match(source, version, `${name}: no exact version pin`);
      assert.ok((source.match(/"[0-9a-f]{64}"/g) ?? []).length >= 2, `${name}: its archives are not pinned by SHA-256`);
    }
  });
});

// The ext10 Piglets exist to expose the ten extensions' tools: an entry with `tools: []` allows none of them, and the Binary then declares no
// bench_tool_NN to the model (found by scripts/bench-equivalence.mjs: 0 of 10 tools declared).
describe('ext10 Piglets expose their tools', () => {
  for (const name of ['bench-ext10', 'bench-ext10-aot', 'bench-ext10-split']) {
    it(`${name}: no bench-ext extension entry restricts its tools`, () => {
      const manifest = parse(readFileSync(join(root, pins.piglets[name].manifest), 'utf8'));
      const benchExtensions = manifest.extensions.filter((e) => /^bench-ext-\d\d$/.test(e.name));
      assert.equal(benchExtensions.length, 10);
      for (const entry of benchExtensions) assert.equal(entry.tools, undefined, `${entry.name} allows ${JSON.stringify(entry.tools)}`);
    });
  }
});
