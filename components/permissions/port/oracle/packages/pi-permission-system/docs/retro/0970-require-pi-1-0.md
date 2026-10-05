---
issue: 970
issue_title: "pi-permission-system: raise the pi-coding-agent peer floor and devDependency pin past 0.86"
---

# Retro: #970 — pi-permission-system: raise the pi-coding-agent peer floor and devDependency pin past 0.86

## Stage: Planning (2026-10-01T22:01:30Z)

### Session summary

Planned the floor and pin raise to `>=1.0.0`/`1.0.0` for both `pi-coding-agent` and `pi-tui`, following the operator's issue comment that moved the target from 0.86 to 1.0.0.
A reverted spike showed the change compiles and passes with no `src/` edit.
The plan is a two-commit `/build-plan`: a `feat(pi-permission-system)!:` dependency commit, then a README `## Upgrading` entry.

### Observations

- Spike (measured): pnpm added 8 `minimumReleaseAgeExclude` entries for the 1.0.0 family by itself, merging five into `'<name>@0.84.4 || 1.0.0'`, and stopped on `ERR_PNPM_IGNORED_BUILDS` for `esbuild@0.28.2` (from `@earendil-works/chord@1.0.0`).
  With `esbuild: false`, `tsc` was clean, the suite passed (175 files, 5174 tests), and `verify:public-types` was OK.
  A first suite run under load had 2 failures plus timeouts; two reruns were clean.
- Operator decision: the header-layout deletion moves out of #970 into #999, which already deletes the whole relocation.
  Doing it here would have rewritten about 50 header-shaped tests that #999 then rewrites again.
  The #999 plan (on branch `issue-999`) says "the header layout is gone" as a prerequisite; that is now false, but its Module-Level Changes ("delete whatever header-layout residue #970 left") covers it.
  The #999 session should re-read `tool-surface-prompt.ts` expecting both layouts still present.
- Operator decision: release is deferred so one major carries both the floor and the #999 fix (ad-hoc batch "pi-1.0 prompt options", #999 the tail; #1009 releases first).
- Commit type `feat!:` follows `10683290` (the prior pure floor raise); the two `fix!:` precedents the issue cited carried behavior fixes.
- Rejected: raising `engines.node` to Pi 1.0's `>=22.19.0`, since Pi enforces it upstream.

## Stage: Implementation — Build (2026-10-01T22:18:33Z)

### Session summary

Both plan steps landed: the `feat(pi-permission-system)!: require Pi 1.0.0 or later` commit raises the peer floors and devDependency pins to 1.0.0 (with the `esbuild: false` decision and the release-age entries pnpm wrote), and the `docs(pi-permission-system): note the Pi 1.0.0 requirement under Upgrading` commit adds the README `## Upgrading` entry.
The install reproduced the spike exactly, and `check`, the full suite (175 files, 5174 tests), `verify:public-types`, root lint, and `fallow dead-code` all passed.

### Observations

- Deviation: the plan said to put `next-version.sh`'s version (it printed `pi-permission-system-v37.0.0`) in the README heading, but the `git-workflow` skill forbids naming an unreleased version in docs, so the heading reads "The major after 36.x — requires Pi 1.0.0".
  The planning stage should have caught that conflict.
- The full suite ran clean on the first try this time; the planning spike's load flake did not recur.
- Pre-completion reviewer: PASS.
  It reminded that the release marker is `mid-batch — defer`, to be dispatched together with #999.

## Stage: Sync (worktree) (2026-10-01T22:51:27Z)

### Session summary

Pre-push checks passed (`pnpm run lint`, `pnpm fallow dead-code`).
The plan's marker is `**Release:** mid-batch — defer` (batch "pi-1.0 prompt options", #999 the tail), so `/ship` should ask and then not name `pi-permission-system` in the dispatch; #1009 (pi-subagents) releases ahead of #999.

**Peer session transcript:** `/Users/chris/.pi/agent/sessions/--Users-chris-development-pi-pi-packages-worktrees-issue-970--/2026-10-01T21-44-34-076Z_01a0f96d-221b-7287-a7e3-33f7662c6884.jsonl` — read with `read_session_file({ path: "<path>" })` for message-level verification at land/retro time.

### Observations

- Closing #970 does not release it; the `feat!:` commit waits on `main` until a dispatch names the package.
- The #999 plan's "header layout is gone" prerequisite is false (deletion moved to #999); see the planning entry.

## Stage: Final Retrospective (2026-10-01T23:46:44Z)

### Session summary

The floor and pin raise to Pi 1.0.0 went from plan to `main` across a planning, build, and sync peer session plus a root ship, with no `src/` change and no rework commits.
The ship fast-forward-merged the branch, CI passed on `2b9faaa3`, #970 closed citing `9c76ae30`, and the release was deferred at the operator's confirmation so one major carries #970 and #999 together.

### Observations

#### What went well

- The planning spike (measured install, `check`, suite, `verify:public-types`, then reverted) predicted the build exactly: the same 8 `minimumReleaseAgeExclude` entries and the same `esbuild` `allowBuilds` stop, so the build ran without surprises.
- The scope question in planning was priced with measured line ranges (about 50 tests rewritten twice), which made moving the header-layout deletion to #999 an easy operator call.
- The sync session's dangling-SHA loop caught the build note's pre-rebase SHAs and replaced them with commit subjects before the land.

#### What caused friction (agent side)

- `instruction-violation` — the plan's step 2 told the build to put `next-version.sh`'s output in the README `## Upgrading` heading, which the `git-workflow` skill's "Numbers a command produces" rule forbids.
  The planning session had loaded `releasing` and `markdown-conventions` but not `git-workflow`, so the rule never reached the session writing the doc instruction.
  Self-identified by the build session, which deviated and recorded it.
  Impact: no rework, but the new heading ("The major after 36.x — requires Pi 1.0.0") breaks the `### <version> — <change>` pattern of the section's older entries.
- `other` — the ship session skipped step 8's `next-version.sh` call because the release was already deferred.
  Impact: none; the deferral made the output moot.

#### What caused friction (user side)

- None; the two operator decisions (scope, release deferral) were asked once in planning and confirmed once at ship.

### Diagnostic details

- Model-performance correlation: planning and build ran on `anthropic/claude-opus-5-5`, sync on `anthropic/claude-sonnet-5-5` — a sensible split, since sync is mechanical.
- Feedback loop: the build ran `check`, the full suite, `verify:public-types`, lint, and `fallow` after the dependency commit's install, before committing; nothing was deferred to the end.

### Changes made

1. `.pi/skills/markdown-conventions/SKILL.md`: added a `### Version numbers` rule — never name an unreleased version in docs or in a plan step, since `next-version.sh` prints a moving prediction.
