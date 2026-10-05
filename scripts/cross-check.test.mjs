import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { describe, it } from 'node:test';
import { devNull } from 'node:os';
import { fileURLToPath } from 'node:url';
import { goCommands } from './cross-check.mjs';

const script = fileURLToPath(new URL('./cross-check.mjs', import.meta.url));

describe('cross-check', () => {
  it('refuses an argument that is not an os/arch target before touching Go', () => {
    const result = spawnSync(process.execPath, [script, 'android'], { encoding: 'utf8' });
    assert.equal(result.status, 2);
    assert.match(result.stderr, /not an os\/arch target: android/);
  });

  it('discards what it builds: go build writes a lone main package into the module directory otherwise', () => {
    const [build, vet] = goCommands();
    assert.deepEqual(build, ['build', '-o', devNull, './...']);
    assert.deepEqual(vet, ['vet', './...']);
  });
});
