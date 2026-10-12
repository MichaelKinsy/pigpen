import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { canonical, digest } from './bench-equivalence.mjs';

describe('bench-equivalence canonical form', () => {
  it('ignores object key order and nothing else', () => {
    assert.equal(canonical({ b: 1, a: [2, { d: 4, c: 3 }] }), canonical({ a: [2, { c: 3, d: 4 }], b: 1 }));
    assert.notEqual(digest({ a: [1, 2] }), digest({ a: [2, 1] }));
    assert.notEqual(digest({ a: 1 }), digest({ a: '1' }));
  });
});
