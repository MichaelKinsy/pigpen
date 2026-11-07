# Review: HistoryLane scope (historyrev)

Scope: TS `src/history-entries.ts history-search.ts history-pages.ts history.ts session-files.ts` (+ `session-lines.ts` as
used) against Go `history_entries.go history_search.go history_pages.go history.go session_files.go session_lines.go`.
Oracle: Node 24.22.1 (V8/ICU, `RegExp.escape`), Pi 1.5.0 node_modules of `port/oracle`, ripgrep 14.1.0 (`/usr/bin/rg`) on
xd6702; Go 1.27.3.

Proof file: `extensions/posthorse/review_historyrev_test.go` (test-only, `package posthorse`). Probe scripts:
`port/review/historyrev-probes/probe.mts` (differential oracle data) and `perf.mts` (`gen` / `run [norg]`). To rerun:
`ph run "cd components/posthorse/port/review/historyrev-probes && node probe.mts"`, then
`go test -run TestReviewHistoryrev .`; the differential tests read `extensions/posthorse/testdata/historyrev_*.json.gz` (shipped; `port/tools/gen-testdata.sh` regenerates them) and fail
when absent. The head-to-head perf test (`HISTORYREV_PERF`) was removed: its 276 MB archives cannot ship; `perf.mts` stays for the TS side and `history_bench_test.go` is the Go side. Perf: `node perf.mts gen && node perf.mts run [norg]`, Go side
`HISTORYREV_PERF=1 [HISTORYREV_NORG=1] [HISTORYREV_ONLY=a200/lorem] go test -run TestReviewHistoryrevPerfShapes -v .`.

## Findings

| ID | severity | TS file:line | Go file:line | what differs | proof | proposed fix |
|---|---|---|---|---|---|---|
| H1 | parity-break (low; search results) | history-search.ts:311, history-entries.ts:156, 169, 183, 250 (`toLowerCase`) | history_entries.go:427-498 (`historyToLower`, `finalSigma`, `isCaseIgnorable`) | `historyToLower` is a second, incomplete copy of `jsToLowerCase`: its Case_Ignorable list lacks U+0387, U+055F, U+05F4 (MidLetter), which ICU skips for Final_Sigma. `"ΑΣ·Β"` lowers to `"ας·β"` (V8 `"ασ·β"`), `"Α·Σ"` to `"α·σ"` (V8 `"α·ς"`). A query typed in lowercase (`"ασ·β"`) finds the entry in TS and nothing in Go; seenText (in-context skip) is affected the same way. | `TestReviewHistoryrevFinalSigmaCaseIgnorable`; `TestReviewHistoryrevLowerDifferential`: 20,000 random strings, `historyToLower` 47 mismatches, `jsToLowerCase` 0; node: `"ΑΣ\u0387Β".toLowerCase()` = `ασ·β`, `.includes("ασ·β")` true | delete `historyToLower`/`finalSigma`/`isCased`/`isCaseIgnorable`; call `jsToLowerCase` (js.go:576, already V8-verified) in `historyIndex`, `seenText`, `historyMatches` |
| H2 | parity-break (error text to the model) | session-files.ts:279 (`readdir`), 301 (`stat`) | session_files.go:784, 794 (`os.ReadDir`), 841 (`os.Stat`) | Raw Go errors leak: `open <dir>/locked: permission denied` instead of `EACCES: permission denied, scandir '<dir>/locked'`; `stat <f>: no such file or directory` instead of `ENOENT: no such file or directory, stat '<f>'`. Reached by `history search all=true` / any `history read` miss when a directory under the session dir is unreadable, or a session file is deleted between listing and stat (concurrent session cleanup). | `TestReviewHistoryrevSessionFilesErrorText`; node probe G: `EACCES: permission denied, scandir '<base>/sess/locked'`, `ENOENT: no such file or directory, stat '<base>/sess/missing.jsonl'` | `nodeFsError(err, "scandir", path)` for both `ReadDir` calls, `nodeFsError(err, "stat", file)` in `newestFirst` |
| H3 | parity-break (low; error text) | history-pages.ts:124, 132 (`${imageOffset}`, `${offset}`) | history.go:134-148 (`historyParamsOf`, `jsToInt`), history_pages.go:155, 158 (`%d`) | TypeBox `Integer` accepts 1e20, 2^60, 1e21. TS prints the JS number; Go truncates/clamps to `int`: `Offset 9223372036854775807 …` vs `Offset 100000000000000000000 …`; `Offset 1152921504606846976 …` vs `… 1152921504606847000 …`; `Image offset 9223372036854775807 …` vs `Image offset 1e+21 …`. Same class as notestools N5 (fixed in notes.go with `jsNumberString`). | `TestReviewHistoryrevReadOffsetNumberText` | keep `offset`/`imageOffset` as `float64` (or keep the raw value beside the int) and print with `jsNumberString` in `readOffsets` |
| H4 | robustness (SDK) | session-files.ts:190 (`JSON.parse(line)`), 325 (header) | session_files.go:593 (`json.Unmarshal`), `sessionHeader` | The SDK decoder refuses nesting deeper than 10,000 (`sdk/json/scanner.go:149` `maxNestingDepth`); V8's `JSON.parse` has no depth limit. A session line holding a value nested 10,001+ deep (e.g. a tool `details`) parses in TS and is silently skipped by the port: the entry is missing from search and `history read` cannot find it. Depth 9,999 parses in both. | `TestReviewHistoryrevParseSessionLineDifferential` (lines `d` depth 10,001 and `d3` depth 100,000: Go ok=false, oracle ok=true) | SDK: an unlimited-depth (or iterative) decode for file content (e.g. a `json.Decoder` option); owner decision |
| P1 | perf (owner rule: not slower than TS) | session-files.ts:233-251, history-search.ts:208-234 | session_lines.go:203 (`slices.Clone`), 225 (`jsFromBytes` copy), session_files.go:593 (`[]byte(line)` copy + `json.Unmarshal` validate+decode), history_entries.go:264-296 (`newHistoryHit` for every line) | Common query over one 276 MB archive, `all: true` (every line parsed and ranked): **Go 8.24-8.58 s vs TS 4.93-5.64 s** (median of 5, two alternating runs, same data, same rg). All other shapes Go is faster (table below). CPU: `json.Unmarshal` 42 % (its `checkValid` pre-pass 15 %), `newHistoryHit` 16 %, GC 10 %; allocations 2.8 GB per search: SDK `objectInterface` 32 %, `jsFromBytes` 19 %, `strings.Builder` (hit header/excerpt) 17 %, `parseSessionLine` 9 %, `slices.Clone` 9 %. Each selected line is copied three times (clone, string, `[]byte` for Unmarshal) and every hit builds its header+excerpt although `archiveCollector` keeps only the newest `limit+2` per priority. | `TestReviewHistoryrevPerfShapes` + `perf.mts` (data `gen`); pprof of `HISTORYREV_ONLY=a200/lorem` | (1) one copy per line: decode `[]byte` directly (`parseSessionLine([]byte)`, `jsFromBytes` only when invalid UTF-8, no `slices.Clone` + string copy); (2) build hit `text`/`headerLength` lazily for hits that survive `archiveCollector` (ranking needs only id+priority; the cursor offset check runs in `accumulator.add` after `finish`); (3) compute `isASCII`/lowercase of `item.text` once per hit (`historyIncludes` + `historyIndex` lower it twice); (4) SDK: single-pass `Unmarshal` (owner) |

## Perf shapes (xd6702 container, 8 CPUs, same generated data; median of 5 calls of `historyMatches`, `all: true`)

| shape | query | TS rg | Go rg | TS no rg | Go no rg |
|---|---|---:|---:|---:|---:|
| a200: one 276 MB archive, 700k entries | rare (`zebra-marker-77`, 100 hits) | 2,729 ms | 649 ms | 3,103 ms | 639 ms |
| a200 | common (`lorem`, every line) | 5,094 ms (rerun 4,933 / 5,639) | 8,264 ms (rerun 8,581 / 8,238) | 7,362 ms | 8,446 ms |
| a200 | absent (`qqqxyz`) | 114 ms | 88 ms | 2,849 ms | 612 ms |
| s1000: 1,000 sessions + 20 nested subagent dirs, 84 MB, 10 % other cwd | rare | 51 ms | 30 ms | 261 ms | 62 ms |
| s1000 | common | 48 ms | 24 ms | 38 ms | 10 ms |
| s1000 | absent | 45 ms | 18 ms | 2,238 ms | 570 ms |

Memory: TS process RSS 400-626 MiB after the a200 searches; Go heap Sys 11-15 MiB (bounded as designed). Hit lists (id,
priority, UTF-16 length) of s1000 rare/common and a200 rare are identical between TS and Go (`HISTORYREV_IDS=1`, diffed).

## Lane notes re-checked against Node

1. `needleLines(["İ"])` matches every line (node: `.test("abc")` true): accurate and unreachable (`prefilterNeedles`
   always puts the fragment first; for `İ` the needles are `["i̇","İ"]`).
2. `/i` over the fold alternatives accepts the Latin-1 case variants `C2 84 AA` and `E5 BF` (node: true for both): accurate;
   the Go matcher agrees on 38,407 random windows (below).
3. **Stale**: "`1e999` … the SDK's Go decoder rejects the number". The SDK now reads an out-of-range number as ±Inf like
   `JSON.parse` (`sdk/json/decode.go:886-897`, `isInfinityOfRange`); `{"…","details":1e999}` parses in both
   (`TestReviewHistoryrevParseSessionLineDifferential`). Remove finding 3 from the lane notes / PORT.md.
4. Excerpt index measured in the lowercased text applied to the original: accurate; the hit differential (İ in the pool)
   agrees with Node on all 14,080 hits.

## SDK / owner items

* H4: SDK JSON nesting limit (10,000) vs `JSON.parse` (none) for file contents.
* P1 (4): SDK `Unmarshal` validates the whole input before decoding (`checkValid`, 15 % of the common-query scan).
* P1 as a whole: a perf regression on one shape; per the owner rule it needs the fix or a decision draft.
* Known and not re-reported: JSON key order lost in SDK decoding; abort message texts (corereview R2).

## Checked and found equal

* **Lowercasing / matching**: `historyIndex` (ASCII `foldIndexString` fast path and the lowered UTF-16 path) = `toLowerCase().indexOf`
  on 20,000 random strings (Greek sigma contexts, İ, U+0307, Kelvin, long s, Cherokee, ΐ/ΐ, lone surrogates, astral): 0
  mismatches (H1 shows only when query and text are lowered differently). `historyHit` (priority, file-qualified id `id@shortKey(rel)`,
  `[subagent]` tag, header length, `excerptAround` at the lowered index): 14,080 random hits, 0 mismatches.
* **Prefilter**: `prefilterNeedles` (split on `"\[](){}:$`, `\p{Cc}`, `\p{Cs}` incl. lone surrogates; `trim` incl. U+FEFF/NBSP;
  stable longest-first; `LABEL_SOURCES`; `image` rule; `İ` needle): 20,000 random queries, 0 mismatches. `needleLines`:
  38,407 windows (Latin-1 path with k/s folds, `C2`/`E5` variants, dotted I, overlap; UTF-8 path with `(?i)` regexp and
  `stableAnchor`), 0 mismatches. `(?i)` vs `/iu` equivalence classes over all 3,037 code points with a case mapping, and every
  rune with a non-trivial `SimpleFold` orbit covered: 0 mismatches; `foldStable` never marks a character JS folds with a
  non-ASCII partner.
* **ripgrep**: flags, order, `--regexp` per needle, `--` then cwd-relative paths, groups of 256 KiB (30,000 on Windows) by
  UTF-16 length, managed `<agentDir>/bin/rg` before `rg`, exit 1 = none, ENOENT (missing binary or cwd) drops the binary,
  other failures keep the group, abort rethrown; real rg 14.1.0 end-to-end: same hits as TS, absent query filtered in 88 ms.
  Needles never contain lone surrogates (split), so Node's UTF-8 argv conversion cannot differ.
* **Cursors**: `Buffer.from(s,"base64url")` (both alphabets, junk skipped, stop at `=`, dangling sextet dropped): 20,000
  strings, 0 mismatches. `decodeCursor` with `JSON.parse` on 81 payloads (BOM, trailing comma, `-0`, `1e400`, `1e-400`,
  `9007199254740992`, `0.5`, `01`, U+2028/NBSP/CR around the value, `\ud800` id, escaped key, std base64 with padding,
  junk-separated): 0 mismatches. Scope key and emitted cursor for lone-surrogate/astral/U+2028/`<>&` queries equal node's
  (`TestReviewHistoryrevCursorKeys`). Accumulator seeking/anchor/`past the entry`/`no longer matches`, archive collector
  `limit+2` window and anchor reset, ranking (priority groups, newest first), lookahead, `searchParts`/`hitFragment`
  arithmetic (prefix, `from char`, header span clamp, `available -= len+1`), reserve/`too large` refusal, skipped note
  only without cursor: read line by line, equal.
* **Read pages**: `readOffsets` defaults (`imageOffset ?? (offset === 0 ? 0 : images)`), both errors' conditions, `shownEntryId`,
  `pages.size` request (`imageOffset` only when the entry has images), `imageEnd` floor (chars ≥ MIN, so no negative slice),
  `end`, continuation text with/without `imageOffset`, header: equal (only H3's number text differs).
* **Ids and scope**: `entryReference` (`same@` = defined empty key matches no file), `/^[\w-]+$/` needle `"id":"…"`, branch first
  only without a key, `startsWith(shortKey(rel))` for 10-char and legacy longer keys, `relative()` = `filepath.Rel` for
  descendants; `membership` (current file always, header `type: "session"` + string `id`, `resolve(cwd)` lexical so
  symlinked cwds behave the same, nested files inherit the nearest `<dir>.jsonl` ancestor, memoized); `sessionFiles`
  breadth-first, symlinks neither followed nor listed, `.jsonl` without `.intent.`; readdir order = byte-sorted in Node
  (probe G: `10, 9, B, Z, _x, a-, a, a0, b, é`) = `os.ReadDir`; `newestFirst` `mtimeMs` formula and stable ties;
  `searchPhases` 256 MiB split; `inCurrent` via `resolve`; projection snapshot at invocation for `all`, lazy otherwise.
* **Lines and parsing**: `parseEntryHead` = `ENTRY_HEAD` (`[^"\\]*`, `null|"…"`, `[,}]`, compaction/context_window excluded);
  head-only lines only advance ancestry; `windowTracker` (map holds only non-initial windows; repeated id deletes) = the
  `Map` projection; `selected` rule; `parseSessionLine` normalisation (string fields, `null` parentId/replacement kept,
  `display`/`isError`/`excludeFromContext` booleans), duplicate keys (last wins), `\ud800` escapes kept, `__proto__` key, BOM
  line rejected, CR before LF accepted, `1e999` (see note 3): equal except H4. `rangeText` short reads and "Session file
  changed" condition; open error via `nodeFsError`.
* **Entries**: `flattenMessage` (bashExecution/excludeFromContext, toolResult identity), `flattenEntry`, `contentText`,
  `ownAssistantText`, `originalMatch` (string-equality with `originals[0]`), `echoPriority`, `entryContent`, `toWindowedEntry`,
  `isWindow`, `windowHandoff`, `contextStart` (`undefined === undefined` for a missing `firstKeptEntryId`), `contextEdits`
  (last wins), `seenText`, `contextVisibility` (reminders never in context, `targetId` for edits, joined projected text):
  equal for Pi-shaped entries.
* **Perf/memory**: bounded memory confirmed (Go heap Sys ≤ 15 MiB on the 276 MB archive); no per-call regexp compile
  except the non-ASCII needle regexp (once per search, as TS); 1,000-session `all` scope faster than TS in every cell.
