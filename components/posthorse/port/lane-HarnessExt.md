# Lane HarnessExt (H1): harness features and proving scenarios

Harness: `components/extension-equivalence/` (library `eq/`, new files `features.go`, `features_run.go`, `triggers.go`,
`files.go`, `project.go`, `systemdiff.go`; edits in `scenario.go`, `run.go`, `llm.go`, `normalize.go`, `upstream.go`,
`driver/eq-driver.ts`; README section "Reproducing a session suite"). Tests: `eq/features_test.go`,
`eq/features_host_test.go`. `NormalizerVersion` is unchanged (`v3`): every feature is opt-in and a scenario that does
not use the new fields records exactly what it did before (`TestDefaultScenarioKeepsItsHostArguments`,
`TestProjectionIsOptInAndBackwardsCompatible`, `TestScenarioModelsFile`).

## Features (scenario fields)

| Field | What it does |
|---|---|
| `model`, `models` | context window, max tokens, reasoning, input kinds, extra models; llm trace records the model id when `models` is set |
| `llm[].usage/finishReason/thinking/error/delayMs/hold` | usage chunk (input/output/cacheRead/cacheWrite), `length`, reasoning stream, HTTP error (overflow 400, 529 with body/headers), delayed or held turn (held until a trigger releases it or the host aborts) |
| `settings` | agent-dir `settings.json` |
| `session` (`files`) | real `--session-dir`, seed files; `{{sessions}}` `{{cwd}}` `{{agent}}` in rpc strings; N9 session-name timestamp |
| `extensions` | helper TS extensions loaded with `-e` before/after the one under test (dump tool, turn_end hooks) |
| `steps[].save`, `quiet`, `{{var:NAME}}` | carry values of a response (entry ids, session file) into later rpc commands and llm turns |
| `steps[].triggers` | event or llm-request condition -> rpc command (steer, follow_up, abort, compact) and/or release of held turns |
| `/eq-fork [id [at]]`, `/eq-tree id`, `/eq-reload` | driver commands for ctx.fork/navigateTree/reload; every other session op is an RPC command |
| `capture` of session events, `project.{full,messageFields,images,drop}` | compaction_*, auto_retry_*, queue_update, ... recorded whole, ids relabeled `<id:N>`; extra message keys; image detail |
| `captureFiles` (scenario + step) | final (or per-step) file state: workspace / agent / sessions / home, globs with `**`, jsonl decoded and normalized, symlinks, modes, `drop` key paths |
| `systemDiff: "hunks"` | per-hunk system-prompt delta (the span delta drags host wording between an extension's tools-list change and its appended guidance into the comparison) |
| `commands` `rg`/`git` | no change needed: `{"rg": {"mode": "real"}}` records the real ripgrep calls (rg and git exist in tern-ci:v7) |

## Scenarios (`port/scenarios/`, all run identically on Pi 1.5.0 and PiG 0.7.0's Node runtime)

tool-definitions, new-context-explicit, get-context-remaining (usage known 20,100 / unknown after reset),
notes-write-read-list-search (+ `.pi/**` files), history-search (+ rg), automatic-rollover-threshold
(session_before_compact path; compaction_start/end; retained-tail sentinel and summary visible in the next request),
reminder, overflow-recovery (HTTP 400 overflow, one compact-and-retry), queued-steer-during-run.
Goldens are not recorded here (`pigeq record --ts` is the lead's step).

## Findings (HOST differences between Pi 1.5.0 and PiG-Node, for the lead)

1. **Compaction token estimates count the host's own system-prompt wording.** `compaction_end.result.estimatedTokensAfter`
   differs by ~18 tokens (2677 vs 2659 in automatic-rollover-threshold, 2566 vs 2548 in overflow-recovery) and
   `result.tokensBefore` in overflow-recovery (2436 vs 2418): the Pi vs PiG preamble/docs paragraph differs by 72 characters.
   Posthorse does not produce these values. The two scenarios mask them with `project.drop` (and `**.tokensBefore` in the
   session capture); remove the `drop` entries to see the raw difference. Decision for the lead: keep the mask (a Go port
   under PiG can never match Pi's golden on a host-computed estimate) or accept the scenarios as Pi-vs-PiG-different.
2. **System messages in session files carry host wording** (`sections.docs`, `sections.preamble`: "Pi documentation" vs
   "PiG documentation", paths, "pi" vs "pig"). Masked with the capture's `drop` (`**.sections.docs`, `**.sections.preamble`);
   the extension's own sections and every other key are compared and identical. The system-message `sections` (cwd, rules,
   tools) are equal on both hosts.
3. Everything else compared identical, including: tool definitions and system delta (hunks), usage-derived context report
   (known/unknown), explicit reset entry, automatic and overflow compaction entries and `fromHook`, retained-tail sentinel,
   reminder custom message and its steer delivery, notes files and the `posthorse-notes-scope` custom entry, history
   search/read output, rg invocations, queue_update + steer disposition, session JSONL (after dropping `timestamp`/`durationMs`).
4. Provider overflow through HTTP is worded by the host as `400: {"code":null,"message":"prompt is too long: ...","param":null,"type":"invalid_request_error"}`
   on both hosts; the suites' literal `errorMessage: "prompt is too long: 300000 tokens > 100000 maximum"` is only reachable
   in-process (faux provider). The text contains it, so Pi's overflow classification (and PiG's) matches.

## Harness gaps found and fixed

* Span system delta included host text between two extension changes (fixed: `systemDiff`).
* Session entry ids are random per run: relabeled in whole events, id-carrying responses and session files; ids quoted
  inside an extension's own text (recovery records, history output) need the scenario's `normalize` rule (see the scenarios).
* A step that was racy by construction (`agent_start` trigger vs `turn_start`) showed up as an unstable trace: use
  `llmRequest` triggers (fire after the request is recorded) or events that the host orders for you.

## Not reproducible through RPC (documented for the scenario author)

In-process hooks a test installs inline (`afterExtension`, `registerDump` etc.) become `extensions` helper sources; faux
`FauxResponseFactory` callbacks that inspect `ctx.messages` become the llm trace; `SessionManager` seeding with hand-built
entries becomes `session.files`; `session.abortCompaction()`, `ctx.abort()` inside a handler and direct `session.dispose()`
have no RPC equivalent (use a helper extension that calls them); assertions on in-memory objects (`boundaries(h)`) are
file/trace assertions on the session JSONL capture. `get_entries` responses carry system messages: use `quiet` plus `save`.

## Commands run

`ph run "export GOWORK=/home/kinsy/ph-work/scratch/HarnessExt/go.work; cd components/extension-equivalence/extensions/extension-equivalence && go vet ./... && GOOS=windows|darwin go vet ./... && go test -race -count=1 ./eq"` green;
`pigeq lane` for pi and pig on all ten scenarios, diffed with `pigeq diff`: 10/10 identical (queued-steer-during-run 5 extra
pig runs identical). Not green and not touched by this lane: `cmd/pigeq TestEnvPrintsTheIsolatedScriptAndWritesGoWork`
(depends on the container's PIG_SDK_DIR/GOWORK environment), root package `TestRunToolValidatesItsMode` and
`TestRunToolBuiltinChecksABinaryWithoutAGoDirectory` when `PIGEQ_PIG` is set in the environment (pass with it unset).
