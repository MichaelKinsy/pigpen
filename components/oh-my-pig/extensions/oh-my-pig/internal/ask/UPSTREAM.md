# Upstream reference: `ask`

Ported from omp (`https://github.com/can1357/oh-my-pi`, MIT, tag `v19.1.1`, commit `31ffee513f`).

## Upstream files followed

- `packages/coding-agent/src/tools/ask.ts` — `html` parameter schema, `ExecutionMode: "sequential"`, `PrepareArguments` + `recoverAskHtml` (including bare string and legacy `questions` recovery, and refusing malformed/missing-`html` shapes with `INVALID_ASK_ERROR` before PiG's host validator runs), `splitRadioFieldsets` rejection, `askFieldByField` (`askChoice`, `askText`), timeout default submission, headless tool hiding on `session_start` (`createIf` equivalent), headless abort and headless-fork guidance, ordered non-HTML-escaped JSON result formatting.
- `packages/coding-agent/src/prompts/tools/ask.md` — verbatim tool description (embedded via `//go:embed`).
- `packages/coding-agent/src/prompts/tools/ask-headless-fork.md` — verbatim headless-fork guidance (embedded via `//go:embed`).
- `packages/coding-agent/src/prompts/tools/ask-result.md` — verbatim result template (embedded via `//go:embed`).
- `packages/coding-agent/src/prompts/tools/ask-split-radio-groups.md` — verbatim split-radio error template (embedded via `//go:embed`).
- `packages/utils/src/dom/parser.ts` — `decodeEntities` (semicolon-terminated fixed table + numeric character refs), `VOID_ELEMENTS`, `RAW_TEXT_CLOSE` (`script`/`style` only), `CLOSE_ON_OPEN`, `P_CLOSERS`, SVG namespace self-closing, `parseStartTag`, `parseInto` (implemented stdlib-only in `html_tokenize.go`).
- `packages/tui/src/html-form/parse.ts` — `parseHtmlForm`, `FormSection`, `FormBlock`, `FormField`, `ChoiceOption`, `FormSpan`, Unicode JS whitespace collapsing, automatic "Other", image-in-label alt text, tables, lists, preformatted blocks, hidden inputs.
- `packages/tui/src/html-form/data.ts` — `FormData` (JS property iteration order, unescaped JSON), `choiceValue`, `textValue` (including `0x`/`0b`/`0o` literals), `fieldError` (WHATWG URL schemes, rune-safe pattern truncation), `defaultFormData`, `fieldQuestions`, `blockText`, `fieldContexts`, `formAnswers`.
- `packages/tui/src/tools/ask.ts` — `AskToolDetails` (`answers`, `notes`, `timedOut`) plus `form` (`*HtmlForm`) so the Tern-native frontend can draw the whole form at once.

## Settings read by omp

Settings are read via package variable `ask.Setting func(ctx sdk.Context, key string) any` (assigned by the integrator; `nil` uses omp's defaults).

| Key | Default | Honoured |
|---|---|---|
| `ask.timeout` | `0` (disabled, seconds) | Honoured via `Setting(ctx, "ask.timeout")` (converted to ms; overridden by `Config.TimeoutMs` if > 0; disabled in plan mode via `gate.PlanActive`). |
| `ask.notify` | `"on"` | Not ported (see below: silent desktop notifications unavailable in SDK; audible bell intentionally omitted by default). |
| `speech.enabled` | `false` | Not ported (see below). |

## Tests ported

12 test functions in `ask_test.go`, covering 19 cited upstream test cases from `packages/coding-agent/test/tools/ask.test.ts`, `packages/coding-agent/test/ask-timeout.test.ts`, and `packages/tui/test/html-form.test.ts`, plus Go coverage for registration/schema parity against `omp_schemas.json`, headless hiding, entity preservation, Unicode whitespace, malformed HTML, underscore-number rejection, plan-mode timeout suppression, ordered unescaped JSON, and lenient `PrepareArguments` malformed-shape refusal (`INVALID_ASK_ERROR`).

## Not ported

| Feature | Why not ported |
|---|---|
| Native whole-form `ui.formDialog` / `HtmlFormDialog` overlay with per-option notes (`OptionNote`) and image paste | PiG's Go SDK has no `formDialog` UI primitive; uses omp's field-by-field fallback (`Select`, `Input`, `Editor`). Parsed `HtmlForm` is included in `AskToolDetails.Form` so the Tern-native frontend can draw the whole form later. |
| User steering signal interruption (`steeringSignal` / `onAbandon`) | PiG's `sdk.Context` does not expose a separate user-steering `AbortSignal` distinct from turn cancellation. |
| `initialIndex`, `selectionMarker`, `checkedIndices`, `helpText` on `ui.select` | PiG's `sdk.DialogOptions` carries only `Timeout float64`; checkbox state is rendered with `[ ] ` / `[x] ` prefixes in option labels. |
| Explicit `onTimeout` / `onTimeoutStart` / `onTimeoutReset` callbacks and local fallback timer | PiG's `sdk.DialogOptions` carries only `Timeout float64` and no callbacks; timeout is inferred via omp's elapsed-time tolerance window (`TIMEOUT_DETECTION_TOLERANCE_MS`). |
| `/tree` re-answer of an ask (`agent-session-tree-ask-reanswer`) | Host session tree-navigation lifecycle outside extension tool execution. |
| JavaScript `RegExp(..., "v")` lookarounds / Unicode sets in `pattern` validation | Go stdlib `regexp` is RE2 (no third-party regex engine allowed); patterns unsupported by RE2 are ignored just as invalid patterns are in omp. |
| Terminal desktop notifications (`ask.notify`, `TERMINAL.sendNotification` OSC payload) and TTS vocalization (`vocalizer.speak`) | PiG's SDK has no silent desktop notification API (only `ctx.Bell()`, which rings an audible bell and is intentionally kept off by default) and no TTS subsystem. |
| Image attachments in option notes (`answerImageBlocks`) and text-model vision fallback | PiG's blocking SDK dialogs return plain text only. |
