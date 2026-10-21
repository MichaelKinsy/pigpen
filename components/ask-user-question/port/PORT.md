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
- **Syntax highlighting** of fenced code in previews (owner decision, 2026-10-20: match the original). Pi's `highlightCode`
  runs cli-highlight with highlight.js 10.7.3; the port uses PiG's Go port of that highlighter (`components/pitui`:
  `internal/hljs`, `highlight.go`, with provenance) and the theme mapping of `buildCliHighlightTheme`
  (`internal/ui/markdown_theme.go`). The frame scenarios that carry fenced code in a language highlight.js knows (for
  example `preview-code-fence-languages`) now match the original frame for frame.
- **Locales (owner decision, 2026-10-20: match the original exactly).** English unless the original's optional peer
  `rpiv-i18n` is installed. `@juicesharp/rpiv-i18n` cannot be imported from Go, so "installed" is read from Pi's settings
  (`i18n_installed.go`); only then does the port apply that package's resolution (`locale.json`, `LANG`, `LC_ALL`).
- **Pitui bugs open when this was written:** see the PR body.

## Refusals

A terminal host whose `ctx.ui.custom` resolves nothing (it cannot render the questionnaire) is answered by the dialog walker, as in
the original. There `refuseUnported` (`refusal.go`, from the earlier refusal change) fails the call for an option preview beyond 600
UTF-16 units and for a `collapseKey` other than the default or `off`, the two things the walker cannot carry. The text is
`@pi-in-go/pigpen-ask-user-question (Go port) does not support <feature> yet. Install the original extension,
npm:@juicesharp/rpiv-ask-user-question@2.11.0, which runs unchanged in PiG.` The questionnaire carries both, so a host that renders it
refuses nothing; in `rpc` mode the original runs the same dialog walker, so nothing is refused there. Tests: `refusal_test.go`.

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

## SDK gap check (`pigeq gaps --go extensions/rpiv-ask-user-question`, and `pigeq record` on the original)

`pigeq gaps --go`: 4 gaps, none blocking: `Context.Custom` and `RemoteOverlayOptions.OverlayOptions` are PARTIAL (a
`RemoteComponent` renders lines and handles input inside the extension; the host overlay owns focus, and a dynamic
overlay options function cannot cross the process boundary), and `ToolDefinition.PromptSnippet` is PARTIAL (the host's
system prompt does not list extension tools' snippets; the `guidance-in-request` scenario shows the same system prompt
delta as Pi).

`pigeq record` (pigeq from `pigeq-pinned-commit`, which checks the Pi by commit) scans the original and reports
`sdk-gaps` FAIL: `ctx.ui.custom(options.onHandle)` is MISSING from the Go SDK. This is G5, the overlay handle, and it is
**pending PiG SDK** (core item in `core-items.md`); it is not marked as a reviewed exception. The other three entries
are the PARTIAL ones above.

## Results (this revision)

Host: xd670, CPUs 64-127, Pi built from commit 1402973a (`pi 1.3.0`, the Pi that PiG v0.5.0 targets), `pig 0.5.0+1.3.0.1402973a`.

- **Twins.** `pigeq twins check`: 624 exact twins and 23 named skips of the original's 647 cases (`port/upstream-tests.json`);
  `slices.json` is `{}`. The 23 skips each name why the case cannot be ported (the terminal bell, the overlay handle, the
  external editor, reference identity in JavaScript).
- **Frames.** `TestFramesAgainstOriginal` replays 173 scenarios (1,276 frames) recorded from the unmodified original
  under Pi 1402973a through the Go component and compares every frame, the notifications, the result and the tool text.
  Three scenarios carry pinned known differences (`port/frames/known-differences.json`): the external-editor notification
  text (two scenarios) and the Shift+letter Kitty decode (PiG divergence D185: PiG decodes `ESC[97;2u` as `A`,
  pi-tui as `a`; pitui follows PiG).
- **RPC scenarios.** The 16 goldens in `port/golden` are re-recorded from the original under Pi 1402973a (only the header
  changed; the traces equal the Pi 1.0.0 ones). `pigeq check`: 15 of 16 pass on both lanes (pig-ts == pi-ts and
  pig-go == pi-ts). **`two-calls` differs between PiG and Pi**: in Pi the first `tool_execution_end` and the first
  select dialog belong to "First?", under PiG, in every run against the Pi recording, to "Second?" (event 11 of the
  original under PiG, event 9 of the port). It is the host: PiG core bug B3 (order of concurrent tool calls, their dialog
  requests and `tool_execution_end` events), and it is a race, not a fixed order: the same scenario recorded from the
  port's own Binary matched Pi's order once and the reverse in the following run. Fix: PiG's
  `fix/parallel-tool-ui-order`. The scenario is kept un-normalized and is excluded from mutation kill counts until
  the fix lands.
- **Mutations.** `pigeq mutate --unit`: 117 mutations, 0 not killed, with `two-calls` excluded (a mutant that only makes
  `two-calls` fail is not a kill). With the earlier runs counting `two-calls`, 6 mutants had survived undetected; each
  now has a unit test: the multi-select dialog title (the mutation now targets `en.json`, where the English text lives;
  the constant in `rpc.go` is only the fallback for a key a table lacks), key releases at the component, the external
  editor notification, keybinding values given as a string, legacy keybinding names, and the headerless tab label
  (`Q<n>`). `port/gen-mutations.py` generates `port/mutations.json` and checks every find string is unique.
- **Vet and race.** `go vet ./...` clean for linux, windows and darwin (amd64); `go test -race -count=1 ./...` passes for
  the extension and for `components/pitui` (scoped runner, xd670).
- **Repo gates** at the branch head: `npm run check` exit 0, `npm run generate` leaves no diff, `npm test` 357 tests with 2
  failures, both in `scripts/perf/perf-harness.test.mjs` (they launch an interactive pig in the container and time out
  after 30 s; they do not touch this Package).
- **Piglet.** `piglets/pig-popular` selects the port and the `pitui` library Package and builds (73,171,207 bytes at
  `d9c7643`). The single-extension Binary of the final code (`scripts/perf/build-singles.mjs`, 72,548,615 bytes) records
  its own traces with `pigeq record --self --builtin`; its 16 traces equal the Pi goldens in every event outside the
  `llm` channel (the channel differs by the Binary's default tool set), and `pigeq check --builtin` against its own
  recording passes 15 of 16 (`two-calls` is B3).
- `pig install ... --validate-only --json` and `pig package validate components/ask-user-question`: valid.

## Performance (xd670, CPUs 64-79; medians of 12 interleaved runs, 20 for start-up)

Compared with the previous Pigpen release (0.1.0, `main` at bee2966: the dialog walker only) and with the original under
PiG's Node runtime (`pig -e port/oracle/index.ts`, PiG's TypeScript extension host). Measured at the branch head with
the questionnaire, the highlighter and the locale gate in.

| Measure | original under PiG (Node) | 0.1.0 | 0.2.0 |
|---|---|---|---|
| Binary, single-extension Piglet (bytes) | not a binary; TypeScript sources 617 KB | 70,947,079 (control `seed-check` 70,861,063: +86,016) | 72,548,615 (+1,687,552 over the control; +1,601,536 over 0.1.0, +2.26%) |
| Start up, `pig -p` with the extension (ms) | 728.6 | 30.7 | 30.1 (control 29.1, plain pig 16.3) |
| Scenario `mixed-questions` through `pigeq lane` (ms, loads the extension and plays the scenario's 20 events) | 2506.0 | 2005.5 | 2108.0 (+5.1%) |
| RPC walker, 1 question, in process (ns, B, allocs per call) | not measured | 280,169 / 41,547 / 683 | 281,617 / 42,469 / 694 (+0.5%, +2.2%, +1.6%) |
| RPC walker, 3 questions, in process | not measured | 408,755 / 88,094 / 1,370 | 418,985 / 89,008 / 1,382 (+2.5%, +1.0%, +0.9%) |
| Questionnaire render at width 120 | 798 µs | not present | 167 µs, 58 KB, 1,141 allocs |
| One key (Down) and render, twice | 1,098 µs | not present | 193 µs, 68 KB, 1,565 allocs |
| Full flow (open, 8 keys, submit, a render after each) | 2,502 µs | not present | 492 µs, 222 KB, 4,959 allocs |
| One-time load of every highlight.js language | in the background at Pi's start | not present | 18.8 ms, once per process |

Notes. The start-up row is `scripts/perf/measure.mjs startup --mode print`; the tmux mode cannot run in the container
(the same limitation as the two failing perf-harness tests). The 0.2.0 `mixed-questions` row is 103 ms over 0.1.0, the
larger Go module that `pig` loads for the run; the in-process walker numbers show the per-call change (under 3%; the extra
allocations are the `rpiv-i18n` settings check at the start of each call). The Node numbers are
`port/perf/bench-original.mjs` (the original's `QuestionnaireSession` loaded with Pi's jiti, Pi's real dark theme for the
preview's markdown, the identity theme elsewhere, V8 allocations not counted) against `internal/ui/bench_test.go` on
the same three-question input and flow (the first option's preview has a TypeScript fence, so both highlight it); the
Go numbers use the identity theme. Input and render run on the TUI path and do bounded work per key: no host call is made
from a render or a key (the session calls the host only to close the overlay and on the collapse listener). The first
fenced preview of a session waits at most for the one-time language load (18.8 ms), which starts when the questionnaire
opens. The 1.6 MB growth of the Binary is pitui (the markdown parser with `regexp2`, the width tables, the word
dictionaries) and the highlighter (the language grammars, 0.33 MB compressed); total size grows 2.26%, under the 10%
rule, and there is no per-call cost.

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
7. **B3 (PiG core): the order of concurrent tool calls differs from Pi.** Pi starts every call of a batch before it
   handles any dialog answer, so the first dialog and the first `tool_execution_end` belong to the first call. Under
   PiG, for the original TypeScript extension as well as for the port, the second call's dialog and result come first
   (`two-calls`: `pig-ts` differs from `pi-ts` at event 11, `pig-go` from `pi-ts` at event 9). Earlier runs read this as a
   flake of the port (G11, 5 of 26 runs); with the real Pi build it is the same for both
   extensions and varies between runs of the Binary, so it is the host. Fix: PiG `fix/parallel-tool-ui-order`. `TestParallelCallsAskEveryDialogBeforeTheFirstEnds`
   states Pi's order and stays skipped until the fix is released.
8. **Twins check at `655be0b`** exited 1: three `ask-user-question.normalize` cases were MISSING and five execute
   skips shared one reason. Review added the two the slice covers (normalized prompt event, normalized RPC dialog
   lines), a named skip for the TUI render, and a reason per skip. Two skip reasons named Go tests that did not exist.
