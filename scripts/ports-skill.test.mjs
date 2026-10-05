// The porting Skill takes one ports row as its input ("port row <id>") and keeps the row's status.
// These checks keep the Skill's instructions and scripts/ports.mjs in step.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const repo = join(dirname(fileURLToPath(import.meta.url)), '..');
const skill = readFileSync(join(repo, 'components/extension-port/skills/pigpen-pi-extension-port/SKILL.md'), 'utf8');
const readme = readFileSync(join(repo, 'components/extension-port/README.md'), 'utf8');
// The ports list (ports/, scripts/ports.mjs) lands from pigpen-ports-list; until it is merged these checks skip.
const present = existsSync(join(repo, 'scripts/ports.mjs'));
const skip = present ? false : 'scripts/ports.mjs is not in this tree (pigpen-ports-list)';
const usage = spawnSync(process.execPath, [join(repo, 'scripts/ports.mjs'), 'no-such-command'], { encoding: 'utf8' }).stderr;

test('the Skill documents `port row <id>`', { skip }, () => {
  assert.match(skill, /### Input: a ports row \(`port row <id>`\)/);
  assert.match(readme, /port row <id>/);
  for (const needle of ['ports/ports.json', 'upstream.commit', 'targetPackage', 'credit', 'ports/README.md']) assert.ok(skill.includes(needle), `Skill should mention ${needle}`);
});

test('every ports.mjs command the Skill tells the agent to run exists', { skip }, () => {
  const commands = new Set([...skill.matchAll(/node scripts\/ports\.mjs ([a-z-]+)/g)].map((m) => m[1]));
  for (const wanted of ['show', 'list', 'set-status', 'provenance']) assert.ok(commands.has(wanted), `Skill should use ${wanted}`);
  for (const command of commands) assert.ok(usage.includes(command), `ports.mjs has no "${command}" command`);
});

test('the Skill sets porting and review and never tells the agent to set done', { skip }, () => {
  assert.match(skill, /set-status <id> porting/);
  assert.match(skill, /Never set `done`/);
  assert.doesNotMatch(skill, /set-status <id> done/);
  assert.match(skill, /- \[ \] Run as `port row <id>`/);
});

test('the flow the Skill describes works end to end on a copy of the real list', { skip }, () => {
  const root = mkdtempSync(join(tmpdir(), 'pigpen-skill-'));
  try {
    cpSync(join(repo, 'ports'), join(root, 'ports'), { recursive: true });
    // Rows that are done must have their Package on disk (the gate checks); copy just those provenance records.
    for (const row of JSON.parse(readFileSync(join(root, 'ports/ports.json'), 'utf8')).ports.filter((p) => p.status === 'done')) {
      for (const target of [row.targetPackage, ...(row.otherTargets ?? [])]) mkdirSync(join(root, target), { recursive: true });
      if (existsSync(join(repo, row.targetPackage, 'provenance.json'))) cpSync(join(repo, row.targetPackage, 'provenance.json'), join(root, row.targetPackage, 'provenance.json'));
    }
    const run = (...args) => spawnSync(process.execPath, [join(repo, 'scripts/ports.mjs'), '--root', root, ...args], { encoding: 'utf8' });
    // The first queued row of the real list (rpiv-todo until it went to review; the flow does not depend on which).
    const first = JSON.parse(readFileSync(join(root, 'ports/ports.json'), 'utf8')).ports
      .filter((p) => p.status === 'queued' && p.priority).sort((a, b) => a.priority - b.priority)[0];
    assert.ok(first, 'the real list has a queued row');
    const shown = JSON.parse(run('show', first.id).stdout);
    assert.match(shown.upstream.commit, /^[a-f0-9]{40}$/);
    assert.equal(shown.status, 'queued');
    assert.equal(run('provenance', first.id).status, 0);
    assert.equal(run('set-status', first.id, 'porting', '--note', first.lane).status, 0);
    assert.equal(run('set-status', first.id, 'review').status, 0);
    assert.equal(run('check').status, 0, run('check').stderr);
    const rework = run('set-status', first.id, 'porting', '--note', 'x');
    assert.equal(rework.status, 0, 'review to porting is rework and is allowed');
    assert.equal(run('set-status', first.id, 'done').status, 1, 'porting to done skips review');
  } finally { rmSync(root, { recursive: true, force: true }); }
});
