# Review: UiLane scope (uireview)

Scope: TS `ui.ts`, `src/ui/cards.ts`, `src/ui/display.ts`, `src/ui/text.ts` against Go `ui.go`, `ui_cards.go`, `ui_display.go`,
`ui_text.go`, plus every `display*` construction site (`context_tools.go`, `notes.go`, `paging.go`, `history_pages.go`).
Oracle: Node 24.22.1, Pi 1.5.0 / pi-tui 1.5.0 (`/home/kinsy/ph-work/pi-oracle`); pig 0.7.0+1.5.0; Go 1.27.3, all on xd6702.

Proof file: `extensions/posthorse/review_uireview_test.go` (test-only). Probes: `port/review/uireview-probes/`:
- `cases.ts` renders the original's `toolCards(...).renderCall/renderResult` and the `registerPosthorseMessages` renderer for
  656 adversarial cases at widths 1, 2, 3, 4, 5, 6, 7, 8, 13, 24, 40, 100 (big texts expanded at 3 and 40) and writes inputs + rows;
  `TestReviewUIDifferential` recomputes every case in Go (`cardCallComponent`, `cardResultComponent`, `cardMessageRows`, zero `UITheme`
  = no palette, chalk bold) and compares row by row; `TestReviewUISummaries` compares `pageSummary`/`n` and the context summary.
  Regenerate: `ph sync`, then in the container `sh components/posthorse/port/review/uireview-probes/run-ts.sh` and
  `sh .../run-go.sh TestReviewUI` (the TS output is shipped as `extensions/posthorse/testdata/ui_cases.json.gz` by `port/tools/gen-testdata.sh`;
  the tests fail when it is absent).
- `run-bench.sh`: `TestReviewSafeCutScripts` and `BenchmarkReviewLeadingRows*`.
- `tmux/gen.py` (adversarial fixtures + 21 scenarios for the `port/tmux` card probe), `tmux/run.sh` (probe mode into the
  reviewer's scratch), `tmux/run-real.sh` (real mode, prints the `details` persisted in each host's session files).

The self-contained proof tests (`TestReviewCleanTextLoneSurrogate`, `TestReviewContextSummaryPercent`,
`TestReviewLocaleNumberShortest`, `TestReviewNoteHeaderLengthNonInteger`, `TestReviewHistorySectionsSplitPair`) assert the
original's values and fail on the current port.

## Findings

| ID | severity | TS file:line | Go file:line | what differs | proof | proposed fix |
|---|---|---|---|---|---|---|
| U1 | parity-break | text.ts:9-12 (`clean`), cards.ts:104 and 246 (`.replace(/\s+/g, " ")`) | ui_text.go:46-61 (`uiCollapseSpaces`, `range s`), 66-99 (`cleanText` filter loop, `range value` + `WriteRune`) | A lone surrogate (the SDK's WTF-8 form `ED A0..BF xx`) becomes **three U+FFFD**. The original's regexes leave the UTF-16 unit alone. `cleanText` takes the decoding loop whenever the text also holds an ESC, a C0/C1 control, or any U+0080–U+00BF or U+F000–U+FFFF character (`·`, `°`, NBSP, fullwidth `，`, the U+FE0F of `❤️`, ...); `uiCollapseSpaces` always decodes (collapsed call headings, history search previews). Reachable without malformed input: a paginated `notes read` (notes.ts `text.slice(at, end)`) or `history read` (history-pages.ts `item.text.slice(offset, end)`) cut through an emoji leaves a lone high surrogate at the end of one page and a lone low surrogate at the start of the next; the card then shows `���` (three cells, re-wrapped rows) where Pi has one unit. | `TestReviewCleanTextLoneSurrogate` (e.g. `cleanText("页面结束，\xed\xa0\xbd")` = `"页面结束，���"`); `TestReviewUIDifferential` lone-* cases: 33 of 40 differ, e.g. `lone-read:loFullwidth/c` w8 row 4 ts `"\xed\xb8\x80，下一页"` go `"���，下 "`, `lone-call:hiPlain/c` w40 (`uiCollapseSpaces` only). Text without a trigger character keeps the unit and matches (`lone-read:hiPlain`). | iterate by `isWTF8Surrogate`/`utf8.DecodeRuneInString` and copy the source bytes (`b.WriteString(value[i:i+size])`, 3 bytes for a WTF-8 surrogate) instead of `range` + `WriteRune`, in both functions |
| U2 | parity-break (low) | display.ts:231 `` (≈${Math.round(usage.percent)}%) `` | ui_display.go:597 `n(math.Floor(*display.usagePercent+0.5))` | The original prints the rounded percent with plain `${}` (no grouping) and JS `Math.round`. Go groups thousands (`≈1,250%` vs `≈1250%`, reachable when the session holds ≥ 10× the context window, e.g. after switching to a smaller model) and `Floor(p+0.5)` rounds 0.49999999999999994 up (`≈1%` vs `≈0%`). The tool text (context_tools.go) is right; only the card summary differs. | `TestReviewContextSummaryPercent`; `TestReviewUISummaries` ctx:p1250 / ctx:p999.5 / ctx:phalf; real terminal: tmux `ur-ctx` (`collapsed.txt` rows 13 and 21 differ under Pi and PiG) | `" (≈" + jsIntegerString(jsRound(*display.usagePercent)) + "%)"` (both helpers exist: context_tools.go:24, js.go) |
| U3 | parity-break (low, model-reachable) | display.ts:209 and cards.ts:30 `value.toLocaleString("en-US")`, used by cards.ts:69-73 (`Offset`/`Image offset`/`Limit` labels of the expanded call card) | ui_display.go:516-548 (`n`) | ICU formats the **shortest** decimal of the double and rounds **half-expand** to 3 fraction digits; `n` formats the exact binary value (`FormatFloat 'f'`) and rounds half-even: `0.0625` → `0.062` (TS `0.063`), `1.0005` → `1` (TS `1.001`), `12345678901234567890` → `12,345,678,901,234,567,168` (TS `…,567,000`), `1e23` → `99,999,999,999,999,991,611,392` (TS `100,000,000,000,000,000,000,000`). The call card renders the model's streamed arguments before validation, so a model that sends such an `offset`/`limit` sees a different label. (The lane note 4 says these are only reachable from foreign saved JSON; the call card makes them model-reachable.) Integers below 2^53 are equal. | `TestReviewLocaleNumberShortest`; `TestReviewUISummaries` page:3/4/10/11; differential `call-num:3,4,10,11` (expanded call cards, all 12 widths) | build from `strconv.FormatFloat(v, 'e', -1, 64)` (shortest digits + exponent), round that digit string half-up at 3 fraction digits, then group |
| U4 | perf | text.ts:59-69 (`leadingRows`: cut at the grapheme containing unit `(count+1)*width+1024`) | ui_text.go:225, 241-275 (`safeCut`, `plainRune`) | `safeCut` accepts a cut only between two "plain" runes (ASCII/Latin-1, kana, Han). Cyrillic, Greek, Arabic, Hebrew, Thai, Devanagari, Hangul and emoji text separated by plain spaces has no such pair, so `safeCut` returns `len(head)` and the collapsed preview wraps the **whole** first four lines instead of ~1,424 units. Rows are equal (differential), cost is not. A 40,000-unit single-line note page at width 100 (one collapsed render): Latin 0.218 ms / 27 KB / 461 allocs; Cyrillic 13.6 ms / 1.0 MB / 15,871 allocs; Thai 14.1 ms; Arabic 13.1 ms; Hangul 16.2 ms / 3.6 MB; emoji 9.4 ms (60–75× slower). The original's cost is the same for every script. | `TestReviewSafeCutScripts` (Latin cut at unit 1,424; the five others at 40,000 of 40,000); `BenchmarkReviewLeadingRows*` (xd6702, 4 CPUs, numbers above) | also accept a cut **before U+0020** when the previous rune is not Grapheme_Cluster_Break=Prepend (UAX #29 GB9b is the only rule that joins anything to a following space; Prepend is U+0600–0605, 06DD, 070F, 0890–0891, 08E2, 0D4E, 110BD, 110CD, 111C2–111C3, 1193F, 11941, 11A3A, 11A84–11A89, 11D46, 11F02) |
| U5 | nit (foreign/malformed `details` only) | cards.ts:186-191, 199-201 (`headerLength > 0`, `body.slice(headerLength)` on the number) | ui_cards.go:207-212 (`int(*display.headerLength)`) | `int()` truncates before the `> 0` test: a saved `headerLength` of 0.5 drops the "… to expand" hint the original shows; 1e19 overflows to a negative int, so Go shows the body the original slices away. | `TestReviewNoteHeaderLengthNonInteger`; differential `notes:hl0.5/c`, `notes:hl1e19/c` | keep the float: compare `*display.headerLength > 0` and slice with `u16sliceF(body, h, math.Inf(1))` (js.go) |
| U6 | nit (malformed `details` only) | display.ts:183-189 (`raw.slice` by UTF-16 units) | ui_display.go:465-500, 729-738 (`u16Cursor.to` advances whole runes) | A span boundary inside a surrogate pair: the original splits the pair (header gets the high half, body starts with the low half); Go moves the whole character into the header, so the body loses a unit. Spans the tools write never split a pair (headers end in ASCII, parts are joined by `\n`). | `TestReviewHistorySectionsSplitPair`; differential `hs:astral-split` | when `to(unit)` would step past `unit` inside a 4-byte rune, cut it (`u16slice` semantics) |

## Pi-vs-PiG screens (`port/tmux`, re-run by the reviewer)

Existing scenarios re-run (probe mode, own scratch): `history-w24`, `messages-w40`, `malformed-args`, `notes-pages-w24`, `controls-w24`,
`pending` (5 screens), `context-w24`, `notes-writes-w40`, `new-context-w24`, `resize` (4 widths), `click-card`, `theme-live`,
`history-w60`, `messages-light`: **all screens same** (plain and colour escapes). `click-message`: `clicked-reminder` and
`clicked-window` differ exactly as the lane's finding 1 says (Pi expands the clicked message, PiG does not); `closed`,
`global-expanded`, `global-collapsed` same.

Real mode (`run-real.sh`): `real-context`, `real-notes`, `real-history`: all screens same, and the `details` persisted in the
session JSONL are **byte-identical** under Pi and PiG, member order included (e.g.
`{"kind":"context","usage":{"tokens":1001,"contextWindow":100000,"percent":1.001},"rollover":"enabled","rolloverAt":83617}`,
`{"kind":"notes-search","headerLength":0,"count":20,"page":{"offset":0,"end":2540,"total":5100}}`,
`{"kind":"history-search","entries":[],"footerLength":52,"more":false,"skipped":2}`).

New adversarial scenarios (`uireview-probes/tmux`, 21 scenarios, each collapsed / Ctrl+O expanded / collapsed again):

| scenario | columns | content | pi vs pig |
|---|---|---|---|
| ur-notes-w100/40/8/4/2 | 100, 40, 8, 4, 2 | note reads: CJK + emoji (ZWJ, flags, keycaps, skin tones), Arabic/Hebrew + bidi controls (RLO/PDF/RLM/LRM/isolates/ALM), ANSI-laden text (SGR, OSC 8, CSI with non-SGR finals, unterminated CSI, APC, `ESC c`, C1 CSI/NEL, U+FFF9–FFFB, NUL/BEL/BS/DEL, tab), 3,000-mark zalgo grapheme | **same** |
| ur-history-w100/40/8/4/2 | 100, 40, 8, 4, 2 | history search sections with the same CJK/RTL/ANSI bodies, query with a tab | **same** |
| ur-msgs-w100/40/8/4/2/1 | 100, 40, 8, 4, 2, 1 | `posthorse-reminder` and `context-window` messages with CJK/RTL/ANSI text | **same** (w8: only the host footer's token percentage, not a card) |
| ur-big-w100/40 | 100, 40 | a 10,000-line note and a 21,000-character Thai line | **same** |
| ur-bigmsg-w100/40 | 100, 40 | 10,000-line reminder and context-window messages, zalgo reminder | **same** |
| ur-ctx | 100 | context cards with percent 1250.4 and 0.49999999999999994 | **differs**: U2 (`≈1250%`/`≈1,250%`, `≈0%`/`≈1%`) |

## Checked and found equal

- **Function-level differential** (656 cases × up to 12 widths, rows byte for byte): note-read, legacy (no details) notes, error
  and partial results, write/append with submitted content (incl. `(empty)`), `new_context` handoff, history search with spans and
  footer, legacy history prefixes (incl. `\r`, U+2028, U+2029 line starts and `[chars a-b of c]`), history read with images /
  `(display hidden)`, all four call cards collapsed and expanded with every text as `path`/`query`, preparing/running/error states,
  malformed arguments (`op` number, `path` object/null, `offset` string, `limit` null, `imageOffset` array, args as array or string),
  message cards (string and array content, missing handoff label, `outputPad` 0/1/2/5/50) for these texts: CJK, emoji, combining
  marks, a 5,000-mark grapheme, Thai, Arabic, Hebrew, bidi controls, ANSI/OSC/APC/C1 controls (incl. `ESC[2A…m` consumed up to the
  `m` as pi-tui does, and a trailing lone ESC), tabs, NBSP/U+3000/BOM, CRLF/CR/U+2028/U+2029/VT/FF, a 500-character word, blank and
  whitespace-only text, a 10,000-line note, 21,000-character Thai and 39,000-character Arabic lines. Only U1, U2, U3, U5, U6 differ.
- **`cleanText`** fast path (ESC/C2/EF/C0/DEL triggers) against `stripTerminalSequences` + the regex; **`piText`** = pi-tui `Text(…,0,0)`
  (empty/whitespace text, tab → 3 spaces, `max(1,width)`, padding); **`piBox`** = pi-tui `Box` (padding, background, no truncation);
  **`cardLeadingRows`** head cut (`split("\n", count+1)`), the `text.length > 0` test on the raw text, the doubling loop and its
  stop condition; `safeCut` cuts at or after the original's boundary, which yields the same first `count` rows (a cut token that
  starts row `count+1` cannot move back to row `count`).
- **`displayOf`**: every `isDisplay` branch incl. JSON `null` for optional numbers, `usage` without `tokens`/`percent`, invalid
  `rollover`, `null` `rolloverAt`, non-boolean `more`, fractional `count`, non-record entries; `historySections` (`validFooter`,
  `validSpan`, tiling, fractional/negative/oversized spans and footers); `historySearchSummary` (`more ?? footerLength > 0`, skipped),
  `historyReadSummary` (image branches), `pageSummary`, `summaryOnly`, `displaySummary` (`content === ""`, `all === true`).
- **`MarshalJSON`** key order of all nine kinds = the TS object literals at context-tools.ts:22-34, 84, notes.ts:133-138, 167, 200,
  paging.ts:127-136 (`more` only when true), history-pages.ts:107-113, 172-181; confirmed on disk (above).
- `toolCards` wiring for all four tools (context_tools.go:110,124, history.go:213, notes.go:668), `registerPosthorseMessages`
  order (extension.go:114 = index.ts:51), `KeyText` is local (no host round trip), the host caches proxy rows per width
  (`render_proxy.go`), so the expanded-card cost (lane benchmarks) is paid once per width as in Pi.

## SDK / owner items

- Lane finding 1 (message-card click) re-confirmed on a real terminal (`click-message`); nothing to add.
- No other host/SDK gap found in this scope.
