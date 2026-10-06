import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, describe, it } from 'node:test';
import { isolatedEnv } from './package-smoke.mjs';

describe('isolatedEnv', () => {
  const scratch = mkdtempSync(join(tmpdir(), 'pigpen-smoke-env-'));
  after(() => rmSync(scratch, { recursive: true, force: true }));

  it('isolates PiG state but shares Go\'s build and module caches with the caller', () => {
    const env = isolatedEnv(scratch, 'demo');
    assert.equal(env.HOME, join(scratch, 'home-demo'));
    assert.equal(env.PIG_HOME, join(scratch, 'home-demo'));
    // Under the isolated HOME, `go env` would point both caches inside it: every Package would rebuild and re-download
    // everything (the npm install checks ran past 30 minutes on CI that way).
    const [cache, mods] = execFileSync('go', ['env', 'GOCACHE', 'GOMODCACHE'], { encoding: 'utf8' }).trim().split('\n');
    assert.equal(env.GOCACHE, cache);
    assert.equal(env.GOMODCACHE, mods);
    const inChild = execFileSync('go', ['env', 'GOCACHE', 'GOMODCACHE'], { env, encoding: 'utf8' }).trim().split('\n');
    assert.deepEqual(inChild, [cache, mods]);
  });

  it('lets the caller override any of it', () => {
    assert.equal(isolatedEnv(scratch, 'demo', { GOCACHE: '/elsewhere' }).GOCACHE, '/elsewhere');
  });
});
