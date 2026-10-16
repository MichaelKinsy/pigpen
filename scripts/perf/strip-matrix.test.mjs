import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import YAML from 'yaml';
import { KEEP_CORE, STRIP_ALL, table, variantManifest } from './strip-matrix.mjs';

const herdr = 'name: herdr\ndescription: "x"\nrelease:\n  version: 0.1.0\nbuild:\n  targets: [linux/amd64]\nextensions:\n  - name: herdr\n    origins: [package:herdr]\n';

describe('strip-matrix', () => {
  it('renames, fuses and keeps the rest of the manifest', () => {
    const doc = YAML.parse(variantManifest(herdr, 'herdr-none', 'none'));
    assert.equal(doc.name, 'herdr-none');
    assert.equal(doc.build.extensionRealization, 'fused');
    assert.deepEqual(doc.build.targets, ['linux/amd64']);
    assert.equal(doc.strip, undefined);
    assert.equal(doc.extensions[0].name, 'herdr');
  });

  it('strips every binary built-in in `all`, and lets a Piglet keep a runtime it needs', () => {
    const doc = YAML.parse(variantManifest(herdr, 'h', 'all', { without: ['extension-sdk-go'] }));
    assert.deepEqual(doc.strip.extensions, STRIP_ALL.extensions);
    assert.deepEqual(doc.strip.apis, STRIP_ALL.apis);
    assert.ok(doc.strip.features.includes('node-extensions'));
    assert.ok(!doc.strip.features.includes('extension-sdk-go'));
  });

  it('uses keep mode for the core, with no deny list beside it', () => {
    const doc = YAML.parse(variantManifest(herdr, 'h', 'keep'));
    assert.deepEqual(doc.strip, KEEP_CORE);
    assert.equal(doc.strip.tools, undefined);
  });

  it('replaces a strip block the source manifest already had', () => {
    const doc = YAML.parse(variantManifest(`${herdr}strip:\n  commands: [/share]\n`, 'h', 'none'));
    assert.equal(doc.strip, undefined);
  });

  it('refuses an unknown mode', () => {
    assert.throws(() => variantManifest(herdr, 'h', 'some'), /unknown strip mode some/);
  });

  it('shows each row against the first', () => {
    const text = table([{ name: 'a', bytes: 100 * 1048576, seconds: 7 }, { name: 'b', bytes: 55 * 1048576, seconds: 39.2 }]);
    assert.match(text, /\| a \| 100\.0 MiB \(104857600 B\) \| - \| 7s \|/);
    assert.match(text, /\| b \| 55\.0 MiB \(57671680 B\) \| -45% \| 39\.2s \|/);
  });
});
