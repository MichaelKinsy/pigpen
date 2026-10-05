// Mutation check for pig-doctor's safety guards: `PIG_BIN=... node components/pig-doctor/mutations/run.mjs [id...]`.
// Each mutation (one or several edits) breaks one guard in a temporary copy of the extension module; the Go tests must then fail
// (a compile error does not count). The unmutated copy must pass first. Needs PIG_SDK_DIR or PIG_BIN.
import { spawnSync } from 'node:child_process';
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { sdkDir } from '../../../scripts/go-modules.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const source = join(here, '../extensions/pig-doctor');
const mutations = JSON.parse(readFileSync(join(here, 'mutations.json'), 'utf8'));
const only = new Set(process.argv.slice(2));
const sdk = sdkDir();

function runTests(dir) {
  const work = mkdtempSync(join(tmpdir(), 'pigdoctor-work-'));
  writeFileSync(join(work, 'go.work'), `go 1.26\n\nuse (\n\t${sdk}\n\t${dir}\n)\n`);
  const r = spawnSync('go', ['test', '-count=1', './...'], { cwd: dir, encoding: 'utf8', env: { ...process.env, GOWORK: join(work, 'go.work'), GOFLAGS: '' } });
  rmSync(work, { recursive: true, force: true });
  return { status: r.status, out: `${r.stdout}${r.stderr}` };
}

const scratch = mkdtempSync(join(tmpdir(), 'pigdoctor-mut-'));
let bad = 0;
try {
  const baseline = join(scratch, 'base');
  cpSync(source, baseline, { recursive: true });
  const base = runTests(baseline);
  if (base.status !== 0) { console.error(base.out); throw new Error('the unmutated module must pass its tests'); }
  console.log(`baseline: pass (${mutations.length} mutations)`);
  for (const m of mutations) {
    if (only.size && !only.has(m.id)) continue;
    const dir = join(scratch, m.id);
    cpSync(source, dir, { recursive: true });
    let invalid = false;
    for (const edit of m.edits || [m]) {
      const file = join(dir, edit.file);
      const text = readFileSync(file, 'utf8');
      const count = text.split(edit.find).length - 1;
      if (count !== 1) { console.log(`INVALID  ${m.id}: pattern occurs ${count} times in ${edit.file}`); invalid = true; break; }
      writeFileSync(file, text.replace(edit.find, () => edit.replace));
    }
    if (invalid) { bad++; continue; }
    const r = runTests(dir);
    const compile = /\[build failed\]|cannot use|undefined:|declared and not used|syntax error/.test(r.out) && !/--- FAIL/.test(r.out);
    if (r.status !== 0 && !compile) {
      const failing = [...new Set([...r.out.matchAll(/^--- FAIL: (\S+)/gm)].map((x) => x[1]))].slice(0, 3).join(', ') || 'panic';
      console.log(`caught   ${m.id}  (${failing})`);
    } else {
      console.log(`SURVIVED ${m.id}: ${m.why}${compile ? ' [mutant did not compile]' : ''}`);
      bad++;
    }
    rmSync(dir, { recursive: true, force: true });
  }
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
console.log(bad === 0 ? 'all mutations caught' : `${bad} mutation(s) not caught`);
process.exitCode = bad === 0 ? 0 : 1;
