// bench/pins.json is what pi-arena reads: every pinned bench Piglet must exist with a fixed release version, list exactly its manifest's
// extensions as slots, and the PiG revision must be the one the copied sources come from.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
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
