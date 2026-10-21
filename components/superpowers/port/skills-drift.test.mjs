import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { chmodSync, cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { check } from './skills-drift.mjs';

const realRoot = fileURLToPath(new URL('../../../', import.meta.url));
const PREFIX = 'pigpen-superpowers-';

function sandbox() {
  const root = mkdtempSync(join(tmpdir(), 'sp-drift-'));
  mkdirSync(join(root, 'components'), { recursive: true });
  cpSync(join(realRoot, 'components', 'superpowers'), join(root, 'components', 'superpowers'), { recursive: true });
  return { root, component: join(root, 'components', 'superpowers'), done: () => rmSync(root, { recursive: true, force: true }) };
}

function drift(mutate, options = {}) {
  const box = sandbox();
  try {
    const extra = mutate(box) ?? {};
    return check({ root: box.root, ...extra, ...options });
  } finally { box.done(); }
}

test('the shipped tree is the original plus the recorded adaptations', () => {
  assert.deepEqual(drift(() => {}), []);
});

test('an edit to a shipped Skill that no rule records is drift', () => {
  const problems = drift(({ component }) => {
    const file = join(component, 'skills', `${PREFIX}writing-plans`, 'SKILL.md');
    writeFileSync(file, `${readFileSync(file, 'utf8')}\nExtra line.\n`);
  });
  assert.equal(problems.length, 1);
  assert.match(problems[0], /writing-plans\/SKILL\.md: differs from the original/);
});

test('an unrewritten plugin reference is drift', () => {
  const problems = drift(({ component }) => {
    const file = join(component, 'skills', `${PREFIX}executing-plans`, 'SKILL.md');
    writeFileSync(file, readFileSync(file, 'utf8').replace(`${PREFIX}writing-plans`, 'superpowers:writing-plans'));
  });
  assert.match(problems.join('\n'), /executing-plans\/SKILL\.md: differs/);
});

test('a missing, an extra and a mode-changed file are drift', () => {
  const problems = drift(({ component }) => {
    rmSync(join(component, 'skills', `${PREFIX}brainstorming`, 'scripts', 'stop-server.sh'));
    writeFileSync(join(component, 'skills', `${PREFIX}brainstorming`, 'NOTES.md'), 'x');
    chmodSync(join(component, 'skills', `${PREFIX}executing-plans`, 'scripts', 'task-done'), 0o644);
  });
  assert.match(problems.join('\n'), /brainstorming\/scripts\/stop-server\.sh: missing/);
  assert.match(problems.join('\n'), /brainstorming\/NOTES\.md: not in the original/);
  assert.match(problems.join('\n'), /executing-plans\/scripts\/task-done: executable bit differs/);
});

test('a changed original without a regenerated tree is drift', () => {
  const problems = drift(({ component }) => {
    const file = join(component, 'port', 'oracle', 'skills', 'test-driven-development', 'SKILL.md');
    writeFileSync(file, `${readFileSync(file, 'utf8')}\nAn upstream line.\n`);
  });
  assert.match(problems.join('\n'), /test-driven-development\/SKILL\.md: differs/);
});

test('a stale ADAPTATIONS.md or CREDITS.md is drift', () => {
  const problems = drift(({ component }) => {
    const file = join(component, 'port', 'ADAPTATIONS.md');
    writeFileSync(file, readFileSync(file, 'utf8').replace(/\n\d+ references rewritten in total\.\n/, '\n7 references rewritten in total.\n'));
    const credits = join(component, 'CREDITS.md');
    writeFileSync(credits, readFileSync(credits, 'utf8').replace(/bb92a77741419a4ab5f06e711a283343f1ada0c3/g, 'x'));
  });
  assert.match(problems.join('\n'), /ADAPTATIONS\.md: the reference total/);
  assert.match(problems.join('\n'), /CREDITS\.md: does not name the pinned commit/);
});

test('the vendored original is compared with the pinned upstream commit', () => {
  const box = sandbox();
  try {
    const repo = join(box.root, 'upstream');
    mkdirSync(repo);
    cpSync(join(box.component, 'port', 'oracle', 'skills'), join(repo, 'skills'), { recursive: true });
    cpSync(join(box.component, 'port', 'oracle', 'LICENSE'), join(repo, 'LICENSE'));
    // The bootstrap extension and its test are vendored from the same commit, so the sandbox upstream carries them too.
    for (const dir of ['.pi', 'tests']) cpSync(join(box.component, 'port', 'oracle', dir), join(repo, dir), { recursive: true });
    const git = (...args) => execFileSync('git', ['-C', repo, '-c', 'user.name=t', '-c', 'user.email=t@example.invalid', '-c', 'commit.gpgsign=false', ...args], { encoding: 'utf8' }).trim();
    git('init', '-q');
    git('add', '-A');
    git('commit', '-q', '-m', 'upstream');
    const commit = git('rev-parse', 'HEAD');
    const old = JSON.parse(readFileSync(join(box.component, 'provenance.json'), 'utf8')).upstreams[0].revision;
    for (const file of ['provenance.json', join('port', 'ADAPTATIONS.md'), 'CREDITS.md']) {
      writeFileSync(join(box.component, file), readFileSync(join(box.component, file), 'utf8').replaceAll(old, commit));
    }
    assert.deepEqual(check({ root: box.root, upstream: repo }), []);
    const changed = join(box.component, 'port', 'oracle', 'skills', 'using-superpowers', 'SKILL.md');
    writeFileSync(changed, `${readFileSync(changed, 'utf8')}x`);
    writeFileSync(join(box.component, 'port', 'oracle', 'skills', 'using-superpowers', 'EXTRA.md'), 'x');
    const problems = check({ root: box.root, upstream: repo }).join('\n');
    assert.match(problems, /port\/oracle\/skills\/using-superpowers\/SKILL\.md: differs from upstream/);
    assert.match(problems, /port\/oracle\/skills\/using-superpowers\/EXTRA\.md: not in upstream/);
  } finally { box.done(); }
});
