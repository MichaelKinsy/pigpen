// Builds one Piglet Binary per component extension (one extension each, the same origin and tool scope the
// shipping Piglet gives it) so an extension's startup, memory and idle cost can be measured on its own against
// plain pig. Output: dist/perf/bin/<extension> (or <dir>/bin with --out). Needs PIG_BIN and PIG_SOURCE_ROOT like
// `npm run build:piglet`. pig runs with a scratch PiG home: a build records receipts and keeps a copy of every Binary
// under PIG_HOME, which would otherwise be the user's ~/.pig.
//   node scripts/perf/build-singles.mjs [--out dir] [extension ...]
import { spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { root, scratchDir } from '../go-modules.mjs';
import { requirePig } from '../pig-bin.mjs';

// extension name -> { packages it needs (alias = component directory), origin package (or `local` source dir), tools scope }
export const SINGLES = {
  herdr: { packages: ['herdr'], origin: 'herdr', tools: [] },
  pigrunner: { packages: ['pig-play', 'pig-runner'], origin: 'pig-runner', tools: [] },
  angrypigs: { packages: ['pig-play', 'angry-pigs'], origin: 'angry-pigs', tools: [] },
  'pig-snake': { packages: ['pig-play', 'pig-snake'], origin: 'pig-snake', tools: [] },
  'session-ingest': { packages: ['session-ingest'], origin: 'session-ingest' },
  'context-info': { packages: ['context-info'], origin: 'context-info' },
  'pig-doctor': { packages: ['pig-doctor'], origin: 'pig-doctor', tools: [] },
  jev: { packages: ['typesafe', 'jev'], origin: 'jev', tools: ['jev_ask'] },
  a2a: { packages: ['a2a'], origin: 'a2a' },
  websearch: { packages: ['websearch'], origin: 'websearch' },
  warden: { packages: ['typesafe', 'warden'], origin: 'warden', tools: [] },
  'pi-typesafe': { packages: ['pi-typesafe', 'pi-typesafe-api', 'typesafe'], origin: 'pi-typesafe' },
  acp: { packages: ['acp'], origin: 'acp', tools: [] },
  'extension-equivalence': { packages: ['extension-equivalence'], origin: 'extension-equivalence' },
  // The control: an extension that registers nothing (the build-pipeline fixture). Whatever it costs is PiG's cost of
  // hosting any extension at all, so an extension's own cost is its delta over this row.
  'seed-check': { local: 'piglets/pig-with-batteries/extensions/seed-check', origin: null, tools: [] },
  // Packages no Piglet selects; measured so every extension in components/ has a row.
  'dirty-repo-guard': { packages: ['dirty-repo-guard'], origin: 'dirty-repo-guard' },
  ahp: { packages: ['ahp'], origin: 'ahp' },
};

const args = process.argv.slice(2);
const outAt = args.indexOf('--out');
const out = outAt >= 0 ? resolve(args.splice(outAt, 2)[1]) : join(root, 'dist/perf');
const names = args.length ? args : Object.keys(SINGLES);
const pig = requirePig();
const pigHome = scratchDir('pigpen-perf-pighome-');
const pigEnv = { ...process.env, PIG_HOME: pigHome, PIG_CODING_AGENT_DIR: join(pigHome, 'agent') };
mkdirSync(join(out, 'bin'), { recursive: true });
for (const name of names) {
  const s = SINGLES[name];
  if (!s) throw new Error(`unknown extension ${name}; known: ${Object.keys(SINGLES).join(', ')}`);
  const dir = join(out, 'piglets', `perf-${name}`);
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(join(dir, 'packages'), { recursive: true });
  for (const pkg of s.packages ?? []) cpSync(join(root, 'components', pkg), join(dir, 'packages', pkg), { recursive: true, filter: (p) => !/node_modules|\/\.git(\/|$)/.test(p) });
  const lines = [`name: perf-${name}`, 'description: "one extension, for perf measurement"', 'release:', '  version: 0.1.0', 'build:', '  targets: [linux/amd64]', ...(s.packages ? ['packages:', ...s.packages.map((p) => `  ${p}: local:./packages/${p}`)] : []),
    'extensions:', `  - name: ${name}`, `    origins: [${s.local ? 'local:./extensions/seed-check' : `package:${s.origin}`}]`];
  if (s.local) cpSync(join(root, s.local), join(dir, 'extensions/seed-check'), { recursive: true });
  if (s.tools) lines.push(`    tools: [${s.tools.join(', ')}]`);
  lines.push('skills: []', 'discovery:', '  extensions: []', '  skills: []');
  writeFileSync(join(dir, 'piglet.yaml'), `${lines.join('\n')}\n`);
  const target = join(out, 'bin', name);
  rmSync(target, { force: true });
  const r = spawnSync(pig, ['piglet', 'build', join(dir, 'piglet.yaml'), '--format', 'binary', '--out', target, '--targets', 'linux/amd64'], { cwd: root, env: pigEnv, stdio: ['ignore', 'ignore', 'inherit'] });
  console.log(`${name}: ${r.status === 0 ? 'built' : `FAILED (${r.status})`}`);
}
