# Lane notes: CoreLane (lane A)

Files (all under `extensions/posthorse/`): `extension.go`, `contracts.go`, `dyn.go`, `js.go`, `message_text.go`, `files.go`
(+ `files_unix.go`, `files_windows.go`), `session_lines.go`, `budget.go`, `guidance.go`, `paging.go`, `reset_requests.go`,
`context_tools.go`. Tests: `twin_posthorse_core_test.go`, `twin_package_notes_test.go`, `twin_quality_skips_test.go`,
`js_test.go`, `budget_test.go`, `paging_test.go`, `message_text_test.go`, `reset_requests_test.go`, `files_test.go`,
`session_lines_test.go`, `context_tools_test.go`.

## Contract table

| ID | Upstream | Go | Test |
|---|---|---|---|
| A1 | index.ts `createPosthorse` (registration order) | `newPosthorse`, `Extension()` | `TestToolRegistrationOrder`, `TestHandlerRegistrationOrder` |
| A2 | index.ts:46 live policy | `liveCompactionPolicy` (`ctx.GetSettings` + `SettingsManagerInMemory(...).GetCompactionSettings(model)`) | twins 7, 10, 30 use the injected reader; live path is exercised by the scenarios |
| A3 | index.ts:22-39 `activeToolTokens` | `newActiveToolTokens` (per-tool cache keyed on description+parameters) | fixtures |
| A4 | contracts.ts, records.ts | `contracts.go` | `TestRequireValue`, `TestTextResult`, `TestIsRecordAndReminderType` |
| A5 | JS string/number semantics | `js.go` | `js_test.go` (reference-model property tests over code units) |
| A6 | message-text.ts | `message_text.go` | `message_text_test.go` |
| A7 | budget.ts | `budget.go` | `budget_test.go`, twins 7, 30, 31 |
| A8 | guidance.ts | `guidance.go` | twin 9 |
| A9 | paging.ts | `paging.go` | `paging_test.go`, twins 32, 33, 35 |
| A10 | reset-requests.ts | `reset_requests.go` | `reset_requests_test.go`, twin 7 |
| A11 | context-tools.ts | `context_tools.go` | `context_tools_test.go`, twins 10, 30 |
| A12 | session-lines.ts | `session_lines.go` | `session_lines_test.go` |
| A13 | files.ts | `files.go` | `files_test.go`, `twin_package_notes_test.go`, twins 39-42, 48 |

Counts: `pigeq twins check` for `posthorse,package-notes,quality-*,retained-qualification`: 74 exact twins (all lanes), 59 named
skips (mine: 13 posthorse twins, 11 package-notes twins, 59 quality skips, every reason distinct).

## Representation decisions

* Go strings are WTF-8 like the SDK: a lone surrogate is ED A0..BF xx xx; `u16len/u16slice/...` count UTF-16 units and a cut
  through an astral character leaves the lone half. Two cut halves rejoined by `+` stay two 3-byte forms (same unit count);
  `jsUTF8Bytes` (hashing, file writes) canonicalises them. `jsFromBytes` is `Buffer.toString("utf8")` (WHATWG maximal subparts).
* `toolTokensFunc = func(ctx sdk.Context) (int, error)`; `pageCapacity` reads the handler context out of the policy snapshot
  (`hostOf`), so a bare `policyContext` fake cannot serve the unknown-usage branch (it errors loudly).
* AbortSignal = `context.Context` (nil = none); `signalOf(sdk.Context)`. A custom cancel cause is thrown as itself.
* Node fs errors: `fsError`, `nodeFsError(err, syscall, path)` ("ENOENT: no such file or directory, open '/x'").
* Test seams in files.go: `renameSync`, `nowMillis` (the TS tests mock `fs.renameSync` and `Date.now`).
* `lfChunksFile(..., reuse bool)`: sessionLines reads into one reused buffer and copies only matching lines; `bytes.IndexByte`.

## Findings and SDK gaps (for the owner)

1. **Key order of dynamic objects is lost.** Event data and session entries arrive as `map[string]any`, so any text rendered from
   them (`textOf` of a tool call's `arguments`, `executionArguments`) prints keys in sorted order, not the session file's
   insertion order. History must parse raw JSON with an ordered decoder where the order is shown.
2. **System message `sections` order (guidance.ts:35-38).** `context_with_system` messages carry `sections` as an unordered map;
   `{...system.sections, posthorse}` keeps the original order in TS. `withPosthorseSection` rebuilds the order from the run's
   `before_agent_start` sections (known keys first, remaining sorted, `posthorse` in place or last) and writes a
   `*sdk.OrderedObject`. Exact only when no other extension adds sections. Needs an ordered `sections` in the SDK event to be exact.
3. **`before_agent_start` options are a snapshot**, not the shared object of Pi: a later handler changing `forceSystemPrompt` is
   not seen (guidance.go keeps only `forceSystemPrompt` presence and section order of the run).
4. **Global notes directory**: `getAgentDir()` is `sdk.AgentDir()` (PiG's agent tree, `~/.pig/agent`, not Pi's `~/.pi/agent`);
   package notes of global checkouts therefore live under PiG's agent directory. Without `PIG_EXT_HOST_INFO` (not started by a
   host) `notesDir` returns an error for any project that has a `.git`.
5. **Windows**: `symlinkSync(..., "junction")` is a directory symlink (Go cannot create junctions); `path.dirname/basename` use
   `filepath` semantics there. Only `go vet` was run for windows.
6. `sdk.ToolResult` omits an empty text block when images are present (TS keeps `{type:"text",text:""}`); only observable for an
   empty page with images.
7. TS defect ported as is: `paging.ts` `refuse` can throw `new Error("")` (empty text when the refusal no longer fits the remaining
   budget); the Go tool returns an error with an empty message. The fake host's `RunErr` needed `RunFailed` for it.

## Named skips

All `quality-*` (52) and `retained-qualification` (1) cases: oxlint/oxfmt/tsc/Renovate/YAML/npm tooling of the TS repo, each
with its own reason in `twin_quality_skips_test.go`. quality-registrations has 5 scraped fixture snippet names ("top level",
"suite child", "owned work", "parent", "body") that are not test cases.
Adaptations of twins (not skips): #39 uses an exact-text replace in place of Pi's `createEditTool`; #41 fixes Pi's `parseGitUrl`
results as constants; #40 uses the fixture's `withAgentDir`.

## Allocations (non-race, amd64 xd6702, `go test -bench`)

budgetFor 83 ns 2 allocs; buildGuidance 2.0 us 10 allocs (4.5 KB); guidanceTokens 4.0 us 12 allocs; freshPayloadChars 4.6 us 13;
pageCapacity 4.3 us 13; pageReservations.size 4.2 us 13; notes() 400 rows 43 us 33 allocs; contextReport 1.0 us 17;
u16len ASCII 2.75 GB/s 0 allocs, mixed 375 MB/s 0 allocs; u16slice ASCII 23.6 us/40k 0 allocs; shortKey 421 ns 2 allocs;
lfLines 1.8 GB/s (10.7 MB for 10 MB, 319 allocs); indexLF 43 GB/s; sessionLines scan 680 MB/s, 2 allocs per line (head string +
`text` closure) and 38 MB per 27 MB file (head strings); resetRequests cycle 1.2 us 7 allocs. `guidanceTokens` rebuilds the guidance
(as TS does) on every page/budget evaluation: a cache keyed on (window, reserve, enabled) would remove ~80 % of the budget
evaluation cost; not done (the TS recomputes too).

## Open questions

* `liveCompactionPolicy` costs two host round trips per snapshot (`SettingsManagerInMemory` + `getCompactionSettings`); a local
  resolution of `compaction.*` would save them but would duplicate Pi's validation. Decision for the owner if first-turn latency matters.

## Review round (port/review/corereview.md) - status

| ID | status | what changed |
|---|---|---|
| R1 | fixed | `packageRelativePath` excludes `"."` (Go's spelling of Node's `""`): `TestReviewNotesDirInstallsRootStaysLocal`, `...ProjectContainerRootStaysLocal`, `TestReviewPackageRelativePathSame` |
| R2 | fixed | two constructors: `throwIfAborted` (the `signal.throwIfAborted()` sites: publishFile start and after close, session-lines `rangeText`) = DOMException text "This operation was aborted" (a custom `WithCancelCause` reason is still thrown as itself); `fsAborted` (fs calls handed the signal: lfChunks read loop, readText, publishFile's writeFile loop) = "The operation was aborted". Other lanes' call sites must pick by their TS site: `signal?.throwIfAborted()` -> `throwIfAborted`; `readFile/createReadStream/writeFile({signal})` -> `fsAborted`. |
| R3 | fixed (residue below) | `errno_messages.go` = every name/message of Node 24's libuv map (dumped with `util.getSystemErrorMap()`); `errno_linux.go` = Linux errnos with a libuv name; `errno_darwin.go` = the same names via `syscall` constants; `errno_windows.go` = libuv's Win32 translation table; `errno_other.go` = the POSIX subset. An errno libuv does not name (e.g. EDQUOT, ESTALE) is Node's `UNKNOWN: unknown error` (lib/internal/errors.js `uvUnmappedError`), which is what Node prints for it, so the review's expected `EDQUOT: ...` is not what Node 24 does. Residue: Windows table typed from libuv's `uv_translate_sys_error`, not run on Windows; macOS table from libuv's name list, not run on macOS. |
| R4 | fixed | fd reads (`readText`, `isBinaryFile`, `lfChunksFile`, session-lines `rangeText`) report `read` with no path |
| R5 | fixed | `matchOwnerAndMode` returns `fstat` / `fchown` / `fchmod` Node fd errors itself (files_unix.go, files_windows.go) |
| R6 | fixed | `utf8Decoder` is a line-by-line port of string_decoder.cc `DecodeData` (lead-shaped tail hold-back, completion decoded apart); `TestUTF8DecoderHoldsLeadShapedTails`, `TestReviewUTF8DecoderDifferential` (needs the scratch JSON, skips when absent) |
| S1 | SDK, not fixed | repro: `sdk.EstimateTokens(map[string]any{"role":"user","content": <8 lone surrogates, WTF-8 "\xed\xa0\x80" x8>})` returns 6; Pi's `estimateTokens` returns 2 (`"\ud800".repeat(8).length / 4`). Cause: `compaction_estimate.go utf16Length` ranges runes, each byte of the three-byte surrogate is a RuneError. The regression test is skipped with this text. Not reachable from `guidanceTokens` (ASCII); `retention.go messageCost` (RolloverLane) sizes host messages with it. |
| N3 | fixed | `mkdirRecursive(dir)` in files.go (Node `mkdirSync(dir,{recursive:true})` errors: existing file -> `EEXIST ... mkdir`, ancestor file -> `ENOTDIR`); `notesDir` uses it |
| N5 | not done | `pageRequest.offset`/`notesPage.offset` stay `int`; offsets above 2^53 print as Go ints (JS prints `1152921504606847000` for 2**60). Changing the field type breaks every history.go/notes.go caller; needs a coordinated change of the three lanes. |

## O1: exact Pi 1.5 `getCompactionSettings` (settings-manager.js 643-684) and a local resolution

Pi 1.5.0 (dist/core/settings-manager.js):

```
getCompactionEnabled()  = settings.compaction?.enabled ?? SETTINGS_DEFAULTS.compaction.enabled        // true
getCompactionTokenSetting(field, model):                                                             // field = reserveTokens | keepRecentTokens
  ordinary = settings.compaction?.[field]
  if ordinary !== undefined and (typeof ordinary !== "number" || !Number.isSafeInteger(ordinary) || ordinary < 0)
      throw Error(`Invalid compaction.${field} setting: ${String(ordinary)}. Expected a non-negative safe integer.`)
  modelKey = model ? `${model.provider}/${model.id}` : undefined
  entry    = modelKey !== undefined ? settings.compaction?.modelOverrides?.[modelKey] : undefined
  if entry !== undefined and !isMergeableObject(entry)
      throw Error(`Invalid compaction.modelOverrides["${modelKey}"] setting: ${String(entry)}. Expected an object.`)
  override = entry?.[field]
  if override !== undefined and (typeof override !== "number" || !Number.isSafeInteger(override) || override < 0)
      throw Error(`Invalid compaction.modelOverrides["${modelKey}"].${field} setting: ${String(override)}. Expected a non-negative safe integer.`)
  return override ?? ordinary ?? DEFAULT_COMPACTION_TOKEN_SETTINGS[field]                             // 16384 / 20000
getCompactionSettings(model) = { enabled, reserveTokens, keepRecentTokens }  // reserve first, then keepRecent: the first throw wins
```

`settings` is the merged global + project object (project wins per key, nested objects merged one level by
`deepMergeSettings`), after `migrateSettings` (settings-manager.js:281). `SettingsManager.inMemory(pi.getSettings())` migrates
`structuredClone(settings)` and loads it as the only (global) scope, so the call sees exactly `ctx.GetSettings()` after migration.
`model` is `ctx.model` (an object with `provider` and `id`; `undefined` when no model is selected).

A local resolution from `GetSettings()` must replicate:
1. `migrateSettings` for the compaction keys (check the function body for renames/moves of `compaction.*`; not read in this round);
2. `deepMergeSettings`: `GetSettings()` already returns the effective merged object, so no merge is needed;
3. the three checks above with `String(x)` formatting of any JSON value (numbers through JS `Number#toString`, strings raw, arrays
   joined by `,`, objects `[object Object]`, `null` as `null`, booleans) and `Number.isSafeInteger` (|x| <= 2^53-1, integral);
   `isMergeableObject` (non-null, non-array object);
4. the key `${provider}/${id}` built from the host's `model.provider`/`model.id` (the SDK offers `Context.Model()` and `ModelProvider()`
   locally);
5. evaluation order: enabled, then reserveTokens (ordinary check, entry check, override check), then keepRecentTokens, the first
   throw propagating as the tool/handler error.

Differential test plan: generate settings objects (absent / `{}` / valid / negative / fractional / > 2^53 / string / null / array /
object values for `compaction.enabled`, `reserveTokens`, `keepRecentTokens`, `modelOverrides[<key>]` entries of every shape, with and
without a model, the model key containing `/`), run each through Pi's `SettingsManager.inMemory(s).getCompactionSettings(model)` under
Node (value or thrown message) and through the Go resolution, and compare results and messages 1:1 (the pigeq harness can host the
Node side; a 5,000-case random corpus plus the hand-written edge list). Cost today: `GetSettings` is local, `SettingsManagerInMemory`
and `GetCompactionSettings` are two host round trips plus a host-side object per evaluation. `liveCompactionPolicy` is unchanged.
