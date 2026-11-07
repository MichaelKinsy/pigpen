# Mutation check: UiLane (ui.go, ui_cards.go, ui_display.go, ui_text.go and every `display*` construction site)

`UiLane.json` has **276** mutants (pigeq format `[{name,file,find,replace}]`, each `find` occurs exactly once; the list is the proof file, the
log of the runs is `UiLane.run.log`). They cover, one or more per function/contract row: card titles, call targets and statuses, call details
labels, markers/glyphs, title colors and bold, collapsed/expanded branches, row caps (5 summary rows, 3 preview rows, room arithmetic), the hint
and its key, attachments wording and plural/hidden forms, submitted content, note header slicing, legacy history prefix, history sections
(footer, span validation, tiling, trimming, UTF-16 split), every summary string of every display kind, the context summary arithmetic and
percent, `n` (grouping, rounding, fraction trim), `displayOf` validation per kind, `MarshalJSON` key order and members of the nine kinds, the
constructors and the nine production construction sites (`context_tools.go`, `notes.go`, `paging.go`, `history_pages.go`), message cards (types,
titles, label colors, padding clamp, inner width, preview rows, history line, hint, background, box padding, registration), text helpers
(whitespace set, `clean` ranges and fast-path triggers, `piText` tab/pad/width/blank, `leadingRows` line cut/limit/stop, hint).

## Result

| layer | count |
|---|---|
| killed by the Go tests (pigeq `--unit-only`) | **269** of 276 |
| survived, equivalent (reason below) | **7** |
| survived, not equivalent | **0** |
| invalid (did not build) | 0 (9 first-run builds fixed in the list; the last one, `site-context-rollover`, is killed) |

The screens (tmux Pi-vs-PiG) were not used as a mutation layer: the Go tests kill every non-equivalent mutant, including colors (ANSI of the host
palette, `ui_colors_test.go`) and the tool-result `details` shape (`TestUiDisplaySites`, `TestDisplayJSONKeyOrder`). The first-failing test per killed mutant
is in `UiLane.run.log` (largest groups: `TestUiResultCards` 51, `TestTwinRenderersUiTools` 41, `TestTwinRenderersUi` 37, `TestUiCallCards` 23, `TestUiMessageRows` 18,
`TestUiColorTokens` 16, `TestUiTextHelpers` 11, `TestUiDisplayOfAccepts` 9, `TestUiMessageColors` 7, the other tests of the notes/history lanes kill about 25 of the construction-site and wording mutants).

History of the check: the first run killed 46 of 276 on the lane's own tests, which showed that the card wording, colors, row caps and display kinds were pinned
only loosely by the 15 renderers twins. Added tests (all mine): `ui_cards_test.go` (every display kind and flag, collapsed and expanded, exact rows),
`ui_cards_more_test.go` (call cards, messages, box, text helpers, `leadingRows`/`safeCut`, display sites), `ui_cards_edge_test.go` (error/partial/legacy edges,
option-vs-context flags, sanitising, split pairs, previews), `ui_colors_test.go` (every theme token on the raw escapes), `ui_display_more_test.go` (summaries, `n`, sections,
`displayOf` accepts/rejects, JSON members). Second run on the 21 non-killed/invalid: 13 killed; one more after `TestUiSearchPreviewsAreOneRowEach`; 7 equivalent remain.

## Equivalent survivors (7)

| mutant | change | why it cannot change an observable |
|---|---|---|
| `clean-fast-c0` | the byte pre-scan treats `\n` as a trigger for the filter loop | the filter loop keeps `\n`; the output is identical, only the fast path is skipped |
| `n-negzero` | `neg := value < 0` instead of `math.Signbit(value)` | `strconv.FormatFloat(-0, 'f', -1)` already prints `-0`, so a negative zero keeps its sign either way (`TestUiNumberLimits` asserts the output) |
| `preview-sections-join` | the preview of several sections joins with a blank instead of `\n` | `cardPreviewBody` gets several sections only for `history-search`, whose preview is `cardSearchPreviews` (never `cardPreviewBody`); `history-read` has one section |
| `search-previews-count` | the search preview takes 4 sections instead of 3 | the collapsed view shows `rows[:min(3, room)]` and decides the hint on `details` (sections exist, so always true): the fourth row is never shown |
| `preview-rows` | `leadingRows(…, 4)` instead of 3 for the result preview | the shown rows are capped at 3 and the hint needs only `len(rows) > len(shown)`; with 3 or 4 rows asked the same decision and the same first three rows result |
| `leading-double` | the wrap limit grows by 1 instead of doubling | the loop ends on the same condition and returns the same rows; only the number of wrap passes (time) changes |
| `sec-valid-body` | a span may overshoot the body by one unit | the next iteration's span check or the final tiling test (`start == bodyLength+1`) still rejects every such list |

## Command lines

`pigeq mutate` copies the port into a temporary directory per mutant. Two facts of this tree made the stock command unreliable (recorded, not worked around in the tests):
the unit layer runs `go test ./...` of the copy, in which `../../port/oracle/src/notes.ts` (a notes-lane test) is missing, and all mutants share one `$TMPDIR`
while the fixture's default session directory is `os.TempDir()`, so a concurrent mutant's `all`-sessions history search reads another mutant's directories. The run therefore
used a private snapshot of `components/posthorse` (so concurrent `ph run` syncs cannot change it mid-run), `TMPDIR` per shard with a `port` symlink two levels above the copy,
and six shards of one job each:

```sh
# inside the xd6702 container, cwd = a private snapshot of components/posthorse (scratch under /home/kinsy/ph-work/scratch/UiLane/)
for k in 0 1 2 3 4 5; do          # shard k = mutants k, k+6, ... of port/mutations/UiLane.json
  mkdir -p $S/tmp/s$k; ln -sfn $S/snap/posthorse/port $S/tmp/s$k/port
  TMPDIR=$S/tmp/s$k pigeq mutate --go extensions/posthorse --mutations $S/shards/s$k.json \
      --unit-only --sdk-dir $PIG_SDK_DIR --jobs 1 > $S/res/s$k.log 2>&1 &
done; wait
```

The single command of the request, `ph run "cd components/posthorse && pigeq mutate --go extensions/posthorse --mutations port/mutations/UiLane.json --unit-only --sdk-dir \$PIG_SDK_DIR --jobs 6"`,
runs the same list but reports false kills (oracle file absent in the copy) and flaky ones (shared `$TMPDIR`) in this tree; use the sharded form above.
The twins of the lane also made their fixture robust to that sharing: `uiCwd` makes the working directory the session directory, as the original's `context(cwd)` does.
