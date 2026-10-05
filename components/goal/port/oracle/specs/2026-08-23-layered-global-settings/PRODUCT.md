# PRODUCT — Layered global settings (clean rewrite of PR #27)

## Problem

Settings lived only in a per-project file (`<cwd>/.pi/pi-goal-x-settings.json`).
Users with many projects had to duplicate configuration. PR #27 proposed a
global layer, but its implementation conflated sparse file contents with
resolved runtime values, rejected whole files over unrelated unknown keys,
dropped explicit `false`/`0` overrides (making project-over-global impossible
for disabling), saved cached whole objects (lost-update risk across
processes), and wrote non-atomically.

## User-visible behavior

Settings resolve per leaf in this order:

    environment > project layer > global layer > defaults

Files:

    global:  ${PI_CODING_AGENT_DIR:-~/.pi/agent}/pi-goal-x-settings.json
    project: <cwd>/.pi/pi-goal-x-settings.json

Overrides: `PI_GOAL_GLOBAL_SETTINGS_FILE`, `PI_GOAL_SETTINGS_FILE`, and the
existing per-setting `PI_GOAL_*` variables.

- Explicit `false` overrides an inherited `true`; explicit `0` overrides an
  inherited positive value.
- Nested keybindings inherit per leaf; a project overriding one key does not
  reset other global keys.
- `/goal-settings` shows each row's effective value and source
  (`environment`, `project override`, `inherited from global`, `default`) and
  can set a project/global override or remove one to return to inheritance.
- Malformed files are diagnosed by scope and path; valid known values in the
  same file still load when unrelated unknown keys are present.
- Writes never lose concurrent changes from another process (path lock) and
  are atomic (temp file + rename + mode preservation).
- Steady-state reads remain zero-op (cache); `/goal-refresh` invalidates and
  re-reads both layers and reports their fingerprints separately.
- No global file means behavior is identical to project-only behavior.

## Out of scope

- Any change to what settings mean; only where they come from and how they're
  edited. The model-facing behavior of every setting is unchanged.
