# TECH — Layered global settings

## Sparse layers vs resolved settings

Two distinct types (never one interface for both):

- `GoalSettingsLayer` — sparse file contents: every field optional, booleans
  tri-state (`true`/`false`/absent), integers optional (`0` meaningful),
  keybindings sparse per leaf.
- `GoalSettings` (= exported alias of the fully resolved shape) — what runtime
  code consumes, with concrete defaults filled in.

`GoalSettingsLayer` is never passed to prompt/runtime code.

## Pure path resolution

```ts
resolveAgentDir(env, homeDir)   // PI_CODING_AGENT_DIR (abs or ~-relative) else ~/.pi/agent
goalGlobalSettingsPath(env?, homeDir?)   // PI_GOAL_GLOBAL_SETTINGS_FILE else <agentDir>/pi-goal-x-settings.json
goalSettingsPath(cwd, env?)              // unchanged project path
```

Relative overrides resolve against the injected `homeDir`/`cwd`; tests pass
isolated env objects and never mutate `process.env`.

## Parse result and diagnostics

```ts
SettingsDiagnosticCode = "invalid_json" | "not_object" | "unknown_key" | "invalid_value" | "invalid_nested_key"
SettingsDiagnostic { scope, path, settingPath?, code, message }
SettingsLayerRead { scope, path, status: "ok"|"missing"|"invalid", layer, diagnostics, fingerprint }
```

`parseSettingsLayer(raw, scope, path)` never throws for content problems:
unknown keys and invalid values become diagnostics; all valid known keys are
still applied. A file whose JSON cannot be parsed at all yields status
`invalid` with an empty layer.

## Resolution and provenance

Per leaf:

    env value ? environment : project value ? project : global value ? global : default

`loadSettingsSnapshot(cwd, env)` returns `{ global, project, value,
provenance, diagnostics }` where `provenance: Map<pathKey,
ResolvedSetting<unknown>>` records each leaf's source (+ env var name when
relevant). Nested keybindings resolve leaf by leaf from the sparse layers —
never via a pre-defaulted object.

Boolean leaves honor explicit `false` (e.g. project `disableTasks: false`
over global `true`). Integer leaves honor explicit `0`
(`stallTimeoutMinutes`, `objectiveMaxChars`).

Cache: mtime-independent zero-op cache keyed by absolute path per layer;
extension writes invalidate only the affected path; `session_start`
invalidates everything (existing behavior).

## Conflict-safe scoped mutation

```ts
mutateSettingsLayer({ scope, cwd, env?, mutation }) -> SettingsSnapshot
mutation := { op: "set", path, value } | { op: "unset", path }
```

Under a generic filesystem path lock (`<target>.lock`, bounded attempts,
stale-TTL recovery — same discipline as the goal lock): re-read the target
layer FRESH (cache bypass), refuse to overwrite an invalid layer, apply the
path mutation to a structured clone, prune empty objects, re-parse (abort on
invalid-value diagnostics), write atomically (same-dir temp `wx` + fsync +
rename + directory fsync best-effort + existing-mode preservation, default
0o600), invalidate that cache entry, and return a fresh snapshot.

## Settings UI semantics

Every row shows `<label>: <effective value> (<source>)`. Selecting a row opens
an action menu (never an implicit toggle based on raw file values): boolean →
set true / set false / use inherited value; integer → set override / use
inherited; model rows keep the existing picker plus explicit inherit. Global
scope has no "inherit global" (it deletes the override to fall back to
defaults). Headless mode reports both paths read-only.

After every mutation the central side-effect hook reapplies the effective
settings (task-tool profile reinstall on disableTasks change) and refreshes
the UI.

## Tests

Path resolution (injected env/home), layering matrix (project false over
global true, project zero over global positive, env over both, nested leaf
inheritance, partial keybinding override), diagnostics (unknown key with valid
values still applying; malformed global with valid project; invalid layer not
overwritten without repair), UI provenance display, unset round trip,
two-process lost-update race, stale lock recovery, failed temp-write
preserving the original, symlink refusal, `/goal-refresh` external-change
detection, headless report. Race coverage is runnable via
`npm run test:settings-race`.
