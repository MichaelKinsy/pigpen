# Upstream Reference: `todo`

Ported from omp (`oh-my-pi`, commit `4df653426f40`, MIT).

## Upstream files followed
- `packages/coding-agent/src/tools/todo.ts` — tool schema, state reducer (`init`, `start`, `done`, `rm`, `drop`, `block`, `unblock`, `append`, `view`), `normalizeInProgressTask`, `inferTodoOp`, `formatSummary`, `getLatestTodoPhasesFromEntries`, `phasesToMarkdown`, `markdownToPhases`, `resolveTodoMarkdownPath`.
- `packages/coding-agent/src/prompts/tools/todo.md` — verbatim tool description (embedded via `//go:embed`).
- `packages/tui/src/tools/todo.ts` — `TodoStatus`, `TodoOperation`, `TodoItem`, `TodoPhase`, `TodoToolDetails`, `isClosedTodo`, `selectCollapsedTodos`, `phaseRomanNumeral`, `formatPhaseDisplayName`, display sanitization.
- `packages/coding-agent/src/modes/interactive-mode.ts` — HUD widget layout (`#renderTodoList`, `#formatTodoLine`).
- `packages/coding-agent/src/modes/controllers/todo-command-controller.ts` — `/todo` slash command verbs, fuzzy matching, manual-edit `<system-reminder>`.
- `packages/coding-agent/src/session/todo-tracker.ts` — stop completion reminder (`checkCompletion`).
- `packages/coding-agent/test/tools/todo.test.ts` — ported unit tests (28 upstream cases + Go replay/widget/command/reminder tests).

## Not ported

| Feature | Why not ported |
|---|---|
| `/todo edit` (external `$EDITOR` round-trip) | Requires suspending/resuming the host terminal UI (`ctx.ui.stop()` / `ctx.ui.start()`) and spawning an interactive editor, which PiG's extension SDK does not expose. |
| `/todo copy` (system clipboard) | No cross-platform clipboard helper in PiG's extension SDK or Go stdlib without shelling out or adding a third-party dependency. |
| Subagent-matched todo highlighting & auto-completion (`todoMatchesAnyDescription`, `#reconcileTodosWithSubagents`) | Depends on omp's observer registry of live subagents, which is not part of this extension area or PiG's SDK. |
| Animated completion strikethrough & native TSP `checklist` / `tree` node rendering (`todoToolRenderer`, `TodoHudContainer`) | PiG's widget API (`SetWidget`) takes `[]string` lines; terminal-native TSP checklist nodes and frame-driven strike animations are omp TUI-specific. |
| Auto-clear timer for completed todo HUD (`#todoAutoClearTimer`) and persisted HUD visibility (`todo_hud_state`) | Interactive TUI timer/dismissal state; widget stays visible until cleared or session shutdown. |
| Eager todo first-turn prelude (`createEagerTodoPrelude`, forced `tool_choice`) and mid-run mutation nudge (`takeMidRunNudge`) | Requires host-level `tool_choice` forcing and settings (`todo.eager`, `todo.reminders`) not exposed by PiG's SDK. |
| `isAwaitingUserAnswer` heuristic and `hasPendingAsyncWake` suppression on stop reminder | `agent_before_settle` in PiG's SDK does not expose pending async job state; the reminder fires whenever open (`pending`/`in_progress`) todos remain up to `defaultRemindersMax` (3). |
