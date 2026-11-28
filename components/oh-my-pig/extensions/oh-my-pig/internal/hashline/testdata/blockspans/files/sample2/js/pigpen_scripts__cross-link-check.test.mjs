import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { buildEnv, classify, targetsFor } from './cross-link-check.mjs';

const script = fileURLToPath(new URL('./cross-link-check.mjs', import.meta.url));

const LINKED = 'Compiling and linking Go binary (0s) — pig-games → /tmp/x\nChecksumming and verifying binary (35s) — /tmp/x\n';

describe('cross-link-check', () => {
  it('counts a target whose Binary linked but could not run on this host as linked', () => {
    const result = classify({ status: 1, stdout: '', stderr: `${LINKED}builder native failed after execution started; no fallback was attempted: basic artifact verification failed: fork/exec /tmp/x: exec format error: \n` }, 'windows/amd64');
    assert.deepEqual(result, { ok: true, level: 'linked', detail: 'compiled and linked; the host cannot run a windows/amd64 Binary' });
  });

  it('counts a build that also ran as built', () => {
    assert.equal(classify({ status: 0, stdout: LINKED, stderr: '' }, 'linux/arm64').level, 'built');
  });

  it('fails when the compiler or linker failed, even if the output mentions the exec error later', () => {
    const result = classify({ status: 1, stdout: '', stderr: 'Compiling and linking Go binary failed (4s): pig-games\n# example.com/x\nundefined: syscall.Foo\nexec format error\n' }, 'darwin/arm64');
    assert.equal(result.ok, false);
    assert.match(result.detail, /undefined: syscall\.Foo/);
  });

  it('fails a verification error that is not a wrong-platform Binary', () => {
    const result = classify({ status: 1, stdout: '', stderr: `${LINKED}basic artifact verification failed: --version exited 2\n` }, 'darwin/amd64');
    assert.equal(result.ok, false);
  });

  it('fails when the build stopped before linking', () => {
    assert.equal(classify({ status: 1, stdout: '', stderr: 'Selecting builder failed (0s): native\nexec format error\n' }, 'windows/arm64').ok, false);
  });

  it('builds with the target in the Go environment, a host target for pig, and no cgo', () => {
    const env = buildEnv({ PATH: '/bin', GOOS: 'linux', CGO_ENABLED: '1' }, 'windows/amd64', '/tmp/h');
    assert.equal(env.GOOS, 'windows');
    assert.equal(env.GOARCH, 'amd64');
    assert.equal(env.CGO_ENABLED, '0');
    assert.equal(env.PIG_HOME, '/tmp/h/pig');
  });

  it('defaults to every runner target except the host and refuses a malformed one', () => {
    const targets = targetsFor([], { goos: 'linux', goarch: 'amd64', target: 'linux/amd64' });
    assert.deepEqual(targets, ['linux/arm64', 'darwin/arm64', 'darwin/amd64', 'windows/amd64']);
    assert.throws(() => targetsFor(['windows'], { target: 'linux/amd64' }), /not an os\/arch target: windows/);
    assert.throws(() => targetsFor(['linux/amd64'], { target: 'linux/amd64' }), /host target/);
  });

  it('refuses a malformed target before touching pig', () => {
    const result = spawnSync(process.execPath, [script, '--targets', 'android'], { encoding: 'utf8' });
    assert.equal(result.status, 2);
    assert.match(result.stderr, /not an os\/arch target: android/);
  });
});
