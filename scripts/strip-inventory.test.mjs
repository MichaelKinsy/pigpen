// The comparison that proves a strip list removes exactly its IDs, on synthetic inventories: no Binary is built here.
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { PROBES, compareInventories, normalize, stripIdsOf } from './strip-inventory.mjs';

const ok = (output = 'same') => ({ exit: 0, output });
const inventory = (over = {}) => ({ help: ok(), commands: ok('a [extension]'), ...Object.fromEntries(Object.keys(PROBES).map((id) => [id, ok(id)])), ...over });

describe('compareInventories', () => {
  it('accepts a strip whose probes changed and nothing else did', () => {
    const stripped = inventory({ 'self-update': ok('stripped'), 'node-extensions': ok('stripped') });
    assert.deepEqual(compareInventories(inventory(), stripped, ['self-update']).length, 1, 'node-extensions changed without being listed');
    assert.deepEqual(compareInventories(inventory(), inventory({ 'self-update': ok('stripped') }), ['self-update']), []);
  });
  it('refuses a stripped ID whose probe did not change: the strip did nothing', () => {
    const problems = compareInventories(inventory(), inventory(), ['self-update']);
    assert.match(problems.join('\n'), /self-update is in the strip list but its probe answers the same/);
  });
  it('refuses a probe that changed for an ID the list does not name', () => {
    const problems = compareInventories(inventory(), inventory({ docs: ok('gone') }), []);
    assert.match(problems.join('\n'), /docs is not stripped but its probe changed/);
  });
  it('refuses a strip ID it has no probe for', () => {
    assert.match(compareInventories(inventory(), inventory(), ['mermaid']).join('\n'), /mermaid has no probe/);
  });
  it('reports a changed slash command set unless node extensions are stripped', () => {
    const stripped = inventory({ commands: ok('a [extension]\nb [extension]') });
    assert.match(compareInventories(inventory(), stripped, []).join('\n'), /the slash commands changed/);
    const withNode = inventory({ commands: ok('x'), 'node-extensions': ok('stripped') });
    assert.deepEqual(compareInventories(inventory(), withNode, ['node-extensions']), []);
  });
});

describe('stripIdsOf', () => {
  it('reads features, extensions and apis of a manifest and nothing else', () => {
    assert.deepEqual(stripIdsOf('name: x\nstrip:\n  features: [self-update]\n  extensions: [mcp]\n  apis: [google-vertex]\n  tools: [grep]\n'), ['self-update', 'mcp', 'google-vertex']);
    assert.deepEqual(stripIdsOf('name: x\n'), []);
  });
  it('expands a keep-mode feature list to every probed feature it does not keep', () => {
    const everyProbed = Object.keys(PROBES).sort();
    assert.deepEqual(stripIdsOf('name: x\nstrip:\n  keep:\n    extensions: []\n    apis: []\n    features: []\n').sort(), everyProbed);
    assert.deepEqual(stripIdsOf('name: x\nstrip:\n  keep:\n    features: [self-update, docs]\n').sort(), everyProbed.filter((id) => id !== 'self-update' && id !== 'docs'));
    assert.deepEqual(stripIdsOf('name: x\nstrip:\n  keep:\n    extensions: []\n'), [], 'a keep list for another kind strips no probed feature');
  });
});

describe('normalize', () => {
  it('replaces the scratch directory and the Binary path, so two Binaries compare', () => {
    assert.equal(normalize('ran /tmp/a/x via /opt/pig\r\n', '/tmp/a', '/opt/pig'), 'ran <tmp>/x via <bin>');
  });
});
