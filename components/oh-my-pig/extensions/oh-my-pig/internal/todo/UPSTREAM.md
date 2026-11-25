# Upstream Reference: `todo`

Ported from omp (`oh-my-pi`, tag `v19.1.1`, commit `31ffee513f`, MIT).

## Upstream files followed
- `packages/coding-agent/src/tools/todo.ts` — tool schema (`init`, `start`, `done`, `rm`, `drop`, `append`, `view`), state reducer, `normalizeInProgressTask`, `inferTodoOp`, `formatSummary`, `getLatestTodoPhasesFromEntries` (including legacy `blocked` -> `pending` normalization), `TODO_HUD_STATE_CUSTOM_TYPE` (`todo_hud_state`), `getLatestTodoSnapshotIdentity`, `getTodoHudVisibility`, `createTodoHudStateData`, `phasesToMarkdown`, `markdownToPhases`, `resolveTodoMarkdownPath` (including `~` expansion), exclusive (`sequential`) execution mode, label `"Todo"`, `PrepareArguments`.
- `packages/coding-agent/src/prompts/tools/todo.md` — verbatim tool description (embedded via `//go:embed`, trailing newline trimmed as `prompt.render` does).
- `packages/tui/src/tools/todo.ts` — `TodoStatus`, `TodoOperation`, `TodoItem`, `TodoPhase`, `TodoToolDetails`, `todoTaskTargets`, `todoMatchesAnyDescription`, `isClosedTodo`, `selectCollapsedTodos`, `phaseRomanNumeral`, `formatPhaseDisplayName`, display sanitization.
- `packages/coding-agent/src/modes/interactive-mode.ts` — HUD widget layout (`#renderTodoList`, `#getActivePhase`, `#formatTodoLine` with semantic theme styling via `internal/ansi` for every status + superscript note markers + progress-filled spine/tail, `WidthAwareText(formatRows, 1, 0)` 1-column left/right padding and hanging width-aware wrapping via `extensions/sdk/tui` inside `SetWidgetRenderer` on terminal surfaces, and `BuildNativeWidgetView` emitting the structured `kit.View` rooted at `omp.hud.todo` via `SetWidget` when `surface.Native()` is true, updating on `surface.OnChange`), auto-clear timer (`#syncTodoHudState` / `#cancelTodoAutoClearTimer` with `tasks.todoClearDelay` and generation guard), and persisted HUD visibility (`dismissed` on auto-clear, `revealed` on `/todo expand`, restored on `session_start`/`session_compact`/`session_tree`).
- `packages/coding-agent/src/modes/controllers/todo-command-controller.ts` — `/todo` slash command verbs (`expand`, `collapse`, `edit` via `ctx.Editor`, `copy` via `ctx.CopyToClipboard`, `export`, `import`, `append`, `start`, `done`, `drop`, `rm`, `help`), fuzzy matching, manual-edit `<system-reminder>`.
- `packages/coding-agent/src/session/todo-tracker.ts` — stop completion reminder (`checkCompletion` with `reminderAwaitingProgress`, `before_agent_start` reset, `tool_result` progress clearing, `isAwaitingUserAnswer`, `hasWakeSources` via `internal/jobs` + `HasPendingMessages`, plan-mode suppression via `gate.PlanActive`, print/json mode suppression, error/tool-call stop suppression, active-tool roster guard) and mid-run mutation nudges (`onTurnEnd` on tool-result turns, suppressed in plan mode).
- `packages/coding-agent/test/tools/todo.test.ts`, `agent-session-todo-reminder-loop.test.ts`, `print-mode-todo-reminder.test.ts`, `modes/controllers/todo-command-controller.test.ts`, `interactive-mode-todo-clear.test.ts` — ported unit and handler tests.

## Settings read by omp

Settings are read via package variable `todo.Setting func(ctx sdk.Context, key string) any` (assigned by the integrator; `nil` uses omp's defaults).

| Key | Default | Honoured |
|---|---|---|
| `todo.enabled` | `true` | Honoured in stop reminder & mid-run nudge hooks. |
| `todo.reminders` | `true` | Honoured in stop reminder & mid-run nudge hooks. |
| `todo.remindersMax` | `3` | Honoured (default `3`). |
| `todo.midRunNudges` | `"capped"` | Honoured (`"off"`, `"capped"`, `"backoff"`). |
| `todo.eager` | `"default"` | Not ported (see below). |
| `todo.backend` | `"native"` | Not ported (see below). |
| `tasks.todoClearDelay` | `60` | Honoured (`< 0` or non-finite disables auto-clear; `0` clears immediately; default `60` seconds). |

## Tests ported

20 test functions in `todo_test.go` running through the real tool/command/event handlers via a fake PiG host, covering 39 cited upstream test cases from `todo.test.ts`, `agent-session-todo-reminder-loop.test.ts`, `print-mode-todo-reminder.test.ts`, `todo-command-controller.test.ts`, and `interactive-mode-todo-clear.test.ts`.

## Not ported

| Feature | Why not ported |
|---|---|
| `/todo edit` in external `$EDITOR` | PiG's extension SDK cannot suspend/resume the host TUI to spawn an interactive `$EDITOR`; `/todo edit` uses `ctx.Editor` for an in-process Markdown edit round-trip instead. |
| Subagent-matched HUD live highlighting & auto-completion (`#getActiveSubagentDescriptions`, `#reconcileTodosWithSubagents`) | `TodoMatchesAnyDescription` and `SelectCollapsedTodosMatched` are ported, but `internal/jobs` does not emit live HUD refresh triggers for subagent description changes; requires frontend/integrator wiring. |
| Animated completion strikethrough & native TSP `tree` fallback rendering (`todoToolRenderer`, `TodoHudContainer`) | Frame-driven strike animations belong to the frontend lane (`piglets/pig-tern/frontend/`); the native TODO HUD `checklist[omp.hud.todo]` structured `kit.View` is emitted by `BuildNativeWidgetView` when `surface.Native()` is true. |
| Compact one-line HUD under 18 terminal rows | `SetWidgetRenderer` callback receives width only, not terminal height, and `ctx.Height()` is not queried per resize in a widget renderer without a host resize event hook. |
| `todo.toggle` keybinding (`toggleTodoExpansion` shortcut) | PiG's extension host has no global keybinding hook to bind omp's `todo.toggle` shortcut; `/todo expand` and `/todo collapse` are ported, with `/todo expand` canceling auto-clear, revealing a dismissed HUD, and persisting `todo_hud_state` (`visibility: "revealed"`). |
| Eager todo first-turn prelude (`createEagerTodoPrelude`, forced `tool_choice`) | PiG's extension SDK does not expose forced `tool_choice` or prewalk handoff state. |
| Failed-todo error reminder (`agent-session.ts:5252-5275`) | PiG's `tool_result` event does not allow returning a continuation/developer message directly without an extra turn hook; omitted rather than diverging from omp's turn timing. |
| `todo.backend` extension backend mirroring (`tools/settings.ts:468-482`) | No external todo-backend extension contract exists in PiG. |
| `todo_reminder` session event & terminal title refresh on `init` | PiG's extension SDK has no custom session-event emitter for `todo_reminder` and no host title-summary pipeline. |
| `user-force` tool-choice suppression on stop reminder | PiG's extension SDK does not expose the last served `tool_choice` label. (Plan-mode suppression is honoured via `gate.PlanActive`.) |
| `details` (`{phases, storage}`) on lenient `PrepareArguments` error (e.g. invalid `op`) | Not observable on the PiG host: `PrepareArguments` returns `(prepared, error)` before schema validation and `agent/tool_execution.go:378-381` wraps any error into `errorToolResult(err.Error())` with empty `{}` details (unlike omp's `lenientArgValidation` which reaches `execute()` and returns `{phases, storage}`). |
