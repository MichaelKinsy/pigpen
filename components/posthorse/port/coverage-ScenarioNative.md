# Coverage of `port/oracle/test/native-host.ts` (lane ScenarioNative)

`native-host.ts` has 32 `await test(...)` sites; the loops expand them to 59 test titles. The first column is the
upstream title, `file:line` is in `port/oracle/test/native-host.ts`. "S:" = recorded scenario in `port/scenarios/nat-*.json`
(golden from Pi 1.5.0, `pigeq check` on the Go port, host check Pi == PiG-Node), "L1:" = layer-1 Go test through the fake host,
"X:" = named exclusion with its reason and what covers the behaviour instead, "F:" = host finding (scenario kept in
`port/scn-findings/`, not a golden).

Every scenario has the system-prompt delta (`systemDiff: hunks`), tool calls/results, llm requests and (where stated) the
session JSONL / notes files compared. Scripted model turns replace the faux provider; where the upstream test computes a
value at run time that a static script cannot know, the scenario says how (see "Deviations").

| upstream test title | file:line | coverage |
|---|---|---|
| fork notes canonicalize legacy receipt aliases before resolving their source | 203 | S: `nat-fork-legacy-receipt-aliases` (alias symlink + real path reads in a seeded session, fork, private copy read; second parent that also read another store). PARTIAL, see Deviation 1. L1: `TestFork_legacyReceiptAliasesCanonicalizeBeforeResolvingTheirSource` (NotesStoreLane) covers the unbound-legacy branch and the `Incomplete notes snapshot: ... missing or ambiguous` error. |
| fork notes retry initialization after a missing source is restored | 252 | S: `nat-fork-retry-missing-source` (extension_error `Incomplete notes snapshot: source note task.md is unavailable.`, restore, private read, child append leaves the source). L1: `TestFork_retryInitializationAfterAMissingSourceIsRestored`. |
| fork notes retain deleted-worktree source roots across projects and interactive descendants | 348 | X: RPC `fork`/`/eq-fork` keep the session's cwd, and a scenario's host has one fixed workspace; the test forks into another cwd (`SessionManager.forkFrom(source, otherCwd)`), removes a git worktree under a live session and nests an `AgentSessionRuntime` fork. Covered by L1: `TestFork_retainDeletedWorktreeSourceRootsAcrossProjectsAndInteractiveDescendants`. |
| fork notes snapshot the selected branch without sharing its writable checkpoint | 416 | S: `nat-fork-snapshot-selected-branch` (private copy keeps the parent's 2020 mtime, unrelated note left behind, list collapses identical copies / lists diverged ones, labeled live reference, child append private, resume). L1: `TestFork_snapshotTheSelectedBranchWithoutSharingItsWritableCheckpoint`. |
| fork notes keep ownership across tree navigation, adoption, deletion, and resume | 464 | S: `nat-fork-ownership-tree-adoption-resume` (copy of the session file with its leaf on an earlier entry, `/eq-tree`, read-only live reference, guarded native read/edit of the live source, adoption by private edit, parallel codemode appends, parent blocked from the child's notes and from managed metadata, deletion stays after a resume). L1: `TestFork_keepOwnershipAcrossTreeNavigationAdoptionDeletionAndResume`. |
| fork adoption reports a private copy left by failed source verification | 596 | X: the test replaces `fs/promises.copyFile` inside the test process to change the source after the copy; a Go port copies in its own process, no RPC or helper extension can interpose. L1: `TestFork_adoptionReportsAPrivateCopyLeftByFailedSourceVerification`. |
| fork notes recover metadata-only nested writes through edited receipts and compaction | 680 | S: `nat-fork-recovery-metadata-only-receipts` (codemode write over the 8 KiB argument limit, receipt `context_edit` appended to the session file by the harness command `/eq-ctxedit` and reloaded, `dump` rollover whose handoff says the content is not retained, fork copies the note, `context_edit null` excludes it from the next fork). L1: `TestFork_recoverMetadataOnlyNestedWritesThroughEditedReceiptsAndCompaction`. |
| fork notes associate late cancelled background effects with their launch branch | 735 | X: the upstream test itself skips unless the host exposes `"background": true` in the codemode description; Pi 1.5.0 answers `@options only supports max_output_tokens and timeout_ms; got background` (probed on Pi and PiG), so the test is skipped on the host the port targets. L1: `TestFork_associateLateCancelledBackgroundEffectsWithTheirLaunchBranch`. |
| background composition binds a reset only to its active foreground call (early=true) | 837 | X: skipped upstream on this host (same reason as 735). L1: `reset_requests_test.go` (`TestResetRequestSettlesWithTheForegroundCall`, `...NestedCallsJoinTheirRoot`, `...OutsideAForegroundBatchFails`, `...IgnoresUnknownParents`), which drive the `parentToolCallId` binding of `reset_requests.go` directly. |
| background composition binds a reset only to its active foreground call (early=false) | 837 | X: as above. |
| native page reads estimate current active schemas only when usage is unknown | 994 | L1: `TestNativeHost_pageReadsEstimateCurrentActiveSchemasOnlyWhenUsageIsUnknown` (`native_host_pages_test.go`: host-call counters active/all/usage/policy per evaluation, results equal across known/zero/null/absent usage, 280,000-character catalog reduces the capacity, later loadouts are estimated afresh). X for a scenario: the assertion counts calls into in-process spies. |
| native history searches do not serialize unrelated active tool arguments twice | 1143 | S: `nat-history-projection-search` (same session shape written by `/eq-projection`: 5 archived needles, boundary, 64 x 64 KiB unrelated tool calls; pages of 2 with the cursor, skipped counts, absent query). X for the `JSON.stringify` call counts: they instrument the JS engine; Go has no equivalent hook. The bounded work is covered by `BenchmarkSearchPage` / `BenchmarkSearchBranch` (`history_bench_test.go`). |
| native history normalizes a shared edit target only once (all=false) | 1225 | S: `nat-history-shared-edit-target-all-false` (8 context edits replacing one entry with the same tool call; no match, skipped 8). X for the serialization count: as 1143. |
| native history normalizes a shared edit target only once (all=true) | 1225 | S: `nat-history-shared-edit-target-all-true`. X for the count: as 1143. |
| native all-session history keeps invocation-time context visibility across archive I/O | 1257 | OPEN GAP: the test edits the session (`appendContextEdit`) while an all-session search is pending, which needs in-process access to the manager; no scenario can race a call. The Go search takes its visibility from the branch read at invocation (`history.go`); no layer-1 test of the race was written in this lane. The sequential effect of an edit on visibility is in `nat-history-visibility-edits-branches-all-*`. |
| native history visibility follows edits, branches and session replacements (all=false) | 1283 | S: `nat-history-visibility-edits-branches-all-false` (one saved tree searched at each of the upstream's leaves through `/eq-tree`, after a reopen, after the entry's text is rewritten on disk by `/eq-rewrite`, and in another session). |
| native history visibility follows edits, branches and session replacements (all=true) | 1283 | S: `nat-history-visibility-edits-branches-all-true` (random ids of journaled lookup echoes and the cursor are normalized, see rules). |
| native loaded history extracts only early page hits and lookahead from a large archive | 1353 | S: `nat-history-large-archive-search` (4096 archived messages across two windows, ten per page, cursor, oldest entry). X for `accessed.size <= 11` (property-read counting): `BenchmarkSearchArchive` / `BenchmarkSearchArchiveEveryLine`. |
| native loaded history reads only the selected content and preserves abandoned-branch locators | 1382 | S: `nat-history-selected-reads-abandoned-branch` (archived reads by id in both windows; abandoned branch: not found by a branch search, read by bare and source-qualified id, found by all-session search). X for the untouched-record and serialization counters: as 1353. |
| official resume keeps legacy saved window data recoverable after a public reset | 1453 | S: `nat-resume-legacy-window-recoverable` (legacy `context_window` entry, `new_context`, history search and read of the legacy window, session file after a reopen). |
| all-session history scopes project sessions and their nested subagents | 1507 | S: `nat-history-all-scopes-nested-subagents`. |
| history recovers every image across pages and fresh contexts | 1574 | S: `nat-history-recovers-images-across-pages` (13 images, 32,000-token window, two pages learned from the Pi run: offset 0/0 then 52/9, `new_context` after each; image blocks recorded with bytes and digest). |
| automatic rollover keeps namespaced ask_question as evidence, not owner input (visible call) | 1649 | X: the call block carries `namespace: "survey"`; an OpenAI-compatible stream cannot produce that field, so no scenario can send it. Behaviour of namespaced ask evidence is exercised by the "no call namespace" scenario and the L1 twins of posthorse.test.ts (RecoveryLane). |
| automatic rollover keeps namespaced ask_question as evidence, not owner input (omitted call) | 1649 | X: as above, and the edit is `ctx.sessionManager.appendContextEdit` in a `turn_end` hook: under PiG `ctx.sessionManager` is a read-only `RuntimeSessionManager` without that method (probed, host finding 3). |
| automatic rollover keeps namespaced ask_question as evidence, not owner input (replaced call) | 1649 | X: as the omitted case. |
| automatic rollover keeps namespaced ask_question as evidence, not owner input (no call namespace) | 1649 | S: `nat-rollover-namespaced-ask-question-evidence` (tool registered with `namespace: {name: "survey"}`). |
| automatic rollover preserves bare owner question provenance (answer) | 1764 | S: `nat-rollover-owner-question-provenance-answer` (two chained rollovers; the sibling calls are made to end in a fixed order, see Deviation 3). |
| automatic rollover preserves bare owner question provenance (cancelled) | 1764 | S: `nat-rollover-owner-question-provenance-cancelled`. |
| automatic rollover preserves bare owner question provenance (error) | 1764 | S: `nat-rollover-owner-question-provenance-error`. |
| automatic rollover respects context edits in its recovery handoff | 1865 | S (partial): `nat-rollover-respects-context-edits` (seeded omitted and revised owner messages). X for the in-turn `appendContextEdit` of the dump result (APPROVED_TOOL_MARKER): host finding 3; the tool-result edit path is covered by the RecoveryLane twins with edited projections. |
| edited image recovery points to the replacement rather than the raw original | 1946 | S: `nat-edited-image-recovery-points-to-replacement`. |
| public Posthorse guidance survives tool additions and rollover (structured) | 2015 | S: `nat-guidance-survives-tool-additions-structured` (tool added by `/eq-tools`, rollover in the third prompt, session JSONL captured). |
| public Posthorse guidance survives tool additions and rollover (custom) | 2015 | S: `nat-guidance-survives-tool-additions-custom` (`--system-prompt CUSTOM_PROMPT_POLICY`). |
| public Posthorse guidance survives tool additions and rollover (forced-before) | 2015 | S: `nat-guidance-survives-tool-additions-forced-before` (a helper extension loaded before Posthorse forces the prompt; the upstream test unshifts a handler into Posthorse's own list). |
| public Posthorse guidance survives tool additions and rollover (forced-after) | 2015 | F: `port/scn-findings/nat-guidance-survives-tool-additions-forced-after.json`. Pi sends the guidance as a request-local custom message; PiG-Node (and the Go port, identically) lose it, see host finding 1. |
| idle custom wakeup gets current request-local guidance (unforced) | 2130 | S: `nat-guidance-idle-wakeup-unforced` (`/eq-wake` = `pi.sendMessage(... triggerTurn)`, reserve raised to 32,000 by `/eq-settings` + reload). |
| idle custom wakeup gets current request-local guidance (forced) | 2130 | S: `nat-guidance-idle-wakeup-forced`. |
| idle custom wakeup gets current request-local guidance (forced-user-to-custom) | 2130 | S: `nat-guidance-idle-wakeup-forced-user-to-custom`. |
| namespaced tools count toward the fresh handoff budget | 2246 | S: `nat-handoff-budget-counts-namespaced-tools` (the refusal's `limit N` depends on the host's system prompt length and is normalized, finding 4). |
| public mixed searches/list/reads stay below configured capacity (compaction=false, reads=4) | 2302 | X: the four sibling calls of one assistant message run in parallel; which sibling is refused for lack of context, and the order of their `tool_execution_end` events, depend on timing and differed between Pi, PiG-Node and the Go port (the scenario `nat-mixed-*` was written and withdrawn). Capacity arithmetic of siblings: `paging_test.go`, `budget_test.go`, twins of posthorse.test.ts. |
| public mixed searches/list/reads stay below configured capacity (compaction=true, reads=4) | 2302 | X: as above. |
| public mixed searches/list/reads stay below configured capacity (compaction=false, reads=6) | 2302 | X: as above, and the calls quote the live prompt's entry id (random). |
| public mixed searches/list/reads stay below configured capacity (compaction=false, reads=50) | 2302 | X: as the reads=6 case. |
| public answer in the reminder band preserves native settlement (continuation=none, stopReason=stop) | 2439 | S: `nat-settlement-in-reminder-band-none-stop`. |
| public answer in the reminder band preserves native settlement (continuation=none, stopReason=length) | 2439 | S: `nat-settlement-in-reminder-band-none-length`. |
| public answer in the reminder band preserves native settlement (continuation=steer, stopReason=stop) | 2439 | S: `nat-settlement-in-reminder-band-steer-stop`. |
| public answer in the reminder band preserves native settlement (continuation=followUp, stopReason=stop) | 2439 | S: `nat-settlement-in-reminder-band-followup-stop`. |
| public answer in the reminder band preserves native settlement (continuation=boundary, stopReason=stop) | 2439 | S: `nat-settlement-in-reminder-band-boundary-stop`. |
| public enabled-to-disabled policy removes the active reminder but keeps its journal entry | 2557 | S: `nat-reminder-policy-disabled-keeps-journal`. |
| public checkpoint reminder reaches the model after Pi compacts inside a rollover window | 2593 | S: `nat-reminder-after-native-compaction-in-window`. |
| public history search skips entries still in the active context (nativeCompact=false) | 2634 | S: `nat-history-skips-active-context-posthorse-reset`. |
| public history search skips entries still in the active context (nativeCompact=true) | 2634 | S: `nat-history-skips-active-context-native-compact`. |
| public search cursors finish despite appended lookup echoes (all=false) | 2690 | X: 30 chained pages, each call quotes the cursor of the previous result; a cursor contains the run-random id of a journaled lookup echo, and a scripted model cannot echo a value from a tool result. Cursor paging and echo handling: `paging_test.go`, `history_test.go` and the posthorse.test.ts twins; first-page and second-page cursors are in the archive/projection scenarios (deterministic ids). |
| public search cursors finish despite appended lookup echoes (all=true) | 2690 | X: as above. |

## Deviations from the upstream test shape (all deliberate, none weakens an assertion the scenario carries)

1. **Sessions are always bound by Posthorse when opened.** Opening a seeded session (`switch_session`) runs Posthorse's
   `session_start`, which appends `posthorse-notes-scope`; the upstream test 203 keeps its parent session free of the extension
   (`posthorse: false`). The unbound-legacy path (and the ambiguous-roots error) is therefore L1 only.
2. **No `SessionManager.forkFrom` with a moved leaf in RPC.** `/eq-fork` copies only the branch up to the entry; test 464 needs
   the whole file with the leaf on an earlier entry. The helper tool `forkfile` writes that copy (header `parentSession`, a fixed
   session id so the private directory name is known to the script).
3. **Parallel sibling calls are made deterministic or serialized.** `tool_execution_end` events of parallel calls arrive in
   timing order; where upstream issues several calls in one assistant message the scenario either orders their completion through
   a helper dependency (`ask_question` then `dump`, 1764/1649) or issues one call per turn (1453, 1507, 2634).
4. **Run-time values:** entry ids of seeded entries are fixed non-hex strings (`sd000001`) so they are literal in scripts and
   outputs; ids made by the host are normalized (rules below). Prompts of 300,000+ characters are replaced by the reported usage
   (2439, 2557, 2593). Mid-run `SessionManager.appendContextEdit` / settings overrides become the harness commands
   `/eq-ctxedit` (appends a `context_edit` line to the session file, the session is reloaded) and `/eq-settings` (writes the agent
   settings file, `ctx.reload()`).
5. **Fork provenance of the first fork scenarios uses the real `/eq-fork` command** (a branch copy), not `forkFrom` of the whole file.

## N7 / projection rules used (each only for a value the extension does not produce or an id made by the host)

| rule | scenarios | why |
|---|---|---|
| `"anchors":["<8 hex>` -> `<entry-id>` (manifest text); `anchors` arrays of session JSONL are relabeled `<id:N>` by the harness (new in `project.go`) | fork | entry ids of the host |
| `"mtimeMs":..,"ctimeMs":..,"ino":..,"dev":..` -> `<n>` (manifest text) and `**.identity` dropped in captured JSONL | fork, rollover | file-system stat values of the copied notes (size and digest stay compared in the manifest text) |
| minute of real mtimes `20(2[1-9]|[3-9]\d)-..T..:..Z` -> `<time-minute>` | fork | a note written now; fixed 2020 mtimes stay visible |
| `\[<8 hex>[@\]]` -> `[<entry-id>`, `continue with cursor "..."` -> `<cursor>` | history all=true | ids of journaled lookup echoes (host-made) and the cursor that quotes them |
| `\bentry <8 hex>\b`, `(use history read): <8 hex>` -> `<entry-id>` | rollover | entry ids quoted in the recovery record |
| `Read <path>/codemode.md first`, `Wall time N seconds` | codemode | host wording and host-measured time |
| `project.drop`: `**.durationMs`, `**.parentToolCallId`, `**.sections.docs`, `**.sections.preamble`, `**.estimatedTokensAfter` | codemode / rollover | host-measured, host wording, host token estimates (finding 2 and HarnessExt finding 1) |
| `project.skipSystem` (new, README) | history scenarios that navigate the tree | Pi appends a system-message entry after a tree navigation, PiG does not (finding 2); the extension's part of the prompt stays compared in every request |
| `limit N` of the handoff refusal | `nat-handoff-budget-counts-namespaced-tools` | computed from the host's system prompt length (2,404 vs 2,440 characters) |

## Host findings (Pi 1.5.0 vs PiG-Node differ; reported, not normalised away)

1. **Forced prompt applied by a later handler is not visible to an earlier handler** (scenario in `scn-findings/`): with a
   second extension that returns `{systemPrompt}` from `before_agent_start` *after* Posthorse, Pi shares one `systemPromptOptions`
   object so Posthorse sees `forceSystemPrompt` and sends its guidance as a request-local custom message; PiG-Node (the TS original
   and the Go port alike) does not, and the guidance is missing from the model request.
2. **Pi emits a system-message `message_end` after a tree navigation that lands on a branch without one; PiG does not**
   (hence `project.skipSystem`). Pi's RPC `tool_execution_end` carries `parentToolCallId` for nested codemode calls; PiG's does
   not (the extension still receives it; dropped from the trace).
3. **`ctx.sessionManager` in PiG is a read-only `RuntimeSessionManager` with no `appendContextEdit`**; Pi's is the real
   `SessionManager`. Helper extensions (and the upstream tests' `turn_end` hooks) cannot edit the context mid-run on PiG.
4. Handoff budget limit differs by the host's system prompt length (36 characters).
5. Pi 1.5.0 does not expose background codemode (`"background": true`).

## Port bug / SDK finding

* **Fixed, port bug:** `withPosthorseSection` (guidance.go) rebuilt the system message's `sections` alphabetically
  (cwd, docs, preamble, rules, tools, posthorse) because `before_agent_start` options carry no sections to learn the order from;
  Pi and PiG-Node keep `preamble, tools, rules, docs, cwd, posthorse` (guidance.ts:35-38). Every scenario's system delta differed on the Go lane.
  Fix: Pi's built-in order (`core/system-prompt.ts buildSystemPromptSections`) is the fallback order; regression test
  `guidance_sections_test.go`. Another extension's several own sections are still ordered alphabetically (SDK limitation).
* **Open, SDK gap (not fixable in the port):** a `context_with_system` handler that edits the system message in place makes the
  SDK send the whole message list back from decoded Go maps; every JSON object in it (tool-call `arguments`) is re-encoded with
  sorted keys, so the model request differs byte-wise from Pi's and PiG-Node's whenever saved arguments are not alphabetical.
  Reproducer: `port/scn-findings/finding-context-argument-key-order.json` (the nat-history scenarios that carry tool-call
  arguments are seeded with alphabetical keys so they pass). Needs an ordered decode in `extensions/sdk` (owner decision).
