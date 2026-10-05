#!/usr/bin/env node
// Mutation check for the acp port (the adapter has no scenarios, so `pigeq mutate` has nothing to
// replay; see PORT.md). Every entry of mutations.json is one deliberate defect. It is applied to a
// copy of the module, the module's Go tests run (the end-to-end scenarios need PIG_ACP_E2E_PIG and
// are skipped here), and the mutant must be killed by a failing test. A mutant that does not build is
// INVALID and fails the run; an unmutated baseline that is not green refuses the whole run.
//
// A mutant marked "equivalent" (with the reason) changes no observable behavior and is reported, not failed.
//
//   node components/acp/port/mutate.mjs [--only name] [--jobs N] [--sdk DIR]
//
// Each entry: { name, module: "cmd" | "ext", file, find, replace, why? }; find must occur exactly once.
import { spawn, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const modules = {
  ext: resolve(here, '../extensions/acp'),
  cmd: resolve(here, '../extensions/acp/cmd/pig-acp'),
};
const args = process.argv.slice(2);
const opt = (n, d) => (args.includes(n) ? args[args.indexOf(n) + 1] : d);
const only = opt('--only');
const jobs = Number(opt('--jobs', '4'));
const sdk = opt('--sdk', process.env.PIG_SDK_DIR || '');

const list = JSON.parse(readFileSync(join(here, 'mutations.json'), 'utf8'));
const names = new Set();
for (const m of list) {
  if (!m.name || !m.file || !m.find || m.find === m.replace || !modules[m.module]) throw new Error(`bad mutation ${JSON.stringify(m.name)}`);
  if (names.has(m.name)) throw new Error(`duplicate mutation ${m.name}`);
  names.add(m.name);
}

function copyModule(kind) {
  const parent = mkdtempSync(join(tmpdir(), 'acp-mut-'));
  const dir = join(parent, kind === 'ext' ? 'acp' : 'pig-acp');
  // The extension module contains the companion module beneath it; the copy of the extension leaves it out.
  cpSync(modules[kind], dir, { recursive: true, filter: (p) => !(kind === 'ext' && p.includes('/cmd/')) && !p.includes('/testdata/') });
  if (kind === 'cmd') cpSync(join(modules.cmd, 'testdata'), join(dir, 'testdata'), { recursive: true });
  return { parent, dir };
}

function goTest(kind, dir, parent) {
  const env = { ...process.env, GOFLAGS: '' };
  if (kind === 'ext') {
    if (!sdk) throw new Error('the extension module needs --sdk <PiG staged SDK dir> (or PIG_SDK_DIR)');
    const work = join(parent, 'go.work');
    writeFileSync(work, `go 1.26\n\nuse (\n\t${sdk}\n\t${dir}\n)\n`);
    env.GOWORK = work;
  } else {
    env.GOWORK = 'off';
  }
  return new Promise((done) => {
    const p = spawn('go', ['test', '-count=1', '-timeout', '120s', './...'], { cwd: dir, env });
    let out = '';
    p.stdout.on('data', (d) => (out += d));
    p.stderr.on('data', (d) => (out += d));
    p.on('close', (code) => done({ code, out }));
  });
}

const build = (out) => /\[build failed\]|\[setup failed\]|cannot find|undefined:|syntax error|declared and not used|imported and not used/.test(out);

async function baseline(kind) {
  const { parent, dir } = copyModule(kind);
  const r = await goTest(kind, dir, parent);
  rmSync(parent, { recursive: true, force: true });
  if (r.code !== 0) throw new Error(`the unmutated ${kind} module does not pass its tests, so no mutant can be judged:\n${r.out.split('\n').slice(0, 15).join('\n')}`);
}

async function runOne(m) {
  const { parent, dir } = copyModule(m.module);
  try {
    const target = join(dir, m.file);
    if (!existsSync(target)) return { m, status: 'INVALID', detail: `no file ${m.file}` };
    const src = readFileSync(target, 'utf8');
    const n = src.split(m.find).length - 1;
    if (n !== 1) return { m, status: 'INVALID', detail: `find text occurs ${n} times in ${m.file}, want exactly 1` };
    writeFileSync(target, src.replace(m.find, () => m.replace));
    const r = await goTest(m.module, dir, parent);
    if (r.code === 0) return { m, status: m.equivalent ? 'EQUIVALENT' : 'SURVIVED', detail: m.equivalent || '' };
    if (build(r.out)) return { m, status: 'INVALID', detail: r.out.split('\n').slice(0, 4).join(' | ') };
    const fail = r.out.split('\n').find((l) => l.startsWith('--- FAIL') || l.startsWith('panic:') || l.startsWith('FAIL')) || 'failed';
    return { m, status: 'KILLED', detail: fail.trim() };
  } finally {
    rmSync(parent, { recursive: true, force: true });
  }
}

const todo = list.filter((m) => !only || m.name === only);
for (const kind of new Set(todo.map((m) => m.module))) await baseline(kind);
const results = [];
let next = 0;
await Promise.all(
  Array.from({ length: Math.min(jobs, todo.length) }, async () => {
    while (next < todo.length) {
      const m = todo[next++];
      const r = await runOne(m);
      results.push(r);
      console.log(`${r.status.padEnd(8)} ${m.name}${r.detail ? `  (${r.detail.slice(0, 110)})` : ''}`);
    }
  }),
);
const count = (s) => results.filter((r) => r.status === s).length;
console.log(`\n${count('KILLED')} killed, ${count('EQUIVALENT')} equivalent, ${count('SURVIVED')} survived, ${count('INVALID')} invalid of ${results.length}`);
process.exit(count('SURVIVED') || count('INVALID') ? 1 : 0);
