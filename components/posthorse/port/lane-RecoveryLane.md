# Lane D1 recovery (RecoveryLane)

Files: `recovery.go`, `recovery_notes.go`, `recovery_note_touches.go`, `recovery_records.go`, `recovery_results.go`;
tests `twin_posthorse_recovery_test.go`, `twin_posthorse_recovery_notes_test.go`, `recovery_bench_test.go`.

## Contract table

| ID | upstream | Go | test |
|---|---|---|---|
| R1 | recovery.ts `buildAutoHandoff` | `buildAutoHandoff(entries, projected, maxChars, autoHandoffOptions) (string, error)` | twins 0-3, 25-27, 60, 62 |
| R2 | recovery.ts `hasRetainedRolloverMarker` | `hasRetainedRolloverMarker` | rollover twins |
| R3 | recovery-records.ts | `recoveryRecordOf`, `formatRecord`, `indexLine` | twin 3 |
| R4 | recovery-results.ts `recoverableToolResults` | same name, over `[]recoveryEntry` | twins 25, 26, 27 |
| R5 | recovery-notes.ts `renderSessionNotes` | `(string, ok, error)` | twins 60, 62 |
| R6 | recovery-note-touches.ts `collectTouches` | `([]noteTouch, defaultDir)`, consumed by note_evidence.go | twins 60, 62 |

## Twins (9, 0 skips)

posthorse cases 0, 1, 2, 3, 25, 26, 27, 60, 62. Cases 0, 2, 3, 25, 27 run through the fixture host
(`session_before_compact`, `new_context`); 1, 26, 60, 62 call `buildAutoHandoff` directly like upstream.
Case 3 uses `withExtraTools("ask_question")`.

## Decisions / findings

* JS regex line semantics (`/^...$/m`, `[^\r\n]+`, `.`) are hand-coded (`lineAfterPrefix`, the package/legacy note path
  patterns) because Go's `(?m)$` ignores `\r`, `\u2028`, `\u2029`.
* `Map` insertion order matters for learned notes directories (stable length sort downstream): `resultMap`, `orderedSet`.
* `calls.get(undefined)` quirk of `ownerRecords` (a result with no `toolCallId` matches an id-less call) is preserved.
* `ownerRecords` builds the ask_question call index lazily (only when an `ask_question` result is met), `collectResultMap`
  derives tool-result text lazily; both are behaviour-neutral.
* Red phase: the implementation was written before sibling lanes compiled; `port/red/RecoveryLane.log` is the twin run
  against a stub `buildAutoHandoff` returning an empty record.

## Findings for the owner / other lanes

1. **Key order of tool-call `arguments` / `executionArguments` in the record** (`Call arguments: {...}`) is sorted, not the
   session's insertion order (SDK decodes entries to `map[string]any`; same as lane-CoreLane finding 1 / review C-13). The
   golden scenarios therefore use alphabetically ordered members.
2. **`noteTouch.receiptId` cannot tell `""` from undefined** (note_evidence.go sort rank / `touchKey`): an entry whose id is
   the empty string (or missing) that carries a note receipt orders differently from the original. Real sessions always have ids;
   the generator keeps id-less entries away from note receipts.
3. Found by the golden replay: `contextStart` matched an id-less entry when `firstKeptEntryId` was JSON `null`
   (fixed by HistoryLane, history_entries.go).

## Golden replay

`testdata/recovery_golden.json.gz` (1,200 scenarios, seed 20261106) is produced by `port/tools/recovery-golden.mjs` from the
unmodified original (`port/oracle/src/recovery.ts`): random branches (owner/coordination inputs, tool calls and results,
async receipts, ask_question exchanges, windows, context edits, notes calls over many path shapes, codemode nested calls,
UTF-16 edge texts), random budgets, retained sets and projections; each records the output's UTF-16 length and SHA-256.
`TestRecoveryGoldenMatchesOracle` replays them against `buildAutoHandoff`.

## Benchmarks (xd6702, Xeon 8468, 5,000-entry branch, 20,000-char cap)

`BenchmarkBuildAutoHandoff5000`: ~12.5 ms/op, 5.1 MB/op, 27.7k allocs/op; with a 40-entry retained tail: ~13 ms, 5.1 MB,
27.6k allocs. Profile is dominated by dynamic `map[string]any` field reads (`str`) and GC; no TS number was measured.
