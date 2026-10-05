---
issue: 955
issue_title: "pi-permission-system: piInfrastructureDirs includes bare agentDir, so auth.json and mcp-oauth/ are auto-allowed reads that an explicit deny cannot stop"
---

# Retro: #955 — piInfrastructureDirs includes bare agentDir

## Stage: Planning (2026-10-02T23:12:23Z)

### Session summary

I reproduced the bypass through the real extension factory (`makeFakePi` with a real global config).
I priced the change against the operator's review log, which has 486 `infrastructure_auto_allowed` entries.
The resulting six-step plan has two preparatory refactors, three breaking `fix!:` steps (targeted-deny yield, logs carve-out, list narrowing), and a docs step.
I filed #1018 (infra list never canonicalized) and dispositioned it as out of scope for Phase 15.

### Observations

- Two claims in the issue were corrected by measurement.
  - A `path` / `path_read` / `read` deny on `auth.json` already blocks today; only the `external_directory` family is skipped.
  - `flavor.isWithin` returns `true` on equality, so a file entry (`settings.json`) needs no new matching.
- Operator decisions:
  - The list is the issue's list plus Pi's resource-loader roots (`prompts/`, `themes/`, `SYSTEM.md`, `APPEND_SYSTEM.md`, `AGENTS.md`).
  - Carve out the package's own `globalLogsDir`.
    The review log was read twice through the bypass, which contradicts ADR 0010's table row.
  - Only a **targeted** deny overrides the bypass.
    A bare `"*"` (`matchedPattern: "*"`) and the universal fallback (`matchedPattern: undefined`) do not, both measured via the manager.
- Spike trap: on macOS a `mkdtemp` agentDir sits under `/var` → `/private/var`.
  The un-canonicalized infra list then never matches, so the bypass silently does not fire.
  The composition-root tests must `realpathSync` the agentDir.
  This is the origin of #1018.
- The Tidy-First assessor recommended the `InfrastructureReadScope` rename as a leading pure refactor and hoisting `preCheck` above the bypass.
  It also recommended keeping the leaf `isPiInfrastructureRead` array signature with a trailing `excludedDirs = []`, so its ~26 test calls stay unchanged.
- Measured cost of the final design on the operator's log: 6 of 486 entries lose the bypass (4 `sessions/`, 2 review-log reads).

#### Deferred tidyings

- None.
  The assessor's rejections (normalizer-held scope, merging `piInfrastructureReadPaths` into `ExtensionPaths`, and a shared within-any helper) are not worth a separate pass.

## Stage: Implementation — TDD (2026-10-02T23:40:03Z)

### Session summary

All six planned steps landed: two preparatory refactors (`InfrastructureReadScope`, preCheck hoist), three breaking `fix!:` steps (targeted-deny yield, logs exclusion, harness-entry list), and the docs.
The package suite went from 5312 to 5334 tests, all green, and every named killing mutation turned exactly its predicted tests red.

### Observations

- Deviation: `describeExternalDirectoryGate` had 6 direct test calls across the two acceptance files, not the 5 the plan estimated.
- Deviation: `extension-paths.test.ts` lost four per-entry `toContain` tests, subsumed by a full-list `toEqual`, rather than having them rewritten one by one.
- Pre-completion reviewer: WARN on the first round, PASS on the delta.
  - It found that `isTargetedDeny` compared `matchedPattern !== "*"` literally, so a `"**"` catch-all (which compiles identically) counted as targeted.
    It now uses `/^\*+$/`, and a unit test plus two mutations pin it.
  - It noted no prefix-collision pins existed, so `settings.json.bak` and `skills-old/` were added to the composition-root `it.each`.
    A prefix-glob mutation of the entry list kills exactly those two.
  - It found that a symlinked `agentDir` plus a user glob leaves the logs exclusion unmatched: the same un-canonicalized derivation as #1018, recorded as a comment there.
  - Both code fixes were autosquashed into their step commits before push.
    The tree was verified identical across the rebase.
- Scripting trap, twice: an `Edit` body typed `\u2500`/`\u2026` as literal escapes in a comment and a JSDoc.
  Both were caught by grep and rewritten with the real character or plain words.
- A placement slip: inserting the `AGENT_DIR_INFRASTRUCTURE_ENTRIES` constant before `export function` split the function's JSDoc from its declaration, and a scripted move fixed it.

## Stage: Sync (worktree) (2026-10-03T01:44:48Z)

### Session summary

`pnpm run lint` and `pnpm fallow dead-code` pass on the branch.
The plan's marker is `**Release:** ship independently`; all three behavior commits are breaking, so the release is a major.

**Peer session transcript:** `/Users/chris/.pi/agent/sessions/--Users-chris-development-pi-pi-packages-worktrees-issue-955--/2026-10-02T22-41-22-018Z_01a0fec7-7e61-7011-96e4-701e4a6dc9fe.jsonl` — read with `read_session_file({ path: "<path>" })` for message-level verification at land/retro time.

### Observations

- Follow-ups already filed: #1018 (infra list not canonicalized; carries the review's logs-exclusion comment).
- #956 (the bash bypass) stays unblocked by this change but is untouched.

## Stage: Final Retrospective (2026-10-03T01:57:12Z)

### Session summary

The root `/ship` merged the worktree branch with `--ff-only`, CI passed on `cbbf83b9`, #955 was closed with a summary, and the release published `pi-permission-system-v39.0.0` (a major).
The worktree and branch were torn down.
Across all four stages the issue went from planning to release in about three hours without a rejected review or a CI failure.

### Observations

#### What went well

- Measuring before planning paid off twice in planning.
  A spike through the real extension factory corrected two claims in the issue: only the `external_directory` family was bypassed, and file entries already matched on equality.
  Bucketing the operator's 486 `infrastructure_auto_allowed` log entries put a number on the breaking change (6 entries lose the bypass) before anyone had to decide.
- Every behavior step had named killing mutations, applied with a backup-and-`cmp` restore loop (`/tmp/i955/green-*.ts`).
  Each mutation turned red exactly the tests the plan predicted, and the reviewer's `"**"` finding got the same treatment.
- The pre-completion reviewer's first round found a real defect: `"**"` was counted as a targeted deny.
  The fix was autosquashed into its step commit, and a backup tag plus `git diff` showed the tree was unchanged by the rebase.

#### What caused friction (agent side)

- `other` — pre-commit hooks that rewrite files (Biome, `rumdl fmt`) rejected the first `git commit` of steps 1 and 3.
  From step 4 onward the peer wrapped every commit in `commit || { git add -A; commit; }`.
  Impact: 2 extra tool calls; the peer adapted on its own.
- `instruction-violation` (self-identified, caught by gates) — literal `\u2500`/`\u2026`/`\u2014` escapes landed in edit bodies three times: two source comments and the TDD retro heading.
  The escape gates and `grep` caught each one before commit.
  Impact: 3 repair calls, no rework past the commit.
- `instruction-violation` (self-identified) — the peer ran package commands as `cd packages/pi-permission-system; pnpm exec vitest run …` throughout, instead of `pnpm --filter`.
  Impact: none observed.
- `missing-context` — after the sync rebase, `git diff --stat ORIG_HEAD HEAD` showed 30 files, which looked wrong for a no-op rebase.
  Local `main` had moved 11 commits, which explained it.
  Impact: 2 diagnostic calls.
- `instruction-violation` (self-identified) — the ship's final report said the phase-last-step check was not run, instead of running it.
  This retro checked: #955 is listed as out of scope for the roadmap in `architecture.md`, so no phase closed.
  Impact: none.

#### What caused friction (user side)

- None observed.
  The operator's planning decisions (list contents, the logs carve-out, targeted-deny-only) were each settled in a single `ask_user` gate.

### Diagnostic details

- **Model-performance correlation** — planning and TDD ran on `claude-opus-5-5`, and sync and ship on `claude-sonnet-5-5`.
  The three subagents (one `tidy-first-assessor`, two `pre-completion-reviewer`) ran on `claude-sonnet-5-5`.
  The models suited their tasks: the judgment-heavy design ran on Opus, and the first review round still found a real defect on Sonnet.
- **Feedback-loop gap analysis** — `pnpm run check` and the targeted vitest files ran after every step, and the full suite before each commit.
  No step relied on verification left until the end.

### Changes made

1. Appended this Final Retrospective entry to `packages/pi-permission-system/docs/retro/0955-narrow-pi-infrastructure-reads.md`.
   No rule changes were made: each friction point was minor and is already covered by an existing rule or gate.
