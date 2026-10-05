---
issue: 962
issue_title: "pi-permission-system: the tool-surface relocation is a no-op on pi ≥0.86, so a session sees Pi's unfiltered tool list alongside the narrowed one"
---

# Retro: #962 — pi-permission-system: the tool-surface relocation is a no-op on pi ≥0.86

## Stage: Planning (2026-09-23T04:30:57Z)

### Session summary

Verified the diagnosis by building a real pi 0.87.1 prompt with its own `buildSystemPrompt` (registry install in `/tmp/spike962`) and running the unmodified `renderToolSurface` over it.
Planned a layout strategy selected once from the cwd layer present: a footer-shaped `HEADER_LAYOUT` (byte-identical to today) and a `<cwd>`-shaped `SECTION_LAYOUT`.
The plan has 7 steps: 2 tidyings, 4 behavior steps, and docs.

### Observations

- The spike found a second defect that the issue and SlanyCukr's patch both miss.
  On 0.86 there is no footer, so the whole prompt counts as head, and plain-header removal in a pi-authored prompt deletes the user's own `Guidelines:` section in AGENTS.md.
  SlanyCukr's patch keeps the whole-head plain removal, so it would not fix this.
- A custom-preamble (child) section prompt is not idempotent today: a second pass collects a second block.
  Only a peer writer triggers it.
- The "section seam" (`event.systemPromptOptions`) was rejected.
  No option removes pi's `<tools>`/`<rules>` except `customPrompt`, and a custom `sections.tools` entry replaces pi's section in place, in the inherited region.
- Operator decisions: render the relocated block as tagged `<tools>`/`<rules>` on the section shape; carry `promptGuidelines` extras, filtered against every registered tool's guidelines.
  Pi 0.87.1 never sets `promptGuidelines` itself; through 0.85 it was the flattened tool guidelines, so the filter leaves nothing.
- Anchor choice: the section anchor is found by shape only (`<cwd>`/line/`</cwd>`, last occurrence), mirroring the footer.
  When both anchors are present, the later one wins.
  Head removal on the section shape is bounded before the first `<docs>`/`<addendum>`/`<project_context>`/`<skills>`/`<cwd>` open.
- Tidy-First assessor: its Recommended tidying (split bullets from header wrapping) became step 1, and its Optional `PromptLayout` pre-shaping became step 2.
  The fixture-builder and fixture-rename optionals were folded into step 3 or dropped.
- PR #908 (OMP prompt arrays) touches the same two `src/` files and is orthogonal; whichever lands second rebases.
- The `pi-anthropic-auth` sibling writer parses sections by name and treats `tools`/`rules`/`docs` as pi-owned.
  A tagged tail block keeps its shaping consistent.
- Follow-up #970 was filed after the plan commit: raise the `pi-coding-agent` peer floor and devDependency pin together, for this package only, as a separate breaking change after #962 (operator decision).
  A spike pinning the devDependency at 0.87.1 was clean: `tsc` passed, and so did 168 test files with 4590 tests.
  The spike was reverted, including pnpm's automatic `pnpm-workspace.yaml` edits.
  Its roadmap disposition is out of scope for Phase 15.

#### Deferred tidyings

- `src/exposure/tool-surface-prompt.ts`: this package's `guidelinesByTool` (from `getAll()`) duplicates pi 0.86's separate `systemPromptOptions.toolGuidelines`; reconciling the two sources is a separate design change.

## Stage: User Note (2026-09-23T04:53:22Z)

I wonder whether a purpose-built test-running tool would help.
The agent keeps assembling compound shell invocations just to run tests.
In this session's TDD cycle, nearly every run was a chain along the lines of `pnpm --filter … exec vitest run <file> >/tmp/t.log 2>&1; grep -E "×|Tests " /tmp/t.log`, often followed by `pnpm run check`, `eslint`, and a `cp` backup or restore for a killing mutation.

## Stage: Implementation — TDD (2026-09-23T05:09:35Z)

### Session summary

All 7 plan steps are done: 2 tidyings (split the bullets from their headers, `PromptLayout` dispatch), 4 behavior steps (section anchor, removing Pi's `<tools>`/`<rules>`, the tagged block, carried `promptGuidelines`), and the docs step.
Comment-only prose landed in a separate docs commit.
`pi-permission-system` went from 4590 to 4609 tests (+19).
Pre-completion reviewer: PASS; it re-derived all four invariants against real pi 0.87.1 prompts with its own decoy inputs.

### Observations

- Every killing mutation named in the plan turned red exactly the tests it predicted.
  The stability twin in step 5 had no named mutation, so I checked it separately: making `removePiSurface` a no-op turned it red.
- Deviation, step 4 test 3: with Pi's own `<tools>`/`<rules>` present, removing the later-section bound still removes Pi's sections first, so a quoted `<tools>` survives either way and the mutation cannot tell the two apart.
  The test instead builds the prompt as a peer writer leaves it (Pi's two sections already gone), which is the only input where the bound decides.
- Deviation: the module doc comment and the test comment on the header-layout project-context case landed as their own `docs(pi-permission-system):` commit, not inside the code steps.
- Friction: in step 3, the `cp` that backs up the green file ran in the same parallel tool block as the mutating `Edit`, so the "green" backup already held the mutation.
  Take the backup in a call of its own before mutating.
- Friction: I typed `\u2014`/`\u2265` escapes into `Edit` bodies four times (including this retro's own stage heading) and they landed as literal text; each time a `grep -n 'u2014'` caught it before commit.

#### Reviewer warnings

None.

## Stage: Sync (worktree) (2026-09-23T14:31:59Z)

### Session summary

Pre-push checks are clean from the worktree root: `pnpm run lint` and `pnpm fallow dead-code` both pass.
No deferred work; the plan's marker is `**Release:** ship independently`, and the one follow-up it names (#970, raising the `pi-coding-agent` peer floor) is filed and dispositioned separately.

**Peer session transcript:** `/Users/chris/.pi/agent/sessions/--Users-chris-development-pi-pi-packages-worktrees-issue-962--/2026-09-23T04-03-09-329Z_01a0cc6e-8191-7541-9553-a08310463316.jsonl` — read with `read_session_file({ path: "<path above>" })` for message-level verification at land/retro time.

### Observations

No new friction beyond what the TDD stage above already recorded.

## Stage: Final Retrospective (2026-09-23T16:22:05Z)

### Session summary

The fix shipped as `pi-permission-system` 33.1.0 through the worktree lane: fast-forward merge, clean root gates, green CI, and issue closed with 596dca1a as the landing commit.
Across planning, TDD, and sync, the work followed the plan without rework: 2 tidyings, 4 behavior steps, and docs.
The one miss surfaced only at this retro: the shipped mechanism is SlanyCukr's patch from the issue thread, and no artifact credits it.

### Observations

#### What went well

- The planning spike installed pi 0.87.1 from the registry and ran its real `buildSystemPrompt` through the unmodified `renderToolSurface`.
  That found a defect that neither the issue nor the contributor's patch named: plain-header removal on a footerless prompt deleted a user's own `Guidelines:` section in AGENTS.md.
  This is the `reproduction` skill working as intended, with organic input instead of a hand fixture.
- Every killing mutation named in the plan turned red exactly the tests it predicted, and the pre-completion reviewer re-derived the invariants with its own decoy inputs.
- The ship ran without a stop: `ff-ok` prediction, 0 unpushed root commits, lint and `fallow` clean on the merged tree, CI in about 3.5 minutes, and the release in about 1.5 minutes.

#### What caused friction (agent side)

- `instruction-violation` — SlanyCukr's comment on the issue carried measured wire evidence and a patch.
  The patch removes Pi's own `<tools>`/`<rules>` only when Pi wrote the preamble, bounded before the first later Pi section.
  Commit 596dca1a ships that mechanism, adding `<docs>` to the bound.
  Planning framed the patch by its gap (it keeps whole-head plain removal) and never recorded it as adopted.
  The plan does not mention it, no commit carries `Co-authored-by:`, and the `/ship` close comment did not name the contributor.
  The `git-workflow` rule covers this case ("whether or not their patch was taken"), but the `/plan-issue` gate that enforces it sits inside the third-party-issue paragraph, and #962 was filed by `gotgenes`.
  `/ship` step 9 builds the close comment from commits only and never reads the issue's comments.
  Found at this retro, not caught by the operator.
  Impact: the credit is not in git history, since pushed commits cannot be amended; the remedy is a follow-up comment on the issue.
- `other` — the agent wrote `\uXXXX` escapes in `Edit` bodies 5 times: 4 in TDD (claude-opus-5-5, across `src/`, `test/`, and the retro heading) and 1 in sync (claude-sonnet-5).
  The rule already exists in `markdown-conventions` and in the system-prompt addendum.
  A `grep` caught every one before commit.
  Impact: about 5 extra edit-and-grep cycles, and no rework reached a commit.
  The recurrence across two models suggests a lint gate rather than more prose; one pre-existing literal escape in a comment (`src/authority/authorizer-chain.ts` line 47) shows the class already reaches `main`.
- `other` — a green-file backup `cp` ran in the same parallel tool block as the mutating `Edit`, so the backup held the mutation (TDD step 3).
  Self-identified.
  Impact: one hand repair and a re-save.
- `missing-context` — during planning, the agent told the operator that every package keeps its devDependency at its peer floor.
  The `pi-subagents` package does not.
  Self-identified and corrected before the #970 body was written.
  Impact: none on artifacts.

#### What caused friction (user side)

- The User Note on test-running ergonomics is an opportunity, not a correction.
  Nearly every TDD run was a hand-built chain (`vitest run … >/tmp/t.log; grep -E "×|Tests "`, then `check`, `eslint`, and a `cp` restore), so a purpose-built tool would take that composition off the agent.

### Diagnostic details

- **Model-performance correlation:** planning and TDD ran on `anthropic/claude-opus-5-5` (thinking high), which fits the design and mutation work.
  Sync ran on `anthropic/claude-sonnet-5`, which fits mechanical gates and a rebase.
  The `tidy-first-assessor` (25 turns) and the `pre-completion-reviewer` (60 turns) both ran on `claude-sonnet-5`, per their own transcripts; the reviewer's PASS came with independent re-derivation, so there was no mismatch.
  Ship and retro ran on `anthropic/claude-opus-5-5`.
- **Feedback-loop gap analysis:** no gap.
  Each TDD step ran its scoped `vitest`, `check`, and `eslint` before committing.
  The full package suite ran before the step 6 shared-interface commit, and every root gate ran at the end and again on the merged tree at ship.

### Changes made

1. `.pi/prompts/plan-issue.md`: the `Co-authored-by:` planning rule now has its own paragraph and fires on a third-party mechanism from an issue body, comment, or PR, whoever filed the issue.
   It also states that a patch set aside for one gap still credits the mechanism the plan keeps.
2. `.pi/prompts/ship.md` step 9: the close comment now credits by `@login` any third party whose comment supplied the shipped design or measured the defect, read from `gh issue view --json comments`.
3. Posted a credit comment for @SlanyCukr on #962 (issuecomment-5799008917), naming 596dca1a as the commit that ships the patch's mechanism.
4. Filed #973 (`scope:repo`): a purpose-built test-running tool, from the operator's User Note.
   Dispositioned out of scope for Phase 15 in `packages/pi-permission-system/docs/architecture/architecture.md` (commit `docs(pi-permission-system): disposition #973 against Phase 15`).
5. Not filed, by operator decision: a lint gate for literal `\uXXXX` escapes in markdown and TS comments.
   The class recurred a 6th time during this retro: the #973 sweep bullet landed as a literal `\u2014` and was caught by `grep` before commit.
