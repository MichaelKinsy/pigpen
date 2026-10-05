---
issue: 989
issue_title: "pi-permission-system: approving an external directory for the session also approves sibling directories"
---

# Retro: #989 — pi-permission-system: approving an external directory for the session also approves sibling directories

## Stage: Planning (2026-10-03T02:12:10Z)

### Session summary

Planned the fix for a third-party report (`aisensiy`).
A session approval for a directory path records the parent glob, so sibling directories pass without a prompt.
A spike through the real `PathNormalizer` confirmed the defect for built-in `ls`/`find`, a trailing-slash spelling, and an extension tool, not only the reporter's `add_directory`.
The plan has six steps: a refactor, a grant-target fold, a plural pattern carrier, the directory fix, a fallback label, and docs.

### Observations

- The operator confirmed three choices at the gate:
  - Narrow directory grants **and** label the scope.
  - Render the `{D, D/*}` pair as `"D/*"`.
  - Classify the change as a non-breaking `fix:`.
- A directory needs two grants (`D` and `D/*`).
  `*` cannot say "D and its contents" in one pattern: `D/*` misses `D` itself, and `D*` matches `D-evil`.
- The display fold `grantTargets` goes in `session/approval-grant.ts`, not `pattern-suggest.ts`.
  The architecture entry for the latter forbids path semantics.
- The wire shape stays unchanged.
  The serving node recomputes the fold from the grants.
- The directory probe is `statSync` on `value()`.
  It is skipped for a literal-only `AccessPath`, whose `boundaryValue()` is empty.
  An error or a missing path falls back to today's parent glob.
- The fallback label is limited to path-family grants.
  `bash` grants that arrive with no label keep `undefined` options, which tests in `forwarded-request-server.test.ts` assert.
- Tidy-First assessor:
  - Recommended extracting `parentScopePattern`; that is now step 1.
  - Recommended a `tool.test.ts` helper commit; skipped, because `tool.test.ts:60` is already the single construction site.
- Related, and out of scope: [#604], a request to widen the session grant.

## Stage: Implementation — TDD (2026-10-03T02:50:20Z)

### Session summary

I completed all six plan steps, with one commit per step.
Two were refactors, three were fixes, and the last was docs.
A directory's session approval now records `D` and `D/*` instead of the parent glob.
Path asks that prove no direction now name their scope in the session label.
The pi-permission-system test count went from 5334 to 5362 (+28).

### Observations

- Every killing mutation the plan named turned the predicted tests red.
  Mutation 2 in step 4 also killed the pure and round-trip directory cases, more than the plan listed.
- Deviation in step 4: the end-to-end tests first failed with "`ls` is not registered".
  `makeDedupWiring`'s tool registry listed only `read`, `write`, `edit`, and `bash`.
  I added `ls` and `add_directory` to the registry in `test/helpers/external-directory-fixtures.ts`.
- Deviation in step 5: the plan predicted that only the forwarded "names every path" test was affected, and that it would stay green through `objectContaining`.
  It missed the non-forwarded "offers no width option when the grants prove different directions" case in `local-user-authorizer.test.ts`.
  That case now expects `{ sessionLabel: "Yes, allow access to 2 paths for this session" }` instead of `undefined`.
  The change is intended: a mixed-direction path ask now names its scope too.
- In step 3, `describeToolGate` gained private `pathSessionOption` and `valueSessionOption` helpers that return `{ approval, label }`.
  The value-surface `SessionApprovalSuggestion` is unchanged, and a new `PathSessionSuggestion` carries `patterns`.
- Pre-completion reviewer: WARN.
  Its re-derivation confirmed that no input yields a grant wider than the pre-change parent glob.
- Reviewer warnings:
  - A directory whose name contains a glob metacharacter (`a*`) records an exact grant `/r/a*`, which also matches `/r/abc`.
    The folded label `"/r/a*/*"` therefore understates that grant.
    It is still no wider than the old `/r/*`, and the same class already applies to files today.
  - The joined multi-target label in `suggestPathSessionPattern` is unreachable today.

## Stage: Sync (worktree) (2026-10-03T03:26:41Z)

### Session summary

Pre-push `pnpm run lint` and `pnpm fallow dead-code` passed.
The plan's marker is `**Release:** ship independently`; there are no follow-ups or deferred work.

**Peer session transcript:** `/Users/chris/.pi/agent/sessions/--Users-chris-development-pi-pi-packages-worktrees-issue-989--/2026-10-03T02-00-43-343Z_01a0ff7e-024f-775b-bae5-f0fbc58e32df.jsonl` — read with `read_session_file({ path: "<path>" })` for message-level verification at land/retro time.

### Observations

The pre-completion reviewer's two WARN findings (the glob-metacharacter label, the unreachable joined label) are recorded in the TDD stage entry and need no action at land time.

## Stage: Final Retrospective (2026-10-03T03:41:11Z)

### Session summary

The peer worktree session planned, implemented, and synced the fix; the root session fast-forward-merged it, passed CI, closed the issue, and released `pi-permission-system` 39.0.1.
A directory's session approval now records `D` and `D/*` instead of the parent glob, and a path ask with no proven direction names its scope.
The lifecycle ran end to end with no operator correction after the planning gate.

### Observations

#### What went well

- The planning spike drove the real `PathNormalizer` with a throwaway test file before the gate (peer turn 20).
  It showed the defect was not specific to `add_directory`: built-in `ls`/`find` and a trailing-slash spelling hit it too.
  That widened the fix to all five path gates before any design was settled.
- Every TDD step backed up the green file, applied the plan's named mutations, and confirmed the tests went red before restoring with `cmp`.
  No probe leaked into a commit.
- The ship ran without a stop: the ff-merge prediction, root gates, CI, and release each passed on the first attempt.

#### What caused friction (agent side)

- `missing-context` — the plan's step 4 did not check that `makeDedupWiring`'s tool registry knew `ls` or `add_directory`, so the end-to-end tests first failed with "`ls` is not registered".
  Impact: one throwaway spike test (peer turn 91) and a fixture edit; no rework of production code.
- `missing-context` — the plan's step 5 predicted one affected test and missed the non-forwarded mixed-direction case in `local-user-authorizer.test.ts`.
  Impact: one unplanned test edit; the behavior change was intended.
- `other` — two commits failed silently behind `>/dev/null 2>&1` on a pre-commit reformat (Biome on peer turn 78, `rumdl` on turn 116).
  The agent caught each with `git log --oneline -1` and recommitted.
  Impact: one retry each, no rework.
- `other` — three planning calls errored on `fallow guard` given a package-relative path after `cd packages/pi-permission-system` (peer turns 26, 32, 33).
  Impact: three wasted calls.
- `instruction-violation` (self-identified) — at ship, the close comment was not re-resolved with `git rev-parse` / `git merge-base --is-ancestor` before `issue_close`, as `/ship`'s close step requires.
  The SHAs were pasted as full 40-character values from `git log` output, so none was fabricated.
  Impact: none; the report flagged the skip.
- `other` — the ship session ran `head -0` twice ("illegal line count").
  Impact: harmless noise.

#### What caused friction (user side)

- Nothing noted; the operator's only intervention was the planning gate, and its three choices (narrow and label, the `"D/*"` rendering, non-breaking `fix:`) held through implementation.

### Diagnostic details

- **Model-performance correlation** — planning and TDD ran on `claude-opus-5-5`, which suits the design and mutation work; sync ran on `claude-sonnet-5-5`, which suits a mechanical step.
  Both subagents (`tidy-first-assessor`, `pre-completion-reviewer`) ran on `claude-sonnet-5-5`, per their transcripts; the reviewer's re-derivation that no input widens a grant was sound.
- **Feedback-loop gap analysis** — the TDD stage ran targeted `vitest` after each step and `pnpm run check`/`lint` at steps 2–5, so verification was incremental, not end-loaded.

### Changes made

1. None beyond this retro entry; no prompt, skill, or `AGENTS.md` change was justified.

[#604]: https://github.com/gotgenes/pi-packages/issues/604
