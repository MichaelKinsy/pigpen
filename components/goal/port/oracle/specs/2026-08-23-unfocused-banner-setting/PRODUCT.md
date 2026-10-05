# PRODUCT — Optional unfocused UI suppression (clean rewrite of PR #29)

## Problem

With no focused goal but open goals present, pi-goal-x always renders an
above-editor unfocused widget and a `goal: unfocused [N open] - /goal-focus`
status hint. Some users prefer a quiet terminal while keeping goals open.
PR #29 proposed a setting but tied it to a single project file and did not
refresh live; the clean rewrite layers it and refreshes immediately.

## User-visible behavior

A layered boolean `hideUnfocusedBanner` (default false) suppresses BOTH the
above-editor unfocused goal widget and the unfocused status hint when ALL of:

- the session has a UI,
- no goal is focused,
- one or more goals are open,
- the effective setting is true.

Layering follows the standard order: environment-independent file resolution
`project > global > defaults`; explicit project `false` overrides a global
`true`. Toggling the setting in `/goal-settings` takes effect immediately —
no refocus, restart, or manual refresh required.

The setting never hides: a focused goal dashboard, audit UI, notifications
explicitly requested by the user, or any slash command. It does not focus or
unfocus anything and does not change open-goal storage.

## Model-facing safety invariant

The `[PI GOAL UNFOCUSED]` system-prompt guidance ("Do not choose or switch
focus autonomously") is unchanged in every state. This setting is purely
visual.
