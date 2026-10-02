// The helpers that make a scratch go.work and stage the SDK must not leave directories in TMPDIR:
// test:port used to leave one pigpen-sdk-* per mutate call, a pigpen-gowork-* and a pigeq-* per run.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';

const scratch = mkdtempSync(join(tmpdir(), 'go-modules-test-'));
const fakePig = join(scratch, 'pig');
// Reports the required version, and stages an "SDK" inside PIG_HOME as `pig reload --sdk-path` does.
const required = JSON.parse(readFileSync(new URL('./pig-requirement.json', import.meta.url), 'utf8')).pig;
writeFileSync(fakePig, `#!/bin/sh\nif [ "$1" = --version ]; then echo ${required}; exit 0; fi\nmkdir -p "$PIG_HOME/state/pigsdk/sdk"\necho "$PIG_HOME/state/pigsdk/sdk"\n`);
chmodSync(fakePig, 0o755);
const helpers = fileURLToPath(new URL('./go-modules.mjs', import.meta.url));

describe('go-modules scratch directories', () => {
  after(() => rmSync(scratch, { recursive: true, force: true }));
  it('leaves nothing in TMPDIR once the process exits', () => {
    const tmp = mkdtempSync(join(scratch, 'tmp-'));
    const program = `
      import { goWorkEnv, sdkDir, scratchDir } from ${JSON.stringify(helpers)};
      const env = goWorkEnv([], process.env);
      const a = sdkDir(process.env), b = sdkDir(process.env);
      const c = scratchDir('pigeq-');
      console.log(JSON.stringify({ work: env.GOWORK, a, b, c }));
    `;
    const r = spawnSync(process.execPath, ['--input-type=module', '-e', program], {
      encoding: 'utf8', env: { PATH: process.env.PATH, TMPDIR: tmp, PIG_BIN: fakePig },
    });
    assert.equal(r.status, 0, r.stderr);
    const seen = JSON.parse(r.stdout);
    assert.ok(seen.a.startsWith(tmp) && seen.b.startsWith(tmp), 'the staged SDK lives in TMPDIR while the process runs');
    assert.notEqual(seen.a, seen.b);
    assert.deepEqual(readdirSync(tmp), [], 'every scratch directory is removed at exit');
  });
});
