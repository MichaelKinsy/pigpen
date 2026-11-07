# CoreLane mutations

`CoreLane.json`: 114 mutants of files.go, js.go, message_text.go, budget.go, paging.go, reset_requests.go, guidance.go,
context_tools.go, extension.go, session_lines.go (112 original + `tool-tokens-cache-ignores-parameters` (C-07) and
`guidance-custom-displayed` (C-09)).

## Runs (all on xd6702, `pigeq mutate --go extensions/posthorse --mutations <file> --unit-only --sdk-dir $PIG_SDK_DIR`)

1. Full run, 112 mutants, scratch copy, `--jobs 6`: 98 killed, 14 survived, 0 INVALID (three mutants that did not build were rewritten).
2. Confirmation run of the 14 survivors + the two coverage mutants (16 mutants): a frozen snapshot of the whole
   `components/posthorse` dir copied under `/home/kinsy/ph-work/scratch/CoreLane`, four shards of four mutants, each
   `--jobs 1` with its own `TMPDIR`, in parallel. Result: **16 of 16 killed, 0 survivors**.

Final: 114 mutants, 112 killed by Go tests (98 in run 1 plus the 14 former survivors now killed by regression tests and the
two coverage mutants), 0 equivalent removed, 0 surviving. (The two coverage mutants raise 112 to 114; the 14 survivors of
run 1 are among the 16 confirmed, the others are the two coverage mutants.) Arithmetic: 98 + 14 + 2 = 114.

## How each former survivor is killed

| mutant | killed by |
|---|---|
| utf8bytes-pair-join | `TestMutationJSUTF8BytesLoneLowThenLow` |
| assistantFailure-aborted | `TestMutationAssistantFailureAbortedWithoutMessage` |
| boundedBlock-bytes-not-units | `TestMutationBoundedBlockCountsUnitsOfTheHeader` |
| fresh-image-estimate | `TestMutationFreshPayloadImagesAreEstimated` |
| refusal-fit | `TestMutationRefusalTextKeptWhenItFitsExactly` |
| notes-empty-offset | `TestMutationNotesEmptyPageAtOffsetZero` |
| guidance-findlast | `TestMutationGuidanceTargetsTheLastPosthorseSection` |
| guidance-settled-reset | `TestMutationAgentSettledForgetsTheForcedPrompt` |
| migration-suffix | `TestMutationMigrationTargetFirstSuffixIsOne` |
| lstat-enotdir | `TestMutationLstatSyncMissingTreatsNotADirectoryAsMissing` |
| latin1-tail-short | `TestMutationLatin1TailKeepsExactlyTheOverlap` |
| tool-label, tool-snippet | `TestContextToolDefinitionsOnTheRegisterFrame` (label, prompt_snippet and description on the register frame) |
| live-policy-provider | `TestCompactionModelArgKeysOverridesByProviderAndID` (the model argument is built by `compactionModelArg`, a behaviour-preserving extraction in extension.go) and `TestLiveCompactionPolicyAsksTheSettingsManager` (the fake host sees `sessionGraph.new` SettingsManager.inMemory with the replicated settings and `getCompactionSettings` on that handle) |
| tool-tokens-cache-ignores-parameters | `TestCoverageToolTokenCacheSeesParameterChanges` |
| guidance-custom-displayed | `TestCoverageForcedPromptGuidanceIsAHiddenMessageUntilTheRunSettles` |

The fake host's state has no selected model, so the live-path test asserts the no-model case (`ctx.model` undefined: no model
argument); provider/id keying is asserted on the extracted helper.
