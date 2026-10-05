# Milestones: Fix open issues #15–#22

## Plan

1. Create `fix/open-issues` branch off `main`.
2. Re-verify each issue against current code (all four confirmed present in
   v0.25.0; #19 partially fixed — prompt/save already row-driven, bound not).
3. Implement fixes #19, #20+#21, #22 with regression tests.
4. Close junk issues #15–#18 via `gh` with an explanatory comment.
5. Validate: `npm run check` + full `npm test`, CHANGELOG + patch bump,
   map issue → evidence here, commit everything on the branch.

## 2026-08-06 — branch + scaffolding

- `git checkout -b fix/open-issues` off `main` (013cfb9).
- Scaffolded `specs/2026-08-06-fix-open-issues/` (PRODUCT.md, TECH.md, this log).
- Confirmed via questionnaire: close #15–#18 via gh; full fix level (fixes +
  regression tests + spec dir + CHANGELOG per AGENTS.md).

## 2026-08-06 — issue-by-issue verification against v0.25.0

- #19: `extensions/goal-commands.ts` — `positiveInteger` branch hard-codes
  `Number(trimmed) < 1`; prompt/save already row-driven. CONFIRMED.
- #20: `extensions/goal-auditor.ts:62` — regex over whole report. CONFIRMED.
- #21: `extensions/goal-auditor.ts:98` — objective/completion summary/detailed
  summary/contract/warm context interpolated raw. CONFIRMED.
- #22: `extensions/goal-core-tools.ts:174` + agent-pause flow — result text and
  `terminate: true` unconditional; `result.ok` only gates side effects.
  CONFIRMED.

## 2026-08-06 — fixes implemented with regression tests

- **#19** `extensions/goal-commands.ts`: `positiveInteger` branch now derives
  `min = row.key === "stallTimeoutMinutes" ? 0 : 1`; warning message is
  row-specific (`must be an integer >= ${min}`).
  Tests: `tests/goal-command-palette.test.ts` — "stall timeout row accepts 0
  (row-driven lower bound)" + "subtaskDepth rejects 0 (min 1) and never saves
  it".
- **#20** `extensions/goal-auditor.ts`: `parseAuditorDecision` now trims and
  filters lines and exact-matches the LAST non-empty line against
  `<approved/>` / `<disapproved/>`.
  Tests: `tests/goal-auditor.test.ts` — final-line contract suite (#20);
  updated existing mixed-marker assertion + `tests/goal-golden.test.ts` golden
  pins the final-line contract.
- **#21** `extensions/goal-auditor.ts`: new `escapePromptPayload` (
  `&`→`&amp;`, `<`→`&lt;`, `>`→`&gt;`) applied to objective, executor claim,
  goal details, verification contract, warm context, and task titles.
  Tests: `tests/goal-auditor.test.ts` — "escapes payloads so delimiters cannot
  be closed early (#21)" + "escapes verification contract, warm context, and
  task titles (#21)".
- **#22** `extensions/goal-core-tools.ts`: `runGoalBlockedFlow` and
  `runGoalAgentPauseFlow` return the apply failure message with
  `terminate: false` when `!result.ok` (mirrors `runGoalCompletionFlow`),
  leaving disk state untouched.
  Tests: `tests/goal-core-tools.test.ts` — "update_goal(blocked) surfaces an
  apply failure instead of claiming success (#22)" + "update_goal(paused)
  surfaces an apply failure instead of claiming success (#22)" (both
  monkey-patch `core.goalService.apply`).

## 2026-08-06 — junk issues closed

- `gh issue close` on #15, #16, #17, #18 with comment "Invalid: filed with an
  empty body by mistake..." — all now CLOSED; `gh issue list --state open`
  shows only #19–#22.

## 2026-08-06 — validation and wrap-up

- `npm run check` (tsc --noEmit): clean.
- `npm test`: 662/662 pass, 0 failures.
- CHANGELOG: `## [0.25.1] — 2026-08-06` entry with all four fixes; package.json
  bumped 0.25.0 → 0.25.1.
- All work committed on `fix/open-issues`.

## 2026-08-06 — escape no longer stops the working (user report, same branch)

User report: "escape is no longer stopping 'working', but is pausing a goal."
Desired: live goal + Escape → pause goal; paused goal + Escape → stop current
turn ("this is how it used to work before we fixed the escape behaviour").

Investigation:
- The live-goal Escape branch in `goal-widget.ts`
  `syncTerminalInputPause` has paused synchronously AND consumed the key
  (`{ consume: true }`) since the runtime overhaul (967bc7d) — pi never sees
  the Escape, so the running tool execution continues after the goal flips to
  paused. That matches the report exactly.
- `ExtensionContext` exposes `abort()` and `signal`; pi's native Escape
  handling aborts the running tool execution, and `agent_end`/`turn_end`
  already cascade `pauseActiveGoal` on aborted turns (no-op when already
  paused).
- Paused-goal Escape already fell through to `undefined` (no other Escape
  consumer in the extension), so only the live branch needed changing.
- User decision (goal questionnaire): commit the validated pending
  #19–#22 work first, keep the escape fix on the same `fix/open-issues`
  branch, commit directly.

Fix: live-goal branch keeps `core.pauseActiveGoal(ctx)` but returns
`undefined` (passes Escape back to pi, which aborts the current turn).
Comments updated to explain the consume-vs-passthrough split (audit: consume;
pause: passthrough).

Tests: `tests/goal-modal-escape.test.ts` — "Escape on a live goal pauses AND
passes the key back to pi (stops the working)" + "Escape while the goal is
paused passes through to pi without any goal state change"; pre-existing
modal-guard + pause regression tests unchanged and green (4/4).

Validation: `npx tsx --test tests/goal-modal-escape.test.ts` 4/4 pass;
full `npm run check` + `npm test` green; CHANGELOG 0.25.1 entry extended with
the Escape fix; committed on `fix/open-issues`.
