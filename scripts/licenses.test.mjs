// The SPDX table behind the licence guard: what may be fused into what, and what is refused.
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { canFuse, canonicalId, licenseIds, licenseNote, parseSpdx } from './licenses.mjs';

const ok = (component, target) => assert.deepEqual(canFuse(component, target), { ok: true }, `${component} into ${target}`);
const refused = (component, target, pattern) => {
  const verdict = canFuse(component, target);
  assert.equal(verdict.ok, false, `${component} into ${target}`);
  if (pattern) assert.match(verdict.reason, pattern);
};

describe('SPDX parsing', () => {
  it('reads ids, AND, OR, WITH and parentheses', () => {
    assert.deepEqual(parseSpdx('MIT'), { id: 'MIT' });
    assert.deepEqual(parseSpdx('MIT OR (Apache-2.0 AND BSD-3-Clause)'), { or: [{ id: 'MIT' }, { and: [{ id: 'Apache-2.0' }, { id: 'BSD-3-Clause' }] }] });
    assert.deepEqual(parseSpdx('GPL-2.0-only WITH Classpath-exception-2.0'), { id: 'GPL-2.0-only', exception: 'Classpath-exception-2.0' });
  });
  it('rejects empty, malformed and non-licences', () => {
    for (const text of [undefined, '', '   ', 'MIT OR', '(MIT', 'MIT AND AND ISC', 'SEE LICENSE IN LICENSE', 'UNLICENSED', 'NONE', 'LicenseRef-custom']) assert.throws(() => parseSpdx(text), undefined, String(text));
  });
  it('maps the deprecated GPL ids onto the current ones', () => {
    assert.equal(canonicalId('GPL-3.0'), 'GPL-3.0-only');
    assert.equal(canonicalId('GPL-3.0+'), 'GPL-3.0-or-later');
    assert.equal(canonicalId('lgpl-2.1'), 'LGPL-2.1-only');
    assert.equal(canonicalId('Made-Up-1.0'), undefined);
    assert.deepEqual(licenseIds('MIT AND GPL-3.0+'), ['MIT', 'GPL-3.0-or-later']);
  });
});

describe('fusing a component into a Piglet', () => {
  it('refuses GPL and AGPL into MIT, Apache and BSD Piglets because they relicense the whole work', () => {
    for (const component of ['GPL-3.0-or-later', 'GPL-3.0-only', 'GPL-2.0-only', 'AGPL-3.0-or-later']) {
      for (const target of ['MIT', 'Apache-2.0', 'BSD-3-Clause', 'ISC']) refused(component, target, /would force the whole work/);
    }
  });
  it('refuses LGPL and MPL into MIT, Apache and BSD Piglets as Pigpen policy, not as the licence\'s effect', () => {
    for (const component of ['LGPL-2.1-or-later', 'LGPL-2.1-only', 'LGPL-3.0-only', 'MPL-2.0']) {
      for (const target of ['MIT', 'Apache-2.0', 'BSD-3-Clause', 'ISC']) {
        refused(component, target, /^Pigpen policy: .*weak copyleft.*standalone, opt-in Package/);
        assert.doesNotMatch(canFuse(component, target).reason, /force/);
      }
    }
  });
  it('allows a licence into itself and permissive into anything known', () => {
    for (const id of ['MIT', 'Apache-2.0', 'GPL-3.0-or-later', 'AGPL-3.0-only', 'LGPL-2.1-only', 'MPL-2.0']) ok(id, id);
    ok('MIT', 'GPL-3.0-or-later');
    ok('MIT', 'AGPL-3.0-only');
    ok('BSD-3-Clause', 'LGPL-2.1-only');
    ok('Apache-2.0', 'MIT');
    ok('Apache-2.0', 'GPL-3.0-only');
    ok('Apache-2.0', 'GPL-3.0-or-later');
    ok('Apache-2.0', 'AGPL-3.0-only');
    ok('Apache-2.0', 'LGPL-3.0-only');
    ok('Apache-2.0', 'MPL-2.0');
    ok('MIT', 'Apache-2.0');
  });
  it('refuses Apache-2.0 into a licence that stops at version 2: the Binary carries the Piglet\'s licence', () => {
    for (const target of ['GPL-2.0-only', 'GPL-2.0-or-later', 'LGPL-2.1-only', 'LGPL-2.1-or-later']) refused('Apache-2.0', target, /GPLv3-only in effect/);
    ok('MIT', 'GPL-2.0-only');
  });
  it('follows the GPL family: or-later may move up, only may not, the AGPL is one-way', () => {
    ok('GPL-2.0-or-later', 'GPL-3.0-only');
    ok('GPL-3.0-or-later', 'GPL-3.0-only');
    ok('GPL-3.0-or-later', 'AGPL-3.0-or-later');
    ok('GPL-3.0-only', 'AGPL-3.0-only');
    ok('LGPL-3.0-or-later', 'GPL-3.0-or-later');
    ok('MPL-2.0', 'GPL-3.0-or-later');
    ok('MPL-2.0', 'GPL-2.0-only');
    ok('MPL-2.0', 'LGPL-2.1-only');
    ok('LGPL-2.1-only', 'GPL-2.0-only');
    ok('LGPL-2.1-only', 'GPL-2.0-or-later');
    ok('LGPL-2.1-only', 'GPL-3.0-only');
    ok('LGPL-2.1-only', 'GPL-3.0-or-later');
    ok('LGPL-2.1-only', 'AGPL-3.0-only');
    refused('GPL-3.0-only', 'GPL-3.0-or-later', /cannot be relicensed/);
    refused('GPL-3.0-or-later', 'GPL-2.0-only', /cannot be relicensed/);
    refused('GPL-2.0-only', 'GPL-3.0-or-later', /cannot be relicensed/);
    refused('AGPL-3.0-or-later', 'GPL-3.0-or-later', /cannot be relicensed/);
    refused('LGPL-2.1-only', 'LGPL-3.0-only', /cannot be relicensed/);
    refused('LGPL-3.0-only', 'GPL-2.0-or-later', /cannot be relicensed/);
  });
  it('treats the deprecated ids like the current ones', () => {
    ok('GPL-3.0+', 'GPL-3.0-or-later');
    refused('GPL-3.0', 'MIT');
  });
  it('reads OR as a choice and AND as every part', () => {
    ok('MIT OR GPL-3.0-or-later', 'MIT');
    ok('MIT AND Apache-2.0', 'MIT');
    refused('MIT AND GPL-3.0-or-later', 'MIT');
    ok('GPL-3.0-or-later', 'MIT OR GPL-3.0-or-later');
    refused('GPL-3.0-or-later', 'MIT AND GPL-3.0-or-later');
    ok('MIT', 'MIT AND GPL-3.0-or-later');
  });
  it('picks one target alternative for the whole component, not one per part', () => {
    refused('GPL-2.0-only AND Apache-2.0', 'GPL-2.0-only OR GPL-3.0-only');
    refused('GPL-2.0-only AND GPL-3.0-only', 'GPL-2.0-only OR GPL-3.0-only');
    refused('(GPL-2.0-only) AND (GPL-3.0-only)', 'GPL-2.0-only OR GPL-3.0-only');
    ok('GPL-3.0-only AND Apache-2.0', 'GPL-2.0-only OR GPL-3.0-only');
    ok('GPL-2.0-only AND MIT', 'GPL-2.0-only OR GPL-3.0-only');
    ok('GPL-2.0-only OR GPL-3.0-only', 'GPL-3.0-only');
    ok('(GPL-2.0-only AND MIT) OR Apache-2.0', 'GPL-3.0-only OR MIT');
  });
  it('refuses a missing, malformed, unknown or exception-bearing licence with a clear message', () => {
    refused(undefined, 'MIT', /component's licence: no licence is declared/);
    refused('MIT', undefined, /Piglet's licence: no licence is declared/);
    refused('UNLICENSED', 'MIT', /not a licence the guard can check/);
    refused('See license file', 'MIT', /not an SPDX licence expression/);
    refused('Some-Custom-1.0', 'MIT', /not in Pigpen's licence compatibility table/);
    refused('MIT', 'Some-Custom-1.0', /not in Pigpen's licence compatibility table/);
    refused('GPL-2.0-only WITH Classpath-exception-2.0', 'MIT', /carries an exception/);
  });
});

describe('the catalogue notice', () => {
  it('is absent for MIT and names the licence otherwise', () => {
    assert.equal(licenseNote('MIT'), undefined);
    assert.equal(licenseNote(undefined), undefined);
    assert.match(licenseNote('GPL-3.0-or-later'), /^Licence: GPL-3\.0-or-later \(copyleft, not MIT\)\. .*standalone, opt-in Package.*never bundles it/);
    assert.match(licenseNote('GPL-3.0-or-later', 'package'), /standalone, opt-in Package/);
    const piglet = licenseNote('GPL-3.0-or-later', 'piglet');
    assert.match(piglet, /^Licence: GPL-3\.0-or-later \(copyleft, not MIT\)\. Every Package this Piglet selects has to be licence-compatible with it/);
    assert.doesNotMatch(piglet, /standalone|opt-in|install it on its own/);
    assert.equal(licenseNote('Apache-2.0', 'piglet'), 'Licence: Apache-2.0.');
    assert.equal(licenseNote('Apache-2.0'), 'Licence: Apache-2.0.');
    assert.equal(licenseNote('MIT AND Apache-2.0'), 'Licence: MIT AND Apache-2.0.');
  });
});
