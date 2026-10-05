# Product: Tweak auto-resume, auditor persistence through tweaks, and compact auditor toggle

## Status

Confirmed goal (2026-08-07), implementation in progress.

## Motivation

Three small papercuts around goal lifecycle and the auditor — and one hard limit that
no longer needs to be hard:

1. **Tweaking a paused/blocked goal leaves it stalled.** `/goal-tweak` is a
   deliberate user revision of the goal, but the confirm path rewrites the
   objective/tasks/auditor fields without touching `status` — a goal that was
   paused or blocked stays paused/blocked after the tweak, so the user has to
   manually `/goal-resume` afterwards.
2. **The auditor on/off status does not survive a tweak.** When a draft
   starts, `startGoalDrafting` initializes the confirmation dialog's auditor
   toggle from the *global* settings (`auditor disabled`), not from the
   target goal's persisted per-goal `skipAuditor`. A goal created with the
   auditor disabled silently re-enables it when the user confirms a tweak
   without touching the toggle.
3. **The auditor status is invisible and not toggleable from the UI.** The
   per-goal `skipAuditor` flag only shows up implicitly at completion
   ("per-goal auditor disabled"). The compact dashboard says nothing about
   it, and there is no way to flip it without editing the goal file.
4. **The 4000-character objective limit is hard-coded.** Long objectives
   (large plans, detailed success criteria) hit an arbitrary wall at
   create_goal / propose_goal_draft / /goal-tweak. It should be a
   configurable setting with no limit by default.

## Behavior

### 1. Tweak confirm auto-resumes a stalled goal

When `/goal-tweak` is confirmed and applied, and the goal's status is
`paused` or `blocked`, the goal transitions to `active` and the pause
metadata is cleared (`stopReason`, `pauseReason`, `pauseSuggestedAction`),
mirroring `/goal-resume`. A `goal_resumed` ledger event is appended
(reason `tweak`), accounting restarts, and continuation is queued again so
an auto-continue goal picks up right away.

- `budget_limited` stays a hard resource gate: a tweak does **not** resume a
  budget-limited goal (user decision).
- An `active` goal stays `active` (no-op transition).
- Surviving task statuses are untouched (§7.5 merge rule stays).

### 2. Tweak confirmation defaults to the goal's persisted auditor setting

`startGoalDrafting` in tweak mode initializes `draft.auditorEnabled` from the
target goal's persisted per-goal `skipAuditor`:

- `skipAuditor: true` → dialog toggle defaults to **auditor disabled**
  (confirming without touching the toggle keeps it disabled — no silent
  re-enable).
- `skipAuditor: false` → defaults to **auditor enabled**.
- `skipAuditor` unset → fall back to the global `auditor disabled` setting
  (today's behavior).

The confirm path already writes `skipAuditor` from the dialog's toggle, so
the fix is only the default shown.

### 3. Auditor status in the compact dashboard, toggleable

- The compact dashboard shows the focused goal's auditor status as a single
  dot integrated into the bottom-right of the box border: `●` green when the
  auditor is on, muted gray when off (no standalone content line — the
  border dot is the whole indicator). The dot survives every layout mode
  (wide ≥100 / medium 70–99 / narrow 50–69 / minimal <50). In wide and
  medium layouts the footer right-aligns a `Ctrl+Shift+A: toggle auditor`
  note (same frame tone as the hint) directly left of the dot, making
  explicit that the chord turns the auditor on and off; narrow and minimal
  keep just the dot.
- `Ctrl+Shift+A` (a chord pi never binds; fallback to another unbound
  ctrl+shift chord if it collides) toggles the focused goal's auditor
  on/off: the new `skipAuditor` is persisted to the goal file
  (revision-safe `goalService.apply`), an `auditor_toggled` ledger event is
  appended, the dashboard refreshes, and a notification confirms the new
  state.
- Guards: inert when no goal is focused, when a goal-owned modal
  (draft/audit/settings/task overlay) is open, and on completed goals (the
  completion gate is the only consumer of `skipAuditor` for archived goals).
- The expanded dashboard is byte-identical (display stays compact-only; the
  shared model gains the field but the expanded renderer does not use it).

### 4. Objective length limit becomes a configurable setting (no limit by default)

The hard 4000-character objective cap is removed. In its place:

- New setting `objectiveMaxChars` (settings file + env override
  `PI_GOAL_OBJECTIVE_MAX_CHARS`, surfaced in the settings menu and
  `/goal-status`): `0`/unset = **no limit** (the default); a positive value
  caps objective length.
- Enforced consistently at every entry point that accepts a goal objective:
  `create_goal`, `propose_goal_draft` (draft mode), and `/goal-tweak`
  `<new objective>`. Rejections report the configured limit and the given
  length; the goal is never mutated by a rejected objective.
- The settings menu shows the row under **Goal behavior** ("max objective
  length (0 = none)", integer >= 0, defaults to 0) and `/goal-status`
  reports the effective value with provenance.

## Acceptance summary

- Tweak of paused/blocked goal → `active` + pause metadata cleared +
  `goal_resumed` (reason `tweak`); `budget_limited`/`active` unchanged.
- Tweak of a `skipAuditor: true` goal → confirmation dialog defaults to
  auditor disabled; confirming keeps it disabled.
- Compact dashboard shows the auditor status as a bottom-right border dot
  (green on / gray off, all layout modes) with a right-aligned
  `Ctrl+Shift+A: toggle auditor` note in wide/medium; `Ctrl+Shift+A` flips
  it with persisted write + ledger event + notification.
- Expanded dashboard and completion audit gate unchanged.
- Objectives longer than 4000 characters are accepted by default; a
  configured `objectiveMaxChars` caps them at every entry point.
