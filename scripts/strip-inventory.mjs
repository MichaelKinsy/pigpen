#!/usr/bin/env node
// Proves a Piglet's strip list removes only what it names. A Piglet Binary is built twice from the same staged
// manifest, once as written (its `strip:` list) and once with the list removed (the stock Piglet), and both are driven
// through the same probes: the --help text, the slash commands (RPC get_commands) with an explicit extension loaded,
// and one probe for each strippable built-in this script knows. The inventories must differ in the probes of the
// stripped IDs and nowhere else, and each stripped ID's probe must show the difference (the strip took effect).
//
//   node scripts/strip-inventory.mjs <piglet> [--out dir] [--keep]
//
// A strip ID without a probe is refused: the list is only valid where its removal is proved.
import { spawnSync } from 'node:child_process';
import { statSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { isMap, parseDocument } from 'yaml';
import { root } from './go-modules.mjs';
import { hostTarget } from './host-target.mjs';
import { requirePig } from './pig-bin.mjs';
import { build, smokeEnv, stagedPiglets } from './platform-smoke.mjs';

const RUN_LIMIT_MS = 60_000;
const HELLO_TS = 'export default function (pi) { pi.registerCommand("strip-probe-hello", { description: "probe", handler: async () => {} }); }\n';

/** The strip IDs this script proves, each with the one probe that must differ when the ID is stripped. */
export const PROBES = {
  'node-extensions': { run: ({ dir }) => ({ args: ['--mode', 'rpc', '--no-session', '-e', join(dir, 'hello.ts')], stdin: '{"id":"c","type":"get_commands"}\n', pick: commandNames }) },
  'extension-sdk-go': { run: ({ dir }) => ({ args: ['extension', 'init', join(dir, 'go'), '--name', 'go', '--lang', 'go'] }) },
  'extension-sdk-rust': { run: ({ dir }) => ({ args: ['extension', 'init', join(dir, 'rust'), '--name', 'rust', '--lang', 'rust'] }) },
  'extension-sdk-python': { run: ({ dir }) => ({ args: ['extension', 'init', join(dir, 'python'), '--name', 'python', '--lang', 'python'] }) },
  'self-update': { run: () => ({ args: ['update', '--self'] }) },
  docs: { run: () => ({ args: ['docs'] }) },
  'piglet-builder': { run: () => ({ args: ['piglet', 'build', '--help'] }) },
};

function commandNames(stdout) {
  for (const line of stdout.split('\n')) {
    try {
      const reply = JSON.parse(line);
      if (reply.command === 'get_commands') return (reply.data?.commands ?? []).map((c) => `${c.name} [${c.source}]`).sort().join('\n');
    } catch { /* not a reply line */ }
  }
  return `no get_commands reply: ${stdout.slice(0, 200)}`;
}

/** Output with this run's scratch directory and the Binary's own path made comparable. */
export const normalize = (text, dir, bin) => text.replaceAll(dir, '<tmp>').replaceAll(bin, '<bin>').replace(/\r\n/g, '\n').trim();

/** Run one probe on a Binary: { exit, output }. */
function runProbe(bin, probe) {
  const dir = mkdtempSync(join(tmpdir(), 'pigpen-inv-'));
  try {
    for (const sub of ['tmp', 'work']) mkdirSync(join(dir, sub), { recursive: true });
    writeFileSync(join(dir, 'hello.ts'), HELLO_TS);
    const { args, stdin, pick } = probe.run({ dir });
    const result = spawnSync(bin, args, { cwd: join(dir, 'work'), env: smokeEnv(dir), input: stdin, encoding: 'utf8', timeout: RUN_LIMIT_MS });
    const stdout = result.stdout ?? '';
    const text = pick ? pick(stdout) : `${stdout}\n${result.stderr ?? ''}`;
    return { exit: result.error ? `error: ${result.error.code}` : result.status, output: normalize(text, dir, bin) };
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

/** The inventory of one Binary: --help, the slash commands with an extension loaded, and every probe. */
export function inventoryOf(bin) {
  const inventory = {
    help: runProbe(bin, { run: () => ({ args: ['--help'] }) }),
    commands: runProbe(bin, { run: () => ({ args: ['--mode', 'rpc', '--no-session'], stdin: '{"id":"c","type":"get_commands"}\n', pick: commandNames }) }),
  };
  for (const [id, probe] of Object.entries(PROBES)) inventory[id] = runProbe(bin, probe);
  return inventory;
}

/** What the strip list must and must not change. Returns the problems, empty when the strip removed exactly its IDs. */
export function compareInventories(stock, stripped, stripIds) {
  const problems = [];
  for (const id of stripIds) if (!(id in PROBES)) problems.push(`strip ID ${id} has no probe: this script cannot prove its removal`);
  const same = (a, b) => a.exit === b.exit && a.output === b.output;
  for (const id of stripIds) {
    if (id in PROBES && same(stock[id], stripped[id])) problems.push(`${id} is in the strip list but its probe answers the same in the stripped Binary`);
  }
  for (const id of Object.keys(PROBES)) {
    if (!stripIds.includes(id) && !same(stock[id], stripped[id])) problems.push(`${id} is not stripped but its probe changed:\n  stock:    ${JSON.stringify(stock[id])}\n  stripped: ${JSON.stringify(stripped[id])}`);
  }
  const commandsChanged = !same(stock.commands, stripped.commands);
  if (commandsChanged && !stripIds.includes('node-extensions')) problems.push(`the slash commands changed:\n  stock:    ${stock.commands.output}\n  stripped: ${stripped.commands.output}`);
  return problems;
}

/** The strip IDs a staged manifest names or, in keep mode, implies: features, extensions and apis as bare IDs for the probes. */
export function stripIdsOf(manifestText) {
  const strip = parseDocument(manifestText).get('strip')?.toJSON?.() ?? {};
  const named = [...(strip.features ?? []), ...(strip.extensions ?? []), ...(strip.apis ?? [])];
  // Keep mode strips every feature the building core has except the kept ones; the features this script can prove are the probed ones.
  const kept = strip.keep?.features;
  const implied = Array.isArray(kept) ? Object.keys(PROBES).filter((id) => !kept.includes(id) && !named.includes(id)) : [];
  return [...named, ...implied];
}

/** Write the stock variant of a staged manifest (its strip list removed) next to it, which keeps its Package paths valid. */
export function writeStockManifest(manifestPath) {
  const document = parseDocument(readFileSync(manifestPath, 'utf8'));
  document.delete('strip');
  const stock = manifestPath.replace(/piglet\.yaml$/, 'piglet.stock.yaml');
  writeFileSync(stock, document.toString());
  return stock;
}

function main(argv) {
  const [name, ...rest] = argv;
  if (!name || name.startsWith('-')) { console.error('usage: node scripts/strip-inventory.mjs <piglet> [--out dir] [--keep]'); return 2; }
  const outIndex = rest.indexOf('--out');
  const out = resolve(outIndex >= 0 ? rest[outIndex + 1] : mkdtempSync(join(tmpdir(), 'pigpen-strip-')));
  const staged = join(root, 'dist/staged/piglets', name, 'piglet.yaml');
  if (!stagedPiglets().includes(name)) { console.error(`no staged Piglet ${name}; run npm run stage`); return 2; }
  const ids = stripIdsOf(readFileSync(staged, 'utf8'));
  if (!ids.length) { console.log(`${name}: no strip list, nothing to prove`); return 0; }
  const pig = requirePig();
  const host = hostTarget();
  mkdirSync(out, { recursive: true });
  const stockManifest = writeStockManifest(staged);
  try {
    const stripped = build(pig, name, join(out, 'stripped'), host, staged);
    const stock = build(pig, name, join(out, 'stock'), host, stockManifest);
    for (const [label, result] of [['stripped', stripped], ['stock', stock]]) if (result.error) { console.error(`${name} ${label} build: ${result.error}`); return 1; }
    const size = (target) => `${(statSync(target).size / 1048576).toFixed(1)} MiB`;
    console.log(`${name}: stock ${size(stock.target)}, stripped ${size(stripped.target)}`);
    const problems = compareInventories(inventoryOf(stock.target), inventoryOf(stripped.target), ids);
    for (const problem of problems) console.error(`${name}: ${problem}`);
    console.log(`${name}: strip [${ids.join(', ')}] ${problems.length ? 'FAILED' : 'removes exactly its IDs'}`);
    return problems.length ? 1 : 0;
  } finally {
    rmSync(stockManifest, { force: true });
    if (!rest.includes('--keep') && outIndex < 0) rmSync(out, { recursive: true, force: true });
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) process.exit(main(process.argv.slice(2)));
