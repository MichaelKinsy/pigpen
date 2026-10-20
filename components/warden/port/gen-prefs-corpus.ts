// Regenerates extensions/warden/testdata/prefs_corpus.json from the vendored ORIGINAL (port/oracle).
// Run from port/oracle after `npm ci`: node --import tsx --import ./tests/setup-env.ts ../gen-prefs-corpus.ts
// The corpus is user messages and clauses run through the original's prefs.ts detectors; prefs_differential_test.go
// runs the same inputs through the Go port. RE2 has no lookahead, lookbehind or backreference, so the rewritten
// patterns (isHumanTyped's paired tag, STANDING/PERMISSION/IMPERATIVE tails, TEMPORARY, PRONOUN_OBJECT,
// SPECIFIC_REFERENCE's hash, sentence splitting) are exactly what this guards.
import { writeFileSync } from "node:fs";
import { classifyClause, extractPermissions, extractPreferences, isHumanTyped, tokens } from "./oracle/src/prefs.ts";

const base = [
  "don't open pull requests", "Don't commit or stage it", "never force-push the release branch", "always run the linter before you commit", "stop adding emoji to the changelog",
  "stop going to the store", "stop doing that", "stop sing", "stop ing", "from now on write commit messages in the imperative mood", "next time, ask before you delete a branch",
  "from now on would use it", "from now on  would use it", "from now on too would use it", "from now on, too would use it", "from now on too, would use it", "next time  will do", "next time is fine",
  "next time too", "from now on: always sign tags", "I told you to keep the tests offline", "I said don't touch the schema", "I said always lint", "I told you stop going there", "i told you i did",
  "why don't you run the linter first?", "do you always run the full suite?", "don't know why the build broke", "i don't like how the panel looks", "don't worry about the flaky test",
  "the watcher never closes the file handle", "it never printed a summary", "don't merge", "don't merge the migration yet", "don't touch the schema now", "don't push until the checks pass",
  "never squash commits in this PR", "don't rebase this branch onto main", "never rename the helper for this", "never use X for this project", "don't use tabs for this repo", "for this project always use pnpm",
  "from now on  would deploy staging servers", "from now on too would deploy staging servers", "from now on, too would deploy staging servers", "from now on too, would deploy staging servers", "from now on  too  would deploy staging servers",
  "next time  will rebase onto develop", "next time:  should squash merges", "next time  is great, rebase branches", "from now on\u00a0\u00a0were rebasing branches", "from now on\u00a0\u00a0\u2028were rebasing branches",
  "do  not touch the lockfile", "do\u00a0\u00a0not touch the lockfile", "do  use pnpm here for installs", "do  you use pnpm here", "do   we use pnpm here", "do\u2003\u2003it with pnpm", "please do  use pnpm for installs", "pls do  not touch the lockfile",
  "stop  going to staging", "stop\u00a0\u00a0sing", "stop singing loudly", "stop   making noise", "stop  it", "stop thing", "stop doing", "i told you stop going to staging", "I said  stop making noise",
  "I told you  to use pnpm", "i said, don't edit generated files", "i told you:  never edit generated files", "I told you to", "i said to run tests",
  "never  let it run", "never let\u00a0it run", "never  let   things run", "never let   this go", "don't  let it run", "stop letting it run", "never LET\tit run",
  "don't use this", "don't use that.", "don't use that, ok", "don't use that again", "don't use that anymore", "don't use that please", "don't use that yourself", "don't use that here", "don't use that back", "don't use that to x", "don't use that in x", "don't use that on x", "don't use that into x", "don't use that for x", "don't use that with x", "don't use that or x", "don't use that and x", "don't use that\u00a0\u00a0again", "don't use thatx",
  "never touch these", "never touch those!", "never touch those;", "never touch those,x", "never touch it", "never touch it.", "never touch its", "never touch item", "never touch them", "never touch themselves", "never touch themes",
  "Never revert abc-123", "never revert AB-1", "never revert A-1", "never revert ABCDEFGHIJK-1", "never revert A1-12 by hand", "close PR #4", "never close pull  request 12", "never close issue12", "never close issue #12", "never close ticket 5", "never close MR 5x",
  "never rebase on branch  feat/x", "never rebase on branch 'feat/x'", "never rebase on branch `x/y`", "never rebase on branch x", "never use fix/the-bug", "never use release/1.0", "never use features/x", "never use hotfix/",
  "never use a1b2c3d", "never use a1b2c3", "never use 1234567890abcdef", "never use ABCDEF12", "never use abcdefg1", "never use deadbee", "never use 1234567", "never use 0123456789abcdef0123456789abcdef01234567", "never use 0123456789abcdef0123456789abcdef012345678", "never use a1b2c3d-x",
  "a - b - never use c", "a–b never use c", "a—never use c", "ok—never use c", ", never use c", ":never use c", ";never use c", "-never use c", "and never use c", "but never use c", "then never use c", "andnever use c",
  "Next Time, Never Do That", "FROM NOW ON NEVER USE TABS", "Don’t use tabs", "DON’T use tabs", "Dont’ use tabs", "do not   use tabs", "never.use tabs", "never_use tabs",
  "don't skip tests", "never skip", "please skip the tests", "turning off warden", "turn   off the guard", "mute warden", "muting the hooks", "silence the lint", "ignore review", "bypass ci", "omit tests", "disable hooks", "without asking", "without a review", "without  approval", "without reviewing", "--no-verify", "git commit --no-verify",
  "don't ever need to review", "never bother to run tests", "never even use ci", "don't have to ask", "never need to confirm", "don't wait for tests", "don't waiting for reviews", "do not do any tests", "never add a lint step", "never write the checks", "never call verification", "never request reviews", "never use typecheck", "never use warden",
  "never check", "never check it", "never check the code", "never checks", "never checked", "never checking", "never verify", "never verifies", "never review pull  requests", "never test", "never test anything", "never test again", "never tests it", "never test builds", "never testing",
  "don't bump the version for now", "don't deploy today", "never mind the tests", "do not now on", "now on never push", "it is now on fire so never rerun it", "now  on do not push",
  "do use subagents here", "do not use subagents", "do you know", "do it now please", "do the thing", "do  not use it", "do  use it", "do that again", "do we use it", "do I", "do a barrel roll", "do some tests",
  "feel free to push", "you can skip it", "you may now merge", "you can now", "it's fine to push", "it’s ok to do that", "please do use subagents", "pls do commit", "ok do merge it", "and do merge it",
  "Always skip the tests before pushing", "Don't run the tests", "Don't run the live tests", "Never ask before pushing", "Never ask for a review", "Don't wait for the review", "Don't review the diff", "Don't check the output",
  "Never verify before pushing", "Always commit with --no-verify", "Always disable warden in this repo", "Always push without asking", "Always delete old branches without confirmation", "Stop testing it", "Don't ever run the tests",
  "Never check in secrets", "Never test in production", "Never skip the tests", "Always run the checks before a commit", "Dont run subagents do the review yourself", "never check it", "never checks", "don't tested",
  "Never revert ABC-123 by hand", "Don't close PR 42 before the demo", "Never rebase on branch feat/new-login", "Don't cherry-pick 3f9c2ab1 onto main", "Never close #17 by hand", "don't use deadbeef", "don't use 1234567", "don't use abcdefa",
  "don't use 3f9c2ab1deadbeef3f9c2ab1deadbeef3f9c2ab1x", "don't use ABCDEF1", "don't use abc1234_", "Never do that again", "Dont commit it back though", "Don't make it amber anymore", "Don't touch them", "don't let it spawn subagents",
  "never let it run", "never LET it run", "Never let this go", "don't do this", "don't do that here", "don't do this file", "don't do this in prod", "don't do these", "never touch those please", "don't commit or stage this",
  "never mention it to them and it", "don't touch it or them", "never stage it", "always keep replies short", "Always use Firefox for the vendor site", "ok never paste the api_key=abcd1234efgh5678 value", "always " + "write a long careful note ".repeat(20),
  "please never force-push", "pls don't force-push main", "just never push to main", "so always sign commits", "then stop adding tests that do nothing", "also don't forget the changelog entry", "btw never amend pushed commits",
  "never amend pushed commits; always rebase before merge", "yes proceed. but do not bump the version in this commit", "looks fine. never force-push the release branch", "ok, never squash merges",
  "Always — never mind", "foo – don't use bar", "a, don't use b", "a: never touch c", "x - always use y", "don’t use tabs", "dont use tabs", "do not use tabs", "Do NOT use tabs", "NEVER use tabs", "STOP adding noise to the logs",
  "I need you to never push to main", "we should never push to main", "you should always sign", "Please don't open pull requests, leave the branch local.", "never push\nalways pull", "never push. always pull", "never push;always pull",
  "never push!  always pull?", "why. never push", "what? never push", "how do I never push", "can you never push", "will you always sign", "is it always signed", "are tags always signed",
  "# Order\n\nNever push to main.", "<skill name=\"x\">\nAlways lint.\n</skill>", "<a>never push</A>", "<a>x</ab>never push to main", "<ab>never push to main</a>", "<tag attr=\"x\">hello</tag> never push main", "<x never push", "< a>never push</ a>",
  "STATUS REPORT   FROM: worker-1\nNever push to main.", "SCHEDULE · 10:00 · check the queue\nNever push to main.", "never push to main. " + "x".repeat(2100), "never push to main", "From Lead agent:\n\nNever push to main.",
  "Heads up from the Planner: never push to main", "Added by the owner: never push to main", "Owner change: never push to main", "owner x: never push to main", "from now on: never push to main", "Note: never push to main",
  "PROJECT: docs\nBRANCH: tidy\nnever push to main", "PROJECT: docs\nnever push to main", "Tidy up.\nPROJECT: docs\nBRANCH: tidy-readme\nNever push to main.", "\u00a0 never push to main\u00a0", "\ufeffnever push to main", "never\u00a0push\u00a0to\u00a0main",
  "never\u2003push to main", "never  push   to  main", "never\tpush to main", "never push to main\r\nalways pull from origin", "never push to main\ralways pull from origin", "never push to main\u2028always pull from origin", "never push to main\u2029always pull",
  "İstanbul: never push to main", "never push ßtatus", "always ßhip it", "always ﬁx it", "never ŉx it", "always \ud83d\ude00 smile", "always ǰ jump",
  "next time  too, ask", "from now on\tshould do it", "from now on  too  will do", "next time:  would do it", "next time, too: ask first", "from now on too:", "from now on,", "next time ",
  "always now on do it", "stop it now", "never now on", "don't do it for this project", "don't do it for this PR", "don't do it for this", "do it for this project only", "never for this repository", "never for this codebase", "never for this   repo", "for this  project never x y",
  "yet", "don't yet", "don't go yet", "untill", "until", "don't do it until later", "today don't do it", "this time never", "at the moment don't push", "this branch never push", "this PR never push",
];
const wrap = (c: string) => [c, `${c}.`, `${c}!`, `ok, ${c}`, `${c}. thanks`, `${c} \n and more text`, `thanks. ${c}`, `  ${c}  `, `${c.toUpperCase()}`, `${c} ${c}`];
const seen = new Set<string>();
const messages: string[] = [];
const add = (c: string) => { if (!seen.has(c)) { seen.add(c); messages.push(c); } };
for (const b of base) for (const c of wrap(b)) add(c);
let seed = 20260930;
const rnd = () => { seed = (seed * 1664525 + 1013904223) % 4294967296; return seed / 4294967296; };
const seps = [". ", "; ", ", ", "\n", " and ", " - ", ": ", "! ", "? ", "  ", " but ", " so ", " then "];
for (let k = 0; k < 2500; k++) {
  const parts = Array.from({ length: 1 + Math.floor(rnd() * 3) }, () => base[Math.floor(rnd() * base.length)]!);
  let c = parts[0]!;
  for (let p = 1; p < parts.length; p++) c += seps[Math.floor(rnd() * seps.length)] + parts[p];
  add(c);
}
const items = messages.map(text => {
  const clauses = extractPreferences(text);
  const permissions = extractPermissions(text);
  return { text, human: isHumanTyped(text), clauses, permissions, classes: [...clauses, ...permissions].map(c => classifyClause(c)), tokens: [...clauses, ...permissions].map(c => tokens(c)) };
});
// The clause-level functions also run on the raw message texts: classify and tokenize what a lesson or a candidate could be.
const raws = messages.slice(0, 2500).map(text => ({ text, cls: classifyClause(text), tokens: tokens(text) }));
writeFileSync(new URL("../extensions/warden/testdata/prefs_corpus.json", import.meta.url), JSON.stringify({ items, raws }) + "\n");
console.log(items.length, raws.length);
