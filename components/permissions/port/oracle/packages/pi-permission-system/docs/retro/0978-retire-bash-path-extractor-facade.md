---
issue: 978
issue_title: "pi-permission-system: bash-path-extractor.ts has no production caller; retire the facade or narrow its 1300-line test"
---

# Retro: #978 — pi-permission-system: bash-path-extractor.ts has no production caller; retire the facade or narrow its 1300-line test

## Stage: Planning (2026-09-29T03:06:13Z)

### Session summary

Planned retiring `extractExternalPathsFromBashCommand` and its 133-test file.
Every facade test was classified: 56 have an equivalent elsewhere and 77 migrate to a new `test/access-intent/bash/program-external-accesses.test.ts`.
That file also takes `program.test.ts`'s `externalPaths` describe (81 tests, lines 496–1328), so `externalAccesses()` has one test home.

### Observations

- Operator decisions: retire rather than keep a documented seam, and have the new file own **all** slice-scoped `externalAccesses` tests rather than fold them into the 2916-line `program.test.ts` or add a second home.
- A literal-command match found only 15 of 128 facade commands elsewhere; the ~100 end-to-end shell-syntax cases (quotes, comments, heredocs, `/dev/*`, `//`, `cd` prefix, pattern-first commands) have only unit-layer coverage, so wholesale deletion was not viable.
- The `Explore` audit's totals were wrong: it reported 46 equivalent, but its own table listed 57.
  The recount and three corrections gave 56: lines 414 and 419 become move, because the classifier returns `//` as-is and the normalizer does the collapse, and line 263 becomes equivalent, because it runs the same command as 632.
  Re-derive any count a subagent hands back.
- Describes in `program.test.ts` that assert both slices (`workdir seed`, `effect attribution`, `#875`, `#863`) deliberately stay there.
- The mock headers differ: the facade suite uses a `/mock/home` `homedir` and replaces `node:fs` wholesale, while the new file uses the real `homedir()` and a pass-through `node:fs`.
  Migrated `~` cases become `join(homedir(), …)`.
- Tidy-First assessor: no preparatory tidying warranted.

#### Deferred tidyings

- `test/**/*.test.ts` — 18 files inline the same `vi.hoisted` `realpathSync` pass-through `node:fs` mock; the assessor judged a shared helper the wrong abstraction for this change.

## Stage: Implementation — TDD (2026-09-29T03:25:50Z)

### Session summary

Completed all six plan steps: the `externalPaths` describe moved verbatim into `program-external-accesses.test.ts`, three batches migrated the facade cases with no equivalent, and the facade plus its test file were deleted, with the roadmap step marked landed.
Package test count across the three files went from 475 to 420, since 55 cases with a cited equivalent were deleted, and `program.test.ts` went from 2916 to 2082 lines.

### Observations

- Deviation: 78 cases migrated and 55 deleted as equivalent (the plan said 77/56).
  The unquoted `awk -F:` case was migrated because the cited equivalent tests the quoted `-F':'` concatenation, a different construct.
- Deviation: the win32 cases went into a new `Git Bash tokens on a win32 host` describe instead of the existing win32 describe, because the facade's cwd (`C:/projects/app`) differs from the existing one's.
- Several planned killing mutations left their tests green, and each finding is recorded in its commit body.
  Dropping `comment` or `heredoc_body` from `SKIP_SUBTREE_TYPES` changed nothing, even with the type also added to `ARG_NODE_TYPES`, so those cases pin grammar structure rather than the skip set.
  Renaming the `sed`/`grep` keys in `PATTERN_FIRST_COMMANDS` left the cases whose pattern has no path shape green (`s/foo.*/`, the alternation, `^/usr/bin`), because the classifier rejects those patterns anyway.
- Every tightened `toEqual` value came from a run, and all matched what the facade test's name claimed (`cd /tmp && cat ../etc/hosts` → `/tmp`, `/etc/hosts`; `C:/Windows/win.ini` → `c:\windows\win.ini`).
- Once step 4 stripped the facade suite's wholesale `node:fs` and `/mock/home` mocks, it held only presentation cases, so step 5 was a clean deletion.
- Pre-completion reviewer: PASS.
  It re-derived 20+ equivalence rows and confirmed all 81 moved test names survive.

## Stage: Sync (worktree) (2026-09-29T03:27:10Z)

### Session summary

Pre-push checks (`pnpm run lint`, `pnpm fallow dead-code`) both pass with no fixes needed.
The plan's `**Release:** ship independently` marker stands — every commit is `test:`, `refactor:`, or `docs:`, so `/ship` lands the branch and dispatches no release.

**Peer session transcript:** `/Users/chris/.pi/agent/sessions/--Users-chris-development-pi-pi-packages-worktrees-issue-978--/2026-09-28T22-32-52-851Z_01a0ea26-4973-7313-8c6a-f6188fbe23db.jsonl` — read with `read_session_file({ path: "<path>" })` for message-level verification at land/retro time.

### Observations

No deferred work and no follow-up issues from this implementation.
The pre-completion reviewer's PASS re-derived the equivalence audit independently, so the root ship should need no additional verification beyond CI.

## Stage: Final Retrospective (2026-09-29T03:48:18Z)

### Session summary

The root session fast-forward-merged the branch (pre-merge tip `57b81d63`, equal to the plan commit's parent), re-ran lint and `fallow dead-code` on the merged tree, pushed, and CI passed on `3ece1922`.
The script `next-version.sh pi-permission-system` printed nothing to release, so no release was dispatched; #978 was closed citing `a79a4af7`, and the worktree and branch were torn down.
Across all four stages the issue ran with no rework commits and no user corrections.

### Observations

#### What went well

- The planning session's throwaway script (`/tmp/cmp978.mjs`) matched every facade test's command literal against the rest of `test/`, turning a 133-case equivalence audit into a mechanical first pass that the agent then corrected by hand.
  A literal match found only 15 hits, which is what showed wholesale deletion was unsafe.
- Surviving killing mutations were treated as findings, not failures, and each was explained in its commit body (`SKIP_SUBTREE_TYPES` cases pin grammar structure; non-path-shaped `sed`/`grep` patterns are rejected by the classifier too).
  This is the `tdd-plan` mutation guidance working as written.
- The plan's `Release Recommendation` already said the change cuts nothing, and `next-version.sh` confirmed it at ship time, so the no-release outcome was predicted three stages ahead.

#### What caused friction (agent side)

- `other` — the `Explore` audit's totals disagreed with its own table (46 equivalent reported, 57 listed).
  The planning agent caught it and recounted, then corrected three verdicts to reach 56.
  Impact: a few extra tool calls, no rework; the existing delegation rule (verify a subagent's count) did its job.
- `other` — several planned killing mutations left their tests green because the plan predicted them without naming what observably changes.
  Impact: about five extra mutation runs in TDD step 2 and step 3, no rework; the findings were recorded.
- `instruction-violation` (not caught) — the peer prefixed package-scoped bash calls with `cd packages/pi-permission-system;` (72 matching lines in the peer transcript JSONL), against the `AGENTS.md` rule to run `pnpm --filter` from the root.
  Impact: none observed, since each bash call starts fresh at the root.

#### What caused friction (user side)

- None; the operator's two planning decisions (retire rather than keep a documented seam, and give the tests a single home) were made up front at one gate, which is why the plan was not revised.

### Diagnostic details

- **Model-performance correlation:** planning and TDD ran on `claude-opus-5-5` and sync on `claude-sonnet-5`, and this ship and retro ran at the root.
  All three subagents (`Explore`, `tidy-first-assessor`, `pre-completion-reviewer`) ran on `claude-sonnet-5` per their transcripts.
  The `Explore` miscount happened on a count-heavy audit where Sonnet was adequate for the classification but not for the totals, and the parent's recount compensated.

### Changes made

None; no proposal was approved.
