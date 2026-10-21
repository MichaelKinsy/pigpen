// Drift check for the superpowers Skills Package: the shipped skills/ tree must be exactly what port/adapt.py
// makes from the vendored original (port/oracle), and the vendored original must be the pinned upstream commit.
// check({ root, upstream }) returns a list of problems (empty: no drift). `upstream` is optional: a git checkout
// of obra/superpowers; with it the vendored tree is compared byte for byte with `git archive <pinned commit>`.
import { execFileSync } from 'node:child_process';
import { existsSync, lstatSync, readFileSync, readdirSync } from 'node:fs';
import { join, relative, sep } from 'node:path';

const PREFIX = 'pigpen-superpowers-';

function files(directory) {
  const out = [];
  const visit = (dir) => {
    for (const name of readdirSync(dir).sort()) {
      const path = join(dir, name);
      const stat = lstatSync(path);
      if (stat.isDirectory()) visit(path);
      else out.push(relative(directory, path).split(sep).join('/'));
    }
  };
  if (existsSync(directory)) visit(directory);
  return out;
}

const executable = (path) => (lstatSync(path).mode & 0o111) !== 0;

/** What port/adapt.py makes of one oracle file: the Skill name prefix and the `superpowers:<skill>` references. */
export function adapt(name, rel, data, names) {
  if (!/\.(md|txt)$/.test(rel)) return { data, rename: 0, refs: 0 };
  let text = data.toString('utf8');
  let rename = 0;
  if (rel === 'SKILL.md') {
    const head = `---\nname: ${name}\n`;
    if (!text.startsWith(head)) throw new Error(`${name}/SKILL.md: frontmatter name is not ${name}`);
    text = `---\nname: ${PREFIX}${name}\n${text.slice(head.length)}`;
    rename = 1;
  }
  const alternatives = [...names].sort((a, b) => b.length - a.length || (a < b ? -1 : 1)).map((n) => n.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
  const ref = new RegExp(`superpowers:(${alternatives.join('|')})(?![a-z0-9-])`, 'g');
  let refs = 0;
  text = text.replace(ref, (_, n) => { refs += 1; return PREFIX + n; });
  return { data: Buffer.from(text, 'utf8'), rename, refs };
}

export function check({ root, upstream } = {}) {
  const component = join(root, 'components', 'superpowers');
  const oracle = join(component, 'port', 'oracle', 'skills');
  const shipped = join(component, 'skills');
  const problems = [];
  const provenance = JSON.parse(readFileSync(join(component, 'provenance.json'), 'utf8'));
  const pinned = provenance.upstreams[0].revision;

  const names = readdirSync(oracle).filter((n) => lstatSync(join(oracle, n)).isDirectory()).sort();
  const expectedFiles = new Set();
  const rows = [];
  let total = 0;
  for (const name of names) {
    let renamed = 0;
    const perFile = [];
    for (const rel of files(join(oracle, name))) {
      const source = join(oracle, name, rel);
      const target = join(shipped, PREFIX + name, rel);
      expectedFiles.add(`${PREFIX}${name}/${rel}`);
      const made = adapt(name, rel, readFileSync(source), names);
      renamed += made.rename;
      if (made.refs) { perFile.push(`${rel}: ${made.refs}`); total += made.refs; }
      if (!existsSync(target)) { problems.push(`${PREFIX}${name}/${rel}: missing (the original has ${name}/${rel})`); continue; }
      if (!readFileSync(target).equals(made.data)) problems.push(`${PREFIX}${name}/${rel}: differs from the original by more than the recorded adaptations`);
      if (executable(target) !== executable(source)) problems.push(`${PREFIX}${name}/${rel}: executable bit differs from the original`);
    }
    rows.push(`| \`${name}\` | \`${PREFIX}${name}\` | ${renamed ? 'changed' : '-'} | ${perFile.join('; ') || '-'} |`);
  }
  for (const rel of files(shipped)) if (!expectedFiles.has(rel)) problems.push(`${rel}: not in the original (nothing adds files to a Skill)`);

  // port/ADAPTATIONS.md is written by port/adapt.py: its table and total must be the ones computed here.
  const adaptations = readFileSync(join(component, 'port', 'ADAPTATIONS.md'), 'utf8');
  for (const row of rows) if (!adaptations.includes(`${row}\n`)) problems.push(`port/ADAPTATIONS.md: row out of date: ${row}`);
  if (!adaptations.includes(`\n${total} references rewritten in total.\n`)) problems.push(`port/ADAPTATIONS.md: the reference total is not ${total}`);
  if (!adaptations.includes(`\`${pinned}\``)) problems.push(`port/ADAPTATIONS.md: does not name the pinned commit ${pinned}`);
  const credits = readFileSync(join(component, 'CREDITS.md'), 'utf8');
  if (!credits.includes(pinned)) problems.push(`CREDITS.md: does not name the pinned commit ${pinned}`);
  if (!credits.includes(`the ${total} references`)) problems.push(`CREDITS.md: does not say ${total} references are rewritten`);

  if (upstream) {
    const wanted = new Map();
    const listing = execFileSync('git', ['-C', upstream, 'ls-tree', '-r', '--name-only', pinned, '--', 'skills', 'LICENSE', '.pi/extensions/superpowers.ts', 'tests/pi/test-pi-extension.mjs'], { encoding: 'utf8' });
    for (const path of listing.split('\n').filter(Boolean)) {
      wanted.set(path, execFileSync('git', ['-C', upstream, 'show', `${pinned}:${path}`], { maxBuffer: 1 << 28 }));
    }
    for (const [path, data] of wanted) {
      const local = path === 'LICENSE' ? join(component, 'port', 'oracle', 'LICENSE') : join(component, 'port', 'oracle', path);
      if (!existsSync(local)) problems.push(`port/oracle/${path}: missing (upstream ${pinned.slice(0, 7)} has it)`);
      else if (!readFileSync(local).equals(data)) problems.push(`port/oracle/${path}: differs from upstream ${pinned.slice(0, 7)}`);
    }
    for (const rel of files(join(component, 'port', 'oracle'))) {
      if (!wanted.has(rel)) problems.push(`port/oracle/${rel}: not in upstream ${pinned.slice(0, 7)}`);
    }
  }
  return problems;
}
