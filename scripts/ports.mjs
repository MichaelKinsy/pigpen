// The Pigpen ports list. ports/ports.json is the dispatch list (one row per port or move; schema in
// ports/ports.schema.json). ports/README.md is generated from it. This script validates the list,
// renders the README, and reads and updates rows for the porting Skill ("port row <id>").
//
//   node scripts/ports.mjs [--root DIR] <command>
//     check                          validate the list, the tree cross-checks and the README (default; npm run quality)
//     generate                       rewrite ports/README.md (npm run generate)
//     show <id>                      one row as JSON: the input of `port row <id>`
//     list [--status S]              id, status, target and lane, tab separated
//     set-status <id> <status> [--note TEXT]   move a row one stage and regenerate the README
//     provenance <id>                a provenance.json skeleton for the row's Package
//     verify-pins                    fetch every pinned commit from its upstream (needs network; not run in CI)
import { spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import Ajv from 'ajv';
import addFormats from 'ajv-formats';

const defaultRoot = fileURLToPath(new URL('../', import.meta.url));
export const STATUSES = ['queued', 'porting', 'review', 'done'];
// queued -> porting when a lane starts; porting -> review when the port's checklist is complete;
// review -> done when a reviewer accepts it. Rework and abandon go back one stage.
const NEXT = { queued: ['porting'], porting: ['queued', 'review'], review: ['done', 'porting'], done: ['review'] };

const sameUrl = (a, b) => a.toLowerCase().replace(/\.git$/, '').replace(/\/+$/, '') === b.toLowerCase().replace(/\.git$/, '').replace(/\/+$/, '');
const upstreamsOf = (row) => [...(row.upstream ? [row.upstream] : []), ...(row.additionalUpstreams ?? [])];
const short = (sha) => sha.slice(0, 12);

function schemaValidator(schema) {
  const ajv = new Ajv({ allErrors: true, strict: false });
  addFormats(ajv);
  return ajv.compile(schema);
}
let defaultValidator;
const readJson = (path) => JSON.parse(readFileSync(path, 'utf8'));

export function validatePorts(data, { root, schema } = {}) {
  const validate = schema ? schemaValidator(schema) : (defaultValidator ??= schemaValidator(readJson(join(defaultRoot, 'ports/ports.schema.json'))));
  if (!validate(data)) return validate.errors.map((e) => `${e.instancePath || '/'} ${e.message}${e.params?.additionalProperty ? ` (${e.params.additionalProperty})` : ''}`);
  const errors = [];
  const ids = new Map();
  const targets = new Map();
  for (const row of data.ports) {
    if (ids.has(row.id)) errors.push(`duplicate id "${row.id}"`);
    ids.set(row.id, row);
    for (const target of [row.targetPackage, ...(row.otherTargets ?? [])]) {
      if (targets.has(target) && targets.get(target) !== row.id) errors.push(`target "${target}" is owned by both ${targets.get(target)} and ${row.id}`);
      targets.set(target, row.id);
    }
    const upstreams = upstreamsOf(row);
    if (row.kind !== 'original' && !row.upstream) errors.push(`${row.id}: a ${row.kind} row needs an upstream`);
    for (const u of upstreams) {
      try {
        const url = new URL(u.url);
        if (url.protocol !== 'https:' || url.username || url.password) errors.push(`${row.id}: upstream url must be HTTPS without credentials`);
      } catch { errors.push(`${row.id}: upstream url is not a URL: ${u.url}`); }
      for (const value of [u.name, u.url, u.author]) if (!row.credit.includes(value)) errors.push(`${row.id}: credit must include "${value}"`);
    }
    if (row.priority !== undefined && row.status !== 'queued') errors.push(`${row.id}: priority is only for queued rows`);
    if (root) errors.push(...crossCheck(row, upstreams, root));
  }
  return errors;
}

// The tree is the evidence: a Package that exists must carry the pin and license the list states.
function crossCheck(row, upstreams, root) {
  const errors = [];
  const all = [row.targetPackage, ...(row.otherTargets ?? [])];
  if (row.status === 'done') {
    for (const target of all) if (!existsSync(join(root, target))) errors.push(`${row.id}: status is done but target ${target} does not exist`);
    if (existsSync(join(root, row.targetPackage)) && !existsSync(join(root, row.targetPackage, 'provenance.json'))) errors.push(`${row.id}: status is done but ${row.targetPackage}/provenance.json is missing`);
  }
  const path = join(root, row.targetPackage, 'provenance.json');
  if (!existsSync(path)) return errors;
  const provenance = readJson(path);
  const recorded = [...(provenance.upstreams ?? []), ...(provenance.dependencies ?? [])];
  // A port or a move ships the upstream's code, so its Package must record that upstream. (An adapter
  // credits its SDK in the row and CREDITS.md; the provenance schema has no slot for "built on" yet.)
  if ((row.kind === 'port' || row.kind === 'move') && row.upstream && !recorded.some((p) => sameUrl(p.url, row.upstream.url))) {
    errors.push(`${row.id}: ${row.targetPackage}/provenance.json does not record the row's upstream ${row.upstream.url}`);
  }
  for (const p of recorded) {
    const match = upstreams.find((u) => sameUrl(u.url, p.url));
    if (!match) { errors.push(`${row.id}: provenance upstream ${p.url} is not in the row`); continue; }
    if (p.revision !== match.commit) errors.push(`${row.id}: provenance pins ${p.name} at ${short(p.revision)}; row pins ${short(match.commit)}`);
    if (p.license !== match.license) errors.push(`${row.id}: provenance license "${p.license}" for ${p.name} but row says "${match.license}"`);
  }
  return errors;
}

const esc = (text) => String(text).replaceAll('|', '\\|').replaceAll('\n', ' ');
const cell = (rows) => rows.join('<br>');

export function renderReadme(data) {
  const counts = Object.fromEntries(STATUSES.map((s) => [s, data.ports.filter((p) => p.status === s).length]));
  const lines = [
    '# Pigpen ports',
    '',
    '<!-- Generated by `npm run generate` from ports.json. Do not edit by hand. -->',
    '',
    'The dispatch list for every Pigpen port: one row per port or move, the upstream it comes from pinned to a commit,',
    'where it ships and who is doing it. Generated from [`ports.json`](ports.json) (schema:',
    '[`ports.schema.json`](ports.schema.json)); `npm run quality` validates it. Edit `ports.json`, then run `npm run generate`.',
    '',
    `Status: ${STATUSES.map((s) => `${s}: ${counts[s]}`).join(' · ')}`,
    '',
    '| ID | Upstream | Pinned commit | License | Upstream author | Target Package | Lane | Status | Credit |',
    '|---|---|---|---|---|---|---|---|---|',
  ];
  for (const p of data.ports) {
    const ups = upstreamsOf(p);
    const target = cell([`\`${p.targetPackage}\``, ...(p.otherTargets ?? []).map((t) => `\`${t}\``)]);
    lines.push(`| \`${p.id}\` | ${ups.length ? cell(ups.map((u) => `[${esc(u.name)}](${u.url})`)) : '—'} | ${
      ups.length ? cell(ups.map((u) => `[\`${short(u.commit)}\`](${u.url.replace(/\/+$/, '')}/commit/${u.commit})`)) : '—'} | ${
      ups.length ? cell(ups.map((u) => esc(u.license))) : 'MIT (Pigpen)'} | ${
      ups.length ? cell(ups.map((u) => esc(u.author))) : 'Michael Kinsy'} | ${target} | \`${p.lane}\` | ${p.status} | ${esc(p.credit)} |`);
  }
  lines.push('', '## How to read it', '',
    '- **Status** `queued` (approved, no lane has started), `porting` (a lane is working), `review` (its completion checklist is met and it awaits a reviewer), `done` (accepted).',
    '  A row moves one stage at a time; `node scripts/ports.mjs set-status <id> <status>` updates `ports.json` and this file together.',
    '- **Pinned commit** is the upstream revision the port is measured against. A Package that exists must carry the same pin and license in its `provenance.json`; the gate checks it.',
    '  A queued row is pinned when it is listed (the commit of the latest npm release when the registry records one, else the default branch head); the lane confirms or re-pins before it starts.',
    '- **Kind** `port` translates an upstream program; `adapter` is original code on an upstream SDK or protocol; `move` brings existing code in from another repository; `original` is new work.',
    '- **Lane** is the port lane (the port branch `<lane>`); for a queued row it is the name the lane will take.',
    '- To port a row, give the [porting Skill](../components/extension-port/skills/pigpen-pi-extension-port/SKILL.md) its id: `port row <id>`.',
    '');
  const queued = data.ports.filter((p) => p.status === 'queued' && p.priority !== undefined).sort((a, b) => a.priority - b.priority);
  lines.push('## Queue order', '');
  if (queued.length) queued.forEach((p, i) => lines.push(`${i + 1}. \`${p.id}\`: ${esc(p.summary)}`));
  else lines.push('Nothing is queued with a priority.');
  lines.push('', '## Details', '');
  for (const p of data.ports) {
    lines.push(`### \`${p.id}\`: ${esc(p.name)}`, '', `- Kind: ${p.kind}. ${esc(p.summary)}`);
    for (const u of upstreamsOf(p).filter((x) => x.path)) lines.push(`- Upstream path: \`${u.path}\` in ${esc(u.name)}`);
    if (p.notes) lines.push(`- Notes: ${esc(p.notes)}`);
    lines.push('');
  }
  return lines.join('\n').replace(/\n+$/, '\n');
}

export function setStatus(data, id, status, note) {
  if (!STATUSES.includes(status)) throw new Error(`unknown status "${status}" (use ${STATUSES.join(', ')})`);
  const index = data.ports.findIndex((p) => p.id === id);
  if (index < 0) throw new Error(`no port row "${id}"`);
  const next = structuredClone(data);
  const row = next.ports[index];
  if (row.status !== status) {
    if (!NEXT[row.status].includes(status)) throw new Error(`cannot move "${id}" from ${row.status} to ${status} (allowed: ${NEXT[row.status].join(', ')})`);
    row.status = status;
    delete row.priority;
  }
  if (note) {
    // After a full sentence, add a sentence; otherwise continue the list with "; ".
    if (!row.notes) row.notes = note;
    else if (/[.!?]$/.test(row.notes)) row.notes = `${row.notes} ${/[.!?]$/.test(note) ? note : `${note}.`}`;
    else row.notes = `${row.notes}; ${note}`;
  }
  return next;
}

export function provenanceSkeleton(row) {
  const upstreams = upstreamsOf(row).map((u) => ({
    name: u.name, authors: [u.author], url: u.url, revision: u.commit, path: 'port/oracle', license: u.license,
    licenseFile: 'port/oracle/LICENSE', attributionFile: 'CREDITS.md',
  }));
  return { origin: upstreams.length ? 'ported' : 'original', authors: ['Michael Kinsy'], license: 'MIT', licenseFile: 'LICENSE', upstreams };
}

export function checkPorts(root = defaultRoot) {
  const file = join(root, 'ports/ports.json');
  const schemaFile = join(root, 'ports/ports.schema.json');
  if (!existsSync(file)) return { errors: ['ports/ports.json is missing'] };
  if (!existsSync(schemaFile)) return { errors: ['ports/ports.schema.json is missing'] };
  let data;
  try { data = readJson(file); } catch (e) { return { errors: [`ports/ports.json: ${e.message}`] }; }
  const errors = validatePorts(data, { root, schema: readJson(schemaFile) });
  if (errors.length) return { errors, data };
  const readme = join(root, 'ports/README.md');
  if (!existsSync(readme)) errors.push('ports/README.md is missing: run npm run generate');
  else if (readFileSync(readme, 'utf8') !== renderReadme(data)) errors.push('ports/README.md is stale: run npm run generate');
  return { errors, data };
}

function load(root) {
  const { errors, data } = checkPorts(root);
  if (errors.some((e) => !/README\.md is (stale|missing)/.test(e))) throw new Error(errors.join('\n'));
  return data;
}
const rowOf = (data, id) => {
  const row = data.ports.find((p) => p.id === id);
  if (!row) throw new Error(`no port row "${id}"`);
  return row;
};
const save = (root, data) => {
  writeFileSync(join(root, 'ports/ports.json'), JSON.stringify(data, null, 2) + '\n');
  writeFileSync(join(root, 'ports/README.md'), renderReadme(data));
};

// The upstream URLs come from a reviewed file, but a malicious row could still point at any HTTPS host:
// never offer it a stored credential, an askpass program or an interactive login.
export function pinFetchArgs(dir, url, commit) {
  const env = { ...process.env, GIT_TERMINAL_PROMPT: '0', GCM_INTERACTIVE: 'never' };
  delete env.GIT_ASKPASS;
  delete env.SSH_ASKPASS;
  return { args: ['-c', 'credential.helper=', '-c', 'core.askPass=', '-C', dir, 'fetch', '-q', '--depth=1', url, commit], env };
}

function verifyPins(data) {
  let bad = 0;
  const seen = new Set();
  const dir = mkdtempSync(join(tmpdir(), 'pigpen-pins-'));
  try {
    spawnSync('git', ['init', '-q', dir], { env: pinFetchArgs(dir, '', '').env });
    for (const row of data.ports) for (const u of upstreamsOf(row)) {
      const key = `${u.url}@${u.commit}`;
      if (seen.has(key)) continue;
      seen.add(key);
      const { args, env } = pinFetchArgs(dir, u.url, u.commit);
      const r = spawnSync('git', args, { env, encoding: 'utf8', timeout: 120000 });
      if (r.status === 0) console.log(`ok   ${row.id}: ${u.name} ${short(u.commit)}`);
      else { bad++; console.error(`FAIL ${row.id}: ${u.name} ${u.url} ${short(u.commit)}: ${(r.stderr || 'timeout').trim().split('\n').at(-1)}`); }
    }
  } finally { rmSync(dir, { recursive: true, force: true }); }
  return bad ? 1 : 0;
}

function main(argv) {
  const args = [...argv];
  const opt = (name) => { const i = args.indexOf(name); if (i < 0) return undefined; const [, value] = args.splice(i, 2); return value; };
  const root = resolve(opt('--root') ?? defaultRoot);
  const note = opt('--note');
  const only = opt('--status');
  const [command = 'check', ...rest] = args;
  const usage = (message) => { console.error(`${message}\nusage: node scripts/ports.mjs [--root DIR] check|generate|show <id>|list [--status S]|set-status <id> <status> [--note TEXT]|provenance <id>|verify-pins`); return 2; };
  try {
    switch (command) {
      case 'check': {
        const { errors, data } = checkPorts(root);
        if (errors.length) { console.error(errors.join('\n')); return 1; }
        console.log(`Ports list passed: ${data.ports.length} ports (${STATUSES.map((s) => `${s} ${data.ports.filter((p) => p.status === s).length}`).join(', ')})`);
        return 0;
      }
      case 'generate': {
        const data = load(root);
        writeFileSync(join(root, 'ports/README.md'), renderReadme(data));
        console.log(`Wrote ports/README.md (${data.ports.length} ports)`);
        return 0;
      }
      case 'show': {
        if (!rest[0]) return usage('show needs an id');
        console.log(JSON.stringify(rowOf(load(root), rest[0]), null, 2));
        return 0;
      }
      case 'list': {
        for (const p of load(root).ports) if (!only || p.status === only) console.log([p.id, p.status, p.targetPackage, p.lane].join('\t'));
        return 0;
      }
      case 'set-status': {
        if (rest.length !== 2) return usage('set-status needs an id and a status');
        const data = setStatus(load(root), rest[0], rest[1], note);
        data.updatedAt = new Date().toISOString().slice(0, 10);
        const errors = validatePorts(data, { root });
        if (errors.length) throw new Error(errors.join('\n'));
        save(root, data);
        console.log(`${rest[0]}: ${rowOf(data, rest[0]).status}`);
        return 0;
      }
      case 'provenance': {
        if (!rest[0]) return usage('provenance needs an id');
        console.log(JSON.stringify(provenanceSkeleton(rowOf(load(root), rest[0])), null, 2));
        return 0;
      }
      case 'verify-pins': return verifyPins(load(root));
      default: return usage(`unknown command "${command}"`);
    }
  } catch (e) {
    console.error(e.message);
    return 1;
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) process.exitCode = main(process.argv.slice(2));
