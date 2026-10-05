# Port record: rpiv-ask-user-question (slice A, partial)

| Input | Identity |
|---|---|
| Original | `@juicesharp/rpiv-ask-user-question` 2.11.0, `packages/rpiv-ask-user-question` of https://github.com/juicesharp/rpiv-mono, commit `61904e69e1a50e12585bdf15f0310e633a62ba36`, MIT (juicesharp) |
| Oracle | Pi 1.0.0, Node 24.19.0, with the unmodified package at `port/oracle` (dependency `@juicesharp/rpiv-config` 2.12.0 from npm) |
| Target | PiG 0.4.0+1.0.0 (build `76022638`), go1.27.1 |
| Original's tests | 647 cases in 35 files. Slice A files: 173 cases, 148 exact twins and 25 named skips; the other 474 (22 files) are deferred to slice B, 8 of them the locale shim (E1) (`port/slices.json`, stated once) |
| Kind | 1: an extension, no CLI built-in, no provider |

## Scope: what is and is not ported

**Ported (slice A):** the tool (schema, description, prompt guidance, config overrides), normalisation,
validation, the response envelope, the sequential select/input dialog walker, the prompt and blocked events, the
`before_agent_start` reconciler.

**Not ported (slice B), so this port is partial:**

- The interactive questionnaire: a focused component with tabs, a submit tab, per-option notes, multi-select toggles,
  the side-by-side markdown preview pane and the external editor. **The Go SDK can express it** (`Context.Custom` takes a
  `RemoteComponent`: `Render(width)` and `HandleInput(data)`), so this is work, not a gap. It is 458 of the original's 647 test
  cases (state reducer 47, key router 77, wrapping select 48, preview pane 86, factory 44, ...); with the
  collapse-key listener (8) and the locale shim (8), 474 cases are deferred (`port/slices.json`). In a terminal this port
  therefore shows the same select and input dialogs the original uses on RPC hosts.
- The collapse key. Its hide-while-collapsed behaviour needs `ctx.ui.custom(options.onHandle)`, which the Go SDK lacks
  (`pigeq gaps`: MISSING, blocking; owner ruling E2: keep the original's fallback, an approved exclusion; the lead files a PiG SDK request for a `Context.Custom` result handle with SetHidden/IsHidden/IsFocused). The original has a fallback without the
  handle (a one-line row) that slice B would take.
- Locales (E1: English only, an approved exclusion, same as rpiv-todo).
- The terminal bell (G10, below).

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
| M10 | the interactive questionnaire (`state/`, `view/`, `QuestionnaireSession`) | **slice B, not ported** | named deferral |

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
