// The pig these scripts run is chosen by PIG_BIN and checked against scripts/pig-requirement.json, so a
// stale `pig` on PATH (a Mac's ~/.local/bin/pig) can never stand in for the pig Pigpen targets.
import assert from 'node:assert/strict';
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, describe, it } from 'node:test';
import { parsePigVersion, pigVersionMatches, requirePig, requiredPigVersion } from './pig-bin.mjs';
import { goCaches } from './go-modules.mjs';

const scratch = mkdtempSync(join(tmpdir(), 'pig-bin-test-'));
after(() => rmSync(scratch, { recursive: true, force: true }));
let n = 0;
/** A fake pig that prints `output` for `version` and exits `status`; for `--version` it prints Pi's version, as PiG 0.8.0 does. */
function fakePig(output, status = 0) {
  const file = join(scratch, `pig-${n++}`);
  writeFileSync(file, `#!/bin/sh\nif [ "$1" = --version ]; then echo 1.5.2; exit 0; fi\nprintf '%s\\n' '${output}'\nexit ${status}\n`);
  chmodSync(file, 0o755);
  return file;
}
const [major, minor, patch] = parsePigVersion(requiredPigVersion());

describe('pig version', () => {
  it('reads the first x.y.z of the first line and ignores what follows', () => {
    assert.deepEqual(parsePigVersion('pig: 0.8.0\nupstream pi: 1.5.2\ngo: go1.27.4'), [0, 8, 0]);
    assert.deepEqual(parsePigVersion('0.3.1+0.99.2'), [0, 3, 1]);
    assert.deepEqual(parsePigVersion('pig 10.20.30 (abc)\nsecond 1.2.3'), [10, 20, 30]);
    assert.equal(parsePigVersion('dev'), undefined);
    assert.equal(parsePigVersion(''), undefined);
  });
  it('matches the same major.minor at the required patch or later, and nothing else', () => {
    assert.ok(pigVersionMatches([0, 3, 1], [0, 3, 1]));
    assert.ok(pigVersionMatches([0, 3, 4], [0, 3, 1]));
    assert.ok(!pigVersionMatches([0, 3, 0], [0, 3, 1]), 'an older patch');
    assert.ok(!pigVersionMatches([0, 2, 9], [0, 3, 1]), 'an older minor');
    assert.ok(!pigVersionMatches([0, 4, 0], [0, 3, 1]), 'a newer 0.x minor is a different release');
    assert.ok(!pigVersionMatches([1, 3, 1], [0, 3, 1]), 'another major');
  });
});

describe('requirePig', () => {
  it('needs PIG_BIN and never falls back to a pig on PATH', () => {
    assert.throws(() => requirePig({ PATH: process.env.PATH }), /PIG_BIN is not set.*never falls back to the pig on PATH/s);
  });
  it('accepts a pig at the required version, read from `pig version` and not from --version', () => {
    const pig = fakePig(`pig: ${major}.${minor}.${patch}\nupstream pi: 1.5.2`);
    assert.equal(requirePig({ PATH: process.env.PATH, PIG_BIN: pig }), pig);
  });
  it('rejects an older pig and names what it is, what is needed and how to get it', () => {
    const pig = fakePig(`pig: ${major}.${minor - 1}.0`);
    assert.throws(() => requirePig({ PATH: process.env.PATH, PIG_BIN: pig }), (error) => {
      assert.ok(error.message.includes(`PIG_BIN=${pig} is pig ${major}.${minor - 1}.0`), error.message);
      assert.ok(error.message.includes(`>= ${requiredPigVersion()}`), error.message);
      assert.ok(error.message.includes('go build -o'), error.message);
      return true;
    });
  });
  it('rejects a newer minor, a missing binary and output that is not a version', () => {
    assert.throws(() => requirePig({ PATH: process.env.PATH, PIG_BIN: fakePig(`pig: ${major}.${minor + 1}.0`) }), /is pig/);
    assert.throws(() => requirePig({ PATH: process.env.PATH, PIG_BIN: join(scratch, 'missing') }), /cannot be run/);
    assert.throws(() => requirePig({ PATH: process.env.PATH, PIG_BIN: fakePig('not a version') }), /did not report a version/);
    assert.throws(() => requirePig({ PATH: process.env.PATH, PIG_BIN: fakePig(`pig: ${major}.${minor}.${patch}`, 3) }), /did not report a version/);
  });
});

describe('goCaches', () => {
  it('names both Go caches, for a pane whose tmux server did not inherit them', () => {
    const caches = goCaches();
    assert.ok(caches.GOCACHE && caches.GOMODCACHE, JSON.stringify(caches));
  });
});
