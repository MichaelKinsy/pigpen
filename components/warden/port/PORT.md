# Port record: warden (pi-warden)

| Input | Identity |
|---|---|
| Original | `DevMortimer/pi-warden` 0.80.0, commit `a12b2703b2c7ac32401fe13f4ce037d673a5f113`, MIT (Ryan Gapac). Kept unmodified at `port/oracle` |
| Judge seam | `DevMortimer/pi-typesafe` commit `ed439f834665ad6fc652787f5ba7c23852566d49` (`ask`, questions, `errors.ts`), MIT |
| Oracle | Pi 0.87.1 hosting the original; Node 24.19.0 |
| Target | PiG 0.3.0+0.87.1, SDK of a 0.3.0 pre-release build (`63c6ba456`) (host getters return `(value, error)`), go1.27.1; shared client `components/typesafe` |
| Original's tests | 1,264 cases in `port/oracle/tests`; 744 have a Go twin (the learning slice added 77 on top of the context slice's 667), 520 are named skipped subtests with a reason (see below) |

## File-to-file mapping

| Original (`port/oracle/src`) | Go (`extensions/warden`) | Notes |
|---|---|---|
| `guard.ts` `SHELL_RULES`, `SENSITIVE_PATH`, `classifyRm`, git state, `matchPatterns`, `unknownExemptIds`, `EXEMPTABLE_IDS` | `patterns.go` | RE2 has no lookahead or backreference: `printenv`/`echo` secret rules, the `.env` example exclusion, `git checkout/restore` and `git push -f` are small functions with the same behavior (each cites the regexp it replaces) |
| `guard.ts` `splitShell`, `headOf`, `stripDataText`, `blankQuotes`, `blankMessageFlags`, `isReadOnlyCommand` (+ `sed`, `git grep`), `isVisibleCommand` | `shell.go` | |
| `guard.ts` `describeAction`, `truncate`, `sample`, `displayPath` | `describe.go`, `jsstr.go` | strings are cut in UTF-16 units like JavaScript |
| `guard.ts` `evaluateAction`, `buildRequest`, `intentSteer`, `offTaskSteer`, `shouldProceedMessage`, `steerReason`, `textApproves`, `steerFingerprint`, `SteerRepeatWindow` | `guard.go` | messages keep the `pi-warden:` prefix |
| `guard.ts` `authorize`, `scopeMatches`, `hitViolations` (rm family) | `authorize.go` | a prompt that names the deletion and its target releases an `rm`; nothing releases `rm-recursive-dangerous-target` |
| `guard.ts` `questions`, `visibleQuestion`, `intentQuestion`, `shouldProceedQuestion`, `approvalQuestion`; `stuck.ts` `stuckQuestions`; `done.ts` `doneQuestions` | `questions.json` + `questions.go` | verbatim, dumped from the pinned source |
| `guard.ts` `authorize`, `scopeMatches`, `isAuthEligible`, `escalateBlastRadius`, `escalateRulesViolation`, `aggregateLevel`, `removeAuthorized`, `parseViolationJudgments`, violation questions | `authorize.go`, `violation.go` | the full verb table; `Violation`/`Authorization` carry upstream's fields |
| `guard.ts` scratch, `hostPaths`, `wardenHostPaths`, `realTarget`, `tempRoots`, `mktempOnly`, `createdScratch`; `classifyRm` scratch branch | `scratch.go`, `birth_*.go` | birth time: `statx` on Linux (stdlib `syscall`), `Birthtimespec` on macOS, `CreationTime` on Windows |
| `guard.ts` `sqlTarget`, `applySqlTargets` | `sqltarget.go` | the `ALWAYS_TRUE` backreferences are functions |
| `guard.ts` `matchPathRules`, `matchPathGlobs`, `writeSinkTargets`, `inertPathRules` | `pathrules.go` | the mention regex's tail lookahead is a consumed boundary (same yes/no answer) |
| `arming.ts` | `arming.go` | `ArmingTracker`, `unparseableArmingRules` |
| `guard.ts` `regretQuestions`, `commandFamily`, `largeOutputSteer/Notice`, `SLOP_LABELS`; guard questions | `guard_extras.go` + `questions_guard.json` (dumped by `port/gen-questions.ts`) | |
| `extension.ts` arming, scratch, host paths, large-output hooks | `guardcore_wire.go` (one call per handler) | |
| `action-guard.ts` | `action_guard.go` | hold and approval, sibling prejudging; safe for concurrent use; `Wait()` added |
| `stuck.ts` (attempt window, `evaluateStuck`, nudges, `stuckDiff`) | `stuck.go`, `stuckdiff.go` | attempt keys are SHA-1 of the sorted-key JSON of the input (equality only) |
| `evidence.ts` (`parseFailure`, `failureSignature`, `editDiff`, `buildStuckEvidence`) | `evidence.go` | the evidence is sent as a typed struct in upstream's field order; the byte cap measures the JSON without HTML escaping, as `JSON.stringify` |
| `subagent.ts` (`newReports`, `triageReport`, `WakePolicy`, `formatWake`, `reportLabel`) and the `agent_settled` triage of `extension.ts` | `subagent.go`, `supervision.go` | reports are collected from `message_end` events of custom messages (the SDK's `BranchEntry` has no `customType`), bounded to the newest 8 unseen, which is what upstream's scan reads |
| `waste.ts` (`WasteTracker`, detectors, `WASTE_TIP`, `withWasteTip`) and the tool_result / before_agent_start hooks | `waste.go`, `supervision.go`, `results.go` | the detectors also read `shellWrites(command, ...).Writes` (shell_writes.go) for what a command writes |
| `extension.ts` `message_start`, `message_update`, `message_end`, `input`, `agent_settled`; the `agent_end` order | `supervision.go`, `results.go` | one handler per event; each lane's piece is registered in `newWarden` in upstream's order (see the comment at the top of `supervision.go` and above `onAgentEnd`) |
| `done.ts` (evidence, `checkSummary`, UI proof, `evaluateDone`, `doneNudge`) | `done.go` | glob matcher from `rules.ts` `globToRegExp` |
| `shape.ts` `taskSpine` | `spine.go` | |
| `widget.ts` templates and tokens (action, stuck, done) | `widget.go` | `toFixed` rounds ties up like JavaScript |
| `redact.ts` `redact` | `redact.go` | `VALUE_AMPERSAND` lookahead written as a scan |
| `redact.ts` detection: `findSecrets`, `looksLikeSecretValue`, `syntheticish`, `partitionSecrets`, `maskSecrets`, `secretFingerprint`, `secretIds` | `redact_secrets.go` | the credential-assignment rule is a scan like `redact` (no lookahead); signed-URL spans are byte offsets, not UTF-16 units |
| `output.ts` security half: `evaluateOutput`, `buildOutputRequest`, `outputQuestions`, `securityNotice`, `OutputVerdict` | `output_security.go` | the retention and format half (`compressOutput`, `retentionQuestion`, learner) belongs to the context lane (`output_context.go`); `evaluateOutput` reaches it through `retentionQuestions`, `applyRetention`, `learnerSuggest` |
| `runaway.ts` | `runaway.go` | `RunawayMonitor` is used under one mutex by the message handlers; strings are measured in UTF-16 units |
| `prose.ts` (prose check, `ProseTrend`, `RestatementWindow`, `proseNudge`) | `prose.go` | the check runs in a goroutine beside the done check (upstream starts both promises, then awaits prose first) |
| `notify.ts` | `notify.go` | commands run through `os/exec` in the extension process, as upstream's `execFile` does (D15) |
| `extension.ts` `slopSteer`, `SLOP_FIXES`, the slop content note | `slop.go` | the note is a `callNotes` content note of the rules lane's combined steer |
| `extension.ts` security hooks (`message_start`/`message_update` runaway, `agent_end` slots 3, 5 and the prose check, `notifyDesktop`, the security-write steer) | `security_wiring.go` (one call each from `extension.go`, `results.go`, `toolcall.go`, `commands.go`) | the tool-output masking path (`tool_result`) is the context lane's `tool_output.go`, which calls `MaskSecrets`, `EvaluateOutput` and `SecurityNotice` |
| `tools.ts` | `tools.go` | |
| `config.ts` (the carried sections) | `config.go`, `config_*.go` | the types and defaults |
| `config.ts` loader half (`applyUserOverrides`, `applyProjectOverrides`, `loadConfig`, `parseDuration`, the rule parsers, `setNestedValue`/`getNestedValue`/`parseConfigValue`, `readUserConfig`/`writeUserConfig`/`setUserSetting`) | `config_load.go`, `configfile.go` | raw JSON (`any`) over a base `Config`; the port's own keys are `consent` and `backend` (D3, D5) |
| `config.ts` `userConfigPath`/`projectConfigPath`, `host-dirs.ts` `defaultHostDirs` | `host_dirs.go` | `HostDirs{ConfigHome, ConfigDirName}`: PIG_HOME or ~/.pig, and `.pig` (`.pi` under PIG_USE_PI_DIRS=1); the project file keeps upstream's name `pi-warden.json` |
| `extension.ts` `configFor` (loading, trust, once-per-session notices), `/warden status` config lines | `config_runtime.go` | stat-based reload; trust asked once per cwd |
| `extension.ts` `/warden config` | `config_cmd.go` | |
| `shape.ts` `completeConfig`, `shapeWarning`, `CONFIG_SCHEMA` | `shape.go` | zero-section detection instead of `typeof` checks, see C6 |
| `host-compat.ts` | none | Oh My Pi adapter; PiG is not an adapted host |
| `trace-file.ts` | `trace_file.go`, `trace_session.go` | writes on one goroutine, off the guard's path |
| `widget.ts` `LEVEL_COLOR`, `SENTENCE_TEMPLATES`, `pickSentenceTemplate`, `renderSegment`, `parseVerdictLine`, `TOKEN_NAMES`; `widget-render.ts` | `widget_render.go`, `widget_sentences.go` | the sentence table is copied text |
| `extension.ts` `paint` | `widget_paint.go`, `steer.go` (`paint`) | live and stack bars, placement |
| `panel.ts`, `extension.ts` `togglePanel`/`toggleConfigPanel`/`traceText`, the shortcut | `panel.go`, `panel_open.go`, `trace_session.go` | SDK remote components; keys and widths from `components/pitui` |
| `extension.ts` (`tool_call`, `tool_result`, `agent_end`, `agent_start`, `before_agent_start`, `turn_end`, steer delivery, hold, dialog) | `extension.go`, `toolcall.go`, `results.go`, `steer.go` | events are PiG's; results are `{block, reason}` |
| `extension.ts` `/warden` command | `commands.go` | new subcommands, see Deviations |
| pi-typesafe `ask`, `noul`/`choice`/`score`, `safeError` | `judge.go`, `backend.go` | the client is `components/typesafe`; `backend.go` adds the two backends |
| (none) | `disclosure.go` | data-flow disclosure and consent text |
| `saver.ts` | `saver.go` | the ledger is safe for concurrent use (the tool_result, tool_call and message handlers and `warden_recall` share it); it implements the tools lane's `outputLedger` |
| `dedupe.ts` | `dedupe.go` | the rolling hash is 32-bit like `Math.imul`; lines and characters are measured in UTF-16 units |
| `excerpt.ts` | `excerpt.go` | JavaScript source patterns run through `jsRE` (`context_jsre.go`): `\s` is the Unicode white-space set and `.` stops at `\r`, U+2028 and U+2029, as in JavaScript |
| `filter.ts` | `filter.go` | one request per batch, run in parallel; request size counted like `JSON.stringify` + `Buffer.byteLength` |
| `relevance.ts` | `relevance.go` | the compaction runs on a worker pool of four with the same stop-on-first-failure rule as `fanOut` |
| `output.ts` compression half (`compressOutput`, `mergeOutput`, `outputKey`, `saveOutput`, `isGenericExcerpt`, `duplicateNote`, `CompressionLearner`, the retention and format questions) | `output_context.go` | the security half is `output_security.go`; `EvaluateOutput` calls `retentionQuestions`, `applyRetention` and `learnerSuggest` here |
| `extension.ts` `tool_result` handler (masking, output verdicts, credential notices, duplicate note, compression, context filter, repeated runs, banner placement) | `tool_output.go` | one marked hook in `results.go`: the pipeline runs first, then the loop checks, so trace records come in upstream's order |
| `extension.ts` ledger wiring: recall tracking in `tool_call`, `turn_end`, `message_end` (opt-in message dedupe), session reset, `/warden status` lines | `context_wiring.go` | one marked hook each in `toolcall.go`, `extension.go`, `commands.go` |
| `extension.ts` `session_before_compact` | `context_compaction.go` | returns `{compaction: {summary, firstKeptEntryId, tokensBefore, details}}`; PiG's host reads that result and sends `preparation` |
| `prefs.ts` | `prefs.go` (grouping, rules, lessons), `prefs_text.go` (detection), `prefs_scan.go` (sessions, worktrees), `prefs_store.go`, `prefs_regex.go`, `jsupper.go` | RE2 has no lookaround: each rewrite cites its pattern and is checked against the original on 5,594 messages (`prefs_differential_test.go`, `testdata/prefs_corpus.json`, `port/gen-prefs-corpus.ts`) |
| `loops.ts` | `loops.go` | same JSON layout and key order as the original writes |
| `recall.ts` | `recall.go` | the search-tool probe runs at session start (`searchToolFor`), never inside a `tool_result` handler |
| `compact.ts` | `compact.go` | the saved-output ledger and the hold records are interfaces (`outputLedger`, `holdRecords`) |
| `extension.ts` `warden_remember`, `warden_loops`, `warden_recall`, the `pi-warden-prefs` injection, the loops lifecycle, `session_compact`, `/warden prefs`, `/warden loops` | `tools_wiring.go` | one marked hook each in `extension.go`, `results.go`, `commands.go` |
| `rules.ts` (parse, globs, `RuleStore`, targets, request, scoring, steers, sensitive paths, `RulesGuard`), `widget.ts` `rulesTokens`, `trace.ts` `rulesDetails` | `rules.go` | `RulesGuard.Inspect` returns a future; sibling requests are started through the action guard's `startNotifier` so a call returns with every request in flight. Question texts are dumped verbatim from the original by `port/gen-rules-questions.ts` into `rules_questions.json` (`rules_questions.go`) |
| `rules-file.ts` | `rules_file.go` | `resolveRulesFile(cwd, files, fallback)` is for the guard lane's escalation request |
| `rules-lint.ts` | `rules_lint.go` | chunks run concurrently (upstream `Promise.all`) |
| `rules-log.ts`, `rules-report.ts` | `rules_log.go`, `rules_report.go` | the log is `<warden data dir>/rules/<sha256(cwd)[:12]>.jsonl`, the data dir being the directory of `pigpen-warden/config.json` (D5) |
| `rules-audit.ts` (audit and `runBench`) | `rules_audit.go` | `DiscoverSourceFiles` is the audit lane's `discover.go`; `/warden bench` wiring is theirs, `runBench` is ready |
| `rules-calibrate.ts` | `rules_calibrate.go` | |
| `turn-rules.ts` | `turn_rules.go` | every git call is its own process under a timeout and a 1 MiB output cap (Node's `execFile` default) |
| `shell-writes.ts` | `shell_writes.go` | `shellWrites` and `mergeWrites` are ready for `describeAction`'s `writes` (guard lane) |
| `init.ts` | `rules_init.go` | |
| `extension.ts` rules hooks (`tool_call`, `agent_start`, `agent_end`, `session_start`, `turn_end`, `/warden rules`, `report`, `init`) | `rules_wire.go`, `rules_commands.go` | one call each from `toolcall.go`, `results.go`, `extension.go`, `commands.go`; `toolcall_notes` logic (`callNotes`) is in `rules_wire.go` |
| `conscience.ts` | `conscience.go` | `assess` runs up to four requests at once under one deadline (goroutines and a semaphore for upstream's `launchNext`); the question hash is computed over a canonical JSON tree with `SetEscapeHTML(false)`; `words`, `isDestructiveTool`, platform rules and the sanitizer keep JavaScript's `\s` |
| `load.ts` | `conscience_load.go`, `fileid_unix.go`, `fileid_windows.go` | rules 1-10 of load mode; rule 3 calls guard core's `matchPathRules`, rule 7 security's `FindSecrets` |
| `hashing.ts` | `hashing.go` | `nodeUTF8` decodes invalid bytes as Node does before hashing |
| `index-cmd.ts` | `index_cmd.go` | validation, truncation at bullet boundaries, owner-only I/O, the build prompt, lookup; entries are decoded JSON (`any`), as upstream's `unknown` |
| `extension.ts` conscience state (`conscienceGeneration`, `selectedCapability`, `pendingCapability`, `cachedSkills`, `loadedBytes`, `instructionState`, `globalIndexFile`, `indexNudged`, `indexRunning`) and its hooks (`before_agent_start`, `message_start`, `turn_end`, `tool_call`, `tool_result`, `agent_end`, `agent_settled`, `input`) | `conscience_state.go` | one marked call each (`CONSCIENCE-LANE HOOK`) in `extension.go`, `toolcall.go`, `results.go` |
| `extension.ts` `/warden index` and the index write exemption of the action guard | `index_command.go` | the model plays itself: `sendUserMessage`, `waitForIdle` |
| `frontmatter.ts` of `@earendil-works/pi-coding-agent` (`parseFrontmatter`) | `frontmatter.go` | a YAML subset, see D18 |

## Scope

Ported: the action guard (offline pattern floor, judged irreversible / off-task / off-plan / should-proceed / approval,
hold and release, sibling prejudging, user command and deny rules, exempt rules), the stuck guard (exact and
successful repeats, churn, quick repeat, judged loops, stuck diff, and the structured `stuck.evidence` the judge reads: parsed failures per runner, edit diffs, a digest), the subagent report triage and wake, the call-waste notes and tip, the `message_*`, `input` and `agent_settled` event handlers, the done-check (evidence, UI proof, false claims),
the task spine, steer delivery with the per-run budget and repeat window, the trace widget, the configuration loader (user file, trusted project file, validation, warnings, reload on change), `/warden config get|set|editor`, the trace sidebar and the config overlay with the `widget.shortcut` toggle, the status widget's rendering (stack and live bars, placement), the optional trace file, the config schema completion, the project rules guard
(`pi-warden.md`: parsing, scoping, per-rule judged requests, scores, severity and soft tier, sensitive-path notes, the
rules log, `/warden rules` and its `check`, `audit`, `calibrate` and `tune`, `/warden report`, `/warden init`,
end-of-run turn rules, literal shell writes judged as writes), and the memory tools: the three
tools `warden_remember`, `warden_loops` and `warden_recall`, standing preferences (the scan of earlier sessions of the project
and its worktrees, the seven injection rules, the lesson store, forget), open loops (store, reminder at the end of a run, message on resume), the compaction evidence
appendix (`session_compact`), the recall footer (`recall.go`), and `/warden prefs [forget <n>]` and `/warden loops`; the context saver: the tool_result pipeline (credential masking and notices, injection banners, retention judged compression with parser excerpts, the duplicate note, the repeated-run cut, the opt-in context filter), the stored full outputs and their ledger, recall tracking, message dedupe, relevance compaction at `session_before_compact`, and the saver's lines of `/warden status`.

Not ported (each upstream case is a named skipped subtest in `skipped_twins_test.go`, generated by
`port/gen-skipped-twins.py`; OWNER DECISION NEEDED to add any of them): slop and prose
guards, security masking and secret detection, runaway output,
preferences and loops (ported, see the tools lane). Ported by the learning slice: the hold
feedback loop and its database, per-model steer adaptation, the judge cooldown, `/warden recommend` and `/warden unmute`.

Guard core (the rest of `guard.ts` and `arming.ts`, ported): the violation model with authorization, blast-radius and
rules escalation and aggregation (`violation_<i>` questions), user path rules (access dimension, file and command
surface, write sinks), arming rules and their session tracker, session scratch with the birth-time exemption
(macOS and Windows only, as upstream), host paths (`PIGPEN_WARDEN_HOST_PATHS`, `:`-separated, plus the index
directory), SQL target classes (`sql-delete-local`, `sql-hosted`, `sql-hosted-write`), the regret, slop, security and
large-output questions on the action request, the large-output steer (once per command family per session), the bash
`writes` summary. Deviations: D6 names the host-path variable; the one-time config notices (unknown exempt ids, inert
path rules, invalid arming regexes) are said on the first guarded call of a process instead of on every config read
(the port's loader is another module); `Options.ScratchPlatform` exists for tests only; the regret answer is read into
`Judgment.Regretted` and the allowed calls of the previous turn come from `guardcoreState.previousActions`, which the
learning module sets (nil asks no regret question). The trace-detail lines (`actionDetails`: large-output, regret,
slop) are the trace module's.
The ledger of saved outputs and the hold records are interfaces here (`outputLedger`, `holdRecords` in `compact.go`): until those modules land the appendix and `warden_recall` list no saved output and no held action.

Refused, not silent: the `/warden` actions
`audit` and `bench` answer with a command notification: "@pi-in-go/pigpen-warden
(Go port) does not support <feature> yet. Install the original extension, npm:pi-warden@0.80.0, which runs unchanged in
PiG."

## Deviations on purpose

| # | Original | Port | Why |
|---|---|---|---|
| D1 | on by default | **off until `/warden enable`** (or `PIGPEN_WARDEN_ENABLED=1` for a headless run) | opt-in, task requirement |
| D2 | sends to the judge as soon as a key exists | **consent per backend**, shown with the full data flow before the first request; a backend without a matching consent runs offline and says so (a notice, and the status line reads `warden: offline (… not agreed to)`) | disclose data flow |
| D3 | TypeSafe (and OpenRouter, CommandCode) backends | `typesafe` and **`own-model`** (the session's model through PiG's model registry); no key ever read from a file or written | own-model backend must work |
| D4 | steers hidden and notices off by default | `steerVisible` and `notices` on | launch-demo clarity |
| D5 | `/warden` subcommands of the original | `/warden [status|enable|disable|mode|backend|test|trace]`; `test` runs synthetic dangerous calls and executes nothing | UX |
| D6 | `PI_WARDEN_*` environment | `PIGPEN_WARDEN_ENABLED`, `_BACKEND` (the consent, headless), `_MODE`, `_STEER_VISIBLE`; they apply to that run only and are never written to the config file by a `/warden` command | not colliding with the original |
| D7 | judge retries per SDK defaults | no automatic retry; failures fail open with a safe message and the offline patterns decide | a late judgment is worth less than the tool call it gates |
| D8 | `~/.pi/agent/auth.json` not a sensitive path | `pig/agent/auth.json` added to `SENSITIVE_PATH` | PiG's auth file |
| D9 | user rules are JavaScript regexps | Go (RE2) regexps; a pattern that does not compile is skipped | language |
| D10 | request state keys in insertion order | sorted | Go maps |
| D11 | the stuck and done requests send the user's request as typed (`stuck.ts` `buildStuckRequest`, `done.ts` `buildDoneRequest`) | redacted, like the action request's task | the disclosure promises a redacted request; a secret pasted into a prompt must not leave on a stuck or done check (review) |
| D12 | loops and the project's lesson file live in `<agent dir>/pi-warden/` | `<PIG_HOME>/pigpen-warden/` (`loops/<hash>/<session>.json`, `prefs/<hash>.json`, same hashes and JSON layout) | the port's own data folder (D6); nothing is read from or written to the original's |
| D13 | a failed preference scan or an unreadable loops file at session start or resume is a `console.warn` | not surfaced there (a Go extension has no console); `/warden prefs` and `/warden loops` report the error as the original's commands do, and the compaction and end-of-run hooks trace it | no console in a Go extension |
| D14 | JavaScript regular-expression semantics in `prefs.ts` | RE2 with a translation (`prefs_regex.go`: Unicode `\s`, line-terminator-free `.`) and hand-written replacements for every lookahead, lookbehind and backreference; verified on 5,594 messages by `prefs_differential_test.go`. Edge cases that remain different: `(?i)` also folds U+212A and U+017F in RE2 (not in JavaScript); `[^\n]{0,40}` counts runes, not UTF-16 units; the `m` flag recognises only `\n` as a line break; `Date.parse` is read for the ISO 8601 spellings Pi writes only; the lesson file's `JSON.stringify` keeps U+2028 literal where Go writes it escaped | RE2 |
| D15 | desktop notifier commands run with Node's `execFile` (title and body in `PI_WARDEN_TITLE` / `PI_WARDEN_BODY` for PowerShell and configured commands) | the same, through `os/exec` in the extension process; a configured command is spawned inside the hook and only the wait for its exit is off the handler, the probe chain and built-in notifiers run in a goroutine | `Context.Exec` mirrors `pi.exec`, which takes no environment option, and `execFile` (not `pi.exec`) is what the original does |
| D16 | the index lives in `<agent dir>/pi-warden/index/` (`PI_WARDEN_INDEX_DIR` / `PI_CODING_AGENT_DIR` move it) | `<PIG_HOME>/pigpen-warden/index/` (`PIGPEN_WARDEN_INDEX_DIR` moves it, a `~` prefix expands, and the folder `pigpen-warden/index` is added below it as upstream adds `pi-warden/index`); file names, the 12-digit project key and the JSON are the original's | D5 and D12 |
| D17 | a TypeSafe `401`/`403` is saved in the auth state file, and the conscience traces `key_rejected` from then on | no such file: the key is read from the environment; a rejected request is one judge error (`skipReason: error`, category `network` or `auth`). A missing key traces `no_key`, missing consent `no_consent`. The case at `extension.test.ts` 4417 is a named skip for its first half | D3 |
| D18 | skill frontmatter is parsed by a full YAML parser | `frontmatter.go` reads the top-level mapping of the YAML subset skill files use (plain, single and double quoted scalars, `\|` and `>` blocks, comments, flow and nested values kept as non-strings). Only `name`, `description` and `disable-model-invocation` are read; a document it cannot read is `load_failed`, as invalid YAML is. No new dependency | decision: no third-party YAML for three keys |
| D19 | file identity (rule 4) compares `dev`, `ino`, size and mtime | the same on Linux and macOS; on Windows `dev` and `ino` are not read (Go's `FileInfo` has no file index without an open handle), so size and mtime decide | platform |

### Deviations of the configuration, trace and panel slice

| # | Original | Port | Why |
|---|---|---|---|
| C1 | a project file may switch warden on (`enabled: true`) | it may switch it off, never on: `enabled: true` is ignored with one config warning | D1: warden is off until the user runs `/warden enable`; a repository must not start it |
| C2 | `typesafe` (consent) and `typesafeBackend` (a name or an endpoint object) | `consent` and `backend`: `""`, `typesafe` or `own-model`; any other value (including `openrouter`, `commandcode` and endpoint objects) is refused: judgments stay off, one notice, and `/warden status` quotes the reason | D2, D3 |
| C3 | the user file is `<agentDir>/pi-warden/config.json`, the project file `<cwd>/.pi/pi-warden.json` | `<PIG_HOME>/pigpen-warden/config.json` and `<cwd>/.pig/pi-warden.json` (`.pi/` under `PIG_USE_PI_DIRS=1`); the project file keeps its name, so one repository file serves both extensions | D5; `host-dirs.ts` has no PiG counterpart for the agent directory |
| C4 | `JSON.stringify(raw, null, 2)` keeps insertion order | the file is written with keys sorted; a command writes only the keys it changed (`SaveConfigChange`), so unknown keys and values the user did not touch survive | Go maps; the original also writes only what its command sets |
| C5 | `PI_WARDEN_TRACE_DIR` | `PIGPEN_WARDEN_TRACE_DIR` | D6 |
| C6 | `completeConfig` tests `typeof value === "number"` on a stale config module | a zero section means "missing" (all-zero struct, nil `notify.command`, empty enum); a zero number is a legal setting for `shouldProceed.threshold`, `escalationThreshold`, `intentMismatch`, `softThreshold`, `offTask.steer`, and `learning` and `prefs` are never reported missing | Go has no `undefined`; the stale-module-graph failure itself cannot occur in one Go package |
| C7 | conscience numeric settings keep a fractional value | truncated (the Go fields are integers; nothing reads the fraction) | types |
| C8 | warning text for an object-valued rule setting prints keys in insertion order | keys sorted | D10 |
| C9 | `/warden config set` writes anything, including consent | the same: `/warden config set consent typesafe` grants consent without the disclosure dialog, exactly as upstream's `config set typesafe true` does; the user typed it | faithful; named so a reviewer sees it |
| C10 | the config panel is opened by click, the status line is a click target (`MouseRegion`), `closed` settles when the overlay is removed | the sidebar opens by shortcut or `/warden trace`; the status widget's rows are static host rows, not a click target; closing from outside (second toggle) uses `OverlayHandle.Hide()` | SDK needs 1 and 2 below |

SDK needs found: (1) a `Context.Custom` call returns only when the component returns `Done`; Pi's `ui.custom` promise is
settled by the factory's `done()`, which an extension can call from anywhere. The port runs `Custom` on its own
goroutine with the command's retained context (the SDK supports a host call that outlives its handler) and closes an
open panel with `OverlayHandle.Hide()`; whether the host also settles `Custom` on `Hide` is not observable from the SDK,
so a closed panel's goroutine may wait until the session ends. Wanted: a `Done()`-style close function from the
component's constructor, or `Hide` settling the call. (2) widget rows cannot carry a mouse region (Pi's `MouseRegion`
component): wanted: mouse handling for `SetWidget` content. (3) the SDK has no way to learn the project directory name
(`.pig` or `.pi`) or the agent directory: the port reads `PIG_USE_PI_DIRS` itself.

## Results

- Go: `go test -race` on the Package passes (twins, differential, backends, fake host, skipped list);
  `GOOS=windows|darwin|linux go vet` clean.
- Differential (`testdata/diff_corpus.json`, regenerated from `port/oracle` by `port/gen-corpus.ts`): 2,552
  commands, the port equals the original on the pattern hits (id, severity, label), the read-only shortcut, the visible
  effect, the data-text stripping and the redaction, on every one.
- The judge path is tested against a fake TypeSafe server (`faketypesafe_test.go`) and a fake model registry; no test
  reaches the real service, and none reads a credential.

- **Real host, real Binary**: the `pig-warden` Piglet built with `pig piglet build --format binary` (fused Go; the build is not
  byte-reproducible, so no hash is pinned here), driven over RPC by `port/live-smoke/smoke.sh` (a scripted model asks for
  `git push --force origin main` with warden enabled offline): the call is held, the tool result the agent reads is the
  steer text, and the status line reads `warden: offline · steer · 1 checked, 1 held`. `/warden status` and
  `/warden test` were run through the same Binary.

- **Mutation check of the layer-1 tests** (`port/mutate.py`, results in `port/mutation-results.txt`): 31 deliberate defects (consent
  bypass, guard on while off, hold that does not block, thresholds, redaction, config mode, budgets, prejudgment reuse,
  cancellation, error reporting, ...; 8 added by the review: redaction of the stuck/done task, the write excerpt and the
  earlier messages, own-model budget, no retry, environment consent not persisted, status line); 30 killed, 1 equivalent
  mutant kept with its reason (`judge-when-disabled`).
  The tools slice adds 21 (loops cap, drop without a reason, redaction, session id escaping the folder, reminder repeated / starting a turn / not remembered,
  `prefs.inject` ignored, injection repeated on resume, weakening / one-session / stale / unconfirmed / forgotten preferences, `prefs.enabled` ignored by the tool,
  forget not persisted, the current session read by the scan, `compactAppendix` ignored, saved outputs uncapped, recall listing checks, the error line chosen from the front): **21 of 21 killed**, none equivalent.

## Performance (tools slice, 0.1.3)

Measured on imladris (Intel Xeon 6746E, CPU set 96-151 shared with other lanes; load average 68 to 300 during the runs, so the CPU set was not idle and the figures are not noise-free). Go: `go test -bench . -benchmem -count=10` (`extensions/warden/tools_bench_test.go`), median of 10; original under Node 24.19: `port/bench-original.ts`, median of 11 runs (sequential after the Go runs, not interleaved).

A first profile put about two thirds of the time in `Redact` and most of the rest in patterns that cannot match a text without their trigger words (Go's `regexp` has no literal scan; 80% of the time was `tryBacktrack`/`bitState`, 7% `unicode.SimpleFold`). `prefs_fast.go` adds necessary-condition prefilters (`redactFast` skips the fifteen Redact passes when no rule's trigger is present; trigger words and first-letter tests guard the question, imperative, `I said`, `next time`, temporary, past-form and task-reference patterns; `startsNegation` replaces a case-folded regexp). Each is a superset test, so the 5,594-message and 2,500-clause differential still agrees and `TestRedactFastEqualsRedact` compares `redactFast` with `Redact` on the original's 2,552 commands and the messages. A rule added to `Redact` must add its trigger to `credentialTriggers`.

| Path (same input) | Go before | Go now | Original under PiG's Node (tsx) |
|---|---|---|---|
| `warden_loops` add then list, through the file | 250 us, 979 allocs | 311 us, 71 KB, 939 allocs (load was higher in this run) | 443 us |
| `warden_remember` (`recordLesson`, no file) | 84.5 us, 93 allocs | 13.1 us, 2.3 KB, 48 allocs | 16.2 us |
| `extractPreferences` of a two-sentence message | 122.8 us, 133 allocs | 15.8 us, 2.3 KB, 49 allocs | 14.2 us |
| `warden_recall` (`recallText`) | 79.1 us, 213 allocs | 4.9 us, 1.2 KB, 15 allocs | 12.1 us |
| construct the extension and register its tools (`New`) | 23.3 us, 111 allocs | (unchanged) | import 1.66 to 1.83 s (cold TypeScript load through tsx of 1.18 MB of source), factory 4.8 ms median |

- `extractPreferences` is 11% above the original at the median (15.8 against 14.2 us): at the edge of the 10% bar and inside the noise of a host at load 180; the remaining cost is the regexps that do match and the sentence splitting. The other three paths are at or below the original.
- Binary: a `package main` that only runs `warden.Extension()` (linux/amd64, `-s -w`) is 9,081,095 bytes, against 8,761,607 for 0.1.2 (the refusal release): +319,488 bytes (+3.6%), measured before the prefilters (they add a few hundred bytes of code). The original ships no binary (its `src` is 1,178,572 bytes of TypeScript).
- Package init allocation is held under the existing limit by `TestPackageInitDoesNotCompileRegexps` (a `lazyREBuilt` assembles prefs patterns on first use). The previous release's tool latencies do not exist (it registered no tool); its construction was not benchmarked.
- No blocking host call on the input or render path: the scan (200 files, 30 days, a 250 ms read budget), the loops file and the lesson file are read in the `session_start`, `agent_end`, `session_compact` and tool/command handlers only; the recall probe runs in a goroutine started at session start.

## Supervision slice (call waste, subagent triage, stuck evidence, event handlers)

Scope: `waste.go`, `subagent.go`, `evidence.go`, `supervision.go`, the `stuck.go` / `results.go` changes, and the
registries of the `message_start`, `message_update`, `message_end`, `input` and `agent_settled` events.

- **Twins**: `waste.test.ts` 9 of 9, `subagent.test.ts` 5 of 5, `stuck-evidence.test.ts` 11 of 12 (the config case has
  its project-file and junk-value legs in the configui lane's loader; the default and user-file legs are
  `TestStuckEvidenceDefaultsOnAndTheUserFileTurnsItOff`), `stuck-done.test.ts` 24 of 24, `steer-delivery.test.ts` stays
  one named skip (it tests Pi's `createAgentSession` and its faux provider: how many model requests a steer costs; no
  warden code is under test), and 7 `extension.test.ts` cases (`subagent reports`, three `stuck-loop diff`, three
  `waste`); the three `stuck-loop diff` cases that assert the context lane's duplicate note stay named skips with that
  reason, and their stuck-diff halves are Go tests.
- **Default changed**: `stuck.evidence` is on by default, as upstream (`config.ts`: `evidence: true`). The earlier Pigpen
  release forced `false`; the judge request now carries the `evidence` section unless `"stuck": {"evidence": false}`.
- **Subagent reports** are collected from `message_end` events of custom messages (`customType` in `NOTIFY_TYPES`,
  string content) and triaged on `agent_settled`, bounded to the newest 8 unseen, which is what upstream's scan of
  `getBranch()` reads. SDK need: `BranchEntry` has no `CustomType`, so the branch cannot name a custom message's type
  (Pi API mirrored: `SessionManager.getBranch()` entries of `type: "custom_message"` carry `customType`). One
  difference follows: upstream's first scan after a resume also triages custom messages that were already in the
  resumed branch; the port sees only the messages that end during the session.
- **Event subscription**: one handler per event, which runs the pieces the lanes registered in upstream's order (the
  order is the comment at the top of `supervision.go`). `message_start` and `message_update` are subscribed only when a
  lane registers a piece (the runaway guard does, on this stack), because a host asks the extension and waits once per
  message and once per streamed token; `message_end`, `input` and `agent_settled` always are. The runaway guard's two
  handlers and the rules lane's `InitBusy` block moved onto the registries.
- **Scenarios** (all equal to the original under Pi 1402973a): `waste-sleep-poll-note`, `waste-sleep-chain-no-note`,
  `waste-paging-note`, `waste-far-apart-reads-no-note`, `waste-search-note`,
  `stuck-evidence-judged` (the judge request carries the parsed failures, the same-failure link and the digest) and
  `stuck-loop-diff-judged` (the third result is replaced by the diff note). Not a scenario, with the reason: the recheck
  note (a pipeline of two shimmed commands starts them in a different order under Pi and PiG, so the exec events differ
  for reasons outside the extension), the waste
  tip (the model request the harness records carries no system prompt) and subagent reports (no pi-subagents in the
  hermetic host injects `subagent-notify` messages): both are layer-1 tests (`supervision_test.go`) plus twins.
- **Results** (stacked on `port/warden-security-2`, version 0.1.7): the go package passes `go test -count=1`, `go test -race
  -timeout 90m -count=6 .` (902 s), `go vet` for linux, windows and darwin (each with `go env GOOS` printing the target). Twins:
  32 new (waste 9, subagent 5, stuck-evidence 11, extension.test.ts 7), the package has 577 twins and 687 named skips.
  Layer 1: `supervision_test.go` (24 tests through the fake host: waste notes and tip, the stuck-loop diff and its limits,
  stuck evidence reaching the judge, subagent triage and wake window, the input and piece registries).
  Mutations: 34 in `port/mutate.py` (waste, evidence, stuck diff, subagent, registries): 32 killed, 2 equivalent mutants
  documented in `EQUIVALENT`; 13 in `port/mutations.json`, each run against its own scenario only and killed
  (`pigeq mutate`). pigeq: `check` on all 62 scenarios plus the three pseudo checks, twice: 57/64 and 62/64, every
  failure an event-order flake (below) that passes in the other run; the 7 scenarios of this slice alone, three runs: 7/9, 9/9,
  7/9 (counting two pseudo checks), all failures of the same kind.
- **Known harness flake**: on the shared host, about 3 to 10 percent of scenarios of `pigeq check` fail in a run with the
  first difference at an event where the Go lane records the model request (`llm`) before `turn_end` or the user
  `message_end` (or a shimmed command's `exec` before another's): an ordering race between the host's RPC stream and the
  scripted model. It hits scenarios of every lane, with this slice's event handlers subscribed or removed (checked: all five
  subscriptions disabled, 2 to 6 failures in 4 runs), and each failing scenario passes in another run. A mutation killed
  at one of those events cannot be told from the flake, so the scenario-level mutations above are also listed with the event
  of the difference, and the layer-1 mutations are the deterministic evidence.
- **Findings**: the harness's PATH holds only the commands a scenario declares, so a scenario that needs `sleep` or
  `cat` declares it (`commands`, `canned`) or uses shell builtins; an undeclared command's stderr and the stdout of the
  same call arrive in a different order under Pi and PiG.

## Equivalence (live: Pi original vs Go port)

`port/scenarios` holds 26 offline scenarios (19 for the guards, 7 for the tools slice; a sixth is in `port/scenarios-b4`, see below), `port/golden` their traces recorded **from the original under Pi 1.3.0.1402973a (the 19 earlier ones under Pi 0.87.1/1.0.1)**
(`port/eq-entry.ts` loads the vendored, unmodified `port/oracle` from source; `npm ci --ignore-scripts` in
`port/oracle` first). Every scenario sets `"env": {"PIGPEN_WARDEN_ENABLED": "1", "PIGPEN_WARDEN_STEER_VISIBLE": "0"}` (the
per-scenario `env` map of pigeq; the original ignores both), declares `git` as missing and passes
`--tools read,bash,edit,write` (the tools-slice scenarios add the three warden tools). Held: force push (`--force` and `-f`), `rm -rf ~/...`, `chmod -R 777 /`, `DROP DATABASE`,
`curl | sh`, a hard reset; a sibling pair with one held; a hold released by a user reply that approves it; a repeated
failing call steered (quick repeat, then the stuck steer); identical calls; a changing failure and a repeated
mutating call (both quiet); read-only and ordinary commands, secrets and writes (allowed offline). The tools slice:
the three tools in the model request; `warden_loops` add, list, done, drop and every refusal text, and `/warden loops`; the
open-loop reminder (once per unchanged list, for the next turn); `warden_remember` recorded, refused (weakens a check,
task-bound, not standing, too long), confirmed in a second session and quoted at the start of a third; `warden_recall` after a failure;
the compaction evidence appendix after `/compact`.
Results: **the 19 guard scenarios: the original under Pi == the original under PiG's Node runtime (host check) == the Go
port under PiG (Go directory) == the built `pig-warden` Binary (`check --builtin`)**, event for event. **The five tools-slice scenarios in `port/scenarios`
(`tools-listed-in-model-request` 17 events, `loops-add-list-done-drop` 116, `loops-reminder-once-per-list` 72, `recall-after-a-failure` 38,
`compact-evidence-appendix` 34) pass `pigeq run`: Pi == PiG Node == Go port, goldens recorded from the original.** (Under a heavily loaded host a
run can reorder a `turn_end` and the next model request in the original's own two lanes; the host check then refuses to record, and the run is repeated.)
The sixth, `port/scenarios-b4/remember-recorded-then-confirmed` (golden in `port/golden-b4`, recorded with `--skip-host-check`), fails the host check
and `pigeq check`: a message the extension sends from `session_start` after an async scan reaches the trace before the response of the
`/eq-new` command under PiG and after it under Pi (PiG bug B4, reported to the PiG owners). It stays out of `port/scenarios` until B4 is fixed.

What the scenarios deliberately do not pin, and why (each is the parity claim's edge, not a normalizer change):

- `capture` leaves out `extension_ui_request` (gap G2, deviation D4). Everything the agent and the model see is compared: the
  tool result text of a held call, the steer messages, the model requests, the lifecycle, the ordering.
- `PIGPEN_WARDEN_STEER_VISIBLE=0` matches the original's default (`display:false`); the port's default is visible
  (D4). The steer's `customType` is the original's `pi-warden-steer` so a session reads the same on either.
- `--tools read,bash,edit,write` keeps the model's tool list equal for the 19 guard scenarios: the original also registers
  `warden_remember`, `warden_loops` and `warden_recall`, and PiG's default set adds `powershell`, `grep`, `find`,
  `ls` (a host difference). The six scenarios of the tools slice list the three tools in `--tools` explicitly, so
  their descriptions and JSON schemas are compared in the model request.
- The disabled replies (`not available: pi-warden is off`, `not recorded: standing preferences are off`) and
  `prefs.enabled: false` are compared in `tools-disabled-replies` and `remember-with-prefs-disabled`: the original's config
  goes in `agentFiles` (`pi-warden/config.json`), the port's in `homeFiles` (`pigpen-warden/config.json` under
  `PIG_HOME`), which needs the pigeq with the `homeFiles` field. Not reachable through pigeq: `/warden prefs` output and
  the `pi-warden-prefs` message of a project with earlier sessions of known content (the scan reads Pi's own session
  files, whose names and dates a scenario does not control; `/warden prefs` prints real dates). The fake-host tests
  (`tools_extension_test.go`, with a fixed clock) and the twins cover them.
- The port is off unless enabled (D1); the disabled default is proved by `TestWardenIsOffUntilYouEnableIt`.

Mutations (`port/mutations.json`, 16 defects; `pigeq mutate`): 12 killed by scenarios. Four (`quick-repeat-*`,
`differing-output-still-repeat`) survive at the scenario layer and are killed by the stuck twins in layer 1
(`port/mutate.py`, `port/mutation-results.txt`); pigeq's `--unit` cannot run them because its temporary `go.work` does
not include the shared `components/typesafe` library module (finding below).

## Context saver (PR 5)

What this slice ports and how it was checked.

- **Twins** (`// twin: tests/<file>:<line>`): `saver` 6 of 6, `dedupe` 7 of 7, `excerpt` 6 of 6, `filter` 8 of 10, `relevance` 21 of 23, the compression half of `output` 8 of 14 (the other six are the security half's), and 34 of `extension.test.ts` (the saver, the duplicate note, repeated runs, recall kinds, the filter, `session_before_compact`, and the credential and injection notices of the tool_result pipeline; `context_extension_test.go`, `context_security_extension_test.go`, through the fake host). The four config cases (`filter` 33 and 48, `relevance` 228 and 335) assert the config loader's parsing and clamping and stay named skips with that reason: the loader is the config lane's. `extension.test.ts` 355 and 3837 are partial twins: the host cancelling a request mid-pipeline cannot be driven through the fake host, so the unavailable-storage half of 355 and the no-consent, skipped-provider and judge-failure legs of 3837 are twinned and the abort legs are covered by `relevance_test.go` and `filter_test.go` (a cancelled context).
- **Go-only tests**: `TestRedactPrefilterNeverSkipsARedaction`, `TestExcerptGatesNeverHideAMatch` and `TestDiagnosticLineEqualsItsRegularExpression` compare each speed-up with the code it replaces; `TestDedupeRunBoundariesAreTwentyLinesAndFifteenHundredCharacters`, `TestDedupeTrailingSpacesOfTheStoredTextDoNotBreakAMatch` and `TestCompressionLearnerPrefersTheRetentionTheAgentDidNotGoBackFor` close three weaknesses the mutation check found in the upstream cases.
- **Equivalence** (`port/scenarios`, goldens recorded from the original under Pi 0.87.1; `pigeq check` over the stacked tree's 72 scenarios: 69 equal in the full run and the three others equal in a single run, see Rebase below): ten scenarios for this slice. Offline: a token in a tool result is masked and the banner leads and trails it; a documented fixture key is left whole; two calls that print the same large output (the second becomes a duplicate note with a stored copy); a result that repeats a block the agent already has (one pointer line). Judged, against a fake judge both lanes call (G1 below): a build log compressed to its errors and summary with a stored copy and then a copy of it as a duplicate note (the request body of the output check is compared field by field); a compiler log recognised as `tsc` (exact error lines); a retention verdict under the confidence gate (output kept whole); an injection verdict (banner at both ends, no steer); a judge answering 503 (output kept whole); and the context filter on (the filter's chunk request and the kept passages). The scenario `normalize` rule masks `pi-warden-output-<random>` (the original itself randomizes it per run).
- **Blocked** (`port/scenarios-blocked`): `judged-relevance-compaction-replaces-the-summary` (manual `/compact` with relevance compaction on). The judge's requests, the kept and left-out units and the summary text are equal in the original under Pi, the original under PiG and the port; what differs is the host: the `compact` response carries `estimatedTokensAfter`, 12941 on PiG and 12938 on Pi for the same compaction (a host difference to report, not to normalize), and a random `firstKeptEntryId`.
- **Mutations**: layer 1 (`port/mutate.py`, 40 defects of this slice, `MUTATE_RUN` restricts the run to the slice's tests): 39 killed, 1 equivalent (`banner-only-on-first-block`, reason in the script). pigeq (`port/mutations.json`, 10 defects of this slice: masking skipped, banner not leading, notice dropped, duplicate-note and pointer-line wording, footer missing, confidence gate ignored, format parser never selected, repeated runs never cut, duplicates never noted): 10 of 10 killed by the scenarios.

- **Rebase onto the supervision lane (context-2)**: this slice is stacked on `port/warden-supervision-3` (which carries tools, guardcore, security, rules and the shared cores). `onToolResult` composes the three lanes in upstream's order: masking and the output pipeline (`toolOutput`, context), call-waste record, the run memory and stuck window, the stuck-loop diff on the pipeline's content, the quick-repeat and stuck steers, then the call-waste note. The supervision lane's `stuck-loop-diff-judged` scenario now sets `security`/`context` off in its home file as well, because the port reads the home file and the original reads the agent file (the security and context pipelines are now wired, so the port's default-on security judgment would otherwise add a request the original's scenario config forbids). The `message_end` dedupe is one piece of the supervision event registry (`w.sup.onMessageEnd(w.contextMessageEnd)`), after the learning slot as upstream orders it. Names that clashed with other lanes were renamed on this side (`reExcerptEslintFile`, `reExcerptEslintTotal`, `reExcerptDiffHeader`, `reRelevanceHeading`); `isASCII` is the single copy in `jsstr.go`. Full `pigeq check` of the stacked tree: 7 failures in the first full run, 6 of them the known `turn_start`/`turn_end` ordering flake of the original under PiG (all passed when run singly, `recall-after-a-failure` and `sensitive-path-note-steered` on a second single run) and one real one (`stuck-loop-diff-judged`, the home-file config above), which passes twice after the fix. Layer-1 mutations: 39 killed, 1 equivalent. pigeq mutations (each against its own scenario): 10 of 10 killed.

### Performance (imladris, CPUs 96-151 shared, load high; Go 1.27.1, Node 24.19.0)

Inputs: a 200 KB build log (3,500 lines, one error line), a 100 KB `tsc` log (2,000 lines), a 5,000-line report pasted again, a 120-call span of 5 KB results. Go: `go test -bench` (`extensions/warden/context_bench_test.go`), median of 5 runs. Original: `port/bench/bench-original.ts` bundled with esbuild and run under Node, median of 25 iterations in each of 3 processes. The two are not interleaved (the host is shared); the previous release comparison below is.

| Path | Go port | Original (Node) | Before the fixes |
|---|---|---|---|
| compress a 200 KB output, head, diagnostics, tail | 0.91 ms, 15 allocs, 101 KB | 0.40 ms | 19 ms |
| compress a 100 KB `tsc` log with the parser | 0.68 ms, 37 allocs | 0.36 ms | 7.9 ms |
| content key of a 200 KB output | 0.45 ms | 0.61 ms | 0.94 ms |
| repeated-run index and find, 5,000 lines | 4.1 ms, 105 allocs | 4.5 ms | 5.2 ms |
| filter chunks and batches, 200 KB (redacts every chunk) | 5.1 ms, 5.3k allocs | 10.8 ms | 235 ms |
| relevance requests, 120 calls | 26.5 ms | 507 ms | 511 ms |
| relevance summary render, 120 calls | 2.8 ms, 3 MB | 1.6 ms | 21 ms |

The fixes (each tested against the code it replaces): `Redact` returns early for a text none of its rules can match (`redact_prefilter.go`; it was 1 µs a character of regexp scanning on any large text, so it also speeds up every judged request of every guard), the ASCII fast paths of `utf16Len` and `utf16Slice` (eight bytes a step), a one-pass byte scan in place of the diagnostic regexp, a literal gate in front of each per-line pattern of the excerpt parsers, and the dedupe index without a slice per hash. Three paths are still slower than the original by more than 10%, each by about a millisecond: the generic compress (+0.5 ms; of its 0.91 ms, 0.35 ms is the byte scan for the seven diagnostic words, 0.1 ms the ASCII checks, 0.05 ms the line split), the `tsc` parser (+0.3 ms; half of it the literal gates' `strings.Index` calls, the rest the patterns on the lines that carry a diagnostic) and the summary render (+1.2 ms; 3 MB of string concatenation for 120 results of 5 KB). Each sits beside the judge request that follows it in the same code path (hundreds of milliseconds) and none runs on the input or render path. OWNER DECISION NEEDED whether that is accepted or the remaining parsers are rewritten by hand.

Against the previous release (`origin/main`, 0.1.1; a probe `main` that calls `warden.Extension()`, stripped, 31 interleaved runs): binary 8,446,215 to 9,171,207 bytes (+8.6%, this branch also holds the tools, steering-core and security slices); package init 83 KB, 426 allocs to 61 KB, 451 allocs, 0.16 ms (the regexp translation of `jsRE` and `prefsRE` and the parse of `questions.json` now happen on first use, which is what keeps `TestPackageInitDoesNotCompileRegexps` green: it failed at 165 KB with the slices merged); process start to a built extension 7.34 ms to 6.12 ms (noise: the box is shared); `Extension()` itself 14 to 52 µs. The original's module load under Node (`registration-original.ts`; the factory call fails against the stand-in, so only the load is timed) is about 350 ms of wall time per process, of which about 40 ms is Node's own start.
Configuration slice (lane configui): four more scenarios, `config-project-turns-repeat-steer-off`,
`config-project-cannot-weaken-action-guard`, `config-hostile-project-file` and `config-set-command-changes-behavior`. Goldens recorded from
the original under Pi 1.3.0+1402973a; host check pig-ts == pi-ts and `pigeq run`/`check` pig-go == pi-ts on all four (17, 17, 38 and 40
events). The workspace is trusted in RPC mode on both hosts (the project file changes behavior in both lanes). They write the project file
for both hosts (`.pi/` and `.pig/`); the user-file behavior is proved by the `config set` scenario, which writes each lane's own user file.
Four mutations (`cfg-project-removes-tools`, `cfg-project-weaker-switch-taken`, `cfg-file-not-reloaded`, `cfg-stuck-nudge-ignored`) are
killed by these scenarios under `pigeq mutate`. What scenarios cannot show: `/warden config get`, the status line and the panels (UI requests, gap G2),
the shortcut (not visible to pigeq; `TestTheTraceShortcutIsRegisteredFromTheUserFile` proves it at layer 1).

## Named gaps

| # | Gap | Why | Closes when |
|---|---|---|---|
| G1 | The original can be pointed at a fake judge: its own `typesafeBackend` endpoint (loopback `http:` is allowed, `defaultModel` is required) in `agentFiles`, and `TYPESAFE_BASE_URL` for the port: the output check, the context filter and the compaction requests are compared field by field with the original (this slice). Other judged guards are compared by their pinned question texts and the fake-server tests until their lanes add scenarios | the earlier claim that the original cannot reach a fake server is wrong for this client | closed for this slice |
| G2 | The **UI** the two sides draw (status line, widget, notifications) is not compared live | deliberate UX deviation (D4): the port shows a status line, a trace widget and a notice per finding, the original draws its own widget and rules-file warnings; scenarios therefore leave `extension_ui_request` out of `capture` | the UI is proved by the fake-host tests and the Binary smoke (`port/live-smoke/smoke.sh`) |
| G3 | Not compared live: `hasUI == false`, the judged (TypeSafe / own-model) path, `/warden` commands, the done-check (it needs a judge to score a claim) | the original cannot be pointed at a fake TypeSafe server from the hermetic host; its `/warden` differs (D5) | fake-host and fake-server tests, the pinned question texts, the differential corpus |
| G4 | The kept call input of a relevance summary is written with sorted keys (`{"command":..,"timeout":..}`), the original writes insertion order | the host decodes a tool call's arguments into a map, which keeps no order | the SDK carries ordered arguments |
| G5 | The loop checks (stuck, repeats, done evidence) run after the output pipeline, one after the other; the original starts the stuck judgment first and awaits it last | the trace records come in the original's order, a request that both need goes out one after the other | `results.go` runs the stuck judgment concurrently |
| G6 | Stored full outputs of the previous session are removed at session start as files, their temporary directories stay | the original removes the path it recorded, the file | an owner decision to remove the directories too |
| G7 | A line longer than `filter.chunkChars` is split at a UTF-16 unit; inside a surrogate pair the port writes U+FFFD, the original a lone surrogate | Go strings cannot hold a lone surrogate | never (language) |
| G8 | `session_before_compact` reads the aborted request as `ctx.Done`, and a cancelled request mid-pipeline is not driven in a test | the SDK has no per-event AbortSignal | the fake host can cancel a request |
| G9 | The compaction scenario is blocked: PiG's `estimatedTokensAfter` after a compaction differs from Pi's by 3 tokens (12941 and 12938) | host difference | the host counts like Pi |
| G10 | Under load about one scenario in six of a full `pigeq` run shows `turn_end` after the next model request in the original under PiG (any scenario, old ones too); each such scenario passes alone | host event ordering against the scripted model server | the host orders them like Pi |

(The former gap "no live equivalence lane" is closed: see Equivalence above.)

## Findings about the hosts made while porting

1. **`Context.GetBranch` drops assistant messages with tool calls under Pi 0.87.1**: the SDK reads a toolCall
   block's `arguments` as a string (the SDK's `BranchEntry.UnmarshalJSON` in `context.go`), Pi writes an object
   (pi-ai 0.87.1 `ToolCall.arguments: JsonObject`), so decoding the block array fails as a whole and the entry keeps no
   text and no tool calls. Reported to the SDK owners; `sdk-args_test.go.txt` here is the reproduction and the
   skipped test is `TestSkippedSDKToolCallArgumentsAsObject`. Effect until fixed: no plan (intent-mismatch never asked)
   and siblings judged one by one.
2. The Skill's `fakehost_test.go` template (the Package's derived copy is `sdkhost_test.go`) sends commands as `{name, args}`; the SDK reads `tool` and a string argument
   (`unknown command:`). It has no way to feed the session mirror (`watchSessionLog` by cursor, `state_update`) or model
   stream events. The Package's copy is patched and extended (`Host.Notify`).
3. `Context.ConfigHome` reads `PIG_HOME`, not a host-provided value.
4. PiG re-serializes a tool call's arguments with Go's HTML-escaping JSON (`>` becomes `\u003e`, also `<`, `&`) in the
   history it sends the model; Pi keeps them literal. A scenario with a command containing those characters differs on the
   model request between the lanes. The scenarios avoid them; the difference is a host finding for the SDK/host owner.
5. pigeq `mutate --unit` builds the temporary `go.work` from the SDK and the port directory only, so a port that uses a
   shared library module (`components/typesafe`) cannot run its unit layer under it.
6. pigeq's host check with a Binary as the host (`--builtin`) loads the port into the original's lane too; use a plain `pig` for
   the host check and `check --builtin` only for the Binary.

7. Steer-bearing scenarios are timing-sensitive on a loaded host: on imladris (load above 150) `two-calls-one-held` failed three of four
   `pigeq check` runs on the same difference (an `llm` request recorded before the `turn_end` event) and passed the other; a scenario of the same shape
   (stuck.repeatSteer false alone, 40 events) alternated the same way and was left out for that reason. Not re-run on a quiet host, not normalized;
   reported to the steering owner.

## Re-verified on PiG 0.4.1 (porter-verify)

The golden traces were recorded again from the original under Pi 1.0.1 (host check: identical under PiG's Node runtime), with PiG 0.4.1 content (`5f948f86a`, `pig --version`
`0.3.1+1.0.1`) and normalizer v3, because Pi 1.0.x changed the trace format: a `prompt` response now carries
`data.disposition` (Pi 0.99.0, #9098). The bash tool's results now carry `structuredContent` (15 events), whose `wall_time_seconds` the harness normalizes (N6). The record reads `port/eq-entry.ts` with the modules it re-exports, so `sdk-gaps` checks the vendored original again (it said "not an extension" before). An event-by-event diff against the previous traces shows no other
difference, and `pigeq check` passes on the Go port. Details: `docs/plan/progress/porter-verify.md`.


## Rules lane (port/warden-rules-2, stacked on port/warden-tools-1)

Ported modules: `rules.ts`, `rules-file.ts`, `rules-lint.ts`, `rules-log.ts`, `rules-report.ts`, `rules-audit.ts`,
`rules-calibrate.ts`, `turn-rules.ts`, `shell-writes.ts`, `init.ts` and their extension hooks.

Twins: rules 42 of 43 (the shipped-examples case needs `examples/`, which ships in the npm package but is not vendored),
rules-audit 18, rules-calibrate 16, rules-lint 13, rules-log 13, turn-rules 9, shell-writes 22 of 23 (the
`describeAction` case is the guard lane's), the rules-file, `checkPiWardenMissing` and init cases of `violation.test.ts`
and the init case of `violation-pipeline.test.ts`. Not twins, with per-case reasons in the generator: the seven
`rules-replay.test.ts` cases (`scripts/rules-replay.mjs` is a maintainer script outside the published package `files`,
verified against `port/oracle/package.json`), and the `extension.test.ts` rules cases, each named with the layer-1 test
in `rules_wire_test.go` that covers its wiring.

Deviations in this lane:

- `/warden rules check|audit|calibrate` and the judged paths say why no judge is available in the port's own words
  (no consent: run `/warden enable`, or set `PIGPEN_WARDEN_ENABLED=1` and `PIGPEN_WARDEN_BACKEND` headless; a backend that
  cannot start; the spent request budget) where the original names its key and `/typesafe login` (D1, D3).
- The rules log lives under `<PIG_HOME>/pigpen-warden/rules/` (D5), the audit report stays at `<project>/.pi-warden/rules-audit.md`.
- `/warden bench` stays refused until the audit lane wires it; `runBench` takes an optional usage-counter callback, and
  the Go judges expose none, so the bench says "unknown (no usage counters)" for tokens and cost.
- The `rules.*` hook `holds.recordRules` is the func variable `holdsRecordRules`; the learning lane installs it (`learning_wire.go`).
- `/warden` argument completion for `rules check|audit|calibrate|tune` is not registered (the SDK command takes no completion
  callback in this port).
- The `input` hook that blocks user messages while `/warden init` or `rules tune` runs belongs to the supervision lane;
  `warden.InitBusy()` is the flag it reads.
- `rules.sensitivePaths` notes are read in sorted glob order (a Go map has no insertion order).

Layer-1 findings: the choice `Probabilities` field was missing on judge answers (added in `judge.go`, `backend.go`); the
existing `jsRound` of the steering core is reused. The baseline snapshot of turn rules runs in a goroutine started at
`agent_start`; upstream starts its first git process synchronously, so a file written within a few milliseconds of
`agent_start` can be in the baseline (the tests wait for it, as upstream's do through the git shim).

pigeq: 5 scenarios (`sensitive-path-note-steered` and `rules-file-offline-write-runs`, `rules-offline-fallback-document-write-runs`,
`rules-offline-heredoc-write-runs`, `rules-offline-turn-rules-without-git-run-quietly`), goldens recorded from the original;
Pi original == original under PiG Node == Go port on all five. `pigeq check` over all 31 scenarios (19 guards, 7 tools, 5 rules): 29 equal in the full run; `two-calls-one-held` and `write-to-ssh-directory-runs-offline` differed once each at the turn-end/next-request order on a host at load average 85, and were equal in two single reruns each.
Mutations: 22 layer-1 rows (`port/mutate.py`, `port/mutation-results.txt`: the whole file, 93 rows, run on the stacked tree: all killed but `judge-when-disabled` (equivalent) and `disable-keeps-consent`, a tools-era row whose pattern no longer matches in `commands.go`; both are not this lane's) and two pigeq rows
(`rules-sends-an-empty-steer`, killed by `changing-failure-not-steered`; `rules-sensitive-note-not-sent`, see the PR body). `go test -race -count=24` passes.
The hot paths (rule matching, the store cache, shell writes) are benchmarked in `rules_bench_test.go`.

Judged rules behaviour (the rules request to the judge, steers, `rules check|audit|calibrate`) is proven by the twins and the
fake-server layer-1 tests only. G1 is disproved for the judge itself (a pigeq `servers` fixture plus `TYPESAFE_BASE_URL` and
the original's `typesafeBackend` config reach the original; recipe from the learning lane), but a write's rules request is
sent concurrently with the action request (upstream's design, kept by the port), so the fake server would see the two
requests in an order that is not fixed and its in-order `times` routes would answer the wrong question set. No judged rules
scenario was written for that reason; `rules check|audit|calibrate` send one request at a time and could be scripted, which
was not done.

`sensitive-path-note-steered` carries the original's config in `agentFiles` (`pi-warden/config.json`) and the port's in
`homeFiles` (`pigpen-warden/config.json` under `<PIG_HOME>`, D5), the pigeq `homeFiles` field of branch `pigeq-harness-gaps`:
pi-ts == pig-ts == pig-go (25 events), golden recorded from the original. One run in five under a loaded host
(load average ~200) showed the turn-end/next-request order differing at event 10; four reruns and three `check` runs were equal.

One scenario stays blocked in `port/scenarios-blocked/` (not run by pigeq): `init-prompt-reaches-the-model`. `/warden init` is
`sendUserMessage` + `waitForIdle`; under Pi (and the TS original under PiG's Node) the RPC `prompt` response arrives before
`agent_start`, with the Go port under PiG the response is held until the handler returns (after `agent_end`), so
`agent_start` precedes it (pi-ts == pi-ts; pig-go differs at event 0). This is the B4 ordering gap, not a port difference in
what the model is sent: the init prompt text is covered by the `TestInit*` twins.

Stacking notes: the supervision lane registered `w.sup.blockInput(w.InitBusy, ...)` in `newWarden`, and the waste detectors
read `shellWrites(command, ...).Writes` directly. `discover.go` is a copy of the audit lane's until it lands (commit `c0a805e`): drop it on stacking.

Stacking on the tools lane: the rules helpers `plural` and `dayMs` collided with the prefs lane's (`prefs.go`) and became `pluralOf` and the shared `dayMs`. `TestRulesAgentStartDoesNotWaitForTheSnapshot` and its neighbours failed now and then with "TempDir RemoveAll cleanup: directory not empty" because the baseline snapshot goroutine and the rules log's queued writes outlived the test (reported by the security lane): `rulesShutdown` now also waits for the snapshot and flushes the log, and the harness runs it as a cleanup. `agent_end` keeps upstream's order: the open-loops reminder (tools hook), then the turn rules pass, then the done check.

## Performance (rules slice, 0.1.4)

Measured on imladris (Intel Xeon 6746E, CPU set shared with other lanes; load average 85 to 125, so not noise-free). Go: `go test -bench` in `extensions/warden/rules_bench_test.go`, `-benchmem -count=10`, median of 10. No benchmark of the original was run, so there is no per-call comparison with it.

| Path | Go (median of 10) |
|---|---|
| parse a 40-rule `pi-warden.md` | 684 us, 84 KB, 950 allocs |
| rule set from an unchanged file (store cache) | 5.0 us, 641 B, 9 allocs (4.9 ms before the cache) |
| rules request for 40 rules | 178 us, 104 KB, 244 allocs |
| score a 40-rule verdict | 46 us, 30 KB, 94 allocs |
| shell heredoc write detection | 22.5 us, 13 KB, 85 allocs |
| user-rule pattern matching | 104 us, 2.7 KB, 145 allocs |
| describe a write target (runs `git check-ignore`) | 9.2 ms, 494 KB, 81 allocs (one process per target, as upstream) |

Binary: a `package main` that only runs `warden.Extension()` (linux/amd64, `-s -w`) is 9,605,383 bytes at this branch against 9,085,191 at `port/warden-tools-1` (4709961): +520,192 bytes (+5.7%), the rules code and the embedded question texts.


Not done in this PR (honest list): pigeq scenarios and goldens for the rules behaviours, the layer-1 and pigeq mutation
rows, the performance section's measurements, and the `go test -race -count=24` run; see the status file.

## Guard core: results and performance

- Layer 1 (imladris, tern-ci:v2): `go vet` clean for windows, darwin and linux; `go test` ok; mutations in `port/mutate.py`
  (74 added, 70 killed, 4 documented equivalents; `port/mutation-results.txt`).
- pigeq: 11 config-free scenarios (`sql-*`, `rm-authorized-by-the-prompt-runs`, `rm-unrelated-target-held`,
  `find-delete-not-authorized-by-the-verb`): `pigeq run` identical for the original under Pi, under PiG's Node runtime
  and the Go port; `pigeq check` passes against goldens recorded from the original. `run` also prints `exec-coverage`
  FAIL for `sqlite-adapter.ts:82 PRAGMA` (a command literal of the original's learning store that no scenario can
  declare; the same message appears for every scenario of the package, and `check`, which reads the port only, passes).
  Scenario-layer pigeq mutations (`port/mutations.json`, six rows): three killed (`sql-delete-local-held`,
  `sql-hosted-write-only-warns`, `sql-hosted-select-held`), three survive at the scenario layer
  (`sql-read-only-wrapper-ignored`, `rm-verb-never-authorizes`, `find-delete-authorized-by-verb`): a warning is a steer
  hidden in these scenarios, so the scenario trace does not tell the two apart; the layer-1 rows of the same names
  (`sql-commit-voids-nothing`, `authorize-segments-ignored`, ...) kill them.
- Config-dependent scenarios (pigeq 0.3.0 `homeFiles`, built from branch `pigeq-harness-gaps`; the original's config in
  `agentFiles` `pi-warden/config.json`, the port's in `homeFiles` `pigpen-warden/config.json`): six scenarios (`path-rule-*`
  four, `arming-*` two) record from the original under Pi, `pigeq run` identical on all lanes, `pigeq check` passes. In
  `path-rule-block-read-of-write-only-path` the record step's host check once failed on event order (user `message_end`
  against the model request), the same host-ordering difference as the real-git scenarios; `run` and `check` pass.
- `scenarios-blocked/`: the real-git scenarios (host event ordering differs between Pi and PiG's Node runtime).
- Benchmarks (`guardcore_bench_test.go`, imladris container on cores 96-151, load average 63 over 56 cores at the time, so
  these are upper bounds; repeat when quieter), median of 10 runs: ordinary command 131 us/op, 6.4 KB, 419 allocs;
  the same call with three path rules 238 us/op, 12.5 KB, 777 allocs; a loopback `psql ... DELETE ... WHERE` 361 us/op,
  15.7 KB, 645 allocs; `ArmingTracker.CheckArmed` on an armed rule 8.5 us/op, 0.8 KB, 69 allocs. No baseline of the
  previous head and no binary-size or cold-start measurement yet (those need a built Binary; the package-init test bounds
  start-up allocation at 150 KB and passes).
- Not run here: `npm run generate/check/test` (an imladris copy has no git history; run from a bundle clone at stacking).

## Security lane (port/warden-security-2)

Scope: the runaway guard, the prose check and the restatement record, desktop notifications, the security-write and
slop steers, and the detection half of `redact.ts` (`findSecrets`, `looksLikeSecretValue`, `syntheticish`,
`partitionSecrets`, `maskSecrets`, `secretFingerprint`, `secretIds`) with the security half of `output.ts`
(`evaluateOutput`, `buildOutputRequest`, `securityNotice`). The tool-output masking path in `tool_result` (masking,
notice, trace, duplicate note, compression) is the context lane's `tool_output.go`: it calls `MaskSecrets`,
`EvaluateOutput` and `SecurityNotice`, so masking is not observable from this lane alone. `output_context_standins.go`
holds the three names `EvaluateOutput` takes from the context lane (`OutputFormat`, `CompressionLearner`,
`retentionQuestions` / `applyRetention` / `learnerSuggest`); the lane that lands second deletes it.

- **Twins (39 added):** `credential-shapes.test.ts` 8 of 8, `runaway.test.ts` 9 of 9, `prose.test.ts` 5 of 5,
  `notify.test.ts` 5 of 5, `output.test.ts` 5 of 15 (32, 53, 71, 133, 183), `guard.test.ts` 129 and 155 (and the
  `findSecrets` assertion of 100), `extension.test.ts` 757 (security-write), 1635 (slop), 1880 (prose), 1974 (runaway),
  2049 (notify). Left skipped: `output.test.ts` 22 (the config loader: configui lane) and 86 to 218 (compression,
  excerpts, the stored-output ledger: context lane; 86 also asks the retention and format questions, so the security
  half is covered by `TestBoundedOutputRequestsRedactBeforeSamplingAndBatchTheSecurityQuestions`); the
  `extension.test.ts` cases about `tool_result` masking (384 to 593, 2788) are the context lane's. Not carried: the
  widget-chip assertion of 1635 (`slop: stub 0.92, hedging 0.75`), because the port draws its action line in
  `steer.go` `traceLine`, which names no slop scores.
- **Deviation D15:** notifier commands run through `os/exec` in the extension process (see the table above).
- **Recovery turn:** `runawayRecovery` sends `{deliverAs: followUp, triggerTurn: true}` once per user prompt, then a
  note that starts nothing; `triggerTurn: false` is the Go default, so the second form carries no explicit flag. Under
  the harness's scripted model the whole reply arrives as one chunk, so the guard's abort cannot shorten it and
  neither the original nor the port starts a recovery turn there (`runaway-single-chunk-reply-not-aborted`): the abort,
  the follow-up and the once-per-prompt rule are proved by `TestRunawayGuardAbortsRecoversOnceAndNeedsNoTypeSafe`.
- **Per-token cost:** registering `message_update` makes the host send every streamed event to the extension. The handler
  itself costs 160 ns and no allocation per event (`BenchmarkRunawayMessageUpdateHandler`); the round trip is the SDK's.
- **Detection by candidates:** Go's `regexp` cannot skip ahead through a case-insensitive pattern or one that opens with
  `\b`, so the first port of the credential scan cost 45 ms on a 64 KB output (58 times the original) and `Redact` cost
  2 ms on a 2 KB reply. `redact_fast.go` finds the candidate offsets with `strings.Index`, matches the credential-key head
  by hand and runs the remaining patterns only there; the token shapes drop their leading `\b` (a literal prefix Go can
  skip to) and check the boundary by hand. `redact_reference_test.go` keeps the plain regexp implementations and compares
  both on the 2,552-command corpus and on 12,000 generated texts; the runaway and prose text splitting, and the
  restatement measure, have the same kind of reference test.

### Results

- Layer 1 (imladris, tern-ci:v2), on the tree stacked on `port/warden-guardcore-3`: `go vet` clean for linux, darwin
  and windows; `go test` ok; `go test -race -count=6` on the whole package ok (747 s). (On the first base,
  `-race -count=24` on this lane's tests passed.) `TestRulesTurnRulesPassJudgesAtMostFiveUnseenFiles`,
  `TestRulesAgentStartDoesNotWaitForTheSnapshot` and `TestRulesPastTheCapOneNoticePerSession` failed now and then on a
  loaded box with `TempDir RemoveAll cleanup: directory not empty` (a background write of the rules lane still running
  when the test ends); reported to the rules lane. They once made mutants look killed for the wrong reason; every such
  line was re-run against the security tests alone.
- Layer-1 mutations, whole `port/mutate.py` on the stacked tree (`port/mutation-results.txt`): 255 rows, 247 killed,
  7 documented equivalents (six of other lanes and `sec-value-needs-one-class`: a single-class value is all digits,
  rejected earlier as a plain number, or all lower case, which is neither digits nor mixed case), 1 skipped
  (`disable-keeps-consent`: its pattern no longer matches `commands.go`; already so on `port/warden-guardcore-3`). This
  lane's rows are in `port/mutate_security.py` (88: 87 killed, 1 equivalent); `bearer-not-redacted` now targets the
  rewritten `Redact`.
- pigeq (`/home/kinsy/pigeq-gaps`, with `--accept-gaps port/accepted-gaps.json`): 7 scenarios
  added (55 in the package), all `pigeq run` (earlier, on the first base) identical for the original under Pi, the original under PiG's Node runtime and the Go port, with
  goldens recorded from the original under Pi: `prose-trend-nudges-the-next-prompt` (judge fixture: the request bodies
  of both clients are equal, and the nudge reaches the next prompt's model request), `prose-disabled-judges-nothing`,
  `write-judged-security-and-slop-steers`, `write-security-guard-off-asks-no-security-question`,
  `notify-disabled-stays-silent`, `runaway-single-chunk-reply-not-aborted`, `runaway-ordinary-reply-untouched`.
  G1 is disproved for the original: pointing `typesafeBackend` at a fixture server in the original's `agentFiles` config
  and `TYPESAFE_BASE_URL` plus `homeFiles` at the port makes both clients ask the same fixture. Judge fixtures must answer
  exactly the asked question ids.
- pigeq mutations (`port/mutations.json`, 10 rows `sec-*`, `pigeq mutate` with `pigeq-gaps`): 10 of 10 killed by the
  scenarios (two first counted as killed by an unrelated scenario's ordering flake and re-run against their own).
- `pigeq check` over all 55 scenarios on the stacked tree: 51 PASS lines (the pseudo-scenarios included) and six
  failures (`arming-edit-then-command-blocked`, `changing-failure-not-steered`, `drop-database-command`,
  `recall-after-a-failure`, `tools-disabled-replies`, `write-judged-security-and-slop-steers`) each passed in at least one of two single
  reruns (a different one failed each time), the ordering race below.
- `scenarios-blocked/notify-held-call-runs-the-configured-command.json`: the shim's `exec` event and the held call's
  `tool_execution_end` race (the original under PiG's Node runtime differs from the original under Pi in one of two
  consecutive runs); the behaviour is covered at layer 1, including the real spawn
  (`TestNotifierCommandsSpawnInTheCall`).
- **Host ordering races found (PiG vs Pi; reported, not normalised):** on a loaded box, in roughly one run of four a
  scenario with a steer or a held call differs from its golden at the first event where the host's `message_end` of
  a user or steer message and the model request swap places (`held-call-released-by-approval`,
  `repeated-failing-call-steered`, `drop-database-command`, `rm-authorized-by-the-prompt-runs`,
  `prose-disabled-judges-nothing`, `write-security-guard-off-asks-no-security-question`). It is not caused by this lane:
  with this lane's `message_start` and `message_update` handlers unregistered, 5 of 24 runs of four such scenarios
  failed the same way. Each passes on a rerun.

### Performance

Imladris container on cores 96-151, load average about 60 over 56 cores, so these are upper bounds. Median of 10
interleaved runs (`port/bench/run.sh`: the Go benchmark binary and the original under Node 24 with
`--experimental-transform-types`, alternating), same inputs (`port/bench/security-bench.ts` and
`security_bench_test.go`; a 64 KB tool output with one key line, a 3.5 KB streamed reply, a 2 KB prose reply):

| Case | Go port ns/op | B/op | allocs/op | original under Node ns/op | port / original |
|---|---|---|---|---|---|
| `maskSecrets` 64 KB | 747,358 | 231,468 | 54 | 767,996 | 0.97 |
| `findSecrets` 64 KB | 599,411 | 161,954 | 22 | 701,882 | 0.85 |
| runaway: 3.5 KB streamed in 7-character deltas, checks included | 422,184 | 86,854 | 402 | 482,328 | 0.88 |
| `restatedShare` of a 5-sentence reply against four finals | 21,573 | 5,640 | 88 | 33,134 | 0.65 |
| `buildProseRequest` (2 KB reply) | 29,198 | 7,766 | 11 | 29,796 | 0.98 |
| `message_update` handler, one delta | 165 | 39 | 0 | (none) | |

Against the previous release (`redactRef`, the plain regexp `Redact`, in the same binary): `Redact` of a 64 KB output
72.7 ms to 0.97 ms, of a 2 KB reply 2.10 ms to 31 us. Before the candidate scanning the first measurements of this
lane were 45 ms (`maskSecrets`, 58 times the original), 38 ms (`findSecrets`), 3.0 ms (runaway stream, 6.2 times) and
97 us (`restatedShare`, 2.9 times); each was profiled (regexp scanning of case-insensitive and `\b` patterns,
regexp whitespace splitting, map-based word sets) and fixed.

- Extension Binary (`npm run build:piglet -- pig-warden`, imladris) on the stacked tree: 72,679,584 bytes against
  71,540,896 for the previous release (0.1.2): +1,138,688 bytes (+1.6%), for all lanes stacked below this one together
  (on the first base, this lane alone sat inside +1.2%).
- Registration time (`port/bench/startup.sh`, the fake host, median of 10 medians of 30 starts): this tree 202 us against
  221.5 us for `port/warden-guardcore-3` (this lane adds nothing measurable); stacked tree against the previous release
  222 us against 151.5 us (+47%, from the lanes below; the box was noisy: the same comparison gave 156.5 against
  145.0 us on the first base).
- Cold start of the Binary and of the original under Pi were not measured: the original is loaded by Pi's TypeScript
  loader and has no comparable in-process registration point.
- No new Go dependency. `/warden status` probes the desktop notifier once per session when `notify.enabled` is on
  (up to six executables, 3 s each, as upstream does); nothing else of this lane blocks a host call.

## Learning lane (port/warden-learning-3, stacked on port/warden-context-2)

Ported modules: `holds.ts`, `learning.ts`, `sqlite-adapter.ts`, `adaptive.ts`, `judge-cooldown.ts`, the `feedbackLog` hold log,
`/warden recommend` and `/warden unmute`, the hold, regret and steer-outcome parts of the `extension.ts` handlers, and
pi-typesafe's answer check (`validResult`) for the TypeSafe judge.

| Original | Go | Notes |
|---|---|---|
| `holds.ts` | `holds.go` | `HoldLedger`, `HoldLog`, `TextRegrets`, `OutcomeNote`, `FormatHolds`; the log line keeps `JSON.stringify`'s key order |
| `learning.ts`, `sqlite-adapter.ts` | `learning.go`, `sqlite_store.go` | the same SQL against the same `holds.db` (WAL, busy timeout 10 s, `json_extract`); the engine is behind `sqlStore` |
| `adaptive.ts` | `adaptive.go` | `SteerStats` (file format and key order of the original), `SteerWatch`, `steerOutcome`; `adaptiveAdaptor` is the steering core's `steerAdaptor` |
| `judge-cooldown.ts` | `judge_cooldown.go` | `IntegrationError.Status` carries the HTTP status the 401/403 rule reads |
| `extension.ts` `holds`, `noteOutcomes`, `settleRegret`, `watched`, `tellCooldown`, `/warden recommend`, `/warden unmute` | `learning_wire.go`, `learning_commands.go` | hooks: `beforeAgentStartWithLearning`, `learnMessageEnd`, `trackHold` and `holdApproved` in `toolcall.go`, `cooldownActive` and `watchJudge` in `judgeFor` |
| pi-typesafe `client.js` `validResult` | `answers_validate.go` | `EvaluatorJudge.CheckAnswers` (TypeSafe backend only) |

The SQLite engine is **`modernc.org/sqlite` v1.60.1** (BSD-3-Clause, pure Go, no cgo; its requirements are `libc`, `mathutil`, `memory`,
`fileutil`, `x/sys`, `uuid`, `go-humanize`, `go-isatty`, `go-strftime`, `bigfft`, all BSD or MIT). It is an **owner-veto item: a new
Go dependency in this component only**. The original opens the user's `holds.db` with `node:sqlite`; the file is shared
state (WAL, an `AUTOINCREMENT` table, queries with `json_extract`), so a faithful port needs an engine that reads and writes
SQLite files. It builds for linux, darwin and windows without cgo and fuses into a Binary (`pig piglet build --format binary`,
checked: `port/live-smoke/smoke.sh` creates `holds.db` from the built `pig-warden`). Cost: the fused `pig-warden` Binary grew from
72,294,560 to 76,546,208 bytes (+4.25 MB, +5.9%) on linux/amd64 (the previous lane's tree against this one, same PiG source). Removing
it means replacing `sqlite_store.go` (the only file that names the engine) with a store that answers every method with "learning
is off" (the original's own NOOP answers).

Deviations (D12): the original's `PI_WARDEN_DB` and `PI_WARDEN_STEER_STATS` are `PIGPEN_WARDEN_DB` and
`PIGPEN_WARDEN_STEER_STATS` (D6); the files live in `<PIG_HOME>/pigpen-warden/` (`holds.db`, `steer-stats.json`, `holds/`).
Database and log writes run on one ordered background queue (the original's promises are not awaited either), so a tool
call never waits for a disk; the hold log writes the newest snapshot only (every snapshot is the whole state). The module's
console warnings have no console: they are kept (last 20) and the learning lines of `/warden status` show the counts.
`/warden status` prints the learning lines separately (D5). The upstream `ctx.hasUI` notices of the cooldown name
`TYPESAFE_API_KEY` for the TypeSafe backend and point at `/warden status` for the others.

Wiring facts:

- `guardcoreState.previousActions` is set by `installLearning`; the regret answer is settled by `settleRegretFromVerdict`.
- The tools lane's `holdRecords` adapter is installed (`holdAdapter`, over `HoldLedger.HeldCalls()`): `w.tools.holds`.
- `message_end` is registered as `w.sup.onMessageEnd(w.learnMessageEnd)`, first in the list (before the context saver's piece); the
  supervision lane's `input` hook settles `w.adaptor.user()`, and its stuck and repeat steers go through `adaptiveSend` and `watchSteer`.
- `adaptiveSend` and `watchSteer` gate and watch the action notes (intent-mismatch, off-task, should-proceed) in `toolcall.go`, with
  the `OffPlan`, `OffPlanTraceOnly` and `OffTask` counters.
- Every `Judge` the extension hands out is wrapped by the cooldown (`watchJudge`); tests that inject `Options.NewJudge` get it too.
- Names shared with other lanes were reused (`endsWithQuestion` from the prefs code, `nonNilStrings` from the relevance code).

Answer check (finding): pi-typesafe's `validResult` rejects a response whose answers are not exactly the questions asked
(count, type, probabilities over every label or level, the choice among them, `noul` in [0, 1]); the original then fails
the request with code `response` and the guard fails open. The shared Go client accepted such responses, so a judge
fixture with a static answer set made the original and the port diverge. `EvaluatorJudge.CheckAnswers` applies the same
rules in warden (the shared client is unchanged) and fails with the original's message. The own-model backend does not use
the check (it builds its answers itself). The text of a response that does not decode at all stays the port's `unreadable`
message (both are `response`).

Judge-server fixture recipe (G1 is disproved: the original can be pointed at a fake judge from the hermetic host):

1. Scenario: `"servers": {"judge": {"recordHeaders": ["authorization"], "routes": [{"method": "POST", "path": "/v1/systemone", "json": {"model": "jev-fixture", "answers": {...exactly the asked ids...}, "usage": {"input_tokens": 10, "output_tokens": 2}}, "times": 1}, ...]}}` and the env `FIXTURE_JUDGE_KEY`, `TYPESAFE_API_KEY` (any string) and `TYPESAFE_BASE_URL={{server:judge}}`. Routes answer in order, so a request whose question set differs (a regret question, a retry's approval question) needs its own `times` route; the answers must be exactly the asked ids (see the answer check).
2. The original: `agentFiles` `pi-warden/config.json` = `{"typesafe": true, "typesafeBackend": {"label": "Fixture judge", "host": "{{server:judge}}", "keyEnv": "FIXTURE_JUDGE_KEY", "defaultModel": "jev-latest"}, "notices": false, "rules": {"enabled": false}}` (an http loopback origin is accepted; `defaultModel` equals the Go client's default so the request `model` is equal). The port: `homeFiles` `pigpen-warden/config.json` = `{"enabled": true, "backend": "typesafe", "consent": "typesafe", ...}`.
3. Commands that print temporary paths (npm, git) differ between lanes; use `touch x.txt`. `pigeq` needs `--accept-gaps port/accepted-gaps.json` for the SQL statements the scanner mistakes for process starts. Examples: `scenarios/judge-*.json`.

Not reachable by pigeq (UI requests cannot be compared: the original and the port draw different status lines and widgets, G2):
the notices of `/warden recommend`, `/warden unmute` and the cooldown, `/warden status`, the hold log and the database files.
They are proved by the layer-1 tests (`learning_wire_test.go`, `learning_ext_test.go`) and by the judge-server scenarios through the requests
that leave (the regret question, the cooldown's silence).

Results of the learning lane (imladris, tern-ci:v2, go1.27.2, linux/amd64; PR 7, stacked on PR 6's content, version 0.1.9):

- **Twins**: holds 10 of 10, learning 31 of 31, learning-dirs 3 of 3, adaptive 12 of 12, judge-cooldown 6 of 6, ask 2 of 2,
  sqlite-adapter 1 of 10 (the real-engine case; the nine others test the bun:sqlite wrapper and stay named skips), the
  `extension.test.ts` cases on adaptive steers, hold feedback (offline, record-time outcome, with Jev, confirm mode) and the judge
  cooldown (seven) 12 of 12. Named skips with per-case reasons: sqlite-adapter 9 and learning-driver 2 (they test which JavaScript
  module opened the database). 744 twins and 520 named skips in all (1,264 upstream cases).
- **Red first**: the tests alone (commit `9507a9c`) do not build (442 undefined symbols; the first error is a test helper
  name the stack commit then resolves).
- **Mutations** (`port/mutate.py`, rows `holds-*`, `learning-*`, `adaptive-*`, `cooldown-*`, `learn-*`): 143 rows, 138 killed, 5 equivalent
  mutants named in `EQUIVALENT` with their reasons (a redundant outer floor, limits that cannot be reached, a defensive guard).
  pigeq (`port/mutations.json`): 3 rows (`learn-regret-candidates-never-offered`, `learn-judge-cooldown-ignored`,
  `learn-answer-check-skipped`), 3 killed by the judge-server scenarios.
- **pigeq** (`pigeq-gaps`, `check` over the 75 scenarios of the stack): three scenarios of this lane,
  `judge-regret-question-rides-the-first-request-after-a-reply` (42 events), `judge-auth-failure-pauses-the-judge` (25) and
  `judge-answers-that-do-not-match-the-questions-fail-the-request` (41): the original under Pi == the original under PiG's Node
  runtime == the Go port, event for event, including every request to the fake judge; goldens recorded from the original. All
  three passed the full check. Under host load (load average 130 to 150) six other scenarios of the stack differed once at event 4,
  10 or 24 (`llm` before `turn_end` or the user message, the harness's event order); each passed when rerun alone (one of them
  needed a third run), none was normalized.
- **Shared file**: the port's `holds.db` and the original's are one file format: the original wrote six rows with `node:sqlite`
  and the port read them (stats, smart history), and the port wrote six rows and the original read them (stats, history,
  recommendations, steer report): `port/perf/learning-cross.mts` with `TestLearningCrossCheck` (needs `WARDEN_LEARNING_CROSS`).
- **Binary and smoke**: `npm run build:piglet`-equivalent single-extension Binaries (`scripts/perf/build-singles.mjs`) of the stack
  head and of this lane: 73,244,832 and 77,508,768 bytes (+4,263,936, +5.8%: the engine). `port/live-smoke/smoke.sh` passes with the
  built Binary and finds `holds.db` created. The package-init allocation limit test passes unchanged (102,864 bytes, 839 allocs).
- **Race**: a first `go test -race -count=6` found one real race (`CloseLearningDatabases` read a connection while its open was still
  in flight in the background queue); fixed (`e.once.Do` before the read) and verified with `-race -count=60` over the wiring and
  extension tests; the full `-race -count=6` is rerun for the PR body.
- **Performance** (median; 4 CPUs of the shared host, `taskset 100-103`, load average 90 to 200, so differences under 10% are noise):

  | | stack head (context lane) | this lane |
  |---|---|---|
  | single-extension Binary | 73,244,832 bytes | **77,508,768** (+4.26 MB, +5.8%; the engine) |
  | startup, 41 interleaved runs, `-p` through the fused Binary, warden off | 89.4 ms | 94.1 ms (+5.3%; seed-check control 85.7 ms) |

  An off warden opens no database (`learnEnsure`). Per operation, 500 rows of one project, Go 4 CPUs against the original under
  Node 24 (`node:sqlite`) on the same CPUs: `recordHold` 173 us (40 allocs, 2.4 KB) against 165 us; `generateRecommendations` 800 us
  (104 allocs) against 490 us; `analyzeSteerEffectivenessReport` 330 us against 185 us; `holdStats` 495 us (54 allocs) against 298 us.
  The pure-Go engine is about 1.6 times slower on the reads. They run in `/warden recommend` and `/warden status`
  (sub-millisecond), never on the tool-call path; the per-call work is the ledger (630 ns, 12 allocs, 888 B) and an enqueue, and the
  database write runs in the background (173 us). The steer counts write 3 KB in 170 us per observed steer. This is the >10% slowdown
  the quality bar names for the reads: the owner decision is the dependency itself (no cgo, one file format).
  The original under PiG's Node runtime was not measured for startup: it is on by default and its first-run judgment notice is a
  message the faux model of the perf harness refuses (`unhandled request`).

## Conscience lane (port/warden-conscience-3, stacked on port/warden-learning-3)

What this slice ports: the conscience (`conscience.ts`: candidates, the two question kinds, scoring, thresholds, the 256-per-category and 32-question envelope, four requests in flight under one deadline), load mode (`load.ts` rules 1-10), the capability index (`index-cmd.ts`, `hashing.ts`), the conscience state machine of `extension.ts` (generation, selection, observation, one re-assessment after a failure, one reminder at the end of an unused run, the settled line, the queued-prompt assessment, the index nudge) and `/warden index`, with the index write exemption of the action guard and the wait of new input while it runs. `conscience.enabled` is off by default (as upstream) and the port itself is off until `/warden enable` (D1).

- **Twins** (`// twin: tests/<file>:<line>`): `conscience.test.ts` 59 of 59, `conscience-gunner.test.ts` 46 of 48, `index.test.ts` 50 of 50, and 43 of the 44 conscience and index cases of `extension.test.ts` (lifecycle, delivery, reminders, load mode, the activation gate, the index write exemption), through the fake host (`conscience_ext_test.go`) with a scripted judge that answers like the original's mock. Named skips with a reason each: `conscience-gunner` 672 and 702 (config loader cases, the config lane's) and `extension.test.ts` 4417 (D17). Four of the gunner cases that only said "tested via extension.test.ts" carry the `// twin:` line of the Go test that does it. One twin is stricter than the original: `conscience-gunner` 217 asserts `load_changed` where the original accepts three outcomes. The path-rule twins (`conscience-gunner` 855, `extension.test.ts` 3600) run through guard core's real `matchPathRules`, with an exempt-rule and a non-matching-rule check added.
- **Go-only tests**: `/warden index` end to end through the host (`conscience_index_ext_test.go`: a model that writes full-shape files, nothing, a bad entry, an entries-only file; the exemption only while it runs; a failed run clears the flag), the frontmatter reader, the hash of invalid UTF-8, the deadline as the smaller timeout, one request per candidate at every batch boundary, the redaction order, `toFixed` ties, the stale generation by operator input, the nudge once per session and for a stale index, the recent-context cap, the reminder as a follow-up steer.
- **Equivalence** (`port/scenarios`, goldens recorded from the original under Pi 1402973a): 14 `conscience-*` scenarios, `pigeq run` equal in all three lanes (pi-ts, pig-ts, pig-go) with the judge a fake server both lanes call (G1): the skill recommended; below the recommend threshold; below the advance threshold (probability 0.5); awaiting the user; a judge model the policy does not name; load mode supplying the body; load refused for a credential, an absolute path and an oversized file (the recommendation instead); `/skill:name` in the prompt (no request); an excluded skill; a user-only skill; a tool recommended with its real description (the three warden tools, the core tools never offered); the conscience off (no request). The judge's request body (questions with their wording, the opaque ids, the state) is compared field by field. The skill is loaded with `--skill skills/impeccable` because the harness starts hosts with `--no-skills`.
- **Mutations**: layer 1 (`port/mutate.py`, 79 defects of this slice): 77 killed, 2 equivalent with their reasons (`state-loaded-bytes-not-counted`, `state-reminder-while-awaiting-user`); six survivors of the first run were closed with new tests (every candidate in exactly one request, the task redacted in `buildState`, the redaction before the 2000-character cut, `maxNudges: 0`). pigeq (`port/mutations.json`, 21 defects of this slice): 21 of 21 killed by the scenarios. Three others were tried and are not rows: `conscience-off-still-asks` (equivalent at that layer: `assess` itself refuses when the conscience is off), and `load-consent-not-required` and `load-off-mode-loads` (a first run on the earlier base reported them killed, but only through the host's ordering flake, G10; on a quiet host they survive, because `loadSkillBody` is reached only in load mode with consent, so no scenario can differ). All three are killed in layer 1 (`state-conscience-off-still-runs` and the Rule 2 twins). A first survivor (`advanceThreshold` ignored) led to the low-advance scenario. `pigeq check` on all 89 scenarios of the stack against the goldens: 89 of 89 equal (four scenarios of other lanes showed the G10 ordering difference in the full run on the loaded host and passed when run alone).

### Performance (conscience slice, 0.1.10)

Measured on imladris (Intel Xeon 6746E, CPU set shared with other lanes; load average 85 to 125, so not noise-free; Go 1.27, Node 24.19.0). Inputs are the same in both: Go `go test -bench Conscience -benchmem` (`extensions/warden/conscience_bench_test.go`), the original `port/bench-conscience-original.ts` run under Node with tsx (median of 11 runs inside each of 5 processes); the two were run alternately in one container, 5 rounds (Go 2 runs a round, median of 10). Skill files are real files for both; the judge is an instant stub in both. The Node figures moved by up to 40% between this run and the one on the earlier base (1.1 to 1.5 ms for the same 40-skill assessment), which is the noise of the shared host.

| Path | Go port (median) | Original (Node) | Go vs Node |
|---|---|---|---|
| assess 40 skills (40 file hashes, 2 requests) | 1299 us, 116 KB, 826 allocs | 1517 us | -14% |
| assess 250 skills + 60 tools (310 candidates, 10 requests) | 8679 us, 880 KB, 6108 allocs | 10617 us | -18% |
| candidates from 100 skill files + 100 tools | 3212 us, 176 KB, 1909 allocs | 4474 us | -28% |
| questions + hash for 31 candidates | 30 us, 14 KB, 12 allocs | 38 us | -22% |
| sanitize a 1.8 KB description | 31 us, 13 KB, 25 allocs | 17 us | +86% |
| validate a 200-entry index | 1394 us, 293 KB, 1010 allocs | 1791 us | -22% |
| write + read a 200-entry index (owner-only, atomic) | 2400 us, 646 KB, 1351 allocs | 1407 us | +71% |
| build the `/warden index` prompt, 100 skills + 60 tools | 34 us, 41 KB, 163 allocs | 35 us | -3% |
| load an 8 KB skill (all rules) | 222 us, 60 KB, 27 allocs | 363 us | -39% |

On the first measurement (earlier base, quieter Node run) the same paths were 1.3 to 36 times slower than the original, found by profiling (`go tool pprof`): the sanitizer's regular expressions dominated every path that cleans text, and `Redact` over a skill body dominated a load. Fixes, each held to the code it replaces by a test: the sanitizer is a scanner with substring searches (`TestSanitizeDescriptionScannerEqualsTheRegularExpressions`: 30,000 random and the edge inputs, equal to the original's chain of regular expressions), the question hash is written by hand and computed once per assessment (`TestQuestionHashWriterEqualsTheReference`), the index is written and measured by a fixed-shape writer and read in one typed pass (`TestIndexJSONEqualsTheGenericEncoder`), the word splitter is one pass (`TestWordsOnePassEqualsTheRegularExpressions`), the build prompt is a builder (`TestBuildIndexPromptIsTheOriginalsTextByteForByte` against text written by the original), and file hashes use Node's four system calls. On the stack, load mode calls security's `FindSecrets` directly, which has its own prefilter, so the conscience's own secret prefilter was dropped.

Rows still above the original: `sanitize` (+86%, 14 us more on 1.8 KB of text that contains every trigger: V8's regular expression engine against a scanner; it runs once per candidate description, 40 of them cost 0.6 ms of an assessment that waits hundreds of milliseconds for the judge) and the index write and read (+71%: the shared `WriteFileAtomic` does a temporary file, chmod and rename, and 20% is JSON decoding; once per `/warden index`). Neither is on the TUI input or render path: an assessment runs in `before_agent_start` under the judge's deadline (3 s default), `/warden index` is a command that waits for the model. On the earlier base, with Node faster, `assess` was also 16 to 21% above Node (a quarter of it system calls: open, stat, read, close of every skill file, which the original does too). OWNER DECISION NEEDED to accept these two rows or ask for more work (the next step would be a stat-keyed hash cache, which changes behavior when a file is replaced within the same tick).

Against the previous slice (`port/warden-learning-3`; a `package main` that only runs `warden.Extension()`, linux/amd64, `-s -w`, 31 interleaved runs pinned to CPUs 100-103): binary 14,487,712 to 14,794,912 bytes (+307,200, +2.1%); `Extension()` 91.3 us to 89.0 us; process start to a built extension 7.37 ms to 7.02 ms (noise: the box is shared). Package init allocates no more than before (`TestPackageInitDoesNotCompileRegexps` green: every expression is a `lazyRE`). The original's module load under Node was not measured for this slice.

### Conscience gaps

| # | Gap | Why | Closes when |
|---|---|---|---|
| G11 | `/warden index` is not compared live: the scripted model cannot name the absolute index paths of a lane (they differ: `pi-warden/index` and `pigpen-warden/index`) | the scenario format has no per-lane placeholder for a path | the harness gains one |
| G12 | The queued-prompt assessment (`message_start` of a user message without `before_agent_start`) and the agent_end reminder are not compared live | no step type that queues a prompt behind a running turn; a scripted run ends on a text reply, which suppresses the reminder | layer-1 twins carry both |
| G13 | The config loader clamps `maxSkillBytes` and `maxLoadedBytes` to 1024..262144 (`config.ts` 1144); the port reads them as written | the config loader is the config lane's | `applyUserOverrides` lands |

Stacked on the other lanes: load mode calls guard core's `matchPathRules` (rule 3, for the advertised and the resolved path) and security's `FindSecrets` (rule 7) directly; the package variables of the first branch are gone. The conscience's pieces of the shared events are the supervision module's registries: `w.sup.onMessageStart(w.conscienceMessageStart)` after security's runaway reset, `w.sup.blockInput(w.conscienceIndexBusy, ...)` after the rules block, `w.sup.onInputPass(w.conscienceInputPass)` for the generation bump, `w.sup.onSettled(w.conscienceSettled)` after the subagent triage; the agent_end reminder is slot 6, between the restatement record and the rules pass. The conscience reads the judge through `judgeFor`, so the learning lane's cooldown wrapper (`watchJudge`) sees its failures and a cooldown makes the judge absent; the skip reason is then `no_consent`, as upstream (no reason passed). It records its trace through `w.record` and steers through `w.steer`, `w.adaptiveSend` and `w.watchSteer`.
