# Port record: rpiv-ask-user-question (slices A and B)

| Input | Identity |
|---|---|
| Original | `@juicesharp/rpiv-ask-user-question` 2.11.0, `packages/rpiv-ask-user-question` of https://github.com/juicesharp/rpiv-mono, commit `61904e69e1a50e12585bdf15f0310e633a62ba36`, MIT (juicesharp) |
| Oracle | Pi commit 1402973a9dbe8c627341e04c9744c54953e285ca (1.3.0+1402973a, built from source: the Pi PiG 0.5.0 targets), Node 24.19.0, with the unmodified package at `port/oracle` (dependency `@juicesharp/rpiv-config` 2.12.0 from npm) |
| Target | PiG 0.5.0 (tag v0.5.0, 7a5422f66d), go1.27 |
| Original's tests | 647 cases in 35 files: 621 exact twins and 26 named skips (the skips and their reasons are in the Go test files; `pigeq twins check`) |
| Kind | 1: an extension, no CLI built-in, no provider |

## Scope

Everything the original ships is ported: the tool, schema, envelope and events (slice A); the RPC dialog walker; and the
interactive questionnaire (slice B: `internal/ui` is `state/` and `view/` file for file, `questionnaire.go` is the
`ctx.ui.custom` factory and the collapse-key listener, built on the shared `components/pitui`). Unlike slice A, slice B cannot be
reached by the RPC scenarios (Pi's RPC mode has no custom UI), so its equivalence evidence is the twins of the original's tests and
`port/frames`: the original's real session, built through its own `makeSessionFactory`, driven by 173 scripted scenarios and
recorded frame by frame under Pi 1402973a (`port/frames/recorder`), then replayed through the Go component
(`TestFramesAgainstOriginal`, 1276 frames compared).

Differences (all SDK-bound, none silent; `port/frames/known-differences.json` pins the observable ones):

- **Overlay handle (E2).** `ctx.ui.custom(options.onHandle)` has no SDK counterpart (`pigeq gaps`: MISSING). The port takes the
  original's own handle-less path (`canReopenWhileHidden` false): collapse shows the one-line row, the same key expands. The raw
  terminal listener is ported (`registerCollapseKeyListener`, 8 twins) but production does not register it: without a handle it
  can do nothing, and the SDK would make the host wait on the extension for a verdict on every keystroke. Needs-SDK: a
  `Context.Custom` result handle with SetHidden/IsHidden/IsFocused.
- **External editor (Ctrl+G).** `state/external-editor.ts` is ported and tested (`ui.EditWithExternalEditor`) but has no production
  caller: it needs `tui.stop()`/`tui.start()`/`requestRender(true)` around a child with inherited stdio. The key reports the original's
  failure path with the Pigpen refusal text. Needs-SDK: terminal suspend.
- **Keybindings.** The factory's `KeybindingsManager` is stood in for by pi-tui's defaults plus `keybindings.json` of the agent
  directory (legacy names migrated). Needs-SDK: `KeybindingsManager.matches`.
- **Bell (G10).** No terminal write from a subprocess; 5 upstream cases are named skips. Needs-SDK: `Context.Bell()`.
- **Syntax highlighting** of fenced code in previews: Pi's `highlightCode` uses cli-highlight; `HighlightCode` is nil. Owner decision
  needed (port a highlighter, or accept).
- **Locales (E1 lifted).** The nine locales are ported with rpiv-i18n 2.13.9's resolution (locale.json, LANG, LC_ALL; `--locale` is
  not available). Owner decision pending: the original is English without `rpiv-i18n` installed.
- **Pitui bugs open when this was written:** see the PR body.

## Mapping

| ID | Original | Go | Scenario / test |
|---|---|---|---|
| M1 | `ask-user-question.ts`: tool `ask_user_question`, TypeBox `QuestionParamsSchema`, description, `promptSnippet`, `promptGuidelines`, `loadConfig().guidance` | `schema.go`, `copy.go`, `extension.go` | `guidance-in-request`, `schema-rejections` (host validation messages equal), `guidance_test.go`, `types_test.go` (a small JSON Schema checker over the same cases) |
| M2 | `tool/normalize-params.ts` | `normalizeQuestionParams` | `line-terminators`; `normalize_test.go` |
| M3 | `tool/validate-questionnaire.ts` | `validateQuestionnaire` | `reserved-and-duplicates`; `validate_test.go`, `TestValidationEnvelopesThroughTheTool` |
| M4 | `tool/response-envelope.ts`, `tool/format-answer.ts` | `buildQuestionnaireResponse`, `buildAnswerSegment`, `formatAnswerScalar` | every answering scenario; `envelope_test.go` (34 twins) |
| M5 | `rpc-fallback.ts`: select/input walker | `rpc.go` (incl. `parseInt`, UTF-16 preview cut; where the cut splits an astral character JavaScript keeps a lone surrogate, which a Go string cannot hold, so the port stops one unit short) | `single-*`, `multi-select-*`, `previews`, `mixed-questions`, `partial-then-dismissed`, `two-calls`; `rpc_test.go` |
| M6 | `events.ts`, `emitAskUserPromptEvent`, blocked pair | `emit` through `ctx.Events()` | `execute_test.go` (the event bus is not in the traces) |
| M7 | `reconcile.ts` | `reconcile` on `before_agent_start` | `reconcile_test.go` (16 twins) |
| M8 | `config.ts` (collapse key spec, guidance) | `config.go` (the todo port's, adapted) | `config_test.go` |
| M9 | `buildItemsForQuestion`, `sentinelsToAppend` | `buildItemsForQuestion` | `items_test.go` (7 twins) |
| M10 | the interactive questionnaire (`state/`, `view/`, `QuestionnaireSession`, the factory and collapse listener) | `internal/ui/*`, `questionnaire.go`, `host.go`, `keybindings.go` | the twins of `state/*`, `view/*`, `factory`, `ask-user-question.listener`; `port/frames` |

## SDK gap check (`pigeq gaps --ts port/oracle`)

5 gaps, 1 blocking: `ctx.ui.custom(options.onHandle)` MISSING (E2); `ctx.ui.custom` and `overlayOptions` PARTIAL
(a `RemoteComponent` stands in for the host component); `tool.promptSnippet` PARTIAL (it did not bite: the
`guidance-in-request` scenario shows the same system prompt delta as Pi).

## Results (this revision)

- `go test -race ./...`: pass.
- `pigeq check` against the golden traces **recorded from the original under Pi 1.0.0**: 15 of 16 scenarios pass on
  every run, plus `port-gaps` and `exec-coverage`; host check pig-ts == pi-ts on all 16 (no widget difference this
  time). **`two-calls` is flaky**: it failed 5 of 26 review runs at `655be0b` (G11 below); the 16 of 16 reported at
  `f21360f` was a lucky run.
- `pigeq mutate --unit`: 87 mutations. At `655be0b` 86 were killed: `preview-cut-at-runes` survived (the 598-unit
  test cannot tell a rune cut from a UTF-16 cut); review added `TestRPCPreviewCutNeverSplitsAnAstralCharacter`, which
  kills it. First run 89: 79 killed, 2 invalid (rewritten so they build), 8
  survivors: 6 got tests (a `selected: []` that must survive decoding, the UTF-16 preview cut, `parseInt` sign and leading
  space, a custom answer that must not be trimmed, the tool label) and 2 were equivalent mutants and removed (the lone-CR
  deletion makes the CRLF replacement redundant in the original too; an integer overflow reads as NaN or as out of range, both
  decline). `port/gen-mutations.py` generates `port/mutations.json` and checks every find string is unique.
- `pig install ... --validate-only --json`: valid (tool `ask_user_question`, handler `before_agent_start`).
  `pig package validate components/ask-user-question`: valid.
- Piglet: `piglets/pig-popular` now selects both ports; `pig-0.4.0 piglet build` fuses both ("Preparing fused Go members
  — rpiv-todo, rpiv-ask-user-question"). The Binary's own traces (`record --self --builtin`, `check --builtin`) pass 16 of
  16 and equal the Pi goldens in every event outside the `llm` channel (the `llm` events differ by the Binary's default tool set).
- tmux: `scripts/tmux-demo.sh ask ... port/demo/turns.json 'type:ask me' 'wait:1' 'grab:select' 'key:Enter' 'grab:input' 'type:1,3' 'wait:2' 'grab:answered'` ran the extension in an interactive `pig-0.4.0`:
  `port/demo/pane-select.txt` (the select dialog with the appended "Type something." row), `pane-input.txt` (the multi-select input dialog) and
  `pane-answered.txt` (the model receives `"Which database?"="Postgres". "Which features?"="Auth, Queue".`).

## Findings

1. **G10 No terminal write from a subprocess extension.** The original rings the terminal bell
   (`process.stdout.write("\x07")` when stdout is a TTY) before it waits for the user. A Go extension's stdout is the
   protocol pipe and the SDK has no bell call, so the port rings nothing. Pi's RPC stdout is not a TTY, so the traces never
   contain a BEL and 5 upstream cases are named skips. Proposed: `Context.Bell()` (or a `ui.attention` call).
2. **E2 overlay handle** (above): `onHandle`, `OverlayHandle.setHidden/isHidden/isFocused`.
3. **The registered name must equal the extension directory** (`register:name_mismatch`): the first `pigeq check` failed on every
   scenario until the directory was renamed `rpiv-ask-user-question`. A generated skeleton gets this right by construction.
4. **`config.go` is the todo port's, copied.** All `@juicesharp/rpiv-*` packages share `@juicesharp/rpiv-config`; a Go library
   Package would remove about 180 duplicated lines per port.
5. **Fake-host gaps in the template:** it records only tool names (this port reads description, guidance, schema and label from
   the extension's register frame in `tooldef_test.go`; at `655be0b` it had edited the template copy, which failed `npm test`),
   and `before_agent_start` needs `systemPromptOptions` in the event data.
6. **`pigeq twins check`** rejects a skip whose reason or title is not a string literal (`<not a literal>`); a shared constant does not work.
7. **G11 A parallel batch shows one dialog at a time.** Pi starts every call of a batch before it handles any dialog
   answer (pi-agent-core 1.0.0 `agent-loop.js:411-453`; each execute runs synchronously up to its first await), so an
   RPC client sees both dialogs of `two-calls` before the first call ends. A Go call reaches its dialog after two
   `pi.events` round trips to the host (the prompt and blocked events), so a quick first answer can end the first
   call before the second asks: `two-calls` fails about one run in five. Holding a call's result until the later
   calls are about to ask narrows the window but cannot close it (the SDK exposes no point at which a dialog request
   has been written; 2 of 32 runs still failed), so the port does not carry that. Needs the Go SDK: a synchronous or
   fire-and-forget `Events().Emit`, or a dialog-sent hook. `TestParallelCallsAskEveryDialogBeforeTheFirstEnds`
   states Pi's order and is skipped until then. `pigeq gaps` does not flag it (it reports `pi.events.emit` as
   supported).
8. **Twins check at `655be0b`** exited 1: three `ask-user-question.normalize` cases were MISSING and five execute
   skips shared one reason. Review added the two the slice covers (normalized prompt event, normalized RPC dialog
   lines), a named skip for the TUI render, and a reason per skip. Two skip reasons named Go tests that did not exist.
