# Lane C2 (NotesToolsLane): notes tool, note guards, note tool effects

Files: `notes.go` (src/notes.ts), `note_guards.go` (src/note-guards.ts), `note_tool_effects.go`
(src/note-tool-effects.ts). Tests: `twin_posthorse_notes_test.go` (+ `_unix_`, `_windows_`, `_append_`
helpers), `note_guards_test.go`, `notes_bench_test.go`. Red snapshot: `port/red/NotesToolsLane.log`
(stubs: `port/red/stubs/NotesToolsLane/*.go.txt`, swapped in for the three production files in a scratch copy).

## Contract table

| ID | Upstream file:symbol | Go | Test |
|---|---|---|---|
| C2-1 | notes.ts:286-365 `registerNotes` (schema, description, promptSnippet byte-exact; `notesDir` migration, scope-with-write refusal) | `registerNotes`, `notesSchema`, `notesDescription` | twins 34 46 49; fork tests |
| C2-2 | notes.ts:51-88 `listNotes` (newest first, size, UTC minute, FORMAT_BATCH yield, notices, empty text) | `listNotes`, `appendISOMinute` | twins 34 43 44 45 46; `BenchmarkNotesList1000` |
| C2-3 | notes.ts:89-139 `readNote` (folder -> listing, binary, offset, read-only reference header, record read) | `readNote` | twins 46 49; fork tests |
| C2-4 | notes.ts:140-168 `writeNote` (queue, prepareWrite, publishFile, clear) | `writeNote` | twins 38 49 |
| C2-5 | notes.ts:169-201 `appendNote` (adopt, separator, one O_APPEND write) | `appendNote`, `appendRecord`, `needsSeparator` | twins 49 59; `BenchmarkNotesAppendLarge` |
| C2-6 | notes.ts:202-214 `matchingLines` | `matchingLines` (+ ASCII paths) | twins 34 46 47 49; `TestMatchingLinesAgainstReference`; `BenchmarkNotesMatchingLines` |
| C2-7 | notes.ts:215-285 `noteHits`/`searchNotes` (16-wide batches, 1 MiB skip, early stop, offset limit) | `noteHitsFor`, `searchNotes` | twins 34 43 44 45 46 47; `BenchmarkNotesSearch1000` |
| C2-8 | note-guards.ts:25-42 `toolPath` | `toolPath`, `fileURLToPath` | `TestNoteGuardsToolPath` |
| C2-9 | note-guards.ts:44-95 `noteAddress`, `guardForeign` | `noteAddressOf`, `guardForeign` | `TestNoteGuardsBatchToolsProtectForeignNotes`; fork tests |
| C2-10 | note-guards.ts:96-127 `guardOperation` | `guardOperation` | same |
| C2-11 | note-guards.ts:128-204 `recordedFiles`, `registerNoteGuards` | `recordedFiles`, `registerNoteGuards` | fork tests (native read/write/edit receipts) |
| C2-12 | note-tool-effects.ts:17-112 `bareToolName`, `noteOperations` (apply_patch headers, batch tools, single tools) | same names | twin 61; `TestVerifiedNoteOperations` |
| C2-13 | note-tool-effects.ts:113-197 `noteOperationKind`, `verifiedNoteOperations` | same | `TestVerifiedNoteOperations`; recovery lane twins |

Twins: 10 exact (34, 38, 43, 44, 45, 46, 47, 49, 59, 61), 0 skips. Case 38 is skipped inside its body on
Windows exactly where the upstream case is (`skip: process.platform === "win32"`).

## Decisions and differences (none observable by the model)

1. **setImmediate yield (notes.ts:68-69).** `cooperate` = `runtime.Gosched()` then `throwIfAborted`. An
   extension handler runs on its own goroutine, so there is no host event loop to give a turn to; the
   cancellation check is the part that matters. Twin 45 therefore proves "Pi keeps running" differently:
   a `turn_start` request completes *while* the walk is blocked inside `noteStatHook` (the TS case checks a
   `setImmediate` callback ran). The cancel proof is the host's cancel frame; a `turn_start` request after it is
   the barrier (the SDK read loop applies a cancel frame before it reads the next one), no sleeps.
2. **`fs.promises.stat` mock (cases 43-45).** Replaced by NotesStoreLane's `noteStatHook` (called after each
   successful stat of the walk).
3. **append reads only the last byte.** TS reads and UTF-8 decodes the whole note to learn
   `existing !== "" && !existing.endsWith("\n")`. A non-empty file decodes to a non-empty string and a final 0x0A
   always decodes to "\n", so the last byte decides. `BenchmarkNotesAppendLarge` (8 MiB note): 17 us, 816 B/op.
4. **Concurrent search reads.** TS `Promise.all` over 16 notes rejects with whichever read fails first in time;
   Go waits for the batch and returns the failure of the first note in listing order.
5. **Case-insensitive search** keeps the original defect: the match index comes from the lower-cased line but
   `excerptAround` cuts the original line, so a line whose lower-casing changes its UTF-16 length (e.g. U+0130)
   shifts the excerpt. ASCII lines/texts use an allocation-free fold; `TestMatchingLinesAgainstReference`
   proves it equals the plain port.
6. **fs errors** use CoreLane's `nodeFsError` (`EACCES: permission denied, open '/p'`), as twins 38 and 47 assert.
7. **Cancellation errors** are `*abortError` from `throwIfAborted` (text "The operation was aborted").
8. **SIGXFSZ.** Node dies on SIGXFSZ unless a handler is installed (the TS child installs one); Go ignores it, so
   the write returns EFBIG with no handler. Checked with a 10-line program under `ulimit -f 2`.
9. **tool_call/tool_result state.** `calls` is mutex-guarded (parallel tool calls); the SDK passes each handler
   its own `sdk.Context`, sessions are shared (NotesStoreLane).
10. **ENOTDIR.** `statNoThrow` follows Node's `throwIfNoEntry: false`, which also swallows ENOTDIR (checked on
    Node 24).

## Performance (xd6702, 8 cpus, `-benchtime=2s`; before/after the allocation pass in listNotes)

See `port/runs/NotesToolsLane.log` for the raw numbers. Most of list/search cost is the walk (NotesStoreLane)
and the file reads; the tool's own code is one string per row and one goroutine per note in a search batch.

## Findings for the owner

- `notes search` lower-cases whole files with `toLowerCase`; Go's `jsToLowerCase` of a non-ASCII text builds a
  rune slice (reported to CoreLane).
- A tool-call guard error for a malformed `file://` path propagates out of the `tool_call` handler (as in TS).

## SDK gaps

None. `sdk.WithFileMutationQueue` needs a host connection (the fixture host answers `fileMutation.enter/exit`);
the benchmarks call `listNotes`/`searchNotes`/`appendRecord` directly because they need no host.

## Review round (port/review/notestools.md, N1-N16)

New files: `node_paths.go` (Node `path.posix/win32.resolve`, `os.homedir`, `path.join`, `decodeURIComponent` and
WHATWG-based `fileURLToPath` for both platforms, as pure functions), `notes_tools_regress_test.go` (the review's
proof tests, renamed), `tool_parameters_test.go` + `testdata/typebox_tool_parameters.txt`,
`testdata/node_path_cases.json`. Dispositions:

| ID | Status | Fix / evidence |
|---|---|---|
| N1 | fixed | `notesSchema()` built with `sdk.Type.Object(sdk.NewOrderedObject(...))`. `TestToolParametersMatchTypeBox` compares the wire `parameters` of **all four** tools with `JSON.stringify(tool.parameters)` captured from the unmodified original (`port/oracle/index.ts` loaded under Node 24 + TypeBox 1.3.27 with a recording `registerTool`; `testdata/typebox_tool_parameters.txt`). `history` was a plain map too: exact replacement sent to HistoryLane; `new_context` / `get_context_remaining` already used the TypeBox port. |
| N2 | fixed | `statNoThrow` uses `noteMissing` (ENOENT or ENOTDIR). The earlier `isNoEntry` it called had been redefined by HistoryLane to ENOENT only. |
| N3 | fixed | `mkdirRecursive` (files.go, CoreLane) in write and append: EEXIST on an existing file, ENOTDIR for a deeper ancestor. |
| N4 | fixed | `needsSeparator` reports `EISDIR: illegal operation on a directory, read` (no path) for a directory. |
| N5 | fixed except a residue | The offset is kept as float64 (`jsNumberString`), compared against the limit/text length before any int conversion: read at 2**60 and search at 1e20 now answer like Node. Residue: `notes list` with an offset > 2^53 hands a clamped int to `pages.notes` (CoreLane refused to make `pageRequest.offset` float64 this round). It only differs from Node when the context is almost exhausted (the "retry with offset N" refusal prints the clamped value and the footer length uses it): the normal answer is `Offset is past the end; restart with offset 0.` in both. Owner item. |
| N6 | fixed | NotesStoreLane (`noteTouchFile.appendJSON`), `TestNotesToolsTouchFileKind`. |
| N7 | fixed | `fileURLToPathOS`: WHATWG file-URL parse (tab/newline stripping, backslash, host lower-casing and percent-decoding, `localhost`, drive-letter host, `?`/`#`, `.`/`%2e` segments, `C|`) then `getPathFromURLPosix/Win32` with their errors and `decodeURIComponent` (`URI malformed`). 116 values come from Node 24.22.1 (`testdata/node_path_cases.json`, `TestNodePathCases`). |
| N8 | fixed | `operationInsideRoots` parses the path only when a root exists. |
| N9 | fixed | `appendRecord` writes `jsUTF8Bytes` for a string with a lone surrogate (U+FFFD, as `write`). |
| N10 | fixed | The whole-text precheck uses UTF-16 `u16index` when the query or the text holds a surrogate form. |
| N11 | fixed | `foldIndex`: no copy when the text has no capitals, otherwise one lower-cased copy from a pool searched with `strings.Index` (linear). 1 MiB of `a` with a 4097-byte query: 3 s before, ~5 ms after (`TestNotesToolsASCIIFoldLinear`, `BenchmarkNotesToolsASCIIFoldWorst`). |
| N12 | fixed | `needsSeparator` re-stats and re-reads (up to 8 times) when the note shrank between the size and the read, like `readFileSync` seeing the current content. |
| N13 | fixed by table | `win32Resolve` is a port of `path.win32.resolve` (device/UNC roots, rooted path without drive takes cwd's drive); `fileURLToPathOS(…, windows)` rejects `%2f` and `%5c` and requires a drive letter. Both verified against Node 24 values for both platforms on Linux (`TestNodePathCases`). Not applied: `domainToUnicode` of an `xn--` UNC host. `path.join(home, tail)` on Windows uses Go's `filepath.Clean`. |
| N14 | fixed | `nodeHomedir`: `$HOME` (`USERPROFILE`) verbatim when set, even empty; otherwise the passwd entry. `TestNotesToolsHomeEmpty`. |
| N15 | fixed | NotesStoreLane (`canonicalPath`), `TestNotesToolsCanonicalPathLoop`. |
| N16 | owner item, no change | Node cannot hold a note > 0x1fffffe8 UTF-16 units (`ERR_STRING_TOO_LONG`) nor read a file > 2 GiB (`ERR_FS_FILE_TOO_LARGE`); Go reads both. Keep as a documented improvement or reproduce the two errors by a size check. |

### Observed: what the model sees for a handler error (owner item, no change)

A `tool_call` handler that throws `new TypeError("boom")` (TS under Pi 1.5.0) and one that returns `errors.New("boom")`
(Go under PiG 0.7.0) were run with the same scripted scenario (`pigeq lane --host pi` / `--host pig`; probe extension in
a scratch dir, removed). Both lanes show the same thing: `tool_execution_end {isError:true, result.content:[{type:"text",text:"boom"}]}`
and the model's next request carries `{"role":"tool","text":"boom"}`. The JS class (`TypeError:`, `URIError:`) and the
`Extension failed, blocking execution:` prefix quoted by the review do not reach the model in either host
(Pi's `agent-session.js` text is not what the 1.5.0 RPC lane produced), so the error class is not observable and needs
no SDK feature.

### Cross-cutting residue (owner decision, not fixed here)

- A model-supplied path containing a lone surrogate (`notes write {path:"\ud800.md"}`) reaches every `os.*` call as the
  SDK's WTF-8 bytes (`ED A0 80`); Node converts the path to UTF-8 with U+FFFD (`EF BF BD`), so the file name differs.
  All lanes' fs calls are affected.
