# Mutation proof of the LangSmith port

**Result: 605 mutants run, 585 killed, 20 equivalent (argued below), 0 survivors, 0 invalid.** 86 of the kills are by tests added for this proof.

`port/mutations.json` is the 585 killed mutants (each `find` occurs exactly once in the tree); `port/mutations-equivalent.json` is the 20 equivalent ones. Judging layer: the port's own Go tests (`pigeq mutate --unit-only`), see the finding below.

| outcome | count |
|---|---|
| killed by an existing golden-vector test (caller, client, runtree, profile, wire, convert, config vectors from the real JS) | 274 |
| killed by an existing unit or twin test | 217 |
| killed by an existing test that hangs on the mutant (`go test` timeout) | 5 |
| killed first by `TestSDKDiffGolden` (JS-driven wire logs, lsdiff) | 3 |
| killed by a test added here (`mutants_*_test.go`) | 86 |
| equivalent (argued) | 20 |

By file: caller.go 27, client.go 48, client_ingest.go 42, client_prepare.go 17, client_queue.go 38, config.go 44, convert.go 75, extension.go 175, metadata.go 25, runtree.go 73, sampling.go 3, timefmt.go 6, uuid.go 9, xxh3.go 23.

1212 mutants were written (the files above, the wire helpers `xxh3.go uuid.go sampling.go timefmt.go`, `extension.go`, `convert.go`, `config.go`, `metadata.go`, `runtree.go`, `caller.go`, `client*.go`); 605 were selected by branch family and all of them were run; the other 607 are variants of the same branches (a second string, a second operand) that were not run. Nothing survived that was not turned into a test or argued equivalent below.

## Finding: the scenario layer of `pigeq mutate` cannot judge this port

`pigeq check` fails on the UNMUTATED port: scenario `command-tracing-enabled`, "pig-go differs from pi-ts: first difference at event 14" (the multipart bodies differ by divergence D1 and key canonicalisation, which only `lsdiff` normalises, not pigeq's byte compare). `pigeq mutate` does not check the scenario baseline, so with `--unit` every mutant that survives the unit tests is reported `KILLED command-tracing-enabled`. I judged by the unit layer alone (`--unit-only`; the first two chunks ran with `--unit` and their scenario-only rows were re-run through the unit layer). The wire scenarios are compared by `port/tools/run-lane.sh` (lsdiff); the wire-only killers used here are `TestSDKDiffGolden` and the differential tests.

## Setup finding: the unit baseline needs `../../port` next to the module copy

The harness copies only the module directory to `$TMPDIR/eq-mutantNNN/langsmith`. `TestSDKDiffGolden` (`../../port/sdkdiff`, `../../port/tools`) and `TestLateSettleTwin` (`../../port/oracle/test/recordings`) read `../../port/...` and fail in the copy ("the unmutated port's own unit tests fail"). Run with `TMPDIR` pointing at a directory holding symlinks `port/{sdkdiff,tools,oracle}` to this Package's `port/`.

## Commands

On xd6702, inside `~/ls-work/in-ci.sh` (tern-ci:v7, `LS_CPUS=2`), from the repository root:

```sh
S=$HOME/ls-work/scratch/<lane>; mkdir -p $S/tmp/port
for d in sdkdiff tools oracle; do ln -sfn $PWD/components/langsmith/port/$d $S/tmp/port/$d; done
export TMPDIR=$S/tmp
pigeq mutate --scenarios components/langsmith/port/scenarios --golden components/langsmith/port/golden \
  --go components/langsmith/extensions/langsmith --pig $PIGEQ_PIG \
  --mutations components/langsmith/port/mutations.json --unit-only --sdk-dir $PIG_SDK_DIR --jobs 5
```

Every mutant runs the whole suite (about 110 s, of which `TestSDKDiffGolden` is ~86 s): 585 mutants take about 3.5 hours at `--jobs 5` on a quiet host. Expected result: every mutant killed. The same command on `mutations-equivalent.json` must report them not killed.

## Production findings

* `TestSDKDiffGolden/replicas-projects` was flaky (17 failures in 30 runs) because the two destination batches are sent from separate goroutines; the lead made that script `unorderedDrain: true`. No mutant revealed a production defect.

## Tests added

* `mutants_ext_test.go` (rig tests, raw request bytes: key order, extra POST/PATCH counts for state that must be cleared, command description frame, per-session turn counter, host failures, replica fields, factory text).
* `mutants_state_test.go` (stale settles, shutdown bookkeeping and the wait for queued batches, negative cost, empty Responses content, config file schema).
* `mutants_ext_internal_test.go` (version separators), `mutants_git_internal_test.go` (the three git invocations, in order, through a recording `git` on PATH).
* `mutants_client_internal_test.go` (pinned SDK defaults, headers, debug and background environment, profile precedence, fetch error texts, direct event filter, blocking root update, multi-replica reroot, Gemini part order, remote provider order).
* `mutants_client2_internal_test.go` (batch thresholds and timers, destination routing for multipart and batch, export errors after close, gzip, queue and caller boundaries, grouping by workspace, server-info caching).

## Equivalent mutants (argued)

* `ext-notify-active-id`: a root run is its own trace (`traceID == ID`), so `active.root.traceID` prints the same id.
* `conv-payload-order-gemini-first`: the mutation inserts a dead `if false { return nil, nil }`; no behaviour changes.
* `cfg-json-blank`: `value == ""` instead of `jsTrim(value) == ""`: a whitespace-only value reaches the JSON parser, which rejects it, and a parse failure is `undefined` as well (config.ts:65-73).
* `cfg-validate-enabled-type`: only the zod message text of an invalid `enabled` differs; `enabled` comes from parseBoolean in the environment (never invalid) and an invalid config FILE is dropped silently, so the text is never shown.
* `conv-gemini-part-text-rest`: `spread(rec)` re-sets `text` (already present, same value, same position) and then the other keys in the same order as `restOf(rec, "text")`: identical bytes.
* `meta-remote-path-empty`: an empty repoPath gives an empty name, which the next check (`name == ""`) rejects the same way.
* `cl-headers-api-key`: mergedHeaders names the credential header `Authorization`, but fetch() rewrites it (applyCurrentAuthHeaders deletes Authorization and sets x-api-key when the client has a key): the request is the same.
* `cl-fetch-api-key-header`: applyCurrentAuthHeaders with an `Authorization` credential and an existing x-api-key sets nothing: the request is the same.
* `clp-sample-rate-one`: a rate of exactly 1 samples everything on both sides (isSampledByID returns true for rate >= 1, and `bucket/1e6 < 1` always holds).
* `wire-sampling-rate-one`: a rate of exactly 1: `bucket/1e6 < 1` is always true, so `>= 1` and `> 1` agree.
* `clq-enqueue-workers`: bounds only the CPU parallelism of body assembly; the sends run on goSend goroutines bounded by the batch caller, so the wire and the concurrency of requests do not change.
* `clq-timer-fired-clear`: a stale `c.timer` pointing at a fired timer is harmless: clearTimerLocked stops it (no-op) and its map entry was deleted.
* `clq-timer-fired-closed`: close() stops every timer; a timer that races close can only drain into enqueueJobsLocked, which refuses on a closed client.
* `clq-flush-drain`: a tracked drain increments pendingDrains and jobDone decrements it symmetrically; flush waits for its own drain either way.
* `clq-await-delay`: the 1 ms wait for backgrounded promises versus a zero timer is scheduling only; no state differs.
* `clq-await-idle`: every item resolves in jobDone after its send returned, so the batch caller is idle whenever the wait before it ends.
* `cli-cursor-chunk`: the size of the internal gzip Write calls changes allocations, not the stream the server decompresses (TestLargeBodyStreamsAndGzipsIntact compares it).
* `wire-uuid7-rand-tail`: which five of the sixteen random bytes fill b[11:] is random either way.
* `cli-export-error-returned`: sendMultipartRequest returns only nil or a 404 (handled just above), so `return err` there is already nil.
* `cli-process-nothing-to-send`: buildBatchBody returns nil only for an empty batch, which processBatch rejects before.

## Per-mutant table

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
| ext-command-description | extension.go | killed: new test | TestMutRegisteredCommandDescription |
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
| ext-settled-dot-not-required | extension.go | killed: new test | TestMutPiVersionSeparatorsAreDots |
| ext-settled-two-components | extension.go | killed: unit | TestPiEmitsAgentSettled |
| ext-settled-version-unset-true | extension.go | killed: unit | TestFactoryReadsThePiVersionFromTheHost |
| ext-settled-inverted | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-agent-end-ignores-settled | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-turn-counter-start-2 | extension.go | killed: unit | TestContractTwins |
| ext-turn-counter-not-stored | extension.go | killed: unit | TestContractTwins |
| ext-turn-counter-global | extension.go | killed: new test | TestMutUserTurnCounterIsPerSession |
| ext-thread-id-dropped | extension.go | killed: unit | TestRootRunPayload |
| ext-cwd-not-passed | extension.go | killed: unit | TestContractTwins |
| ext-model-nameless-pointer | extension.go | killed: unit | TestTurnStartMetadata |
| ext-model-info-nil | extension.go | killed: unit | TestProviderRequestNaming |
| ext-model-error-swallowed | extension.go | killed: new test | TestMutModelInfoFailureFailsTheHooks |
| ext-llm-current-not-set | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-llm-post-dropped | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-safeend-patch-error-swallowed | extension.go | killed: new test | TestMutReplicaPatchFailureRejectsTheHook |
| ext-safeend-no-patch | extension.go | killed: unit | TestInterruptedByNextRun |
| ext-safeend-metadata-dropped | extension.go | killed: unit | TestMessageEndBranches |
| ext-root-project-default | extension.go | killed: unit | TestEnabledFromConfigFile |
| ext-root-inputs-order | extension.go | killed: new test | TestMutRootInputsKeyOrder |
| ext-root-config-metadata-first | extension.go | killed: unit | TestRootRunPayload |
| ext-root-replica-key-empty-kept | extension.go | killed: new test | TestMutReplicaEmptyKeySendsNoCredentialHeader |
| ext-root-replica-updates-dropped | extension.go | killed: new test | TestMutReplicaUpdatesReachThePatch |
| ext-start-stale-not-counted | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-start-stale-double | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-start-loopresult-inverted | extension.go | killed: unit | TestRootRunsAreNewTraces |
| ext-start-loopresult-err-dropped | extension.go | killed: new test | TestMutLoopResultErrorSurvivesTheNextPrompt |
| ext-start-loopresult-outputs-dropped | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-start-interrupted-key | extension.go | killed: unit | TestInterruptedByNextRun |
| ext-start-interrupted-error-text | extension.go | killed: unit | TestInterruptedByNextRun |
| ext-start-interrupted-no-error | extension.go | killed: unit | TestInterruptedByNextRun |
| ext-start-active-kept | extension.go | killed: new test | TestMutFailedPromptStartLeavesNoActiveTrace |
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
| ext-turn-start-pending-kept | extension.go | killed: new test | TestMutPendingLlmStartsOnce |
| ext-turn-start-pending-ignored | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-turn-start-pending-under-root | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-provider-name-precedence | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-provider-defer-inverted | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-provider-defer-ignored | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-provider-pending-not-stored | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-provider-parent-root-always | extension.go | killed: unit | TestLateSettleTwin |
| ext-message-end-role | extension.go | killed: unit | TestDeferredLlmStartsInTheNextTurn |
| ext-message-end-any-role | extension.go | killed: unit | TestMessageEndBranches |
| ext-message-end-no-llm-check | extension.go | killed: new test | TestMutAssistantMessageWithoutAnLlmRunDefersNothing |
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
| ext-tool-end-not-deleted | extension.go | killed: new test | TestMutToolEndedOnceAndForgotten |
| ext-tool-end-error-text | extension.go | killed: unit | TestToolRunLifecycle |
| ext-turn-end-lookup-key | extension.go | killed: unit | TestTurnEndFindsTheTurnByIndexOrCurrent |
| ext-turn-end-no-current-fallback | extension.go | killed: unit | TestTurnEndFindsTheTurnByIndexOrCurrent |
| ext-turn-end-llm-kept | extension.go | killed: new test | TestMutLlmRunEndedByTurnEndIsNotEndedAgain |
| ext-turn-end-toolresults-dropped | extension.go | killed: unit | TestTurnEndFindsTheTurnByIndexOrCurrent |
| ext-turn-end-outputs-order | extension.go | killed: new test | TestMutTurnEndOutputsKeyOrder |
| ext-turn-end-current-turn-kept | extension.go | killed: new test | TestMutToolAfterTheTurnEndedHangsUnderTheRoot |
| ext-turn-end-current-turn-inverted | extension.go | killed: unit | TestTurnEndFindsTheTurnByIndexOrCurrent |
| ext-factory-config-error-plain | extension.go | killed: unit | TestFactoryPanicFirstLineSummarisesTheZodError |
| ext-factory-client-error-plain | extension.go | killed: new test | TestMutFactoryClientErrorNamesTheExtension |
| ext-factory-invalid-text | extension.go | killed: unit | TestFactoryPanicFirstLineSummarisesTheZodError |
| ext-factory-summary-empty-path | extension.go | killed: unit | TestFactoryPanicFirstLineSummarisesTheZodError |
| ext-factory-summary-first-issue | extension.go | killed: new test | TestMutFactorySummaryNamesTheFirstIssue |
| ext-factory-full-text | extension.go | killed: unit | TestFactoryFailsOnAZodError |
| ext-agent-end-guard | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-agent-end-tool-error-text | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-agent-end-tool-outputs | extension.go | killed: new test | TestMutToolEndedOnceAndForgotten |
| ext-agent-end-tools-not-cleared | extension.go | killed: new test | TestMutToolEndedOnceAndForgotten |
| ext-agent-end-pending-ignored | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-agent-end-pending-kept | extension.go | killed: new test | TestMutPendingLlmFlushedByAgentEndIsNotStartedAgain |
| ext-agent-end-pending-parent-root | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-agent-end-llm-error-text | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-agent-end-turn-incomplete-key | extension.go | killed: unit | TestOutstandingToolsAndLlmEndWithAgentEnd |
| ext-agent-end-current-turn-kept | extension.go | killed: new test | TestMutToolAfterTheTurnEndedHangsUnderTheRoot |
| ext-agent-end-order-turns-before-llm | extension.go | killed: new test | TestMutAgentEndEndsRunsInOrder |
| ext-agent-end-empty-messages | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-agent-end-stop-error | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-agent-end-aborted | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-agent-end-aborted-text | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-agent-end-context-usage-nil | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-agent-end-context-usage-window | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-agent-end-context-usage-order | extension.go | killed: new test | TestMutContextUsageKeyOrderAndNulls |
| ext-agent-end-context-usage-tokens-nil | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-agent-end-context-usage-percent-nil | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-agent-end-context-usage-tokens-undef | extension.go | killed: new test | TestMutContextUsageKeyOrderAndNulls |
| ext-agent-end-loop-result-not-stored | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-agent-end-root-error-dropped | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-agent-end-root-active-kept | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| ext-settled-stale-ge0 | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-settled-stale-not-decremented | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-settled-stale-reset | extension.go | killed: new test | TestMutTwoStaleSettlesAreBothSkipped |
| ext-settled-guard | extension.go | killed: sdkdiff | TestSDKDiffGolden |
| ext-settled-active-kept | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-settled-loopresult-inverted | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-settled-loopresult-error-dropped | extension.go | killed: unit | TestStaleSettleIsSkippedAndNextSettleEndsTheRun |
| ext-settled-incomplete-key | extension.go | killed: unit | TestSettleWithoutAgentEndIsIncomplete |
| ext-settled-incomplete-error | extension.go | killed: new test | TestMutSettleWithoutAgentEndHasNoError |
| ext-shutdown-pending-ignored | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-shutdown-pending-parent-root | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-shutdown-llm-error-text | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-shutdown-llm-not-ended | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-shutdown-loopresult-inverted | extension.go | killed: unit | TestPendingLlmIsStartedByAgentEndAndShutdown |
| ext-shutdown-loopresult-error-dropped | extension.go | killed: unit | TestTraceTwins |
| ext-shutdown-key | extension.go | killed: unit | TestShutdownEndsTheRunAndClearsStatus |
| ext-shutdown-root-error-text | extension.go | killed: unit | TestShutdownEndsTheRunAndClearsStatus |
| ext-shutdown-active-kept | extension.go | killed: new test | TestMutShutdownForgetsTheRun |
| ext-shutdown-stale-kept | extension.go | killed: new test | TestMutShutdownForgetsStaleSettles |
| ext-shutdown-no-await | extension.go | killed: new test | TestMutShutdownAwaitsTheBatches |
| conv-safeadd-null-counts | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-nullish-error | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-cost-unknown-gt | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-cost-unknown-ge | convert.go | killed: new test | TestMutNegativeCostIsKept |
| conv-usage-cost-always | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-usage-cost-never | convert.go | killed: golden vectors (unit) | TestConvertVectors |
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
| conv-gemini-parts-required | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-result-order | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-result-rest | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-result-rest-2 | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-outer-key | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-outer-rest | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-outer-name | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-text | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-text-type | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-text-rest | convert.go | EQUIVALENT (argued) |  |
| conv-gemini-part-text-not-string | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-fc-rest | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-fr-type | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-fr-rest | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-iu-type | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-iu-key | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-gemini-part-order-text-last | convert.go | killed: new test | TestMutGeminiTextPartWinsOverFunctionCall |
| conv-responses-messages-precedence | convert.go | killed: golden vectors (unit) | TestConvertVectors |
| conv-responses-content-string | convert.go | killed: new test | TestMutResponsesUserMessageWithEmptyContentIsRebuilt |
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
| conv-anthropic-system-flatten-dropped | convert.go | killed: new test | TestConvertVectors |
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
| cfg-validate-enabled-non-bool-accepted | config.go | killed: new test | TestMutConfigFileWithABadEnabledIsDroppedWhole |
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
| cfg-precedence-no-global | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-precedence-no-local | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-merge-enabled | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-merge-project | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-merge-metadata-dropped | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-merge-replicas-append | config.go | killed: golden vectors (unit) | TestConfigVectors |
| cfg-options-lookup-ignored | config.go | killed: golden vectors (unit) | TestConfigVectors |
| meta-purpose | metadata.go | killed: unit | TestContractTwins |
| meta-runtime | metadata.go | killed: unit | TestContractTwins |
| meta-git-remote-args | metadata.go | killed: new test | TestMutGitInvocationsAndOrder |
| meta-git-order | metadata.go | killed: new test | TestMutGitInvocationsAndOrder |
| meta-git-cache-write | metadata.go | killed: unit | TestGetGitInfoRepository |
| meta-git-parse-fields | metadata.go | killed: unit | TestGetGitInfoRepository |
| meta-git-branch-sha-swapped | metadata.go | killed: unit | TestGetGitInfoRepository |
| meta-remote-name-leading-slash-one | metadata.go | killed: golden vectors (unit) | TestParseRemoteVectors |
| meta-remote-name-git-suffix | metadata.go | killed: golden vectors (unit) | TestParseRemoteVectors |
| meta-remote-name-empty | metadata.go | killed: golden vectors (unit) | TestParseRemoteVectors |
| meta-remote-path-empty | metadata.go | EQUIVALENT (argued) |  |
| meta-remote-gitlab | metadata.go | killed: golden vectors (unit) | TestParseRemoteVectors |
| meta-remote-bitbucket-name | metadata.go | killed: golden vectors (unit) | TestParseRemoteVectors |
| meta-remote-other | metadata.go | killed: golden vectors (unit) | TestParseRemoteVectors |
| meta-remote-order-gitlab-first | metadata.go | killed: new test | TestMutRemoteProviderPrefersGithub |
| meta-remote-https-url | metadata.go | killed: golden vectors (unit) | TestClientDifferential |
| meta-remote-scp-first | metadata.go | killed: golden vectors (unit) | TestParseRemoteVectors |
| meta-scp-at-first | metadata.go | killed: golden vectors (unit) | TestParseRemoteVectors |
| meta-scp-path-empty | metadata.go | killed: unit | TestMatchScp |
| meta-scp-path-split | metadata.go | killed: golden vectors (unit) | TestParseRemoteVectors |
| meta-input-agent-type | metadata.go | killed: unit | TestCodingAgentMetadataKeyOrderAndOptionalKeys |
| meta-input-repo-provider | metadata.go | killed: unit | TestContractTwins |
| meta-input-repo-name | metadata.go | killed: unit | TestContractTwins |
| meta-input-sha | metadata.go | killed: unit | TestContractTwins |
| meta-input-cwd | metadata.go | killed: unit | TestCodingAgentMetadataKeyOrderAndOptionalKeys |
| rt-metadata-order | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-child-project-own | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-child-client-own | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-child-child-exec-order | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-root-exec-order | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-root-child-exec-order | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-trace-id-parent | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-trace-id-root | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-dotted-order-no-parent | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-child-exec-order-next | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-child-meta-inherit | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-child-exec-propagate | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-child-exec-propagate-parent | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-end-outputs-first-wins | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-end-error-first-wins | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-end-time-first-wins | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-end-metadata-empty-merge | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-event-kwargs-nil | runtree.go | killed: new test | TestMutEventWithoutKwargsHasNoKwargsKey |
| rt-event-message-name | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-create-runtime-library | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-create-runtime-env-nil | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-create-parent-id-config | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-create-child-runs-excluded | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-create-end-time | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-create-tags | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-create-key-order | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-create-session | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-create-trace-id | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-create-serialized | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-replica-trace-root-descendants | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-replica-trace-root-merge | runtree.go | killed: new test | TestMutRerootOfTwoReplicasKeepsBoth |
| rt-replica-key-separator | runtree.go | killed: unit | TestUUIDV5 |
| rt-uuid5-version | runtree.go | killed: unit | TestUUIDV5 |
| rt-remap-reroot-distributed | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-remap-reroot-parent | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-remap-reroot-trace-id | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-remap-ancestor-condition | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-remap-primary-session | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-replica-dest-key | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-replica-dest-workspace | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-post-error-text | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-post-replicas-exclude | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-post-replica-runtime-env | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-exclude-env-default | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-exclude-env-one | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-exclude-env-case | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-patch-replica-updates | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-patch-replica-session | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-patch-replica-parent | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-patch-replica-end-time | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-patch-replica-id-from-run | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-patch-error-text | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-patch-children-cleared | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-nonreplica-key-order | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-nonreplica-parent | runtree.go | killed: new test | TestMutUpdateCarriesTheConfiguredParentRunID |
| rt-nonreplica-extra | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-ensure-primaries-error | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-ensure-env-fallback | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-env-conflict-text | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-env-array-item-text | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-env-array-url-text | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-env-array-key-text | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-env-array-project-text | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-env-array-primary-text | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-env-array-trailing-slash | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-env-object-value-text | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-env-fallthrough-text | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-env-parse-error-text | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-typeof-number | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-json-number-leading-zero | runtree.go | killed: unit | TestRtParseJSON |
| rt-json-hex-escape | runtree.go | killed: unit | TestRtParseJSON |
| rt-default-project-langchain | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| rt-default-project-default | runtree.go | killed: golden vectors (unit) | TestRunTreeGolden |
| time-iso-month | timefmt.go | killed: golden vectors (unit) | TestProfileDifferential |
| time-iso-year-negative | timefmt.go | killed: golden vectors (unit) | TestDottedOrderVectors |
| time-iso-range-error | timefmt.go | killed: golden vectors (unit) | TestDottedOrderVectors |
| time-exec-order-pad | timefmt.go | killed: golden vectors (unit) | TestRunTreeGolden |
| time-datestring-slice | timefmt.go | killed: golden vectors (unit) | TestRunTreeGolden |
| time-dotted-strip-dash | timefmt.go | killed: golden vectors (unit) | TestRunTreeGolden |
| cl-batch-size-limit | client.go | killed: new test | TestMutClientDefaultsAreTheSDKs |
| cl-info-timeout | client.go | killed: new test | TestMutClientDefaultsAreTheSDKs |
| cl-request-timeout | client.go | killed: unit | TestNewClientConfig |
| cl-failed-traces-max | client.go | killed: unit | TestNewClientConfig |
| cl-user-agent | client.go | killed: new test | TestMutRequestsCarryTheUserAgentAndKey |
| cl-url-source-text | client.go | killed: unit | TestNewClientDebugPrintsAPIURLSource |
| cl-url-source-profile-text | client.go | killed: new test | TestMutProfileURLSourceAndKeyPrecedence |
| cl-url-source-default-text | client.go | killed: new test | TestMutDebugAndBackgroundEnvironment |
| cl-new-url-quotes | client.go | killed: unit | TestNewClientConfig |
| cl-new-url-option | client.go | killed: unit (test timeout: the mutant hangs) | unit test: panic: test timed out after 3m0s |
| cl-new-profile-auth | client.go | killed: new test | TestMutProfileURLSourceAndKeyPrecedence |
| cl-new-background-false | client.go | killed: new test | TestMutDebugAndBackgroundEnvironment |
| cl-new-background-block | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-new-concurrency-text | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-new-debug-env | client.go | killed: new test | TestMutDebugAndBackgroundEnvironment |
| cl-new-debug-text | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-new-debug-source-option | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-new-debug-option-text | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-new-failed-dir-env | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-new-failed-max-mb | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-new-caller-retries | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-new-batch-caller-retries | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-new-autobatch-default | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-new-autobatch-delay | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-new-batch-bytes-limit | client.go | killed: unit | TestPopBelowOneByteIsReported |
| cl-new-batch-limit | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-new-otel-text | client.go | killed: unit | TestNewClientConfig |
| cl-429-status | client.go | killed: new test | TestMutHandle429 |
| cl-429-header | client.go | killed: new test | TestMutHandle429 |
| cl-headers-api-key | client.go | EQUIVALENT (argued) |  |
| cl-explicit-auth-profile-managed | client.go | killed: sdkdiff | TestSDKDiffGolden |
| cl-fetch-api-key-header | client.go | EQUIVALENT (argued) |  |
| cl-fetch-debug-log | client.go | killed: new test | TestMutFetchLogsWithTheDebugEnvironment |
| cl-fetch-debug-text | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-fetch-error-timeout | client.go | killed: unit | TestDirectTimeoutIsNotRetried |
| cl-fetch-error-failed | client.go | killed: new test | TestMutFetchErrorTexts |
| cl-fetch-parse-url | client.go | killed: new test | TestMutFetchErrorTexts |
| cl-create-start-time | client.go | killed: unit | TestCreateRunDirectDefaultsStartTimeAndSessionName |
| cl-create-batch-action | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-create-op | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-post-dest-url | client.go | killed: unit | TestDirectPostWithDestOptions |
| cl-update-uuid | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-update-inputs | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cl-update-events | client.go | killed: new test | TestMutDirectUpdateFiltersNewTokenEvents |
| cl-update-block-end-time | client.go | killed: new test | TestMutBlockingRootUpdate |
| cl-update-block-root | client.go | killed: new test | TestMutBlockingRootUpdate |
| cl-update-direct-op | client.go | killed: golden vectors (unit) | TestClientDifferential |
| clp-hide-outputs | client_prepare.go | killed: golden vectors (unit) | TestClientDifferential |
| clp-prepare-outputs | client_prepare.go | killed: new test | TestMutPrepareRunInPlaceFiltersEventsAndHidesOutputs |
| clp-prepare-events | client_prepare.go | killed: new test | TestMutPrepareRunInPlaceFiltersEventsAndHidesOutputs |
| clp-mask-nullish | client_prepare.go | killed: unit | TestPrepareRunInPlaceHidesAndKeepsNested |
| clp-merge-omit | client_prepare.go | killed: golden vectors (unit) | TestMergeRuntimeEnvIntoRunVectors |
| clp-merge-runtime-order | client_prepare.go | killed: golden vectors (unit) | TestMergeRuntimeEnvIntoRunVectors |
| clp-merge-metadata-order | client_prepare.go | killed: golden vectors (unit) | TestClientDifferential |
| clp-merge-env-cache | client_prepare.go | killed: new test | TestMutMergeRuntimeEnvReadsTheEnvironmentWithoutACache |
| clp-merge-extra-keys | client_prepare.go | killed: golden vectors (unit) | TestClientDifferential |
| clp-merge-extra-order | client_prepare.go | killed: golden vectors (unit) | TestClientDifferential |
| ext-agent-end-context-usage-percent-wrong | extension.go | killed: unit | TestAgentEndWithoutSettledSupportEndsTheRunAtOnce |
| clp-sample-id-trace | client_prepare.go | killed: unit | TestSamplingIdentifier |
| clp-sample-id-z-rest | client_prepare.go | killed: unit | TestSamplingIdentifier |
| clp-sample-rate-one | client_prepare.go | EQUIVALENT (argued) |  |
| clp-size-string | client_prepare.go | killed: golden vectors (unit) | TestClientDifferential |
| clp-size-object-comma | client_prepare.go | killed: golden vectors (unit) | TestClientDifferential |
| clp-size-number-int | client_prepare.go | killed: golden vectors (unit) | TestClientDifferential |
| clp-size-int-negative | client_prepare.go | killed: golden vectors (unit) | TestEstimateSerializedSizeVectors |
| clq-queue-push-limit | client_queue.go | killed: new test | TestMutQueueAcceptsUpToTheSoftLimit |
| clq-queue-push-empty | client_queue.go | killed: golden vectors (unit) | TestAutoBatchQueueVectors |
| clq-queue-pop-check | client_queue.go | killed: unit | TestPopBelowOneByteIsReported |
| clq-queue-pop-text | client_queue.go | killed: unit | TestPopBelowOneByteIsReported |
| clq-queue-peek | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-drain-split-key | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-drain-key-sep | client_queue.go | killed: new test | TestClientDifferential |
| clq-drain-tracked | client_queue.go | killed: unit (test timeout: the mutant hangs) | unit test: panic: test timed out after 3m0s |
| clq-enqueue-workers | client_queue.go | EQUIVALENT (argued) |  |
| clq-job-done-pending | client_queue.go | killed: unit (test timeout: the mutant hangs) | unit test: panic: test timed out after 3m0s |
| clq-process-drop-warn | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-process-drop-item | client_queue.go | killed: new test | TestMutDroppedItemDoesNotBlockARootUpdate |
| clq-process-merge-runtime | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-limit-bytes-option | client_queue.go | killed: unit | TestPopBelowOneByteIsReported |
| clq-limit-bytes-server | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-limit-count-option | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-limit-count-server | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-after-closed | client_queue.go | killed: new test | TestMutAfterLimitsOnAClosedClientSchedulesNothing |
| clq-after-bytes-limit | client_queue.go | killed: new test | TestMutByteLimitFlushesAtOnce |
| clq-after-count-ge | client_queue.go | killed: new test | TestMutCountLimitIsExclusive |
| clq-after-timer | client_queue.go | killed: unit (test timeout: the mutant hangs) | unit test: panic: test timed out after 3m0s |
| clq-after-timer-delay | client_queue.go | killed: new test | TestMutAggregationTimerUsesTheConfiguredDelay |
| clq-after-timer-store | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-timer-fired-clear | client_queue.go | EQUIVALENT (argued) |  |
| clq-timer-fired-closed | client_queue.go | EQUIVALENT (argued) |  |
| clq-info-flags-key | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-probe-warn-text | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-probe-warn-status | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-probe-store | client_queue.go | killed: new test | TestClientDifferential |
| clq-info-path | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-info-accept | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-info-debug | client_queue.go | killed: golden vectors (unit) | TestClientDifferential |
| clq-flush-drain | client_queue.go | EQUIVALENT (argued) |  |
| clq-await-manual-text | client_queue.go | killed: unit | TestManualFlushMode |
| clq-await-manual | client_queue.go | killed: unit | TestManualFlushMode |
| clq-await-delay | client_queue.go | EQUIVALENT (argued) |  |
| clq-await-pending | client_queue.go | killed: new test | TestClientDifferential |
| clq-await-idle | client_queue.go | EQUIVALENT (argued) |  |
| cli-serialize-undefined | client_ingest.go | killed: new test | TestMutSerializeUndefined |
| cli-serialize-warning | client_ingest.go | killed: unit | TestUnserializableValueIsReplaced |
| cli-process-ctx | client_ingest.go | killed: new test | TestMutCancelledExportIsNotLogged |
| cli-process-size | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-merge-no-id | client_ingest.go | killed: unit | TestMergeCreatesAndUpdates |
| cli-merge-spread | client_ingest.go | killed: new test | TestClientDifferential |
| cli-multipart-boundary | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-multipart-field-keys-order | client_ingest.go | killed: sdkdiff | TestSDKDiffGolden |
| cli-multipart-main-undef | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-multipart-part-key-dot | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-multipart-part-length-value | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-multipart-part-field-json | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-multipart-methods | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-multipart-require-create | client_ingest.go | killed: unit | TestMultipartRejectsRunsWithoutTraceFields |
| cli-multipart-require-trace | client_ingest.go | killed: unit | TestMultipartRejectsRunsWithoutTraceFields |
| cli-multipart-empty | client_ingest.go | killed: new test | TestMutEmptyMultipartBuildsNothing |
| cli-multipart-attachment-warn | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-multipart-context-sep | client_ingest.go | killed: new test | TestMutMultipartFailureContextJoinsRunsWithASemicolon |
| cli-multipart-context-trace | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-multipart-context-id | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-send-streamed | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-send-url | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-send-dest-url | client_ingest.go | killed: new test | TestMutDestinationOptionsRouteTheBatch |
| cli-send-failed-dir | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-batch-key-order | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-batch-prepare | client_ingest.go | killed: new test | TestMutBatchIngestPreparesTheRuns |
| cli-batch-dest | client_ingest.go | killed: new test | TestMutDestinationOptionsRouteTheBatch |
| cli-fallback-filename | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-fallback-budget | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-fallback-budget-text | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-fallback-saved-text | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-fallback-error-text | client_ingest.go | killed: unit | TestFailedTraceDirectoryErrors |
| cli-fallback-size-prefix | client_ingest.go | killed: new test | TestMutFallbackDirSizeCountsTraceFilesOnly |
| cli-gzip-level | client_ingest.go | killed: new test | TestMutGzipBodyIsCompressed |
| cli-body-reader-closed | client_ingest.go | killed: new test | TestMutReadOnAClosedBodyFails |
| cli-cursor-chunk | client_ingest.go | EQUIVALENT (argued) |  |
| ca-status-408 | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-status-425 | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-status-429 | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-status-500 | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-status-502 | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-status-503 | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-status-504 | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-status-501 | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-status-409 | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-default-retries | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-min-timeout | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-delay-round | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-network-deno | caller.go | killed: new test | TestMutNetworkErrorMessages |
| ca-network-fetch-failed | caller.go | killed: unit | TestIsNetworkError |
| ca-network-type-error | caller.go | killed: unit | TestIsNetworkError |
| ca-status-text | caller.go | killed: unit | TestRaiseForStatus |
| ca-org-scoped-message | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-org-scoped-code | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-raise-2xx | caller.go | killed: unit | TestRaiseForStatus |
| ca-raise-message-body | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-queue-limit-text | caller.go | killed: unit | TestQueueSizeLimit |
| ca-concurrency | caller.go | killed: unit | TestQueueSizeLimit |
| ca-idle | caller.go | killed: unit (test timeout: the mutant hangs) | unit test: panic: test timed out after 3m0s |
| ca-retry-ctx | caller.go | killed: unit | TestPreCancelledContextRunsNothing |
| ca-retry-type-error | caller.go | killed: unit | TestNoRetryClassification |
| ca-rethrow-status | caller.go | killed: golden vectors (unit) | TestCallerGoldenRetry |
| ca-rethrow-no-status | caller.go | killed: new test | TestCallerGoldenRetry |
| wire-sampling-reduce-high | sampling.go | killed: new test | TestClientDifferential |
| wire-sampling-lt | sampling.go | killed: new test | TestMutSampledByIDIsStrictlyBelowTheRate |
| wire-sampling-rate-one | sampling.go | EQUIVALENT (argued) |  |
| wire-uuid7-variant | uuid.go | killed: golden vectors (unit) | TestRunTreeGolden |
| wire-uuid7-rand-tail | uuid.go | EQUIVALENT (argued) |  |
| wire-uuid-shape-length | uuid.go | killed: unit | TestNonCryptographicUuid7Deterministic |
| wire-uuid-assert-text | uuid.go | killed: golden vectors (unit) | TestClientDifferential |
| wire-uuid-assert-for-text | uuid.go | killed: unit | TestAssertUUID |
| wire-uuid-warn-text | uuid.go | killed: unit | TestWarnIfNotUUIDv7 |
| wire-uuid-det-version | uuid.go | killed: golden vectors (unit) | TestRunTreeGolden |
| wire-uuid-det-variant | uuid.go | killed: golden vectors (unit) | TestRunTreeGolden |
| wire-uuid-det-v7-gate | uuid.go | killed: unit | TestNonCryptographicUuid7Deterministic |
| wire-xxh3-secret | xxh3.go | killed: golden vectors (unit) | TestClientDifferential |
| wire-xxh3-prime32-1 | xxh3.go | killed: unit | TestNonCryptographicUuid7Deterministic |
| wire-xxh3-prime32-2 | xxh3.go | killed: unit | TestIsSampledByID |
| wire-xxh3-prime32-3 | xxh3.go | killed: unit | TestNonCryptographicUuid7Deterministic |
| wire-xxh3-prime64-1 | xxh3.go | killed: golden vectors (unit) | TestClientDifferential |
| wire-xxh3-prime64-2 | xxh3.go | killed: golden vectors (unit) | TestClientDifferential |
| wire-xxh3-prime64-3 | xxh3.go | killed: unit | TestIsSampledByID |
| wire-xxh3-prime64-4 | xxh3.go | killed: golden vectors (unit) | TestClientDifferential |
| wire-xxh3-prime64-5 | xxh3.go | killed: unit | TestNonCryptographicUuid7Deterministic |
| wire-xxh3-primemx1 | xxh3.go | killed: golden vectors (unit) | TestClientDifferential |
| wire-xxh3-primemx2 | xxh3.go | killed: unit | TestIsSampledByID |
| wire-xxh3-avalanche-shift | xxh3.go | killed: golden vectors (unit) | TestClientDifferential |
| wire-xxh3-w1-sign | xxh3.go | killed: unit | TestIsSampledByID |
| wire-xxh3-len1to3-rot | xxh3.go | killed: unit | TestIsSampledByID |
| wire-xxh3-len9to16-shift | xxh3.go | killed: unit | TestIsSampledByID |
| wire-xxh3-len129-tail | xxh3.go | killed: unit | TestIsSampledByID |
| wire-xxh3-boundary-8 | xxh3.go | killed: unit | TestNonCryptographicUuid7Deterministic |
| wire-xxh3-long-acc-init | xxh3.go | killed: unit | TestNonCryptographicUuid7Deterministic |
| wire-xxh3-long-scramble | xxh3.go | killed: unit | TestXXH3Long |
| wire-xxh3-long-blocks | xxh3.go | killed: unit | TestXXH3Long |
| wire-xxh3-long-merge-low | xxh3.go | killed: unit | TestNonCryptographicUuid7Deterministic |
| wire-xxh3-long-merge-high | xxh3.go | killed: unit | TestNonCryptographicUuid7Deterministic |
| wire-xxh3-accumulate | xxh3.go | killed: unit | TestNonCryptographicUuid7Deterministic |
| cl-post-length | client.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-export-multipart-server | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-export-error-returned | client_ingest.go | EQUIVALENT (argued) |  |
| cli-send-length | client_ingest.go | killed: golden vectors (unit) | TestClientDifferential |
| cli-process-nothing-to-send | client_ingest.go | EQUIVALENT (argued) |  |
| cli-finish-ctx | client_ingest.go | killed: new test | TestMutCancelledExportIsNotLogged |
| cli-gosend-inline | client_ingest.go | killed: new test | TestMutGoSendOnAClosedClientRunsInline |
