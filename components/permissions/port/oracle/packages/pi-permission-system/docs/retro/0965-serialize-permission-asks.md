---
issue: 965
issue_title: "pi-permission-system: a second inline permission ask replaces the first and strands its Promise — serialize asks per session"
---

# Retro: #965 — a second inline permission ask replaces the first and strands its Promise

## Stage: Planning (2026-09-22T17:33:37Z)

### Session summary

Planned the per-session FIFO serialization of human-facing permission asks for third-party issue #965 (reporter `gitwyy`, carrying a private fork).
The plan lands in `packages/pi-permission-system/docs/plans/0965-serialize-permission-asks.md`: a new `AskDialogQueue` in `authority/`, injected into `LocalUserAuthorizer` through `AuthorizerSelectionDeps`, with `releaseAll` driven from `SessionLifecycleHandler.handleSessionShutdown`.
Five TDD steps, one of them the Tidy-First assessor's single accepted preparatory test refactor.

### Observations

- The mechanism was verified against the **published** `@earendil-works/pi-coding-agent@0.87.0` tarball (`showExtensionCustom` at `dist/modes/interactive/interactive-mode.js:2237`) and found byte-identical to the tracking checkout at `d1230ea20`.
  `Container.clear()` calls no `dispose`, so an evicted component cannot detect its own eviction — that ruled out a self-healing design before it was proposed.
- Two facts the issue does not state, both found by reading the real code and both load-bearing.
  `LocalUserAuthorizer` is rebuilt on every activation inside `selectAuthorizer`, so the queue cannot be a field it owns; it has to be threaded through `AuthorizerSelectionDeps`.
  And `docs/cross-extension-api.md` documents `permissions:ui_prompt` as firing "immediately before" the UI is invoked, which **rejects** the smallest possible design (decorating the injected `requestPermissionDecision` at the composition root) because that fires the event at enqueue time.
- Other writers of Pi's inline slot were enumerated rather than assumed: `@eko24ive/pi-ask` (`ctx.ui.custom` with no options, mounted from a tool call) and `pi-subagents`' session navigator.
  Our own `/permission-system` modal uses `{ overlay: true }` and is therefore **not** a colliding writer — a check that would have added scope if skipped in the other direction.
- Honest regression surfaced and accepted at the gate: behind an unbounded FIFO, a foreign clobber of the head jams every later ask, where today it strands one and the next still renders.
  Operator chose the unbounded FIFO anyway, declining a bounded wait because that is issue #931's feature arriving through the back door.
- Operator also chose to release pending asks at shutdown as unanswered denials.
  The plan adds `denialReason` to the released decision, which the issue's proposal omits — issue #726's rule is that the agent-facing string and `decidedBy.reason` are the same string.
- The Tidy-First assessor confirmed every structural claim (field counts 5, 9, 6; the shared fixture is the only `AuthorizerSelectionDeps` construction site) and recommended no preparatory commits beyond one optional test cleanup, which was folded in as step 1.
  Its incidental report that `fallow` has a broken `.fallowrc.json` was **not** reproduced: `pnpm --silent fallow guard` works from the repo root, so the error came from its `pnpm -C` invocation.
  Treated as a lead, checked, discarded — no issue filed.
- No follow-up issues filed.
  The two open questions (an idle-threshold release, a core coordination primitive) already have homes in issue #931 and the closed `earendil-works/pi#7007`.

## Stage: Implementation — TDD (2026-09-22T18:05:15Z)

### Session summary

All five TDD steps landed as planned, in five commits.
`AskDialogQueue` now serializes every human-facing ask a session presents, `LocalUserAuthorizer` admits through it with the `permissions:ui_prompt` emit inside the serialized region, and `SessionLifecycleHandler` releases pending asks at `session_shutdown` as unanswered denials.
Test count went 4570 to 4584 (+14) across 167 to 168 files; `check`, root `lint`, full `test`, and `fallow dead-code` all green.

### Observations

- Deviation, step 2: the plan's settle-once killing mutation (dropping `AdmittedAsk`'s double-settle guard) killed **zero** tests.
  `Promise.withResolvers`' `resolve` is already once-only, so the guard was unobservable; it was removed rather than papered over.
  Its replacement mutation — dropping the `isSettled` check in `run` — was *also* green at first, because no existing test ever let a released ask's turn arrive.
  Added "never presents a released ask whose turn arrives afterwards" (release, then answer the stale dialog) to reach that path; it reddens under the mutation.
  This is the real hazard at shutdown, so the gap mattered.
- Deviation, design: the dependency bags take a narrow `AskDialogAdmission` interface rather than the concrete `AskDialogQueue` the plan wrote, matching the `AskDialogRelease` slice the plan already specified and the `code-design` rule against concrete collaborator types.
- Deviation, placement: `SESSION_ENDED_REASON` lives in `src/handlers/lifecycle.ts` (the site that calls `releaseAll`) rather than in `local-user-authorizer.ts`; the released *shape* assertion landed in step 3 with `unansweredDecision` instead of step 4.
- Near-miss probe caught during mutation testing: the composition-root assertion was written as `not.toContain("User denied")`, but `renderUserDenial` produces `The user denied this …`.
  The probe passed under the attribution mutation until it was rewritten against the producer's literal.
- The rest of the plan's mutations behaved exactly as predicted, including the two-class split in step 3: bypassing `dialogs.run` reddens both the non-overlap and the announce test, while hoisting the emit above `run` reddens only the announce test.
- Every file in the plan's `Module-Level Changes` table was touched and no others; both `fix:` subjects name observable outcomes rather than seams.
- Pre-completion reviewer: PASS.
  It re-derived all four mandated invariants independently (fail-open impossibility, the six admission/settle/release interleavings, emit timing on both the TUI and `select` paths, and the absence of a queue/drain cycle) and reported no warnings.

## Stage: Final Retrospective (2026-09-22T19:11:58Z)

### Session summary

Planning, TDD, ship, and retro all ran in one process for third-party issue #965: a FIFO queue now serializes every human-facing permission ask a session presents, and shutdown releases pending asks as unanswered denials.
Five TDD commits plus a plan, two retro stages, and a docs commit landed; `pi-permission-system` released as v33.0.7 with CI green on both the `ci` and `release` runs.
The dominant theme across stages was that mutation verification, not any type or lint gate, is what caught this change's two real test-quality defects.

### Observations

#### What went well

- **Mutation verification earned its keep twice in one session, on independent defects.**
  The plan's step-2 "settle-once" mutation killed zero tests, which exposed that `Promise.withResolvers` already delivers once-only settling; its replacement (dropping `run`'s `isSettled` guard) was *also* green, which exposed a genuine coverage hole — no test ever let a released ask's turn arrive, which is precisely the shutdown hazard the guard exists for.
  Neither `tsc`, `biome`, `eslint`, the full 4584-test suite, nor the pre-completion reviewer would have caught either one.
- **The upstream claim was verified against the published artifact, not just the tracking checkout.**
  `showExtensionCustom` was read in the `@earendil-works/pi-coding-agent@0.87.0` tarball and confirmed byte-identical to `../pi` at `d1230ea20`, so "core still has this defect" rests on what ships rather than on what `main` happens to hold.
  Reading `Container.clear()` in the same pass established that an evicted component cannot detect its own eviction, which killed a self-healing design before it was proposed.
- **Enumerating the other writers of the shared slot changed the plan.**
  `@eko24ive/pi-ask` (`ctx.ui.custom` with no options, mounted from a tool call) and `pi-subagents`' session navigator are real co-writers; our own `/permission-system` modal is not, because it uses `{ overlay: true }`.
  That enumeration is what turned the foreign-clobber jam from an unknown into a priced, operator-accepted regression in the plan's Risks.
- **A subagent's incidental claim was treated as a lead and disproved in one command.**
  The Tidy-First assessor reported `fallow` as having a broken `.fallowrc.json` "worth its own issue"; `pnpm --silent fallow guard` from the repo root works, so the error came from its own `pnpm -C` invocation.
  No issue was filed on a false premise.

#### What caused friction (agent side)

- `premature-convergence` — the plan specified step 2's killing mutation from the code's *shape* (a guard exists, so mutate the guard) without asking what the guard observably changes.
  `AdmittedAsk`'s double-settle guard was redundant with promise semantics, so the mutation could not fail.
  Impact: two mutation rounds (~12 tool calls) before the class was actually pinned.
  The cost bought a real test, so it was not pure waste — but the plan could have specified the right mutation for free.
- `instruction-violation` (self-identified, via mutation testing) — two assertion literals were written from memory instead of copied from the producer, which both the `testing` skill and `/tdd-plan`'s Red step explicitly forbid.
  The composition-root probe asserted `not.toContain("User denied")` where `renderUserDenial` emits `The user denied this …`, and the same test first read `result.message` where `tool-call-boundary.ts` returns `{ block: true, reason }`.
  Impact: the near-miss probe would have shipped as a vacuous assertion had the attribution mutation not been run; ~8 tool calls to locate `renderUserDenial` and repair both.
- `instruction-violation` (self-identified, too late to act on) — the `git-workflow` skill was never loaded during the TDD stage, though `/tdd-plan`'s Load-skills step names it "before the first commit."
  That skill carries the rule that an **accepted design** earns a `Co-authored-by:` trailer whether or not the contributor's patch was taken. #965's design — the FIFO, `releaseAll`, and the unanswered-denial semantics — was adopted close to wholesale from `gitwyy`'s proposal.
  Impact: five commits landed and were pushed and CI-verified before the omission surfaced at ship step 9; attribution was given in the close-comment prose instead, so GitHub records no co-author.
  Not correctable without rewriting pushed history.
- `instruction-violation` (self-identified at lint) — every `[#N]` in the plan was written inside backticks, which `markdown-conventions` states verbatim makes it a code span rather than a link reference, so all twelve `[#N]:` definitions tripped `MD053`.
  The skill had been read minutes earlier in the same stage.
  Impact: one scripted `perl` pass plus `rumdl fmt`, ~3 tool calls.
- `instruction-violation` (self-identified immediately) — the TDD stage note was authored with literal `—` escape tokens in the `Edit` body, which the addendum and `markdown-conventions` both forbid.
  Impact: one `python3` substitution pass.
- `missing-context` — four `grep` attempts preceded the one `colgrep` call that located the agent-facing denial dispatch (`renderRefusal` → `renderUserDenial`).
  "Which function renders the denial for an unavailable decider" is exactly the intent-shaped query the `colgrep` decision table names.
  Impact: ~4 wasted tool calls, no rework.

#### What caused friction (user side)

- Nothing to change.
  Both clarification gates were answered in one round each, and the answers materially shaped the design — the unbounded-FIFO choice and the shutdown-release choice each closed a branch the plan would otherwise have had to hedge.
- One thing worth flagging for a future session rather than fixing now: the operator chose plain "Unbounded FIFO" over "unbounded plus file the jam as its own issue," so the foreign-clobber regression is recorded only in the plan's Risks and the architecture doc's "One dialog at a time" subsection, with no tracking issue.
  That is a deliberate recorded decision; it just means the next session to hit a stuck queue will find prose, not a backlog item.

### Diagnostic details

- **Model-performance correlation** — planning, TDD, and this retro ran on `anthropic/claude-opus-5`; the ship stage ran on `anthropic/claude-sonnet-5`.
  Both subagents (`tidy-first-assessor`, `pre-completion-reviewer`) ran on `anthropic/claude-sonnet-5`, attributed from their own transcripts under `tasks/` rather than from their agent definitions.
  No mismatch to flag: ship is procedural and executed without error, and both subagents produced correct, detailed, judgment-heavy reports — the assessor verified all three structural field counts (5, 9, 6) against the real files and the reviewer independently re-derived four invariants.
  The assessor's one defect (the false `fallow` claim) was a diligence miss about its own environment, not a reasoning-capacity limit.
- **Escalation-delay tracking** — no `rabbit-hole` friction points.
  The longest same-target run was the ~12-call step-2 mutation sequence, but each call changed the hypothesis rather than retrying the same one, and it terminated in a new test.
  Not a flag.
- **Unused-tool detection** — see the `colgrep` item above; it is the only instance, and the tool was eventually reached without prompting.
- **Feedback-loop gap analysis** — no gap.
  `pnpm run check` ran immediately after each interface-widening step, file-scoped `vitest` ran at every red and every green, the full package suite ran at the end of steps 3 and 4, and root `check`/`lint`/`test`/`fallow dead-code` ran at both the baseline and the end of the cycle.
  The two ship-time gates (`lint`, `fallow dead-code`) ran on the exact tree that was pushed.

### Changes made

1. `.pi/prompts/plan-issue.md` — in the `Decide` step's third-party branch, record the resolved `Co-authored-by:` trailer in the plan's TDD Order when the operator adopts a third-party design.
   `/plan-issue` is the only stage that establishes third-party authorship, and the plan is the artifact that carries it to the committing stage.
2. `.pi/skills/testing/SKILL.md` — under `### TDD planning rules` → `### Step sequencing and breakage`, a guard-deleting killing mutation must name what observably changes without the guard, or it is vacuous.

Considered and rejected, with reasons, in the proposals above: emphasis for the `MD053` backtick slip and the `\uXXXX` escape slip (both already stated verbatim in `markdown-conventions`), a rule that a replacement mutation be re-verified (already covered by the red-count rule, and done unprompted here), widening the Tidy-First "a count is a lead" rule to environmental claims (verified unprompted here), and a `colgrep`-reach-sooner rule (already in the `colgrep` decision table).
