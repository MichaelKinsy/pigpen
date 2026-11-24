# Upstream reference: `ask`

Ported from omp (`https://github.com/can1357/oh-my-pi`, MIT, commit `4df653426f40`).

## Upstream files followed

- `packages/coding-agent/src/tools/ask.ts` — parameter schema, `\r` sanitization, reserved-label and uniqueness validation, recommended-option suffixing and disambiguation, degraded selector flow (`askSingleQuestion`), timeout auto-selection, response formatting (`formatSingleQuestionResponse`, `formatQuestionResult`), and headless abort behavior.
- `packages/coding-agent/src/prompts/tools/ask.md` — tool description (no template branches; embedded verbatim).
- `packages/tui/src/render/render-utils.ts` — `sanitizeCarriageReturns`, `disambiguateDisplayLabels`, `TRUNCATE_LENGTHS.LINE`.
- `packages/tui/src/tools/ask.ts` — `AskToolDetails` and `QuestionResult` shapes.
- `packages/coding-agent/src/modes/settings.ts` — `ask.timeout` default (`0`, disabled).
- `packages/tui/src/overlays/ask-dialog.ts` — referenced for dialog semantics and empty-option/timeout behavior.

## Input kind to PiG SDK UI mapping

| omp input kind | PiG SDK UI call | Notes |
|---|---|---|
| Single choice (`multi: false`, `options.length > 0`) | `ctx.Select` / `ctx.SelectWithOptions` | Displays `label` (or `label — description`), appends ` (Recommended)` to `recommended` index, disambiguates collisions, and appends `Other (type your own)`. |
| Multiple choice (`multi: true`, `options.length > 0`) | Repeated `ctx.Select` / `ctx.SelectWithOptions` | PiG's SDK has no built-in multi-select dialog; uses omp's degraded toggle loop with `[ ] ` / `[x] ` prefixes, `(N selected) ` prompt prefix, `✔ Done selecting` row once at least one option is checked, and `Other (type your own)`. |
| Custom "Other" text | `ctx.EditorWithoutPrefill` | Mirrors omp's `ui.editor(title, undefined, ...)` preserving multiline input and empty-string submission; dismissing returns to the option selector. |
| Free-text question (`options: []`) | `ctx.EditorWithoutPrefill` (or `ctx.InputWithoutPlaceholderWithOptions` when timeout > 0) | Opens text input directly instead of a 1-item menu; uses `InputWithoutPlaceholderWithOptions` when a timeout is configured because `Editor` has no `DialogOptions.Timeout`. |
| Headless (`!ctx.HasUI()`, e.g. print/JSON mode) | `ctx.Abort()` + error | Matches omp: aborts the turn and returns `"Ask tool requires interactive mode"`. |

## Tests ported

25 top-level test functions (12 subtests across table-driven tests; 37 test runs total) in `ask_test.go`, covering 18 upstream test cases from `packages/coding-agent/test/tools/ask.test.ts` and `packages/coding-agent/test/ask-timeout.test.ts` plus Go-port coverage for registration/schema, free-text empty options, print-mode headless abort, RPC mode, empty questions, and formatter edge cases.

## Not ported

| Feature | Why not ported |
|---|---|
| Rich tabbed `AskDialogComponent` (`ui.askDialog`) with `header` chips, `preview` panes, per-answer notes (`note`), "Chat about this" redirect, and review tab | PiG's Go SDK has no built-in `askDialog` UI method; uses omp's degraded `select` + `editor` path. `header` and `preview` remain in the JSON schema and are accepted/sanitized for model compatibility. |
| Left/Right arrow navigation between questions (`onLeft` / `onRight`) and `initialIndex` focus cursor in `ui.select` | PiG's `Context.Select` / `DialogOptions` do not expose `initialIndex`, `onLeft`, `onRight`, `helpText`, or `selectionMarker`/`checkedIndices` callbacks. Multi-question asks proceed sequentially. |
| Explicit `onTimeout` / `onTimeoutStart` / `onTimeoutReset` callbacks and `timeoutStartsOnPresentation` | PiG's `sdk.DialogOptions` carries only `Timeout float64`; timeout auto-selection uses omp's deadline-window fallback (`TIMEOUT_DETECTION_TOLERANCE_MS`) instead of `onTimeout` callbacks. |
| Reading `ask.timeout`, `ask.notify`, `speech.enabled`, and plan-mode state from settings | PiG's extension `Context` does not expose omp's `ask.timeout`, `ask.notify`, `speech.enabled`, or plan-mode settings; default matches omp's unset default (`ask.timeout: 0`, no timeout). |
| Image attachments in custom answers / notes (`customInputImages`, `noteImages`) and text-model vision fallback (`describeAttachedImagesForTextModel`) | PiG's `ui.editor` and `ui.input` return plain text only; image pasting in ask dialogs is unavailable through blocking SDK dialogs. |
| Terminal desktop notifications (`TERMINAL.sendNotification`) and TTS vocalization (`vocalizer.speak`) | omp-specific terminal notification and speech subsystems have no equivalent on PiG's extension SDK. |
| Custom TUI transcript renderer (`askToolRenderer`) and `/tree` re-answer recovery (`recoverAskQuestions`) | Transcript rendering is handled by the frontend / skin lane, and `/tree` re-answer is host-session functionality outside the tool handler. |
| `AskTool.createIf` conditional registration (`canPromptUser` / `advertiseHeadlessAsk`) | `Register(e *sdk.Extension)` runs before session mode is known; the tool is always registered and fails closed in headless runs via `!ctx.HasUI()`, matching omp's `execute` guard. |
