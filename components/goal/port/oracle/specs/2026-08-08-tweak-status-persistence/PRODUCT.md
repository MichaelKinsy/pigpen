# Product: Tweak status persistence + proposal presentation fixes

## Status

Implementing. Audit complete; fixes and regression tests pending.

## Outcome

Goal-tweak must never change the completion state of steps that persist across
the revision, the confirmation dialog must keep the auditor toggle usable on
bounded terminals, and a proposal must present exactly one task set that
matches what is persisted.

- `/goal-tweak` merges proposed task lists into the existing tree by id:
  surviving ids keep `status`/`evidence`/`completedAt`/`skippedAt`/`skipReason`
  unchanged, new ids start pending, removed ids drop, and `currentTaskId`
  survives only while its task is still pending.
- The proposal confirmation dialog shows the auditor on/off status and accepts
  the `a` toggle with visible feedback even when the terminal-height churn
  guard bounds the dialog, and it keeps the proposed task lines in frame.
- A proposal shows exactly one task set: explicit tasks when proposed; the
  retained current list for a tweak without explicit tasks; the derived
  fallback (new drafts only) deriving from the same objective text the apply
  path persists.

## Requirements

### R1 — Tweak status preservation

- A `/goal-tweak` that proposes a task list merges it into the existing tree by
  id (§7.5). Matching ids keep `status`, `evidence`, `completedAt`, `skippedAt`,
  `skipReason`; new ids start pending; removed ids drop; nested subtasks merge
  with the same semantics.
- A `/goal-tweak` without a task list retains the current list unchanged,
  including all progress fields.
- `currentTaskId` survives the tweak only while its task is still pending in
  the merged tree; it clears when the task is completed, skipped, or removed.
- The persisted goal file round-trips every progress field (JSON meta block is
  authoritative; the markdown `## Tasks` section is a summary).

### R2 — Auditor toggle at goal propose

- The proposal confirmation dialog always renders the current auditor status
  line ("● Auditor enabled / ○ Auditor disabled (press 'a' to toggle)").
- Pressing `a` toggles the status with visible feedback.
- The churn guard (terminal-height bound) must protect the auditor line and the
  proposed task lines within the bound; the dialog frame never exceeds the
  terminal height.

### R3 — Single task set per proposal

- The scrollback presentation (`propose_goal_draft` renderCall), the
  confirmation dialog, and the persisted goal file show the same task list.
- Tweak without explicit tasks: renderCall shows the retained current list, not
  a phantom derived-from-objective list.
- New draft without explicit tasks: the derived preview derives from the same
  objective text as the apply path (shown == persisted).

## Status (behavior)

- Auditor status can no longer be seen or changed in the proposal confirmation
  dialog on bounded terminals (regression from the pi 0.84 height-bound +
  complete-presentation work, commits `7bc07ee` / `b9cb6a6`).
- The bounded dialog shows an empty `┌─ TASKS ─┐` box: task lines are dropped
  because the segment scan is not ANSI-aware.
- The scrollback presentation for a tweak without explicit tasks shows a
  derived-from-objective list that is never persisted.
