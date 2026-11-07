# CoreLane mutations

`CoreLane.json`: 112 mutants of files.go, js.go, message_text.go, budget.go, paging.go, reset_requests.go, guidance.go,
context_tools.go, extension.go, session_lines.go. Command (scratch copy on xd6702, other lanes' in-flux tests removed from the copy):
`pigeq mutate --go extensions/posthorse --mutations port/mutations/CoreLane.json --unit-only --sdk-dir $PIG_SDK_DIR --jobs 6`.

First run: 98 killed, 14 survived, 0 INVALID (three mutants that did not build, `u16len-counts-runes`, `fresh-prompt-tokens`,
`capacity-guidance`, were rewritten to keep their variables in use).

Survivors and their disposition (`core_mutation_regress_test.go` holds the new tests):

- utf8bytes-pair-join, assistantFailure-aborted, boundedBlock-bytes-not-units, fresh-image-estimate, refusal-fit,
  notes-empty-offset, guidance-findlast, guidance-settled-reset, migration-suffix, lstat-enotdir, latin1-tail-short: killed by new tests.
- tool-label, tool-snippet: not equivalent; the tool `Label` and `PromptSnippet` are model/UI-visible and are compared by the pigeq
  scenario traces (tool definitions in the model request), which `--unit-only` does not run. No Go unit test reads them.
- live-policy-provider: not equivalent; `liveCompactionPolicy` is only reachable through the host settings graph, which the fake host
  does not model; covered by the scenarios (settings in the model request), not by Go unit tests.
