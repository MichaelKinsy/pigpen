# Lane E (ui) notes: UiLane

Files: `ui.go` (ui.ts), `ui_cards.go` (src/ui/cards.ts), `ui_display.go` (src/ui/display.ts), `ui_text.go` (src/ui/text.ts),
tests `twin_renderers_ui_test.go`, `twin_renderers_ui_tools_test.go`, `ui_testhost_test.go`, `ui_display_test.go`, `ui_bench_test.go`,
and the TUI harness `port/tmux/` (below).

## Published API (other lanes call it)

| Name | Use |
|---|---|
| `posthorseDisplay` (`MarshalJSON` in the TS key order) | the `details` of every tool result |
| `displayKind(kind)` | `{kind}` only: `context` (usage unknown), `new-context`, `note-write`, `note-append` |
| `displayContext(usage *contextUsage, rollover string, rolloverAt *int)` | context-tools.ts:29 |
| `displayNotes(kind, headerLength, count, offset, end, total int, more bool)` | paging.ts:127-136 (`more` only when true) |
| `displayNoteRead(headerLength, offset, end, total int)` | notes.ts:133 |
| `displayHistorySearch(entries []historySpan, footerLength int, more bool, skipped int)` | history-pages.ts:108 |
| `displayHistoryRead(headerLength, offset, end, total, imageOffset, imageEnd, imageTotal int)` | history-pages.ts:173 |
| `toolCards(name) sdk.ToolRenderers`, `applyToolCards(def *sdk.ToolDefinition, name)` | the `...toolCards(name)` spread |
| `registerPosthorseMessages(e *sdk.Extension)` | ui.ts:20 |

Internal names are prefixed `card*`/`ui*` where the TS name would clash with another lane or a fixture (`textBlock` is a fixture
identifier, `callDetails` is recovery-results.ts, `textOf` is message-text.ts); the Go-visible TS names kept are
`displayOf, historySections, pageSummary, displaySummary, summaryOnly, n, toolCards, registerPosthorseMessages`.

## Mapping decision: line renderers, not kit views

`ToolRenderers{Call, Result}` (rows at the requested width) and `Extension.MessageRenderer` carry every observable of the original.
All original components are pure functions of the width (their `invalidate()` bodies are empty, cards.ts:93, 331; ui.ts:44, 67),
and Pi owns the card shell, the background, the padding and the click/Ctrl+O expansion of tool cards (`renderShell` stays default).
The kit forms (`CallView/ResultView`, `MessageViewRenderer`) add nothing here: the only interactive node, `MouseRegion`, cannot reach
the extension from a renderer view (finding 1), and the wrapped `Text`/`Box` rows are what the host would compute from the same pi-tui ports.
Both hosts were run on the cards: rows and color escapes are identical (table below).

pi-tui `Text(…,0,0)` and `Box` are re-implemented over `tui.WrapTextWithAnsi`/`VisibleWidth`/`TruncateToWidth` (`piText`, `piBox`).
`leadingRows`' `Intl.Segmenter` cut is replaced by `boundaryBetween(a, b)`, a test that answers true only where UAX #29 (15.1+, with GB9c) certainly
breaks: it is false for every pair a rule glues (GB3 CR LF, GB6-8 Hangul pairs, GB9/9a when `b` is an Extend/ZWJ/SpacingMark, GB9b when `a` is Prepend, GB9c and GB11 when `a`
is a mark or ZWJ, GB12/13 two regional indicators) and true otherwise. The class tests are supersets built on `unicode.Mn/Me/Mc/Other_Grapheme_Extend/Join_Control/Cf` plus the
explicit exceptions (U+0E33, U+0EB3, emoji modifiers, tag characters, the few non-Cf Prepend characters, lone surrogates), so a "certain boundary" is a real one. Any real boundary at or
after the original's cut wraps the same first `count` rows (the greedy wrap of the prefix equals that of the whole text up to its last token, which starts beyond the rows shown).
`safeCut` finds one within a few units for every script (`TestUiBoundaryBetween`, `TestUiLeadingRowsScripts` over eleven scripts/sequences and five widths, the 656-case differential, the tmux screens).

## Contract table

| ID | Upstream | Go | Test |
|---|---|---|---|
| E1 | display.ts:15-49 `PosthorseDisplay` and its JSON shape | `posthorseDisplay`, `display*` constructors, `MarshalJSON` | `TestDisplayJSONKeyOrder`; all tool twins (`details`) |
| E2 | display.ts:51-147 `isDisplay`/`displayOf` | `displayOf` | `TestDisplayOfRejectsMalformed`; renderers "legacy history and malformed display spans" |
| E3 | display.ts:149-207 `historySections` (UTF-16 spans, footer, tiling) | `historySections` (linear cursor, ASCII fast path) | `TestHistorySections`; "history searches lead with content", "paginated searches" |
| E4 | display.ts:209-312 summaries, `n`, `summaryOnly` | `pageSummary`, `contextSummary`, `historyReadSummary`, `historySearchSummary`, `displaySummary`, `summaryOnly`, `n` | `TestLocaleNumber`; "notes show page range", "context summaries", "history pages" |
| E5 | text.ts:9-23 `clean`, `argsOf`, `argument` | `cleanText`, `cardArgsOf`, `cardArgument` | `TestCleanText`; "partial and error results cannot inherit ... terminal controls" |
| E6 | text.ts:25-88 `textOf`, `textBlock`, `leadingRows`, `hint` | `cardTextOf(Blocks)`, `cardBlock`, `cardLeadingRows`, `cardHint` | "collapsed previews show exactly the first rows", benchmarks |
| E7 | cards.ts:39-112 call card | `cardCallComponent` | "all four native cards identify pending calls", tmux pending/malformed-args |
| E8 | cards.ts:114-333 result card (collapsed/expanded, history sections, images, submitted text, legacy prefix strip) | `cardResultComponent` and helpers | all 15 twins, tmux |
| E9 | cards.ts:335-359 `toolCards` | `toolCards`, `applyToolCards` | `TestTwinRenderersUi*` |
| E10 | ui.ts:20-85 message renderers | `registerPosthorseMessages`, `renderPosthorseMessage`, `piBox` | "owned reminders", "new_context remains a conditional request ...", tmux messages* |

## Twins

`pigeq twins check --ledger port/upstream-tests.json --go extensions/posthorse --files renderers`: **15 exact twins, 0 skipped**.
Eight run on a hermetic extension that carries the four cards (`uiExtension`), seven execute the real tools through the Posthorse fixture
(`twin_renderers_ui_tools_test.go`, one fresh extension per `execute(...)` as the original's `setup()` does). Adaptations (the originals inspect Pi components):
- `ToolExecutionComponent`/`CustomMessageComponent` are host code: `uiCard`/`uiMessage` compose the rows the way the shells do (spacer, `Box(outputPad,1,bg)`).
- The left-click toggles of tool cards (`click(component, ...)`) are Pi's shell (`createResultRegion`); the twin toggles the expansion the click would, and the tmux scenario `click-card` proves the click on both hosts.
- The drag-event assertion of the theme twin is the shell's too (nothing reaches a renderer); the colour change is asserted on the row.
- Theme and `ctrl+o` keybinding come from `theme_change`/`state_update` notifications.
- One **named sub-skip** inside "new_context remains a conditional request ...": `click toggles the message's local state` (finding 1). The case itself stays an exact twin: every other assertion in it runs.
- `package`-wide: `go vet` linux/darwin/windows clean; `go test -race -count=3` for all renderer, display and text tests green.

## Review fixes (port/review/uireview.md)

| ID | status | fix | test |
|---|---|---|---|
| U1 | fixed | `cleanText` and `uiCollapseSpaces` copy bytes that are not valid UTF-8 (the SDK's WTF-8 lone surrogates) unchanged instead of writing U+FFFD per byte; `safeCut` counts and keeps a WTF-8 surrogate as one unit | `TestUiCleanTextLoneSurrogate`, differential `lone-*` |
| U2 | fixed | the context card prints `uiIntegerString(jsRound(percent))` (no grouping, JS `Math.round`) | `TestUiContextSummaryPercent`, `TestUiSummaries` |
| U3 | fixed | `n` formats the shortest round-trip decimal, rounds half away from zero to three fraction digits by decimal-string increment, keeps `-0`, prints `NaN`/`∞` | `TestUiLocaleNumberShortest`, `TestLocaleNumber`, `TestUiSummaries` |
| U4 | fixed | `safeCut` uses `boundaryBetween` (UAX #29 certain boundaries, below) instead of the plain-rune pair test: collapsed previews wrap a bounded prefix in every script | `TestUiBoundaryBetween`, `TestUiLeadingRowsScripts`, `TestUiSafeCutScripts`, `BenchmarkUiLeadingRows*` (table above) |
| U5 | fixed | `cardNoteHeaderLength` stays a float64: `> 0` on the number, `u16sliceF(body, h, +Inf)` for the slice | `TestUiNoteHeaderLengthNonInteger`, differential `notes:hl0.5`, `notes:hl1e19` |
| U6 | fixed | `u16Cursor.to` reports a target inside a surrogate pair (or behind the cursor); `historySections` then slices by UTF-16 unit (`u16slice`), splitting the pair as JS does | `TestUiHistorySectionsSplitPair`, differential `hs:astral-split` |

The reviewer's tests were renamed to `ui_proof_test.go` (self-contained) and `ui_differential_test.go` (the 656-case TS differential, `TestUiDifferential`/`TestUiSummaries`, and the U4 benchmarks);
both skip when `/home/kinsy/ph-work/scratch/uireview/ui-cases.json` is absent. The probe scripts stay in `port/review/uireview-probes/` (`run-ts.sh` regenerates the cases inside the container, `run-go.sh 'TestUi'` runs them,
`run-bench.sh` the benchmarks). After the fixes: 656 cases, 0 differ.

## Findings and host/SDK gaps (decisions for the lead/owner)

1. **Message-card click toggle is not expressible (SDK/host gap).** The original keeps a per-message `WeakMap` state and answers a left click
   on `context-window`/`posthorse-reminder` cards through a pi-tui `MouseRegion` (ui.ts:14-17, 74-80). In PiG a message renderer returns rows (or a kit view)
   and the host drives its `renderProxyComponent` from `render_message` requests carrying only `{message, options{expanded,outputPad}, width}`: there is no message
   identity and no mouse event for a renderer (`docs/plan/extension-component-kit.md` §9 "renderer views take no mouse"; `view_kit_mouse_region.go` queues a mouse event
   that no renderer overlay receives; `renderProxyComponent` has no `HandleMouse`). Consequence, measured on both hosts (`click-message`): a click on a message card expands it under Pi and does nothing under PiG;
   global expansion (Ctrl+O) works and is identical. Tool cards are unaffected (the host's shell handles their click). Proposal below. Not shipped as a stand-in.

### SDK/host change proposal: `clickToggle` for message renderers (not applied; the SDK is not edited by this lane)

*Facts the proposal rests on* (all in `~/work/ph-pig` at 979bf718f7): `internal/codingagent/interactive_transcript.go:544-566` (`appendCustomMessage`) builds one `extension.CustomMessage` per
displayed message and a closure `renderer(_, options)` that `tui.CustomMessageComponent` calls on every rebuild (`SetExpanded`, `SetOutputPad`, `Invalidate`: `tui/custom_message.go:49-60`), each call creating a new
`renderProxyComponent` (`coding/extension/host/subprocess/host.go:3939-3955`) which requests `render_message` (`render_proxy.go:260-286`) with `{message, options, width}` and caches the rows;
`tui.CustomMessageComponent` is a `Container`, which forwards mouse events to children that implement `HandleMouse` (`tui/mouse.go:206-245`), and `tui.MouseRegion` (`tui/mouse_region.go`) is the pi-tui port the original uses.
The per-message state of the original is only `{global, expanded}` with the reset rule `state.global !== options.expanded` (ui.ts:22-28); no extension code runs on a click.

**Option A (recommended): a declarative flag, host-owned state, no mouse event crosses the wire.** The extension declares that its renderer toggles its own expansion on a left click; the host keeps the
same two-field state per message and wraps the renderer's component in a `MouseRegion`, i.e. it executes ui.ts:22-28 and 74-80 for the renderer.

| File | Change |
|---|---|
| `extensions/sdk/protocol.go:176` | `rendererDef` gains `ClickToggle bool \`json:"click_toggle,omitempty"\`` |
| `extensions/sdk/extension.go:975` (+ `view.go:248`) | `type MessageRendererConfig struct{ ClickToggle bool }`; `func (e *Extension) MessageRendererWith(customType string, handler RendererFunc, c MessageRendererConfig)` and `MessageViewRendererWith`; `MessageRenderer` stays and means the zero config. `docs/extension-sdk-surface.md`: a "pig additive" row `message renderer clickToggle` |
| `coding/extension/host/subprocess/protocol.go:469` | `MessageRendererDecl` gains `ClickToggle bool \`json:"click_toggle,omitempty"\`` (the register frame carries it; a late `declare` re-sends it) |
| `coding/extension/host/subprocess/host.go:3874-3876`, `coding/extension/renderer_registry.go` | `ext.SetMessageRendererClickToggle(customType, rd.ClickToggle)` next to `SetMessageRenderer`; the runner (`coding/extension/host/inproc/runner.go:1166`) gains `MessageRendererClickToggle(customType) bool` |
| `internal/codingagent/interactive_transcript.go:544-566` | in `appendCustomMessage`, when the flag is set: `state := &struct{global, expanded bool}`; in the closure `if state == nil \|\| state.global != options.Expanded { reset to options.Expanded }`; call `registered(customMessage, MessageRenderOptions{Expanded: state.expanded, OutputPad: options.OutputPad}, nil)` and return `tui.NewMouseRegion(component, func(e tui.TuiMouseEvent) *tui.TuiMouseEventResult { if e.Type != "click" \|\| e.Button != "left" { return nil }; state.expanded = !state.expanded; customComponent.Invalidate(); return &tui.TuiMouseEventResult{Handled: true} })` where `customComponent` is the `CustomMessageComponent` the closure belongs to |
| `test/extension-conformance` + `testfixture` | a fixture extension registering a clickToggle renderer that prints its `expanded` option |

*Wire fields:* only `click_toggle` (bool) on the message renderer declaration; `render_message` requests are unchanged (`options.expanded` already carries the local choice).

*Tests:* (1) `extensions/sdk`: a register frame with `MessageRendererWith(…, ClickToggle: true)` carries `click_toggle: true`, plain `MessageRenderer` omits it. (2) `coding/extension/host/subprocess`: declaration round trip and `SetMessageRendererClickToggle`
visible through the runner. (3) `internal/codingagent` (`interactive_transcript` tests): a left click on the rendered card re-requests with `expanded: true`, a second click with `false`; a click of another type or button is unhandled;
two messages keep independent state; a global `SetExpanded` change resets the local choice and an unchanged global value does not; `SetOutputPad`/`Invalidate` keep the choice (the original's last assertions); a renderer without the flag gets no region. (4) conformance: the fixture over a real socket.
(5) Posthorse: `registerPosthorseMessages` switches to `MessageRendererWith(…, ClickToggle: true)`, the twin sub-skip in "new_context remains a conditional request…" becomes a click twin, and the tmux scenario `click-message` goes from `differs` to `same`.

*Why not mouse events to the extension (Option B):* it needs a message identity in `render_message` (a host-minted key per `CustomMessageComponent`, stable across rebuilds), a new `render_message_mouse` request or notify,
a release notification when the chat is cleared, per-message state maps in the extension, a round trip before the card repaints, and the same in every SDK; the original needs none of it because its handler touches no extension state.
`kit.MouseRegion` events of renderer views would have to be routed the same way (`view_kit_mouse_region.go` already queues them; `rendererView` has no consumer), which is the larger change; Option A makes it unnecessary for this port and any renderer with "click to expand".

2. **`context-window` is a legacy type.** Only `recovery.ts:69` and `rollover.ts:92` read it; no current flow emits it. It renders from saved sessions
   (scenario `real-saved-messages` seeds one into a session file and both hosts draw the same card).
3. `leadingRows` (text.ts:45-81) relies on Intl.Segmenter; see the mapping section for the Go substitution (same rows, proved).
4. `n(...)` is `toLocaleString("en-US")`: shortest decimal, half-expand to three fraction digits (U3, fixed).
5. cards.ts:204-206 `/^...$/gm`: `^` matches after `\n`, `\r`, U+2028 and U+2029 in JS; `cardStripHistoryPrefixes` reproduces that (`TestStripHistoryPrefixes`).
6. Original oddity ported: when a history result has spans, the collapsed card previews `sections.map(body).join("\n")` unwrapped by `leadingRows`; expanded cards color bodies `toolOutput` and headers `dim`.

## Pi-vs-PiG screen proof (`port/tmux`)

Real terminal (tmux 100 columns unless noted), Pi 1.5.0 against pig 0.7.0+1.5.0, the same scripted OpenAI-compatible model
(`mockllm.py`, extended with streamed `tool_calls`), plain text compared row by row, and the colour escapes (`*.ansi`) compared too;
the footer (cwd, token counts), the startup warning and the spinner frame are dropped, file times are normalised (`compare.py`).

- `probe/probe.ts` (Pi loads the original's own `ui.ts` cards) vs `probe/probe.go.tmpl` (assembled by `build_probe.py` from the port's `ui*.go`): four tools with canned results (`fixtures.json`, generated by `gen_fixtures.py`) and a `/pt` command (`message <name>`, `theme <name>`).
- `scenarios-real/` (`tmux.sh ... real`): the original `index.ts` under Pi against the whole port under PiG, the real tools and hooks.
- Run: `~/work/ph-tools/ph run "sh components/posthorse/port/tmux/tmux.sh [SCENARIO|all] [pi|pig|both] [probe|real]"`; scenarios are generated by `gen_scenarios.py`.

Probe scenarios (each snaps collapsed, expanded with Ctrl+O, collapsed again unless noted):

| scenario | columns | what | pi vs pig |
|---|---|---|---|
| click-card | 100 | left click on a result card toggles its expansion | **same** (3 screens) |
| click-message | 100 | left click on the two message cards, then global expansion | differs (see finding 1) (6 screens) |
| context-w24 | 24 | get_context_remaining: enabled, unsupported (warning color), disabled, unknown | **same** (3 screens) |
| context-w40 | 40 | get_context_remaining: enabled, unsupported (warning color), disabled, unknown | **same** (3 screens) |
| context | 100 | get_context_remaining: enabled, unsupported (warning color), disabled, unknown | **same** (3 screens) |
| controls-w24 | 24 | terminal-control sanitising (ESC/OSC/APC/NUL/CR in args and result) | **same** (3 screens) |
| controls-w40 | 40 | terminal-control sanitising (ESC/OSC/APC/NUL/CR in args and result) | **same** (3 screens) |
| controls | 100 | terminal-control sanitising (ESC/OSC/APC/NUL/CR in args and result) | **same** (3 screens) |
| errors-w24 | 24 | error results (notes read missing, history read gone) | **same** (3 screens) |
| errors-w40 | 40 | error results (notes read missing, history read gone) | **same** (3 screens) |
| errors | 100 | error results (notes read missing, history read gone) | **same** (3 screens) |
| history-w24 | 24 | history search (all sessions / current branch), empty, read with images, legacy header text, malformed spans | **same** (3 screens) |
| history-w40 | 40 | history search (all sessions / current branch), empty, read with images, legacy header text, malformed spans | **same** (3 screens) |
| history-w60 | 60 | history search (all sessions / current branch), empty, read with images, legacy header text, malformed spans | **same** (3 screens) |
| history | 100 | history search (all sessions / current branch), empty, read with images, legacy header text, malformed spans | **same** (3 screens) |
| malformed-args-w24 | 24 | malformed streamed arguments ([invalid op], null/array/object values, numeric offset labels) | **same** (3 screens) |
| malformed-args-w40 | 40 | malformed streamed arguments ([invalid op], null/array/object values, numeric offset labels) | **same** (3 screens) |
| malformed-args | 100 | malformed streamed arguments ([invalid op], null/array/object values, numeric offset labels) | **same** (3 screens) |
| messages-light | 100 | message cards under the light theme | **same** (3 screens) |
| messages-w24 | 24 | posthorse-reminder and context-window message cards: short, long, wide text, control characters | **same** (3 screens) |
| messages-w40 | 40 | posthorse-reminder and context-window message cards: short, long, wide text, control characters | **same** (3 screens) |
| messages | 100 | posthorse-reminder and context-window message cards: short, long, wide text, control characters | **same** (3 screens) |
| new-context-w24 | 24 | new_context request card with a 60-line handoff, and without | **same** (3 screens) |
| new-context-w40 | 40 | new_context request card with a 60-line handoff, and without | **same** (3 screens) |
| new-context | 100 | new_context request card with a 60-line handoff, and without | **same** (3 screens) |
| notes-pages-light | 100 | cards under the light theme | **same** (3 screens) |
| notes-pages-w24 | 24 | notes read/list/search cards: short, 490-char wide/emoji page, empty, list, search with "more", no notes | **same** (3 screens) |
| notes-pages-w40 | 40 | notes read/list/search cards: short, 490-char wide/emoji page, empty, list, search with "more", no notes | **same** (3 screens) |
| notes-pages | 100 | notes read/list/search cards: short, 490-char wide/emoji page, empty, list, search with "more", no notes | **same** (3 screens) |
| notes-writes-w24 | 24 | write / clear (empty content) / append cards with submitted content, long legacy text | **same** (3 screens) |
| notes-writes-w40 | 40 | write / clear (empty content) / append cards with submitted content, long legacy text | **same** (3 screens) |
| notes-writes | 100 | write / clear (empty content) / append cards with submitted content, long legacy text | **same** (3 screens) |
| pending-w24 | 24 | preparing (args streaming), running (partial result), finished; collapsed and expanded | **same** (3 screens) |
| pending | 100 | preparing (args streaming), running (partial result), finished; collapsed and expanded | **same** (5 screens) |
| resize | 100 | the same cards re-laid out at 100, 60, 30 and 100 columns in one session | **same** (4 screens) |
| theme-live | 100 | dark -> light -> dark switched live (/pt theme), cards and message | **same** (3 screens) |

Real scenarios (the actual tools, 100 and 40 columns): `real-notes` (write, read, append, wide 7 KB note, list, search, missing note error, clear), `real-context`
(enabled and `-disabled`), `real-history` (match and no match), `real-reminder` (a real checkpoint reminder at 78,000/100,000 tokens),
`real-rollover` (`new_context` with a 30-line handoff), `real-saved-messages` (saved legacy `context-window` and `posthorse-reminder` entries): all **same** (colors identical).
The only rows that differed in the first runs were file times (minute granularity), now normalised, and the footer token counts (host-owned).

Differences: only `click-message` (finding 1). No other screen differs, so no further port bug or host finding was recorded.

## Benchmarks (`ui_bench_test.go`, xd6702, linux/amd64, zero UITheme so ANSI wrapping per style is excluded)

| benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| CardNotePage40k collapsed (40,000-char page) | 1,062,000 | 29,360 | 546 |
| CardNotePage40k expanded | 7,108,000 | 1,468,683 | 14,761 |
| CardHistoryPage collapsed (40 entries, 20 KB) | 32,700 | 4,016 | 27 |
| CardHistoryPage expanded | 1,549,000 | 627,122 | 7,423 |
| CardMessage40k collapsed | 491,000 | 28,312 | 522 |
| CardMessage40k expanded | 10,467,000 | 2,105,013 | 22,149 |
| DisplayOf (history-search details) | 438 | 248 | 5 |

The collapsed note card wraps only `(rows+1)*width+1024` units like the original (`leadingRows`), the expanded rows are the SDK's `WrapTextWithAnsi` over the whole page (the original wraps the same text in `Text.render`; a host caches the rows per width).
`historySections` costs one forward pass (a first version sliced with `u16slice` per span: 700 µs → 33 µs on the history page).

### U4 before/after: `cardLeadingRows` on a 40,000-unit single-line page, collapsed, width 100 (`BenchmarkUiLeadingRows*`, xd6702, 200 iterations, `-benchmem`)

| script | before (`plainRune` pairs) ns/op | B/op | allocs/op | after (UAX #29 certain boundaries) ns/op | B/op | allocs/op |
|---|---|---|---|---|---|---|
| Latin | 239,934 | 26,784 | 461 | 198,830 | 26,784 | 461 |
| Cyrillic | 14,311,252 | 1,019,057 | 15,871 | 692,255 | 31,872 | 588 |
| Thai | 14,461,952 | 833,327 | 13,367 | 842,058 | 31,168 | 497 |
| Arabic | 13,666,585 | 1,027,308 | 16,859 | 705,738 | 32,184 | 623 |
| Hangul | 17,106,537 | 3,635,024 | 10,744 | 956,578 | 68,872 | 410 |
| emoji (ZWJ + skin tone) | 9,910,519 | 977,908 | 10,342 | 552,245 | 32,402 | 393 |
