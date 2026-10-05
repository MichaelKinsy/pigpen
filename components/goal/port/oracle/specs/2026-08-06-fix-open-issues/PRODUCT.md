# Product: Fix open issues #15–#22 (2026-08-06 round)

## Status

Shipped (all four actionable issues fixed on `fix/open-issues` with
regression tests, spec docs, CHANGELOG entry, patch bump to 0.25.1; junk
issues #15–#18 closed; plus the Escape-stops-the-working fix below).

## Outcome

Address the four actionable open issues filed on `tmonk/pi-goal-x`
(v0.23.0-era findings, re-verified against current v0.25.0 code on the
`fix/open-issues` branch):

- **#19** — Settings menu positive-integer rows: the prompt and save are
  already row-driven, but the validation lower bound is hard-coded to `1`, so
  `stallTimeoutMinutes` (default `0`, meaning "no stall timeout") can never be
  set to `0` and its error message is wrong. The lower bound must be row-driven:
  `0` for `stallTimeoutMinutes`, `1` for `subtaskDepth`.
- **#20** — `parseAuditorDecision` matches `<approved/>`/`<disapproved/>`
  anywhere in the report via regex, but the auditor prompt requires the marker
  to be the *final line*. A report that merely mentions the marker in prose can
  be misread as an approval. Parse must require the marker as the last
  non-empty line.
- **#21** — `buildGoalAuditorPrompt` interpolates the objective, completion
  summary, and other payloads raw between XML-ish delimiters; a payload
  containing `</objective>` closes the block early and the remainder reads as
  instructions. Payloads must be escaped (`&`, `<`, `>`) before interpolation.
- **#22** — `runGoalBlockedFlow` and the agent-pause flow return a success text
  and `terminate: true` even when `goalService.apply` failed; the goal stays
  active on disk while the agent is told it was blocked/paused. On failure the
  flows must surface the mutation error and NOT terminate, so the agent can
  retry (mirrors the existing `runGoalCompletionFlow` failure pattern).

Also close the four empty-body junk issues **#15–#18** via `gh` with a comment
noting they are invalid duplicates of #19–#22.

## Escape stops the working (post-#19–#22 follow-up)

User-reported regression (same branch, same day): pressing Escape no longer
stops the goal's "working" — it only pauses the goal. Desired behavior:

- **Live goal (active + autoContinue) + Escape → pause the goal AND stop the
  current turn.** The pause path consumed Escape (`{ consume: true }`) and
  never aborted the in-flight tool execution, so the agent kept working after
  the goal flipped to paused.
- **Paused goal + Escape → stop the current turn.** Escape must pass back to
  pi (not be consumed), aborting the running tool execution; goal state stays
  unchanged.

Fix in `extensions/goal-widget.ts` (`syncTerminalInputPause`): the live-goal
branch still calls `pauseActiveGoal(ctx)` but returns `undefined` instead of
`{ consume: true }`, so pi also receives the key and aborts the current turn.
The abort cascade (`agent_end`/`turn_end` → `pauseActiveGoal`) is a no-op
because the goal is already paused. The paused-goal case already fell through
(after the modal-depth guard, audit-abort branch, and expanded-dashboard
collapse) and is pinned by a new regression test. Audit-abort consumption and
the modal guard are unchanged.

## Scope

In scope: the four fixes above with regression tests, spec docs, CHANGELOG
entry + patch bump (0.25.1), full test suite + typecheck green, junk issues
closed.

Out of scope: upstream `capyup/pi-goal` (no open issues), unrelated
refactors, release/PR publishing unless the user asks.
