# Lane B: history (HistoryLane)

Files: `history_entries.go`, `history_search.go`, `history_pages.go`, `history.go`, `session_files.go`, `session_json.go`
(ports of `src/history-entries.ts`, `history-search.ts`, `history-pages.ts`, `history.ts`, `session-files.ts`; `session_json.go`
is the `JSON.parse` + `parseSessionLine` half of `session-files.ts`; `session_lines.go` (CoreLane's) was changed for P1, see below).
Tests: `twin_posthorse_history_test.go` (24 twins), `twin_archive_test.go` (2 twins), `history_test.go`
(unit), `history_ripgrep_test.go` (fake-rg contract), `history_bench_test.go` (benchmarks), `session_json_test.go` +
`session_json_reference_test.go` (the JSON reader against the SDK decoder), `history_parity_test.go` (differential tests against
Node, from the review `port/review/historyrev.md`; they read `/home/kinsy/ph-work/scratch/historyrev/*.json` written by
`port/review/historyrev-probes/probe.mts` and skip when absent).

Twins: 26 exact, 0 skipped (`pigeq twins check --files posthorse,archive`: 65 exact twins, 0 skipped over the whole
ledger at the time of the run). Red log: `port/red/HistoryLane.log`.

## Naming and API decisions

| TS | Go | Why |
|---|---|---|
| `historyHit()` (function) | `newHistoryHit()` | the TS type `HistoryHit` is the Go type `historyHit` (contracts.go) |
| `HistoryMatches` | `historyMatchSet` | the function `historyMatches` keeps its TS name |
| `LineFilter = (entry) => boolean` | `lineFilter func(id string) bool` | both callers read only `entry.id`; avoids building an entry for head-only lines |
| `HistoryParams` | `historyParams` (query/id carry presence; limit/offset/imageOffset are `*int`; cursor `""` = absent) | `undefined` vs `""` |
| `registerHistory(pi, policy, pages)` | `registerHistory(ext, policy snapshotReader, pages pageAccess)` | `policy(ctx)` is called once per use, as the TS does (each call is its own snapshot) |
| `windowProjection()` returns a closure | `windowProjection() *windowTracker`, `.project(entry rec)`, `.track(id, parent, isWindow)` | head-only lines project from three facts without a map per line |
| async generators | `iter.Seq2[T, error]` (`scopedSessionFiles`, `sessionWindowEntries`) | the CoreLane convention |
| `sessionWindowEntries(ctx, file, wanted, matcher)` | `sessionWindowEntries(ctx, file, wanted, matcher, forRead bool)` | a search gets entries that alias the read buffer (valid within the loop step) and no images; a read gets owning entries with images |
| `AbortSignal` | `ctx context.Context` | `signalOf(sdkCtx)` at the tool boundary; an abort returns `throwIfAborted(ctx)` |

`contextStart(entries) (start, boundary rec)`, `contextEdits(entries) map[string]rec`, `windowHandoff(entry) (string, bool)`,
`parseSessionLine(line) (rec, bool)` are called by the recovery and note-store lanes.

## Contract table

| ID | Upstream | Go | Test |
|---|---|---|---|
| H1 | history.ts:82-148 `registerHistory`: schema, description, snippet, guidelines, ops | `history.go registerHistory`, `historySchema` (SDK TypeBox port, declaration order; `TestToolParametersMatchTypeBox`) | twins 14-22, 28, 29, 36, 37, 50-58 |
| H2 | history.ts:13-70 `entryReference`, `findEntry` (`id@fileKey`, 10-char and legacy full keys, `same@` = defined empty key) | `history.go entryReference`, `findEntry` | posthorse 15, 16, 52, 53, 54, 55, 58 |
| H3 | history-search.ts:305-329 `historyMatches`; cursors 45-70; accumulator 121-173; archive collector 174-203 | `history_search.go` | posthorse 19, 22, 36, 37; `TestDecodeCursor`, `TestSearchAccumulatorCursorSeeking` |
| H4 | history-search.ts:85-110 `contextVisibility` (projection snapshot at invocation time for `all`) | `contextVisibility` | posthorse 18 |
| H5 | history-pages.ts `searchPage`, `historyReadPage` (chars budget, image pages, offsets, retry offsets) | `history_pages.go` | posthorse 17, 28, 29, 36, 55; `BenchmarkReadPage`, `BenchmarkSearchPage` |
| H6 | history-entries.ts flatten/echo ranking/original match/window ancestry | `history_entries.go` | posthorse 17, 19, 21, 50, 57 |
| H7 | session-files.ts `prefilterNeedles`, `needleLines`, `LABEL_SOURCES`, `ASCII_FOLDS` | `session_files.go` | posthorse 56, 57, archive 1; `TestPrefilterNeedles`, `TestNeedleLinesMatcher`, `TestFoldPatternDifferential` |
| H8 | session-files.ts `ripgrepFilter`, `execFileText`, `commandGroups` | `session_files.go ripgrepFilter` | posthorse 56; `TestRipgrepPrefilterContract` |
| H9 | session-files.ts `parseSessionLine`, `entryHead`, `sessionWindowEntries` | `parseSessionLine`, `parseEntryHead`, `sessionWindowEntries` | posthorse 20, 57; archive 0 |
| H11 | session-files.ts:190, 211-241, 325 `JSON.parse` of a line, `parseSessionLine`, header | `session_json.go jsonParse`, `parseSessionLine`, `parseSessionLineView` | `session_json_test.go`, posthorse 20, 57; `TestHistoryParityParseSessionLineDifferential` |
| H10 | session-files.ts `sessionFiles`, `newestFirst`, `searchPhases`, `sessionHeader`, `parentSession`, `membership`, `scopedSessionFiles` | same names | posthorse 15, 16, 54, 58 |

## Observable-behaviour notes (what was matched, how)

* **Ripgrep**: the original uses `child_process.execFile` (not `pi.exec`), so the port uses `os/exec`: binaries
  `[<agentDir>/bin/rg, rg]`, same flags, `cwd` = session dir, paths relative, 64 MiB output cap, command groups of
  256 KiB (30,000 on Windows) run concurrently. Exit 1 = no match; ENOENT (`exec.ErrNotFound` / `fs.ErrNotExist`,
  also a missing cwd) drops that binary for the rest of the search; any other failure keeps every file of that group
  as a candidate; an abort returns the abort reason. `getAgentDir()` is `sdk.AgentDir()`; a host that does not provide
  it (`ErrHostInfoUnavailable`) leaves only `rg` on PATH (the prefilter is an optimization and cannot change results).
  The "ripgrep unavailable" case is proved both with the real `rg` and `PATH=""` (twin 56) and with fake `rg` scripts
  (`history_ripgrep_test.go`: arguments and cwd, exit 1, exit 2, managed binary before PATH, fallback, none, abort).
* **Raw-line matcher** (`needleLines`): ASCII needles search the Latin-1 byte view; a window containing a fold sequence
  of k or s (`E2 84 AA`, `C5 BF`, and the Latin-1 case variants `C2 84 AA`, `E5 BF` that `/i` also accepts) is decided by
  an exact backtracking matcher; otherwise `foldPattern` (rarest-byte `IndexByte` in both cases, then a folded compare).
  Non-ASCII needles use `(?i)` regexp, guarded by an anchor search (longest run of characters that fold only among
  ASCII letters) so the regexp only runs on windows that contain an anchor.
* **UTF-8 chunk boundaries and Unicode separators**: line splitting and the tail/overlap rules are CoreLane's
  `lfChunks`/`sessionLines`; U+2028/2029 are never line terminators (twin 20 writes them raw, with CRLF record separators,
  an unterminated final record and an entry spanning 64 KiB chunks; archive 1 puts every match one byte before a 1 MiB
  read boundary).
* **mtime ordering and ties**: libuv's `scandir` sorts names, so `os.ReadDir` (sorted) + a stable sort by Node's
  `mtimeMs` (`sec*1e3 + nsec/1e6` as a double) reproduces the original order, ties included (twin 58).
* **JS `toLowerCase`**: `historyToLower` adds the two SpecialCasing rules Go's `unicode.ToLower` lacks (U+0130 -> `i` + U+0307,
  final sigma). The excerpt index is computed in UTF-16 units of the lowercased text and applied to the original text, as
  the original does (so İ shifts excerpts by one unit, ported as is).
* **Cursors**: `base64url` decode is Node-lenient (both alphabets, skips other characters, stops at `=`).

## V8 string limit (archive twin 0)

The original proves a 513 MiB record, which cannot be one JS string, passes a nonmatching search and later reads. A Go
string has no such limit, so the observable intent is kept and the hazard it guards is tested directly:

* identical results (first page, cursor page, reads of `older` and `newer`, the image of `newer`);
* a heap sampler fails the twin if the live heap exceeds 256 MiB while the 513 MiB record is scanned and while later reads
  skip it: the line is never held whole (only a 512-byte head, the matcher overlap and, for a selected line, its text).

## Findings (original behaviour ported, for PORT.md "Findings")

1. `needleLines` with only the İ needle builds `new RegExp("", "i")`, which matches every line (unreachable: the fragment is
   always a needle). Ported (a matcher that accepts every line).
2. The `/i` regexp over the fold alternatives also accepts Latin-1 case variants of their first byte (`C2 84 AA`, `E5 BF`),
   i.e. spurious prefilter hits on raw bytes that are not those characters. Harmless (the entry match decides) and ported.
3. `historyHit` indexes the original text with an index measured in the lowercased text (differs when lowercasing changes
   the UTF-16 length, e.g. İ). Ported.

## Host/SDK differences

* A signal already aborted before the handler starts cannot be staged on the fake host (fixture model item 4): twins 22 and
  archive 1 call `historyMatches` with a cancelled context (`WithCancelCause`; the reason comes back via `errors.Is`).
* Concurrent calls of case 14/29 (`Promise.all`) run in turn: the fixture holds the host for a call.
* The lone-surrogate query of case 56 crosses the fake wire as the JSON escape `"\ud83d"` (`json.RawMessage`): the fixture's
  `encoding/json` would otherwise replace WTF-8 bytes by U+FFFD.
* `PI_CODING_AGENT_DIR` is not set by the twin: the extension's agent dir comes from `PIG_EXT_HOST_INFO`
  (`TestRipgrepPrefilterContract` sets it).

## Review fixes (historyrev H1-H4, P1)

| ID | status | change |
|---|---|---|
| H1 | fixed | `historyToLower` and its sigma code are deleted; every `toLowerCase` is `jsToLowerCase` (`TestHistoryParityFinalSigmaCaseIgnorable`, `TestHistoryParityLowerDifferential`: 20,000 strings, 0 mismatches) |
| H2 | fixed | `sessionFiles` reads directories with `readDirNode` (`EACCES: permission denied, scandir '<dir>'`), `newestFirst` stats through `nodeFsError` (`ENOENT: ..., stat '<file>'`) (`TestHistoryParitySessionFilesErrorText`) |
| H3 | fixed | `historyParams.offset`/`imageOffset` are `*float64` (JS numbers); `readOffsets` prints them with `jsNumberString`; once checked they are integers within the entry and become `int` for `pageRequest` (`TestHistoryParityReadOffsetNumberText`) |
| H4 | fixed in this lane (SDK item stays open) | session lines and the header are read by `session_json.go`, a JSON.parse reader with no 10,000 nesting limit (limit 2^20, then the line is skipped); `TestJSONParseDeepNesting` (depth 9,999 / 10,001 / 100,000) and `TestHistoryParityParseSessionLineDifferential` pass. The SDK decoder still refuses depth > 10,000 (`sdk/json/scanner.go` `maxNestingDepth`) and validates the whole input before decoding for every other consumer: an SDK/owner item |
| P1 | fixed | see below |
| stale "Finding 3" (`1e999`) | removed | the SDK decodes an out-of-range number as an infinity like `JSON.parse`; `session_json.go` does the same (`TestJSONParseGrammar`) |

## P1: the common-word search on a large archive

Mechanism (each step measured with the reviewer's `perf.mts` probe, `all: true`, query `lorem` matching every line of one 276 MB, 700k-entry archive):

1. **No per-line copy or allocation in `sessionLines`** (`session_lines.go`): `head` and `text()` alias the read buffer for a line that is whole
   in it (valid until the iterator advances; `session_lines_test.go` clones what it keeps), the `text` closure is made once per file, the
   512-byte head is a view when it is valid UTF-8. Before: head string + clone + second copy + closure per line.
2. **Single-pass JSON reader** (`session_json.go`): no validity pre-pass (15 % of the scan), no generic decode of keys the normalization drops,
   strings are substrings of the line (one copy per line in `parseSessionLine`, none in the search path), numbers/escapes/surrogates as
   `JSON.parse`/the SDK. Differential tests: 58,902 random and mutated texts against the SDK decoder and 48,834 entry lines against the
   previous implementation (kept as `session_json_reference_test.go`): 0 mismatches.
3. **Lazy hits**: a scan keeps the newest `limit+2` candidates per priority and builds header and excerpt for the survivors only
   (`hitCandidate`, `matchHit`/`hit`); the file's `shortKey` is computed once per file; the query's folding pattern once per search;
   images are extracted only for a read or an assistant message with recovery calls.
4. `flattenMessage` makes one allocation; `textOf` (message_text.go, CoreLane) returns a single block without copying it (same value); `toWindowedEntry` checks the id before flattening.
5. The window map no longer stores initial-window entries (the lookup default), so its size does not grow with the archive.

| shape (xd6702, 8 CPUs, median of 5) | TS | Go before | Go after |
|---|---:|---:|---:|
| a200 common `lorem`, rg | 5,734 ms (4,933-5,639 earlier) | 8,264 ms | **3,170 ms** |
| a200 common, no rg | 7,432 ms | 8,446 ms | 3,277 ms |
| a200 rare `zebra-marker-77`, rg / no rg | 2,981 / 3,068 ms | 649 / 639 ms | 315 / 319 ms |
| a200 absent `qqqxyz`, rg / no rg | 107 / 3,050 ms | 88 / 612 ms | 88-105 / 312 ms |
| s1000 rare, rg / no rg | 54 / 281 ms | 30 / 62 ms | 31 / 59 ms |
| s1000 common, rg / no rg | 51 / 33 ms | 24 / 10 ms | 26 / 6 ms |
| s1000 absent, rg / no rg | 48 / 2,119 ms | 18 / 570 ms | 24 / 466 ms |
| a200 common: peak RSS | 604 MiB (TS process) | 15 MiB (heap Sys) | **22 MiB** (VmHWM of the test process) |
| a200 common: allocations per search | n/a | ~2.8 GB (reviewer) | 883 MiB, 11.0 M allocs (15.7 per line) |

## Performance of the benchmarks (xd6702 container, Go 1.27.3, 8 CPUs, `-race` off)

Synthetic archive: 200,000 entries, 55 MB, one file (`POSTHORSE_BENCH_LINES` scales it). Peak RSS is `VmHWM` of a process that ran only
that benchmark (the archive is streamed to disk by the generator). "v1" is this lane's first version.

| Benchmark | ns/op v1 -> now | MB/s now | B/op now | allocs/op now | peak RSS now |
|---|---|---|---|---|---|
| SearchArchive rg, rare marker (1,000 hits) | 240,301,629 -> 75,802,373 | 816 | 1,402,289 | 2,942 | 16 MiB |
| SearchArchive no rg, rare marker | 253,459,736 -> 72,101,999 | 858 | 1,346,302 | 2,819 | 16 MiB |
| SearchArchive rg, `entry 1999` | 238,006,220 -> 70,689,148 | 876 | 1,309,625 | 1,774 | 16 MiB |
| SearchArchive no rg, `entry 1999` | 217,471,361 -> 62,135,060 | 996 | 1,256,758 | 1,652 | 16 MiB |
| SearchArchiveEveryLine (200k parsed + ranked) | 1,733,196,500 -> 688,138,768 | 90 | 199,599,273 | 2,600,345 | 17 MiB |
| FindEntry (last id of the file) | 226,186,131 -> 44,650,836 | 1,386 | 1,130,808 | 127 | 16 MiB |
| SearchArchive no rg, 2,000,000 entries (620 MB) | 1,607,084,158 -> 722,153,397 | 860 | 3,177,212 | 26,240 | 17 MiB |
| SearchBranch (20,000 entries) | 5,238,706 -> 1,305,139 | | 339,336 | 149 | |
| ReadPage (400k-char entry, 10 pages) | 3,271,427 -> 3,133,487 | 128 | 414,275 | 156 | |
| SearchPage (50 hits) | 159,677 -> 159,866 | | 66,827 | 1,059 | |
| LineMatcher rare-needle-marker (1 MiB window) | 172,976 | 5,955 | 0 | 0 | |
| LineMatcher `dolor sit` | 210,173 | 4,925 | 0 | 0 | |
| LineMatcher `sk` | 209,549 | 4,939 | 0 | 0 | |
| LineMatcher `éclair 🐴` (anchor + regexp) | 69,946 | 14,797 | 0 | 0 | |

## Mutation check, coverage tests and a real bug

* `port/mutations/HistoryLane.json`: 254 mutants over `history_entries.go`, `history_search.go`, `history_pages.go`, `history.go`,
  `session_files.go`, `session_json.go` and the aliasing code of `session_lines.go`; `port/mutations/HistoryLane.md` lists the 8 removed
  equivalent mutants (one line each). Run: `pigeq mutate --go extensions/posthorse --mutations port/mutations/HistoryLane.json
  --unit-only --sdk-dir $PIG_SDK_DIR --jobs 6` on a frozen copy of the Package (a scratch copy under
  `~/ph-work/scratch/HistoryLane/`; pigeq copies only the module directory, so `notes_tool_behaviour_test.go`, which reads
  `../../port/oracle/src/notes.ts`, was pointed at an absolute path in that copy, and the untracked WIP `rollover_mutation_test.go`,
  which failed on the unmutated tree, was left out).
* First complete run: 262 mutants, 177 killed, 65 survivors, 20 invalid (unused variables). Rewrote the 20 invalid ones, removed the
  equivalent ones, added `history_units_test.go` (about 50 unit tests) and re-ran: 247 killed, 9 survivors, 1 invalid; fixed those with
  7 more tests and 3 more equivalents (8 in all); the last run of the 7 remaining mutants: all killed. Every survivor was a missing test; none
  needed a production change except the bug below.
* **Real bug found (RecoveryLane's replay corpus)**: `contextStart` compared `firstKeptEntryId` and `id` with `str()`, which reads a
  JSON null like an absent member, so a compaction with `firstKeptEntryId: null` matched the first id-less entry. It now compares the
  raw members like `===` (absent equals absent only, null equals null only): `TestContextStartBoundaries`.
* C-02 (`TestHistoryAllSearchKeepsInvocationTimeContextVisibility`, native-host.ts:1257): the fixture session is persisted, a fake `rg`
  blocks the prefilter on named pipes while the test appends a `context_edit`; the search still reports `skipped: 1` and no hit, the
  next search sees the entry (`skipped: 0`). POSIX only (skips on Windows).
* C-17 (`history_snapshot_test.go`): native-host.ts:1143, 1225, 1353 and 1382 count JSON.stringify calls and content accessors; the Go
  twins bound the bytes allocated by a search/read relative to rendering the entries involved once
  (`allocated(fn)` over `runtime.MemStats.TotalAlloc`): 64 unrelated 64 KB tool-call arguments rendered at most once per page and for a
  no-match search (23.6 MiB measured, bound 35.9 MiB, two renderings would be 47 MiB); 8 edits of one target rendered a bounded number
  of times (all=false and all=true); a 4096-entry, 8 KB-per-entry branch searched for a first page extracts about page-size entries
  (bound far below n entries) and a read of any entry extracts one. They fail when an entry is rendered again or when the scan
  extracts entries it does not need, unlike the benchmarks.

## Open questions

None blocking. `registerHistory` reads `getSessionDir`, `getSessionFile` (and `getBranch`/`buildSessionProjection` only when
needed) per call, like the TS `historySource`.
