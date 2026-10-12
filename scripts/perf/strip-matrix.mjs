// Size and build time of a Piglet Binary against the same Piglet with built-ins stripped (PiG's `strip`, D92), and of the empty
// base: PiG's own parts with a Piglet baked in, nothing stripped / everything a Binary can compile out stripped / keep mode.
//
//   PIG_BIN=/path/to/pig-0.5 PIG_SOURCE_ROOT=/path/to/pig-git-checkout node scripts/perf/strip-matrix.mjs [--piglet herdr] [--out dir]
//
// Needs a PiG that has `strip` (0.5 and later); pig 0.4.1 rejects the field. A Piglet with an extension must also build with that
// PiG's SDK (see docs/plan/progress/piglets-pigpen.md: several modules need the 0.5 `ConfigHome` signature first). Startup and idle
// of the Binaries come from `node scripts/perf/measure.mjs startup|idle name=path ...`. Output: a Markdown table and
// <out>/strip-matrix.json. Build seconds are what the host's Go build cache gave: the first build of a tag set pays the compile.
import { spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import YAML from 'yaml';
import { root, scratchDir } from '../go-modules.mjs';
import { hostTarget } from '../host-target.mjs';

/** Every built-in a Binary can compile out (PiG docs/site/docs/piglets.md "Strip built-ins": `stripped(binary)`). */
export const STRIP_ALL = {
  extensions: ['codemode', 'llama.cpp', 'mcp', 'pig-login', 'tool-search'],
  apis: ['bedrock-converse-stream', 'google-vertex', 'mistral-conversations'],
  features: ['experimental-server', 'node-extensions', 'extension-sdk-go', 'extension-sdk-rust', 'extension-sdk-python', 'syntax-highlight', 'word-dictionaries', 'mermaid', 'export-html', 'self-update', 'changelog', 'docs', 'piglet-builder'],
};

/** Keep mode: four tools and nothing else. */
export const KEEP_CORE = { keep: { tools: ['read', 'bash', 'edit', 'write'], extensions: [], apis: [], features: [] } };

/**
 * One variant of a manifest: fused realization, a new name, and the strip block (`none`, `all`, `keep`). `without` lists
 * feature IDs to leave out of `all`: a Piglet cannot strip a runtime its own extensions need (a fused Go extension is
 * built in, but a Go source extension needs extension-sdk-go).
 */
export function variantManifest(text, name, mode, { without = [] } = {}) {
  const doc = YAML.parse(text) ?? {};
  doc.name = name;
  doc.build = { ...(doc.build ?? {}), extensionRealization: 'fused' };
  delete doc.strip;
  if (mode === 'all') doc.strip = { ...STRIP_ALL, features: STRIP_ALL.features.filter((id) => !without.includes(id)) };
  else if (mode === 'keep') doc.strip = structuredClone(KEEP_CORE);
  else if (mode !== 'none') throw new Error(`unknown strip mode ${mode}`);
  return YAML.stringify(doc);
}

const mib = (bytes) => `${(bytes / 1048576).toFixed(1)} MiB`;

/** The Markdown table of the rows, with the size and time of each against the first row. */
export function table(rows) {
  const first = rows[0];
  const lines = ['| Binary | Size | vs first | Build |', '| --- | ---: | ---: | ---: |'];
  for (const row of rows) {
    const delta = row === first ? '-' : `${(((row.bytes - first.bytes) / first.bytes) * 100).toFixed(0)}%`;
    lines.push(`| ${row.name} | ${mib(row.bytes)} (${row.bytes} B) | ${delta} | ${row.seconds}s |`);
  }
  return `${lines.join('\n')}\n`;
}

function build(pig, manifest, out, host, env) {
  const started = Date.now();
  rmSync(out, { force: true });
  const result = spawnSync(pig, ['piglet', 'build', manifest, '--format', 'binary', '--builder', 'native', '--targets', host.target, '--out', out], { cwd: root, env, encoding: 'utf8', maxBuffer: 64 << 20 });
  if (result.status !== 0) throw new Error(`pig piglet build ${manifest} failed:\n${(result.stderr || result.stdout).trim().split('\n').slice(-20).join('\n')}`);
  return { bytes: statSync(out).size, seconds: Math.round((Date.now() - started) / 100) / 10 };
}

function main() {
  const args = process.argv.slice(2);
  const value = (flag, fallback) => { const i = args.indexOf(flag); return i === -1 ? fallback : args[i + 1]; };
  const pig = process.env.PIG_BIN;
  if (!pig) throw new Error('PIG_BIN is not set: point it at a PiG 0.5 or later');
  if (!process.env.PIG_SOURCE_ROOT) throw new Error('PIG_SOURCE_ROOT is not set: a native Binary build needs a git checkout of the PiG source');
  const host = hostTarget();
  const out = resolve(value('--out', join(root, 'dist/perf/strip')));
  const piglet = value('--piglet', 'herdr');
  mkdirSync(out, { recursive: true });
  const work = scratchDir('pigpen-strip-');
  const home = join(work, 'home');
  const env = { ...process.env, PIG_HOME: join(home, 'pig'), PIG_CODING_AGENT_DIR: join(home, 'agent'), PI_CODING_AGENT_DIR: join(home, 'pi') };

  // The Piglet's staged tree (npm run stage) carries its Packages; a variant is a copy of it with the manifest rewritten.
  const staged = join(root, 'dist/staged/piglets', piglet);
  const manifestText = readFileSync(join(staged, 'piglet.yaml'), 'utf8');
  const emptyBase = 'description: "Empty base."\nrelease:\n  version: 0.1.0\n';
  const variants = [
    { name: 'empty base', file: 'empty-none', text: variantManifest(emptyBase, 'empty-none', 'none') },
    { name: 'empty base, strip all', file: 'empty-all', text: variantManifest(emptyBase, 'empty-all', 'all') },
    { name: 'empty base, keep core', file: 'empty-keep', text: variantManifest(emptyBase, 'empty-keep', 'keep') },
  ];
  const rows = [];
  for (const variant of variants) {
    const dir = join(work, variant.file);
    mkdirSync(dir);
    writeFileSync(join(dir, 'piglet.yaml'), variant.text);
    rows.push({ name: variant.name, ...build(pig, join(dir, 'piglet.yaml'), join(out, variant.file), host, env) });
  }
  for (const mode of ['none', 'all']) {
    const dir = join(work, `${piglet}-${mode}`);
    cpSync(staged, dir, { recursive: true });
    writeFileSync(join(dir, 'piglet.yaml'), variantManifest(manifestText, `${piglet}-${mode}`, mode));
    rows.push({ name: `${piglet}${mode === 'all' ? ', strip all' : ''}`, ...build(pig, join(dir, 'piglet.yaml'), join(out, `${piglet}-${mode}`), host, env) });
  }
  writeFileSync(join(out, 'strip-matrix.json'), `${JSON.stringify({ pig, target: host.target, rows }, null, 2)}\n`);
  process.stdout.write(table(rows));
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  try { main(); } catch (error) { console.error(`strip-matrix: ${error.message}`); process.exitCode = 1; }
}
