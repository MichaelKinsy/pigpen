# Lane ScenarioNative: scenarios and tests for `test/native-host.ts`

Coverage table (every upstream title -> scenario / Go test / exclusion): `coverage-ScenarioNative.md`.

## Deliverables

* 37 scenarios `port/scenarios/nat-*.json`, goldens `port/golden/nat-*.jsonl` recorded from the ORIGINAL under Pi 1.5.0
  (`pigeq record`, host check Pi == PiG-Node passed for every one), Go port checked with `pigeq check` (see "Commands run").
* 2 reproducers kept outside the golden set in `port/scn-findings/` (a host finding and an SDK finding).
* Layer-1 tests: `native_host_pages_test.go` (native-host.ts:994), `guidance_sections_test.go` (regression of the port bug below).
  The fork-notes cases were already layer-1 tests of NotesStoreLane (`fork_notes_*_test.go`) and are referenced, not duplicated.
* Exclusions with reasons: coverage table (rows marked X), 14 titles; one open gap (1257).

## Port bug found and fixed

| what | TS behaviour | Go fix | test |
|---|---|---|---|
| `context_with_system` rewrote the system message's `sections` in alphabetical order (`cwd, docs, preamble, rules, tools, posthorse`); every scenario's system delta differed on the Go lane | `guidance.ts:35-38` `{...system.sections, posthorse}` keeps Pi's order `preamble, tools, rules, docs, cwd` | `guidance.go` `withPosthorseSection`: fallback order is Pi's built-in section order (`hostSectionOrder`), then remaining keys sorted, `posthorse` last | `guidance_sections_test.go` |

## Harness additions (components/extension-equivalence, all opt-in, goldens of other scenarios unaffected)

* `{{cwd}}/{{agent}}/{{sessions}}` are expanded in llm turn arguments/text (`features_run.go turnHasVars`, was only `{{var:}}`).
* `project.skipSystem` (`scenario.go`, `normalize.go`, README, `TestProjectionSkipSystemLeavesOutSystemMessageEvents`).
* `anchors` arrays are relabeled `<id:N>` in whole events and captured JSONL (`project.go`, `TestRelabelIDsCoversAnchorLists`).
* (ScenarioOfficial added `{{repeat:C:N}}`, `collapseRuns` and trigger `save`; used here.)

## Helper extensions the scenarios embed (TS, loaded in every lane)

`fsx` (workspace fs changes: write/unlink/utimes/symlink/find), `forkfile` (SessionManager.forkFrom copy with a moved leaf),
`codemode` (`createCodemodeExtension()` with `--tools +codemode`), `dump` (600,000-character result), `ask`, `catalog`, `extra`,
`controls` (`/eq-force`, `/eq-wake`, `/eq-settings`, `/eq-tools`), `ctxedit` (`/eq-ctxedit`), `rewrite` (`/eq-rewrite`),
`bigsessions` (`/eq-archive`, `/eq-projection`), `continue` (turn_end steer/followUp/boundary). Seeded session files come from
`session.files` with `{{cwd}}` in the header.

## Host findings for the lead (details and values in the coverage file)

1. forced prompt applied by a later `before_agent_start` handler is invisible to an earlier handler under PiG
   (`scn-findings/nat-guidance-survives-tool-additions-forced-after.json`: Pi sends the guidance as a custom message, PiG loses it).
2. Pi emits a system-message `message_end` after a tree navigation onto a branch without one, PiG does not; Pi's RPC
   `tool_execution_end` has `parentToolCallId` for nested calls, PiG's RPC does not.
3. PiG's `ctx.sessionManager` has no `appendContextEdit` (Pi's real `SessionManager` has).
4. The `new_context` handoff limit differs (2,404 vs 2,440 characters) with the host's system prompt length.
5. Pi 1.5.0 has no background codemode: three upstream tests skip themselves.
6. SDK gap: `context_with_system` replies re-encode every message object with sorted keys (tool-call `arguments`);
   reproducer `scn-findings/finding-context-argument-key-order.json`. Needs an ordered decode in `extensions/sdk`.
7. Parallel tool calls in one assistant message end in timing order, which differs between Pi, PiG-Node and the Go port
   (also the order in which siblings reserve page capacity); scenarios avoid it (one call per turn or a helper dependency).

## Findings about the port's behaviour worth a look

* Opening any session binds Posthorse's notes scope immediately (`posthorse-notes-scope`), so the unbound-legacy fork path
  of test 203 can only be reached with the extension absent (layer-1).
* `ph run` rsyncs the shared tree; two lanes' half-edited Go files repeatedly broke the Go build mid-run
  (`uvMessages`, `nodeResolve`, `win32Resolve`, `mkdirNode`). The lane used a snapshot of the extension taken when it built.

## Commands run

* `pigeq run` per scenario (all three lanes) while developing; `pigeq record` over the 37 scenarios (Pi golden + PiG-Node host check: 37 PASS);
  `pigeq check` rounds with the Go port (see the final report for the counts); harness `go vet ./eq` and the new harness tests; package
  `go vet` and the two new layer-1 tests (the full package run was blocked by other lanes' half-edited files at the time, see above).
