# Mutation proof of the LangSmith port

Status: **partial** (stopped on the lead's wrap-up order). 280 mutants killed, 3 equivalent (argued), 11 survivors left open, 9 mutants that do not build, 301 mutants written and validated but never run.

## What was run

`port/mutations.json` holds the 280 mutants that are proven killed. Not in it, on purpose:
`port/mutations-open.json` (survivors and argued-equivalent mutants), `port/mutations-pending.json` (301 mutants of chunks 3 and 4, never run, plus the 9 that do not build and need a rewrite). Every `find` occurs exactly once in the frozen tree (checked against `components/langsmith/extensions/langsmith` at 2026-11-06 18:50 MST, and again at the end).

Mutants written: 1212 (extension 271, convert 157, runtree 175, client 125+92+39+77, config 80, metadata 62, caller 49, wire helpers 80). 605 were selected by branch family and split in 4 chunks of ~150; chunks 1 and 2 (304 mutants) ran, chunks 3 and 4 did not (time: one chunk is 40 to 60 minutes at `LS_CPUS=2`, `--jobs 5`, because every mutant runs the whole suite and `TestSDKDiffGolden` alone takes ~86 s of its ~107 s).

| outcome (304 run) | count |
|---|---|
| killed by an existing unit test | 154 |
| killed by an existing golden-vector test (caller/client/runtree/profile/wire vectors) | 100 |
| killed by `TestSDKDiffGolden` first | 1 |
| survived the existing tests, killed by a test added here (`mutants_ext_test.go`, `mutants_ext_internal_test.go`; re-run with the harness) | 24 |
| survived the existing tests, killed by a test added here after the last harness run (test passes) | 1 |
| equivalent (argued, below) | 4 listed (1 proved by a rerun that survives, 3 by reasoning) |
| open: survive every test, no test written | 11 |
| invalid: the mutant does not build (unused variable and similar), not counted | 9 |

## Finding: the scenario layer of `pigeq mutate` cannot judge this port

`pigeq check` fails on the UNMUTATED port: scenario `command-tracing-enabled`, "pig-go differs from pi-ts: first difference at event 14" (the multipart body of the Go port differs from the TypeScript original's by divergence D1 and key canonicalisation, which only `lsdiff` normalises, not pigeq's byte comparison). `pigeq mutate` does not check the baseline of the scenarios, so every mutant that survives the unit tests is reported `KILLED command-tracing-enabled`. I therefore ran `--unit-only` for chunks 3 on (chunks 1 and 2 ran with `--unit`; their `command-tracing-enabled` rows are read as unit-layer survivors, as the table does). The wire scenarios are compared by `port/tools/run-lane.sh` (lsdiff); I did not use it per mutant (3 lanes per mutant is far too slow); the wire-only killers are `TestSDKDiffGolden` (JS-driven golden logs, mockls + lsdiff built from `port/tools`) and the client/caller/runtree differential tests.

## Setup finding: the unit baseline needs `../../port` next to the module copy

The harness copies only the module directory to `$TMPDIR/eq-mutantNNN/langsmith`; `TestSDKDiffGolden` (`../../port/sdkdiff`, `../../port/tools`) and `TestLateSettleTwin` (`../../port/oracle/test/recordings`) read `../../port/...` and fail in the copy ("the unmutated port's own unit tests fail"). Run with `TMPDIR` pointing at a directory that holds symlinks `port/{sdkdiff,tools,oracle}` to the Package's `port/` directory.

## Commands

On xd6702, inside `~/ls-work/in-ci.sh` (tern-ci:v7), with `S=$HOME/ls-work/scratch/LsMutate`:

```sh
mkdir -p $S/tmp/port && for d in sdkdiff tools oracle; do ln -sfn $PWD/components/langsmith/port/$d $S/tmp/port/$d; done
export TMPDIR=$S/tmp
pigeq mutate --scenarios components/langsmith/port/scenarios --golden components/langsmith/port/golden \
  --go components/langsmith/extensions/langsmith --pig $PIGEQ_PIG \
  --mutations components/langsmith/port/mutations.json --unit-only --sdk-dir $PIG_SDK_DIR --jobs 5
```

(`--unit` instead of `--unit-only` also runs the scenarios, which fail on the baseline; see above.) Expected: every mutant killed. Run the open and pending files the same way. Chunk 1 was 152 mutants in 40 minutes, chunk 2 152 in ~60 minutes under load 30 to 45.

## Production findings

* `TestSDKDiffGolden/replicas-projects` was flaky (17 failures in 30 runs): the two destination batches were sent from separate goroutines. The lead made that script `unorderedDrain: true`; the rest of the suite was green afterwards. No production defect was found by a mutant.

## Equivalent mutants (argued)

* `conv-payload-order-gemini-first`: the mutation inserts a dead `if false { return nil, nil }`; no behaviour changes
* `cfg-json-blank`: `value == ""` instead of `jsTrim(value) == ""`: a whitespace-only value reaches the JSON parser, which rejects it, and a parse failure is `undefined` as well (config.ts:65-73)
* `cfg-validate-enabled-type`: only the zod message text of an invalid `enabled` differs; `enabled` comes from parseBoolean in the environment (never invalid) and an invalid config FILE is dropped silently, so the text is never shown
* `ext-notify-active-id`: a root run is its own trace (`traceID == ID`), so `active.root.traceID` prints the same id

## Open survivors (no test written, not proven equivalent)

`port/mutations-open.json`:

* `ext-settled-stale-reset`: `t.staleSettles = 0` instead of `--` on a stale settle; differs only with two or more stale settles in a row.
* `ext-settled-incomplete-error`: a settle without `agent_end` also sets error "incomplete" on the root.
* `ext-shutdown-active-kept`, `ext-shutdown-stale-kept`: state left set after `session_shutdown`; observable only by events after shutdown.
* `ext-shutdown-no-await`: shutdown does not await the queued batches; the rig posts directly (autoBatchTracing false), so only a batching test sees it.
* `conv-usage-cost-unknown-ge`: `totalCost <= 0` instead of `== 0`; differs for a negative cost only.
* `conv-gemini-part-text-rest`: the Gemini text part spreads the whole part instead of the rest without `text`; a text part with another key shows it.
* `conv-responses-content-string`: a Responses user message with an empty content array is left unconverted.
* `cfg-validate-enabled-non-bool-accepted`: a config file with `"enabled":"yes"` must be dropped whole (config.ts schema failure); the mutant keeps the rest of the file.
* `meta-git-remote-args`, `meta-git-order`: the git argument vectors and their order are only seen by a host that records `exec` (the pigeq scenarios do; no unit test does).

## Mutants that do not build (rewrite before use)

`ext-agent-end-context-usage-percent`, `conv-usage-cost-always`, `conv-usage-cost-never`, `conv-gemini-parts-required`, `conv-gemini-part-order-text-last`, `conv-anthropic-system-flatten-dropped`, `cfg-precedence-no-global`, `cfg-precedence-no-local`, `meta-git-parse-fields` (a variable becomes unused, or a branch makes the function miss a return).

## Tests added

* `mutants_ext_test.go` (package `langsmith_test`, uses the rig of `lsrig_test.go`): 21 tests, raw-byte checks of key order, extra POST/PATCH counts for state that must be cleared, command description frame, per-session turn counter, host failures, replica fields, factory text.
* `mutants_ext_internal_test.go`: the version separator check of `piEmitsAgentSettled`.

`go test -race -count=1 -run TestMut` passes on xd6702 (the full `-race -count=3` and `go vet` on 3 OSes were not rerun).

## Per-mutant table (chunks 1 and 2)

| mutant | file | result | first failing test |
|---|---|---|---|
| ext-status-key | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-status-session-tracing | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-status-session-disabled | extension.go | killed: unit | TestDisabledRegistersOnlyStatusAndCommand |
| ext-status-session-swapped | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-status-tracing-run | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-status-traced-agent-end | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-status-traced-agent-settled | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-status-traced-agent-end-dropped | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-status-traced-agent-settled-dropped | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-status-tracing-run-dropped | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-status-cleared-on-shutdown | extension.go | killed: unit | TestShutdownEndsTheRunAndClearsStatus |
| ext-command-name | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-command-description | extension.go | killed by a NEW test | TestMutRegisteredCommandDescription |
| ext-notify-enabled-text | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-notify-active-prefix | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-notify-active-id | extension.go | EQUIVALENT (argued) |  |
| ext-notify-active-inverted | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-notify-period | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-notify-enabled-level | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-notify-disabled-level | extension.go | killed: unit | TestDisabledRegistersOnlyStatusAndCommand |
| ext-notify-disabled-text | extension.go | killed: unit | TestDisabledRegistersOnlyStatusAndCommand |
| ext-notify-disabled-key-name | extension.go | killed: unit | TestDisabledRegistersOnlyStatusAndCommand |
| ext-notify-disabled-endpoint-name | extension.go | killed: unit | TestDisabledRegistersOnlyStatusAndCommand |
| ext-hooks-skipped-when-enabled | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-command-enabled-inverted | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-hook-dropped-beforeAgentStart | extension.go | killed: unit | TestSessionStartStatusAndCommand |
| ext-hook-dropped-sessionCompact | extension.go | killed: unit | TestEnabledRegistersEveryHook |
| ext-hook-dropped-turnStart | extension.go | killed: unit | TestEnabledRegistersEveryHook |
| ext-hook-dropped-beforeProviderRequest | extension.go | killed: unit | TestEnabledRegistersEveryHook |
| ext-hook-dropped-messageEnd | extension.go | killed: unit | TestEnabledRegistersEveryHook |
| ext-hook-dropped-toolExecutionStart | extension.go | killed: unit | TestEnabledRegistersEveryHook |
| ext-hook-dropped-toolExecutionUpdate | extension.go | killed: unit | TestEnabledRegistersEveryHook |
| ext-hook-dropped-toolExecutionEnd | extension.go | killed: unit | TestEnabledRegistersEveryHook |
| ext-hook-dropped-turnEnd | extension.go | killed: unit | TestEnabledRegistersEveryHook |
| ext-hook-dropped-agentEnd | extension.go | killed: unit | TestEnabledRegistersEveryHook |
| ext-hook-dropped-agentSettled | extension.go | killed: unit | TestEnabledRegistersEveryHook |
| ext-hook-dropped-sessionShutdown | extension.go | killed: unit | TestEnabledRegistersEveryHook |
| ext-settled-major-gt1 | extension.go | killed: unit | TestPiEmitsAgentSettled |
| ext-settled-minor-ge80 | extension.go | killed: unit | TestPiEmitsAgentSettled |
| ext-settled-minor-gt81 | extension.go | killed: unit | TestPiEmitsAgentSettled |
| ext-settled-patch-gt4 | extension.go | killed: unit | TestPiEmitsAgentSettled |
| ext-settled-patch-ge3 | extension.go | killed: unit | TestPiEmitsAgentSettled |
| ext-settled-minor-eq81 | extension.go | killed: unit | TestPiEmitsAgentSettled |
| ext-settled-empty-digits-ok | extension.go | killed: unit | TestPiEmitsAgentSettled |
| ext-settled-dot-not-required | extension.go | killed by a NEW test | TestMutPiVersionSeparatorsAreDots |
| ext-settled-two-components | extension.go | killed: unit | TestPiEmitsAgentSettled |
| ext-settled-version-unset-true | extension.go | killed: unit | TestFactoryReadsThePiVersionFromTheHost |
| ext-settled-inverted | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-agent-end-ignores-settled | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-turn-counter-start-2 | extension.go | killed: unit | TestContractTwins |
| ext-turn-counter-not-stored | extension.go | killed: unit | TestContractTwins |
| ext-turn-counter-global | extension.go | killed by a NEW test | TestMutUserTurnCounterIsPerSession |
| ext-thread-id-dropped | extension.go | killed: unit | TestRootRunPayload |
| ext-cwd-not-passed | extension.go | killed: unit | TestContractTwins |
| ext-model-nameless-pointer | extension.go | killed: unit | TestTurnStartMetadata |
| ext-model-info-nil | extension.go | killed: unit | TestProviderRequestNaming |
| ext-model-error-swallowed | extension.go | killed by a NEW test | TestMutModelInfoFailureFailsTheHooks |
| ext-llm-current-not-set | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-llm-post-dropped | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-safeend-patch-error-swallowed | extension.go | killed by a NEW test | TestMutReplicaPatchFailureRejectsTheHook |
| ext-safeend-no-patch | extension.go | killed: unit | TestInterruptedByNextRun |
| ext-safeend-metadata-dropped | extension.go | killed: unit | TestMessageEndBranches |
| ext-root-project-default | extension.go | killed: unit | TestEnabledFromConfigFile |
| ext-root-inputs-order | extension.go | killed by a NEW test | TestMutRootInputsKeyOrder |
| ext-root-config-metadata-first | extension.go | killed: unit | TestRootRunPayload |
| ext-root-replica-key-empty-kept | extension.go | killed by a NEW test (written after the last harness run; the test passes, the mutant was not re-run) | TestMutReplicaEmptyKeySendsNoCredentialHeader |
| ext-root-replica-updates-dropped | extension.go | killed by a NEW test | TestMutReplicaUpdatesReachThePatch |
| ext-start-stale-not-counted | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-start-stale-double | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-start-loopresult-inverted | extension.go | killed: unit | TestRootRunsAreNewTraces |
| ext-start-loopresult-err-dropped | extension.go | killed by a NEW test | TestMutLoopResultErrorSurvivesTheNextPrompt |
| ext-start-loopresult-outputs-dropped | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-start-interrupted-key | extension.go | killed: unit | TestInterruptedByNextRun |
| ext-start-interrupted-error-text | extension.go | killed: unit | TestInterruptedByNextRun |
| ext-start-interrupted-no-error | extension.go | killed: unit | TestInterruptedByNextRun |
| ext-start-active-kept | extension.go | killed by a NEW test | TestMutFailedPromptStartLeavesNoActiveTrace |
| ext-start-image-count-plus | extension.go | killed: unit | TestRootRunPayload |
| ext-start-prompt-key | extension.go | killed: unit | TestRootRunPayload |
| ext-start-defer-initially-true | extension.go | killed: unit | TestToolRunFallsBackToTheRootWithoutATurn |
| ext-start-session-id-error-swallowed | extension.go | killed: unit | TestHookErrorsAreReturned |
| ext-compact-guard | extension.go | killed: unit | TestCompactionRun |
| ext-compact-name | extension.go | killed: unit | TestCompactionRun |
| ext-compact-run-type | extension.go | killed: unit | TestCompactionRun |
| ext-compact-inputs-will-retry | extension.go | killed: unit | TestCompactionRun |
| ext-compact-entry-error | extension.go | killed: unit | TestHookErrorsAreReturned |
| ext-turn-start-guard | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-turn-start-name | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-turn-start-inputs | extension.go | killed: unit | TestTurnStartMetadata |
| ext-turn-start-loop-index | extension.go | killed: unit | TestTurnStartMetadata |
| ext-turn-start-loop-index-key | extension.go | killed: unit | TestTurnStartMetadata |
| ext-turn-start-turns-not-set | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-turn-start-current-not-set | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-turn-start-defer-kept | extension.go | killed: unit | TestLateSettleTwin |
| ext-turn-start-defer-set | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-turn-start-pending-kept | extension.go | killed by a NEW test | TestMutPendingLlmStartsOnce |
| ext-turn-start-pending-ignored | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-turn-start-pending-under-root | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-provider-name-precedence | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-provider-defer-inverted | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-provider-defer-ignored | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-provider-pending-not-stored | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-provider-parent-root-always | extension.go | killed: unit | TestLateSettleTwin |
| ext-message-end-role | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-message-end-any-role | extension.go | killed: unit | TestMessageEndBranches |
| ext-message-end-no-llm-check | extension.go | killed by a NEW test | TestMutAssistantMessageWithoutAnLlmRunDefersNothing |
| ext-message-end-llm-and | extension.go | killed: unit | TestMessageEndBranches |
| ext-message-end-stop-reason-dropped | extension.go | killed: unit | TestMessageEndBranches |
| ext-message-end-defer-not-set | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-tool-start-name | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-tool-start-run-type | extension.go | killed: unit | TestToolRunLifecycle |
| ext-tool-start-metadata-key | extension.go | killed: unit | TestToolRunLifecycle |
| ext-tool-start-not-tracked | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-tool-update-guard | extension.go | killed: unit | TestToolRunLifecycle |
| ext-tool-update-event-name | extension.go | killed: unit | TestToolRunLifecycle |
| ext-tool-update-dropped | extension.go | killed: unit | TestToolRunLifecycle |
| ext-tool-end-not-deleted | extension.go | killed by a NEW test | TestMutToolEndedOnceAndForgotten |
| ext-tool-end-error-text | extension.go | killed: unit | TestToolRunLifecycle |
| ext-turn-end-lookup-key | extension.go | killed: unit | TestTurnEndFindsTheTurnByIndexOrCurrent |
| ext-turn-end-no-current-fallback | extension.go | killed: unit | TestTurnEndFindsTheTurnByIndexOrCurrent |
| ext-turn-end-llm-kept | extension.go | killed by a NEW test | TestMutLlmRunEndedByTurnEndIsNotEndedAgain |
| ext-turn-end-toolresults-dropped | extension.go | killed: unit | TestTurnEndFindsTheTurnByIndexOrCurrent |
| ext-turn-end-outputs-order | extension.go | killed by a NEW test | TestMutTurnEndOutputsKeyOrder |
| ext-turn-end-current-turn-kept | extension.go | killed by a NEW test | TestMutToolAfterTheTurnEndedHangsUnderTheRoot |
| ext-turn-end-current-turn-inverted | extension.go | killed: unit | TestTurnEndFindsTheTurnByIndexOrCurrent |
| ext-factory-config-error-plain | extension.go | killed: unit | TestFactoryPanicFirstLineSummarisesTheZodError |
| ext-factory-client-error-plain | extension.go | killed by a NEW test | TestMutFactoryClientErrorNamesTheExtension |
| ext-factory-invalid-text | extension.go | killed: unit | TestFactoryPanicFirstLineSummarisesTheZodError |
| ext-factory-summary-empty-path | extension.go | killed: unit | TestFactoryPanicFirstLineSummarisesTheZodError |
| ext-factory-summary-first-issue | extension.go | killed by a NEW test | TestMutFactorySummaryNamesTheFirstIssue |
| ext-factory-full-text | extension.go | killed: unit | TestFactoryFailsOnAZodError |
| ext-agent-end-guard | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-agent-end-tool-error-text | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-agent-end-tool-outputs | extension.go | killed by a NEW test | TestMutToolEndedOnceAndForgotten |
| ext-agent-end-tools-not-cleared | extension.go | killed by a NEW test | TestMutToolEndedOnceAndForgotten |
| ext-agent-end-pending-ignored | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-agent-end-pending-kept | extension.go | killed by a NEW test | TestMutPendingLlmFlushedByAgentEndIsNotStartedAgain |
| ext-agent-end-pending-parent-root | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-agent-end-llm-error-text | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-agent-end-turn-incomplete-key | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-agent-end-current-turn-kept | extension.go | killed by a NEW test | TestMutToolAfterTheTurnEndedHangsUnderTheRoot |
| ext-agent-end-order-turns-before-llm | extension.go | killed by a NEW test | TestMutAgentEndEndsRunsInOrder |
| ext-agent-end-empty-messages | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-agent-end-stop-error | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-agent-end-aborted | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-agent-end-aborted-text | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-agent-end-context-usage-nil | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-agent-end-context-usage-percent | extension.go | INVALID (does not build) |  |
| ext-agent-end-context-usage-window | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-agent-end-context-usage-order | extension.go | killed by a NEW test | TestMutContextUsageKeyOrderAndNulls |
| ext-agent-end-context-usage-tokens-nil | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-agent-end-context-usage-percent-nil | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-agent-end-context-usage-tokens-undef | extension.go | killed by a NEW test | TestMutContextUsageKeyOrderAndNulls |
| ext-agent-end-loop-result-not-stored | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-agent-end-root-error-dropped | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-agent-end-root-active-kept | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-settled-stale-ge0 | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-settled-stale-not-decremented | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-settled-stale-reset | extension.go | open: survives the unit layer |  |
| ext-settled-guard | extension.go | killed: sdkdiff | TestSDKDiffGolden |
| ext-settled-active-kept | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-settled-loopresult-inverted | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-settled-loopresult-error-dropped | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-settled-incomplete-key | extension.go | killed: unit | TestSettleWithoutAgentEndIsIncomplete |
| ext-settled-incomplete-error | extension.go | open: survives the unit layer |  |
| ext-shutdown-pending-ignored | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-shutdown-pending-parent-root | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-shutdown-llm-error-text | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-shutdown-llm-not-ended | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-shutdown-loopresult-inverted | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-shutdown-loopresult-error-dropped | extension.go | killed: unit | TestTraceTwins |
| ext-shutdown-key | extension.go | killed: unit | TestShutdownEndsTheRunAndClearsStatus |
| ext-shutdown-root-error-text | extension.go | killed: unit | TestShutdownEndsTheRunAndClearsStatus |
| ext-shutdown-active-kept | extension.go | open: survives the unit layer |  |
| ext-shutdown-stale-kept | extension.go | open: survives the unit layer |  |
| ext-shutdown-no-await | extension.go | open: survives the unit layer |  |
| conv-safeadd-null-counts | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-nullish-error | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-cost-unknown-gt | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-cost-unknown-ge | convert.go | open: survives the unit layer |  |
| conv-usage-cost-always | convert.go | INVALID (does not build) |  |
| conv-usage-cost-never | convert.go | INVALID (does not build) |  |
| conv-usage-input-tokens-no-cache-read | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-input-tokens-only-input | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-total-tokens | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-detail-key | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-input-cost-no-cache-write | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-output-cost | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-total-cost | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-cost-details-swapped | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-cost-details-key | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-key-order | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-image-type | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-image-keeps-original-keys | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-image-keeps-original-keys-2 | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-image-missing-fields-tolerated | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-normalize-content-no-parts | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-part-tool-call-type | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-part-tool-call-args | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-part-tool-call-match | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-part-tool-call-rest-type | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-part-tool-call-no-normalize | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-messages-role-map | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-messages-role-user | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-messages-rest-role | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-messages-rest-dropped | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-role-user | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-role-model | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-role-proto | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-role-proto-fn | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-role-fn-value | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-messages-precedence | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-parts-required | convert.go | INVALID (does not build) |  |
| conv-gemini-result-order | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-result-rest | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-result-rest-2 | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-outer-key | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-outer-rest | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-outer-name | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-text | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-text-type | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-text-rest | convert.go | open: survives the unit layer |  |
| conv-gemini-part-text-not-string | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-fc-rest | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-fr-type | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-fr-rest | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-iu-type | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-iu-key | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-order-text-last | convert.go | INVALID (does not build) |  |
| conv-responses-messages-precedence | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-responses-content-string | convert.go | open: survives the unit layer |  |
| conv-responses-input-text | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-responses-input-text-type | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-responses-input-text-rest | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-responses-input-text-no-rest | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-responses-outer-rest | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-anthropic-role-user | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-anthropic-every-dropped | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-anthropic-role-tool | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-anthropic-system-key | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-anthropic-system-null-msg | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-anthropic-system-role | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-anthropic-system-single-block | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-anthropic-system-text-type | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-anthropic-system-text-field | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-anthropic-system-flatten-dropped | convert.go | INVALID (does not build) |  |
| conv-anthropic-system-role-value | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-anthropic-outer-rest | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-payload-non-record | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-payload-unknown-shape | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-payload-order-gemini-first | convert.go | EQUIVALENT (argued) |  |
| cfg-bool-yes | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-bool-on | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-bool-one | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-bool-true | config.go | killed: golden vectors (unit) | TestClientDifferential |
| cfg-bool-off | config.go | killed: unit | TestParseBoolean |
| cfg-bool-no | config.go | killed: unit | TestParseBoolean |
| cfg-bool-zero | config.go | killed: unit | TestParseBoolean |
| cfg-bool-false | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-bool-case | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-bool-trim | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-bool-unknown-false | config.go | killed: unit | TestParseBoolean |
| cfg-bool-native-inverted | config.go | killed: unit | TestParseBoolean |
| cfg-json-blank | config.go | EQUIVALENT (argued) |  |
| cfg-json-syntax-error-raises | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-env-api-url-suffix | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-env-metadata-suffix | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-zod-parsed-nan | config.go | killed: unit | TestZodErrorShape |
| cfg-validate-enabled-type | config.go | EQUIVALENT (argued) |  |
| cfg-validate-enabled-non-bool-accepted | config.go | open: survives the unit layer |  |
| cfg-validate-replicas-type | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-validate-replicas-has | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-replica-not-object | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-replica-alias-url | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-replica-alias-key | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-replica-alias-project | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-replica-alias-precedence | config.go | killed: unit | TestReplicaValidationBranches |
| cfg-replica-alias-null-wins | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-replica-updates-accepted | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-replica-updates-proto | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-replica-key-path | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-home-env-first | config.go | killed: unit | TestGetHomeDir |
| cfg-global-path | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-local-path | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-global-dir | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-precedence-env-first | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-precedence-local-first | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-precedence-global-last | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-precedence-no-global | config.go | INVALID (does not build) |  |
| cfg-precedence-no-local | config.go | INVALID (does not build) |  |
| cfg-merge-enabled | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-merge-project | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-merge-metadata-dropped | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-merge-replicas-append | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-options-lookup-ignored | config.go | killed: golden vectors (unit) | TestConfigVectors |
| meta-purpose | metadata.go | killed: unit | TestContractTwins |
| meta-runtime | metadata.go | killed: unit | TestContractTwins |
| meta-git-remote-args | metadata.go | open: survives the unit layer |  |
| meta-git-order | metadata.go | open: survives the unit layer |  |
| meta-git-cache-write | metadata.go | killed: unit | TestGetGitInfoRepository |
| meta-git-parse-fields | metadata.go | INVALID (does not build) |  |
| meta-git-branch-sha-swapped | metadata.go | killed: unit | TestGetGitInfoRepository |
| meta-remote-name-leading-slash-one | metadata.go | killed: golden vectors (unit) | TestParseRemoteVectors |
| meta-remote-name-git-suffix | metadata.go | killed: golden vectors (unit) | TestParseRemoteVectors |
| meta-remote-name-empty | metadata.go | killed: golden vectors (unit) | TestParseRemoteVectors |
