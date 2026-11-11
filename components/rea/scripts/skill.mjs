// REA's skill as the pig-rea Piglet ships it: the published `skills/reverse-engineer-anything` tree, byte for byte, except the
// frontmatter name, which Pigpen's skill rule prefixes (pigpen-reverse-engineer-anything). Used by payload.mjs
// (--sync-skill writes the copy; a payload build fails on drift) and by tests/faithful.test.mjs.
import { lstatSync, readdirSync, readFileSync } from 'node:fs';
import { join, relative, sep } from 'node:path';

export const upstreamSkill = 'reverse-engineer-anything';
export const shippedSkill = `pigpen-${upstreamSkill}`;

/** Relative slash paths of the regular files under dir, sorted. Anything else is an error. */
export function skillFiles(dir) {
  const out = [];
  const visit = (current) => {
    for (const name of readdirSync(current).sort()) {
      const path = join(current, name);
      const stat = lstatSync(path);
      if (stat.isDirectory()) visit(path);
      else if (stat.isFile()) out.push(relative(dir, path).split(sep).join('/'));
      else throw new Error(`${path}: not a regular file or directory`);
    }
  };
  visit(dir);
  return out.sort();
}

/** The shipped bytes of one file of the published skill. */
export function adaptSkillFile(rel, data) {
  if (rel !== 'SKILL.md') return data;
  const head = `---\nname: ${upstreamSkill}\n`;
  const text = data.toString('utf8');
  if (!text.startsWith(head)) throw new Error(`${upstreamSkill}/SKILL.md: frontmatter does not start with name: ${upstreamSkill}`);
  return Buffer.from(`---\nname: ${shippedSkill}\n${text.slice(head.length)}`);
}

/** The first difference between the shipped copy and what adaptSkillFile makes of the published skill, or undefined. */
export function skillDrift(published, shipped) {
  let want;
  let got;
  try {
    want = skillFiles(published);
    got = skillFiles(shipped);
  } catch (error) {
    return error.message;
  }
  if (want.join('\n') !== got.join('\n')) return `the file lists differ (published: ${want.length}, shipped: ${got.length})`;
  for (const rel of want) {
    if (!adaptSkillFile(rel, readFileSync(join(published, rel))).equals(readFileSync(join(shipped, rel)))) return `${rel} differs`;
  }
  return undefined;
}
