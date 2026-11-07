# Lane ScenarioOfficial: `test/official-host.ts` as recorded scenarios

Target: all 53 tests of `port/oracle/test/official-host.ts` (parametrised ones expanded: 71 recorded scenarios). The
table `upstream test -> scenario / Go test / exclusion` is `port/coverage-ScenarioOfficial.md`. Scenario files are
`port/scenarios/off-*.json` (names prefixed `off-`); the seven scenarios on which Pi 1.5.0 and PiG's Node runtime
disagree are kept apart in `port/scenarios-hostdiff/` (a recorded set must not contain a scenario `pigeq record`
refuses) and are the host findings below.

## Counts

* recorded scenarios: **71** (`port/scenarios/off-*.json`); goldens `port/golden/off-*` (recorded from the original under Pi).
* host-finding scenarios: **7** in `port/scenarios-hostdiff/` (6 upstream variants + 1 diagnostic probe).
* layer-1 Go tests: **6** test functions in `extensions/posthorse/official_host_test.go` (+ 2 in `notes_order_test.go`).
* exclusions of a scenario (RPC cannot reach): routed virtual model, colliding foreign `new_context` (x2), injected
  policy reader, mocked session manager, `session.abortCompaction()` variant; each is covered by a Go test (see the table).
* determinism: 5 runs of every scenario under load (6 concurrent lanes) for Pi and for the Go port: all 71 identical for Pi;
  Go identical except `off-note-publication-symlinks-modes-failure` (1 of 5), which was a real port bug (fixed, below).

## Harness additions (opt-in, `NormalizerVersion` unchanged, documented in the harness README, tests in `eq/suite_ext_test.go`)

| Feature | Why |
|---|---|
| `{{repeat:C:N}}` load-time macro | the suite feeds 1.2 MB seeds, 600 kB prompts and 350 kB tool results; the scenario files stay small |
| scenario `collapseRuns: MIN` | a run of >= MIN identical characters in a trace string is recorded as `<run C xN>` (character and exact length kept); goldens stay small, no extension text is hidden |
| `{{cwd}}`, `{{agent}}`, `{{sessions}}` in `session.files`, workspace `files`, `setup` | a seeded session header names the lane's workspace |
| trigger `save` + `quiet` | `get_entries` while a model request is held, saved ids quoted by the held turn (`history read id`) |
| llm turns are expanded after their `hold` ends | so the saved id is visible to the turn that quotes it |
| `systemDiff: "inserted"` | the host's own summarization request has a host-owned base prompt worded differently by Pi and PiG; only inserted lines are compared |

Seeded sessions (`session.files`, opened by a `switch_session` step) stand for the tests' `seed()` / `appendMessage`
calls; entry ids are fixed (`5eed0001`...), so helper extensions can name a seeded entry.

## Normalisation (N7 and projection), every rule with its reason

* Entry ids the host invents (never extension text): `^[0-9a-f]{8}$`, `"id":"<8 hex>"`, `id "<8 hex>"`, `(entry|entries|id|window|through|and) <8 hex>`,
  `[<8 hex>]`, and the ids after `use history read): ` / `, `. They only match the 8-hex shape of a Pi entry id.
* `project.drop` `**.estimatedTokensAfter`, `**.tokensBefore` (and the same in the captured session): host token estimates over each
  host's own system-prompt wording (harness finding 1 of lane HarnessExt); `**.sections.docs`, `**.sections.preamble` in captured
  files: host wording by design.
* codemode scenarios only (`composed-explicit-reset-*`, `mixed-direct-and-composed-resets-*`): `Wall time <s> seconds`
  and `**.durationMs` (the host's measured time), `**.parentToolCallId` (Pi stamps nested tool events with it, PiG's Node
  runtime does not), the host-generated `Read <path>/docs/codemode.md first.` tail of the codemode tool description (install
  path). `message_end` and `tool_execution_end` are recorded whole there so the drops apply.
* `tool_execution_end` is not captured in scenarios whose batch mixes tools with different latencies
  (new_context + notes, nested codemode, several note writes): see finding F1. Their results are compared as `toolResult`
  messages in source order and in the captured session/note files.
* Steps that end in an overflow/length recovery use `settleMs` 800 to 1500: the retried turn's last events arrive after the step's
  `agent_end` on PiG's host (they landed in the next step on the Node lane).

## Host findings (Pi 1.5.0 vs PiG 0.7.0 Node runtime; for the lead; scenario files in `port/scenarios-hostdiff/`)

* **H1 forced system prompt.** `handoff-admission-counts-prompt-and-tools`: a `before_agent_start` handler that returns `systemPrompt`
  makes Pi set `systemPromptOptions.forceSystemPrompt` on the very object Posthorse captured (identity); Posthorse then injects its guidance
  as a `posthorse-guidance` custom message. Under PiG the object is not shared: Posthorse injects nothing (first llm request: only the
  forced prompt, no guidance), so the extension-visible behaviour differs. Go port == PiG-Node. Go layer-1 test covers the arithmetic.
* **H2 orphan tool result.** `incomplete-projected-tool-group-omitted-call`: with the assistant tool call omitted by a `context_edit`, Pi still
  sends the `tool` message `LEGACY_ORPHAN_RECEIPT` to the provider (first request, 4 messages), PiG drops it (3 messages).
* **H3 abort while tools run / in turn_end.** `explicit-reset-cancelled-by-abort-during-sibling-work`, `explicit-reset-commits-after-later-turn-end-abort`:
  the assistant message that ends the run is Pi `stopReason:error`, `errorMessage:"This operation was aborted"`, `content:[]`; PiG
  `stopReason:aborted`, `errorMessage:"context canceled"`, one empty text block.
* **H4 `session_before_compact.branchEntries`.** `owner-retention-permits-second-rollover` (+ diagnostic `probe-owner`): for the
  second compaction of a session Pi passes the full branch; PiG passes a subset without user and system message entries
  (probe: Pi `message:user ... message:system ... message:assistant ...`, PiG `message:assistant thinking_level_change custom ...`). The second record then says
  `No discarded owner or visible coordination inputs were selected` under PiG and quotes `DISCARDED_OWNER` under Pi. Go port == PiG-Node.
* **H5 read-only session manager.** `automatic-chain-preserves-durable-intent-replaced-owner` / `-omitted-owner`: the test appends a
  `context_edit` with `sessionManager.appendContextEdit`; the object handed to extensions is writable under Pi and has no such method under
  PiG (the helper throws `appendContextEdit is not available`).
* **H6 host-measured and host-wording values** (normalised, see above): codemode timing, `parentToolCallId`, codemode docs path,
  summarization prompt wording.
* **H7 duplicate tool names.** A second extension registering `new_context` aborts the host start in RPC mode (`Failed to load extension ...: Tool "new_context" conflicts with ...`);
  the in-process loader of the test tolerates it. Not a Pi/PiG difference; it only makes the two colliding tests unreachable by RPC.

## Port findings

* **F1 completion order of concurrent tools (not fixed, no deterministic fix).** In a batch `notes write` + `new_context`, the TS original
  finishes `new_context` first (synchronous) and the Go port finishes the local note write first (`new_context` costs host round trips). Only the
  order of the two `tool_execution_end` events differs; results, messages and files are identical. Those events are not captured in such scenarios.
* **P1 (fixed) concurrent write + append of one note lost the append (1 of 5 runs under load).** TS: both executions reach
  `withFileMutationQueue` after the same single `await store.get(ctx)`, so the queue sees them in start order. Go: each execution runs on its own goroutine with a
  different number of host round trips before it asks the host's queue for its turn, so the host could grant the turns in either order (append first, write
  second: final `new` instead of `new\ntail\n`). Fix: `notes_order.go` (a ticket at the start of the `notes` execution, passed when the queue turn is acquired,
  on an early failure or for an op that never queues; an execution asks the host for its turn only after the previous ticket passed); wired in `notes.go`
  (`Execute`, `writeNote`, `appendNote`). TS behaviour: `src/notes.ts:151-177` (`withFileMutationQueue`), official-host.ts:1457-1495. Regression tests: `TestNoteMutationsReachTheQueueInStartOrder`,
  `TestNoteTicketPassesWithoutQueueing`. Suggestion for the SDK owner: the SDK's `toolStartOrder` keeps start and first-host-call order of a batch; ordering the
  `fileMutation.enter` request the same way would make this gate unnecessary.

## Temporary workarounds

* **TEMPORARY-GATE** (`notes_order.go`, the `order` field and three call sites in `notes.go`, tests `TestTEMPORARYGATENote*` in `notes_order_test.go`):
  remove when the host's file-mutation queue grants turns in tool-start order (PiG lane fix/071-host-parity-ph). `grep -rn TEMPORARY-GATE extensions/posthorse` finds every piece.
  Repro of the lost append without the gate: scenario `off-note-publication-symlinks-modes-failure` (batch `notes write` + `notes append` on `alias.md`; expected `target.md` = `new\ntail\n`).
  Make `queuedInOrder` call `sdk.WithFileMutationQueue` directly (drop the wait on `t.previous`), then run `pigeq check --scenarios <dir with that file> --golden port/golden --go extensions/posthorse --pig $PIGEQ_PIG` repeatedly under load (a second lane running at the same time): about 1 run in 5 records `target.md` = `new`.

## Differences between the original's test and the scenario (the scenario never weakens the behaviour under test)

* faux provider usage (prompt size) becomes scripted `usage` (`input` grows by 20,000 tokens per 80,000-char result, honest relative to the window); the model's answers are scripted.
* appended-to-live-session messages (`h.sessionManager.appendMessage`) are a seeded session file; the initial "Establish" exchange is part of the seed.
* in-process settings (`settingsManager.applyOverrides`) become `settings.json`, per-model overrides, a second model, or `set_auto_compaction` steps; `t.mock` of `ctx.getContextUsage` becomes zero provider usage after a reset; `session.steer` inside a tool becomes a trigger `steer` while the answer is held.
* `getPolicy` injection (fresh-window fallback retains paired receipts): the injected reader returns the live values, so the default reader is equivalent.
* `persisted ` x 70,000 becomes `persisted ` + 700,000 `p` (same size, the test only matches `/persisted/`).
* `fresh-window threshold` third turn is always the final answer: a request that still held the oversized result shows in the llm trace instead of being answered with a provider overflow.

## Commands run (xd6702, tern-ci:v7)

* `pigeq run` over all 71 scenarios (4 shards, frozen Go snapshot): 71 PASS (`host check: pig-ts == pi-ts`, `port: pig-go == pi-ts`).
* determinism: 5 x {Pi lane, Go lane} x 71 scenarios, 6 lanes concurrently: see Counts.
* `cd components/extension-equivalence/extensions/extension-equivalence && go vet ./... && go test -count=1 ./eq`: green.
* `cd components/posthorse/extensions/posthorse && go vet ./... && go test -race -count=3 ./...`: see the final report.
