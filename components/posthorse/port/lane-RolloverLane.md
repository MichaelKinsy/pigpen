# Lane D2 (rollover): RolloverLane

Files: `retention.go` (src/retention.ts), `reminders.go` (src/reminders.ts), `rollover.go` (src/rollover.ts).
Tests: `twin_posthorse_rollover_test.go` (9 posthorse twins + 2 rollover-cost twins), `rollover_hooks_test.go`,
`retention_unit_test.go`, `retention_pi_test.go`, `retention_recovery_test.go`, `rollover_bench_test.go`.
Test data: `testdata/retention_pi_cases.json`, `testdata/retention_recovery_cases.json` (generated from the oracle, see below).

## Twins

| Ledger file | Title | Go |
|---|---|---|
| posthorse | budget policy sends one reminder and lets Pi own rollover (window=${contextWindow}, reserve=${reserve}) | `TestTwinPosthorseRollover` (6 subtests) |
| posthorse | reminders respect live pending messages and tool calls even with a stop reason | same, 4 cases |
| posthorse | a large reserve does not send checkpoint reminders in a fresh window | same |
| posthorse | reminder lookups are leaf-certified and preserve the full kept range (${archived} archived entries) | same, archived=10 and 43,000 |
| posthorse | disabling compaction filters persisted reminders without changing history or deduplication | same |
| posthorse | reminder-free context and fork reminder filtering never read history | same |
| posthorse | context filtering removes reminders from an older window or a different budget | same |
| posthorse | a reminder before a native compaction does not suppress the next reminder | same |
| posthorse | new_context beside a failed sibling tool does not suppress the reminder | same |
| rollover-cost | reserve scenario ${scenario.reserve} agrees with the native threshold | `TestTwinRolloverCost` |
| rollover-cost | synthetic prices distinguish a cache read, fresh input, and both write lifetimes | same, over `sdk.CalculateCost` |

11 exact twins, 0 skipped. `pigeq twins check` reports `63 exact twins, 0 skipped` for posthorse and `2 exact twins, 0 skipped` for rollover-cost.
Adaptations inside the twins: the TS `toolCallId` and "id" stay "id" (the fixture's); "pending = undefined" (a context without
`hasPendingMessages`) and "false" are both a host answer of false, because `Context.HasPendingMessages` always exists in the SDK;
the TS fixture's throwing `getBranch` is `withHostError("sessionRead.getBranch")` plus read counters. In the 43,000-entry case the fixture's
10s wait is raised (`fxWaitTimeout`) because the negative cold lookup is 43,000 wire reads in one `turn_end`: 3.5s plain, ~20s under `-race`.

## Contract table

| ID | Upstream | Go | Test |
|---|---|---|---|
| RO-1 | rollover.ts:393-456 `registerRollover` (handler order: tool_execution_start/end via ResetRequests, turn_start, session_start, session_tree, guidance, context, session_before_compact, turn_end, tools) | `registerRollover` | all twins |
| RO-2 | rollover.ts:39-74 `explicitReset` (compaction entry `{posthorse:1, reason:"explicit"}`, `posthorse-reset-deferred` custom_message, aborted outcome) | `explicitReset` | `TestRolloverExplicitReset`, twin 4 |
| RO-3 | rollover.ts:76-115 `staleReminderPredicate`, 150-166 context filter | `scanReminderMessages`, `staleReminderPredicate`, `dropStale`, `registerReminderFilter` | twins 11, 12, 13; benchmarks |
| RO-4 | rollover.ts:116-149 `chronologicalRetainedTail` (`posthorse-retained-tail` sentinel) | `chronologicalRetainedTail` | `TestRolloverChronologicalRetainedTail` |
| RO-5 | rollover.ts:167-203 `automaticDraft`, `reportAutomaticFailure` | `automaticDraft`, `reportAutomaticFailure` | `TestRolloverAutomaticCompaction` |
| RO-6 | rollover.ts:204-303 threshold fallback | `freshWindowWithoutInput`, `reachedThreshold`, `fallbackTurnEligible`, `thresholdFallback` | `TestRolloverThresholdFallback` |
| RO-7 | rollover.ts:304-392 `prepareAutomatic`, `registerAutomaticRollover` | `prepareAutomatic`, `registerAutomaticRollover` | `TestRolloverAutomaticCompaction` (incl. cancellation) |
| RO-8 | reminders.ts:31-51 `reminderMatches`, `hasReminder` | same names | twins 4, 8, 13 |
| RO-9 | reminders.ts:52-123 `reminderDecision` | `reminderDecision`, `reminderBudget`, `endsRun` | twins 4, 5, 6, 23, 24 |
| RO-10 | reminders.ts:129-202 `createReminderLookup`, `scanAncestry` (leaf-certified append-only cache) | `createReminderLookup`, `(*reminderLookup).read/reset` (mutex + reset generation, R8; one `GetLeafEntry`, R6), `scanAncestry` with the optional hybrid walk (R7, off) | twin 8 (read counts), `reminders_test.go`, `BenchmarkReminderLookup*` |
| RO-11 | reminders.ts:204-288 `retainedEntryIdSet`, `countRetainedFromProjected`, `countRetainedMessages` | `retainedProjection`, `countRetainedFromProjected`, `countRetainedMessages` (projection only, review R5) | `TestRolloverChronologicalRetainedTail`, `TestRetainedProjectionCountMatchesPi`, `TestRetainedProjectionCountMatchesBranchWalk`, `TestRetainedTailContextNeedsNoBranchRead` |
| RO-12 | retention.ts:96-182 `projectedCut`, `pairedTail`, `boundedTail`, `retainedTail` | `projectedCut`, `pairedTail`, `boundedTail`, `retainedTailOf` | `retention_unit_test.go`, `TestAutomaticRecoveryMatchesOracle` |
| RO-13 | retention.ts:183-209 `automaticRecovery` | `automaticRecovery` | `TestAutomaticRecoveryMatchesOracle` (40 oracle cases) |
| RO-14 | `findCutPoint`, `estimateTokens`, `DEFAULT_COMPACTION_SETTINGS` of Pi 1.5.0 | `sdk.FindCutPoint`, `sdk.EstimateTokens`, `sdk.DefaultCompactionSettings` | `retention_pi_test.go` (120 + 150 Pi 1.5.0 cases) |

## Decisions and findings

1. **`console.error` without a UI** (rollover.ts:195-203). Go has no console; the SDK has no diagnostic API. PiG drains every extension's
   stderr into a bounded log (docs/performance/extension-ipc.md: first 1 MiB, last 64 KiB) and the SDK itself writes its recovered-panic and
   host-failure notices to `os.Stderr`. `fmt.Fprintln(os.Stderr, message)` is therefore the faithful stand-in. `os.Stderr` is not on the fuse
   hazard list (`os.Exit/Chdir/Stdout`, `log.Fatal*`, `fmt.Print*`); `pigeq gaps` does not flag it. Tested by capturing `os.Stderr` in headless mode.
2. **Entry identity** (`previous.leaf === entry`, reminders.ts:160). A Go read decodes a fresh record, so identity is unavailable; `sameEntry`
   compares the id and then the whole record (`reflect.DeepEqual`). It is the conservative reading: an equal record is the same stored entry, and a
   reused id with different content is a miss (as `===` would be). Cost 200 ns, 0 allocs per certification on a 20,000-entry branch.
3. **ReminderState.manager** (`ctx.sessionManager` object identity) has no Go counterpart; the cache is keyed by session id and file only. A replaced
   session always has a new id.
4. **Dead duck-typing branch.** `countRetainedMessages` falls back to `contextEdits`/`entryProducesRetainedMessage` when the session manager lacks
   `getBranch`/`buildSessionProjection`. `ReadonlySessionManager` always has both, so the branch cannot run in Go and is not ported.
5. **Empty session.** TS asks `getEntry("")`; Go reads `GetLeafEntry`, which is undefined for a null leaf (R6, below).
6. **By-id ancestry stays by-id (R7, decision pending).** TS tests forbid `getBranch` for reminder checks ("Reminder checks must not copy full history")
   and count the by-id reads. Go reads the same way. The negative cold lookup of a branch with no Posthorse window is one wire round trip per entry
   (20,000 entries: 1.07-1.2 s through the fake host; TS ~2 ms in-process). A hybrid walk exists behind the constant `reminderBranchAfter` (default 0 = off):
   after K by-id reads it fetches the rest of the ancestry with one `GetBranch(&parentId)` and continues the same scan locally, with at most K by-id reads and one
   branch read and an identical result (`TestHybridAncestryWalkMatchesByIDWalk`, 300 seeded shapes x 6 values of K, with and without a certified previous leaf,
   and a dangling parent). Turning it on is the owner decision (latency vs one decoded branch at peak); the twin counts apply to the by-id mode.
7. **Reads per reminder lookup (R6, fixed).** `read` is three wire reads (session id, session file, `getLeafEntry`) plus the by-id reads of a changed leaf; before it
   was four (`getLeafId` + `getEntry`). The twins count `getEntry + getLeafEntry` as TS's by-id reads (TS reads the leaf with `getEntry`). The session id/file reads
   only stand in for the TS `manager` identity; dropping them is a further owner decision.
7b. **Retained-tail context cost (R5, fixed).** The `context` hook of a window opened by a retained-tail rollover counted the retained messages by reading the whole branch
   (`GetBranch(nil)`, 317 ms with 20,000 earlier entries over the wire) and the projection. `countRetainedMessages` now reads the projection only: Pi's `buildContextEntries`
   lists the newest compaction first, then the entries from `firstKeptEntryId` up to it, then the compaction's child and everything after, so the kept span is projection
   index 1 up to the first entry whose `parentId` is the compaction's id (`retainedProjection`). Same count as the branch walk, proved by
   `TestRetainedProjectionCountMatchesPi` (100 Pi 1.5.0 `SessionManager` sessions with the oracle's `countRetainedMessages` as the answer, `testdata/retained_count_cases.json`)
   and `TestRetainedProjectionCountMatchesBranchWalk` (400 seeded fixture sessions against the old full-walk code, kept in the test as `roOldCount`). One projection read per
   event remains (`TestRetainedTailContextNeedsNoBranchRead`; through the fake host 11 ms per event with 20,000 earlier entries, 2.0 MB, 10,700 allocs).
7d. **Retained count cached between `context` events (PerfLane workload e).** `countRetainedMessages` read the whole session projection on every `context`
   event of a window opened by a retained-tail rollover (19% of host CPU in `handleSessionRead`, plus the decode in the extension). `retainedCache`
   (reminders.go) now keeps the retained messages (role, customType, details: all the stale predicate reads) and certifies them for the new leaf instead of
   re-reading. Why that is exact, from Pi's session semantics: the span is the projection between the newest compaction and its child, i.e. the entries from
   `firstKeptEntryId` up to the compaction. Entries are immutable and the span lies before the compaction, so it changes only when (a) the newest compaction
   changes (a new `compaction` entry), (b) a `context_edit` entry edits a retained entry (a null replacement removes its messages), or (c) the leaf leaves the branch
   (navigation, another session). A new leaf is certified by walking its ancestors back to the leaf the cache was read at (`certified`): no `compaction` or
   `context_edit` entry among them means the cached messages are exact. Anything else, a walk longer than `retainedCertifySteps` (16), a missing parent or a
   leaf that is not a descendant, is a miss that reads the projection again (so navigation without an event is still correct). `session_start` and
   `session_tree` reset the cache. Cost of a hit: one `getLeafEntry` plus one `getEntry` per entry appended since the cached leaf, minus one (a child of the cached
   leaf costs none). The stale predicate is evaluated per event over the cached messages, so a change of window, budget or `compaction.enabled` needs no invalidation.
   Proof of identical results: `TestRetainedCacheAgreesWithTheProjection` (session that grows, is edited by null and replacing `context_edit`, compacted again, with
   several stale rules), `TestRetainedCacheLeavesTheCachedBranch` (navigation with and without the event, another session, empty session),
   `TestRetainedCacheWalkIsBounded`, `TestRetainedCacheWalkReadsOnlyTheNewEntries`, `TestRetainedCacheIsResetByTheSessionEvents`: at every step the cached count equals the
   fresh count and the projection is read exactly when it must be; the projection-only count itself is still proved against Pi 1.5.0 and the old branch walk (item 7b).
   Numbers (fake host, a window of 1,000 entries after the rollover, one entry appended per `context` event, per event): 43 ms, 15-19 MB, 72,600 allocations before;
   0.4-0.6 ms, 39-44 KB, 500 allocations after (steady state; the first event of a window reads the projection once). The same window with an unchanged leaf:
   49 ms before, 27-29 ms after (the first event averaged in). In-memory: a hit is 10.8 us and 0 allocations (200 retained messages), a miss counting a projection
   already in memory is 83 us and 407 allocations, before the wire read and its decode.
   Other per-event work in the hot handlers: `context` makes no host call and allocates nothing when no reminder message is present (9-14 us for 1,000 messages;
   `scanReminderMessages` is the TS early exit, `dropStale` copies only when something is stale); with a reminder and no window marker it costs the three reminder-lookup
   reads (session id, file, leaf entry; see item 7). `context_with_system` (CoreLane's guidance.go) makes one `getContextUsage` read per event, as `ctx.getContextUsage()`
   does in TS; its guidance string is rebuilt per event (could be memoized by window, reserve and enabled; not done, it is not in this lane's files).

7c. **Reset during a lookup (R8, fixed).** `reminderLookup` guards its state with a mutex and bumps a generation on `reset`; a lookup that began before a reset returns its
   result but does not store it (`TestReminderLookupResetDuringReadStoresNothing`, `TestReminderLookupConcurrentReadsAndResets` under `-race`). R9: `onlyReminders` (dead) deleted.
   R10: the call site is RecoveryLane's `collectTouches`; NotesStoreLane added `sortNoteDirectories`/`matchSortedNoteDirectory` and told RecoveryLane.
8. **turn_end drafts re-emit foreign entries.** A `BoundaryResult` replaces the entry list; the extension appends to a clone of `event.entries` it decoded into
   `map[string]any`, so a foreign draft's `data`/`details` members come back in sorted order, not insertion order (TS keeps object order). Entries written by
   this lane have their members in TS order (`posthorse` before `reason`; `reminderDetails` is a struct in the order `windowId, contextWindow, reserveTokens`).
   An SDK-level limitation (decoded maps lose order).
9. **`BuildSessionProjection` decodes more than it needs.** `ReadonlySessionManager.BuildSessionProjection` returns `SessionContext` (all context messages, model,
   thinking level) plus `Entries`; the extension only reads `Entries` (rollover.go countRetainedMessages / prepareAutomatic). The decode of `messages`
   is wasted work per call (SDK-side; no API to ask for entries only).
10. **`event.signal` / `ctx.signal`** map to `ctx.Err()` (the request, per the surface table) and `ctx.Signal()` (the run's signal, nil outside a run). A
    cancellation staged mid-hook is tested deterministically (the cancel frame is followed by another request on the same ordered connection before the gated
    host answer is released).
11. **`estimateTokens(nativeMessage(m))`** is `sdk.EstimateTokens(m)`: `nativeMessage` rebuilds only the members `estimateTokens` reads; a `system` message
    (filtered from costs) becomes an empty user message in the cut materialization, which `emptyUserMessage` keeps. `projectedCut` materializes minimal
    `{type, message}` records (not copies of the source entries) and maps the cut back by index instead of by id.
12. **Pi parity of the SDK functions** (`retention_pi_test.go`): `testdata/retention_pi_cases.json` holds Pi 1.5.0's answers (oracle host's
    `@earendil-works/pi-coding-agent`, Node 24) for 120 `estimateTokens` messages and 150 `findCutPoint` sessions over every entry/message kind, plus
    `DEFAULT_COMPACTION_SETTINGS`; all agree. `retention_recovery_test.go` runs `automaticRecovery` against the oracle's `src/retention.ts` on 40 seeded sessions
    (31 with a record, 17 with a retained tail, 9 "no room"); handoff text and first kept id agree (this also exercises RecoveryLane's `buildAutoHandoff`).
    Regenerate: copy `port/oracle/src` + `ui.ts` to a scratch dir, link Pi's `node_modules`, run the generator scripts (kept out of the tree; the two
    JSON files are the artifacts).
13. **Handlers use the first-arrival order.** `registerRollover` registers in TS order; `Fire` of the fixture reaches the first handler of an event like TS `callbacks[0]`.

## Review findings that are SDK/host limits (R1-R4): exact observable effect

Not worked around in this lane; the demonstration tests are in `review_recroll_test.go`, marked `t.Skip` with the finding id.

- **R2 (every turn, persisted bytes).** Whenever another extension's `turn_end` draft (`custom`, `custom_message`, `compaction`) is proposed before Posthorse's handler runs, the SDK
  echoes the decoded `event.entries` back as `_pigBoundaryEntries` and the host adopts that list. The members inside the draft's `data`/`details` come back in sorted key order,
  not in the order the other extension wrote, and Pi persists them that way. It happens on every turn that has such a draft, also when Posthorse returns nothing; the values are
  unchanged, only the member order of those foreign objects (visible in the session JSONL and to any reader that depends on member order).
- **R3 (model-visible request).** When the `context` hook changes the list (a stale reminder is dropped, or a retained tail is moved ahead of the checkpoint: every LLM call of a window
  that a retained-tail rollover opened), the SDK sends back the whole list as decoded maps. PiG keeps the order it decodes for tool-call arguments, so every assistant tool call in that
  context goes to the provider with its `arguments` members sorted instead of in the order the model emitted. The text is the same JSON value; the bytes differ, so the request prefix
  changes against requests Posthorse left alone (a prompt-cache miss from the first reordered call on).
- **R1 (model-visible handoff text).** The automatic recovery record prints `Call arguments: <JSON>` and `Execution arguments:` for tool-result evidence. The arguments reach the port as
  decoded `map[string]any`, so the JSON is printed with sorted members (`{"limit":2,"offset":1,"path":"a.txt"}`) where TS prints the model's order (`{"path":"a.txt","offset":1,"limit":2}`).
  Any automatic record that carries a tool call with two or more arguments not already in byte order differs.
- **R4 (headless failure report).** The failure report of an automatic rollover without a UI goes to the extension process's stderr (`fmt.Fprintln(os.Stderr, ...)`). PiG keeps a Go subprocess
  extension's stderr in a temporary log that is removed unless a load/crash diagnostic references it, and only Node extensions have their console output forwarded to the terminal. In
  `pi -p`/json/rpc runs the message that Pi's `console.error` shows on the terminal is therefore not shown to the user with the Package; the rollover itself is cancelled as in TS.
  Decision for the owner: forward Go extension stderr in non-TUI modes, or add an SDK console call, or record a named exclusion.

## Verification

- Twins and all lane tests: `go test -race -count=3` over `TestRollover*|BoundedTail|RetainedTail|Retention*|AutomaticRecovery|TwinPosthorseRollover|TwinRolloverCost` ok (104 s, 43,000-entry case under race).
- `go vet` clean for linux, darwin, windows (whole package at the time of the run).
- Mutation spot checks (container copy): 20 mutants of the lane's files (thresholds, band edges, explicit reason, aborted outcome, sentinel text, delivery mode,
  kept-range bound, leaf shortcut, stale filter enabled flag, tail budget, pairing, delegation conditions): all killed except `limit < minPageChars` (an exact-1000
  boundary of the payload arithmetic, not reachable by a settings choice without a one-token search) and two equivalent mutants (`maxTokens <= 0` guard, the nil-leaf cache shortcut).

## Benchmarks (xd6702 container, Go 1.27, 8 CPUs, `-benchtime=2s`)

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| `BenchmarkReminderLookupCold20000NoWindow` (scan, in-memory reader) | 1.85 ms | 80 | 1 |
| `BenchmarkReminderLookupCold20000WindowNearLeaf` | 490 | 80 | 1 |
| `BenchmarkReminderLookupCertified20000` (unchanged leaf) | 204 | 0 | 0 |
| `BenchmarkContextHookNoReminder1000` (scan of 1000 messages) | 8.5 µs | 0 | 0 |
| `BenchmarkContextHookStaleReminders1000` (scan + filter, 100 stale) | 51 µs | 16384 | 1 |
| `BenchmarkRetainedTailCut` (5000 projected entries) | 2.5 ms | 1.95 MB | 17,875 |

Through the fake host (`POSTHORSE_WIRE_COSTS=1 go test -run TestRolloverWireCosts -v`; includes JSON framing, the SDK's event decode and the fixture's own work, so an upper bound):
turn_end cold lookup on a 20,000-entry branch 1.07 s, 127 MB, 2.1 M allocs; certified turn_end 0.71 ms, 650 KB, 693 allocs; `context` of 1000 messages 8.6 ms
(no reminder), 10.0 ms (100 stale), ~3.2 MB, ~50,000 allocs (all SDK/fixture decode; the hook's own part is the table above).

## Open points for the lead

- Red log: the three production files were written before the red log; `port/red/RolloverLane.log` is the run of the same tests against signature-only stubs.
- Owner decisions: item 6 (turn on the hybrid walk or keep the by-id walk), the session id/file reads of item 7, and the R1-R4 effects above.
- `BuildSessionProjection` over-decode (item 9) and the unordered foreign draft members (item 8) are SDK findings.
