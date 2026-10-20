# Port record: warden (pi-warden)

| Input | Identity |
|---|---|
| Original | `DevMortimer/pi-warden` 0.80.0, commit `a12b2703b2c7ac32401fe13f4ce037d673a5f113`, MIT (Ryan Gapac). Kept unmodified at `port/oracle` |
| Judge seam | `DevMortimer/pi-typesafe` commit `ed439f834665ad6fc652787f5ba7c23852566d49` (`ask`, questions, `errors.ts`), MIT |
| Oracle | Pi 0.87.1 hosting the original; Node 24.19.0 |
| Target | PiG 0.3.0+0.87.1, SDK of a 0.3.0 pre-release build (`63c6ba456`) (host getters return `(value, error)`), go1.27.1; shared client `components/typesafe` |
| Original's tests | 1,264 cases in `port/oracle/tests`; 278 have a Go twin, 986 are named skipped subtests with a reason (see below) |

## File-to-file mapping

| Original (`port/oracle/src`) | Go (`extensions/warden`) | Notes |
|---|---|---|
| `guard.ts` `SHELL_RULES`, `SENSITIVE_PATH`, `classifyRm`, git state, `matchPatterns`, `unknownExemptIds`, `EXEMPTABLE_IDS` | `patterns.go` | RE2 has no lookahead or backreference: `printenv`/`echo` secret rules, the `.env` example exclusion, `git checkout/restore` and `git push -f` are small functions with the same behavior (each cites the regexp it replaces) |
| `guard.ts` `splitShell`, `headOf`, `stripDataText`, `blankQuotes`, `blankMessageFlags`, `isReadOnlyCommand` (+ `sed`, `git grep`), `isVisibleCommand` | `shell.go` | |
| `guard.ts` `describeAction`, `truncate`, `sample`, `displayPath` | `describe.go`, `jsstr.go` | strings are cut in UTF-16 units like JavaScript |
| `guard.ts` `evaluateAction`, `buildRequest`, `intentSteer`, `offTaskSteer`, `shouldProceedMessage`, `steerReason`, `textApproves`, `steerFingerprint`, `SteerRepeatWindow` | `guard.go` | messages keep the `pi-warden:` prefix |
| `guard.ts` `authorize`, `scopeMatches`, `hitViolations` (rm family) | `authorize.go` | a prompt that names the deletion and its target releases an `rm`; nothing releases `rm-recursive-dangerous-target` |
| `guard.ts` `questions`, `visibleQuestion`, `intentQuestion`, `shouldProceedQuestion`, `approvalQuestion`; `stuck.ts` `stuckQuestions`; `done.ts` `doneQuestions` | `questions.json` + `questions.go` | verbatim, dumped from the pinned source |
| `action-guard.ts` | `action_guard.go` | hold and approval, sibling prejudging; safe for concurrent use; `Wait()` added |
| `stuck.ts` (attempt window, `evaluateStuck`, nudges, `stuckDiff`) | `stuck.go`, `stuckdiff.go` | attempt keys are SHA-1 of the sorted-key JSON of the input (equality only) |
| `done.ts` (evidence, `checkSummary`, UI proof, `evaluateDone`, `doneNudge`) | `done.go` | glob matcher from `rules.ts` `globToRegExp` |
| `shape.ts` `taskSpine` | `spine.go` | |
| `widget.ts` templates and tokens (action, stuck, done) | `widget.go` | `toFixed` rounds ties up like JavaScript |
| `redact.ts` | `redact.go` | `VALUE_AMPERSAND` lookahead written as a scan |
| `tools.ts` | `tools.go` | |
| `config.ts` (the carried sections) | `config.go`, `configfile.go` | |
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

## Scope

Ported: the action guard (offline pattern floor, judged irreversible / off-task / off-plan / should-proceed / approval,
hold and release, sibling prejudging, user command and deny rules, exempt rules), the stuck guard (exact and
successful repeats, churn, quick repeat, judged loops, stuck diff), the done-check (evidence, UI proof, false claims),
the task spine, steer delivery with the per-run budget and repeat window, the trace widget, and the memory tools: the three
tools `warden_remember`, `warden_loops` and `warden_recall`, standing preferences (the scan of earlier sessions of the project
and its worktrees, the seven injection rules, the lesson store, forget), open loops (store, reminder at the end of a run, message on resume), the compaction evidence
appendix (`session_compact`), the recall footer (`recall.go`), and `/warden prefs [forget <n>]` and `/warden loops`; the context saver: the tool_result pipeline (credential masking and notices, injection banners, retention judged compression with parser excerpts, the duplicate note, the repeated-run cut, the opt-in context filter), the stored full outputs and their ledger, recall tracking, message dedupe, relevance compaction at `session_before_compact`, and the saver's lines of `/warden status`.

Not ported (each upstream case is a named skipped subtest in `skipped_twins_test.go`, generated by
`port/gen-skipped-twins.py`; OWNER DECISION NEEDED to add any of them): the rules guard (`pi-warden.md`), slop and prose
guards, security masking and secret detection (the masking and notices of tool results are ported with the saver's pipeline), runaway output, conscience,
subagent supervision, call-waste, adaptive steering, path and arming rules, session scratch,
host paths, SQL target classification (`sql-delete-local`, `sql-hosted*`; the plain `sql-*` rules still apply),
shell-write summaries, the large-output steer, jev violation escalation (the `violation_i` questions), the hold
feedback database and regret question, learning, the config schema migration, and stuck evidence (runs as upstream's
documented `stuck.evidence: false`). The ledger of saved outputs and the hold records are interfaces here
(`outputLedger`, `holdRecords` in `compact.go`): until those modules land the appendix and `warden_recall` list no saved output and no held action.

Refused, not silent: the `/warden` actions `rules` (and `rules check|audit|calibrate|tune`), `report`, `config`, `init`,
`audit`, `index`, `recommend`, `unmute` and `bench` answer with a command notification: "@pi-in-go/pigpen-warden
(Go port) does not support <feature> yet. Install the original extension, npm:pi-warden@0.80.0, which runs unchanged in
PiG." Also not yet ported: the trace panel shortcut and the upstream events with no Go handler (message_start, message_update,
input, agent_settled; `message_end` runs the saver's dedupe only).

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

## Equivalence (live: Pi original vs Go port)

`port/scenarios` holds 25 offline scenarios (19 for the guards, 6 for the tools slice), `port/golden` their traces recorded **from the original under Pi 0.87.1**
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
Results: see "Tools slice" below for the six new scenarios; **the 19 guard scenarios: the original under Pi == the original under PiG's Node runtime (host check) == the Go
port under PiG (Go directory) == the built `pig-warden` Binary (`check --builtin`)**, event for event.

What the scenarios deliberately do not pin, and why (each is the parity claim's edge, not a normalizer change):

- `capture` leaves out `extension_ui_request` (gap G2, deviation D4). Everything the agent and the model see is compared: the
  tool result text of a held call, the steer messages, the model requests, the lifecycle, the ordering.
- `PIGPEN_WARDEN_STEER_VISIBLE=0` matches the original's default (`display:false`); the port's default is visible
  (D4). The steer's `customType` is the original's `pi-warden-steer` so a session reads the same on either.
- `--tools read,bash,edit,write` keeps the model's tool list equal for the 19 guard scenarios: the original also registers
  `warden_remember`, `warden_loops` and `warden_recall`, and PiG's default set adds `powershell`, `grep`, `find`,
  `ls` (a host difference). The six scenarios of the tools slice list the three tools in `--tools` explicitly, so
  their descriptions and JSON schemas are compared in the model request.
- Not reachable through pigeq for these tools: the disabled replies (`not available: pi-warden is off`, `not recorded:
  standing preferences are off`) and `prefs.enabled: false`, because the original is on by default and reads its
  config from `<agent dir>/pi-warden/config.json`, while the port is off until enabled (D1) and reads
  `<PIG_HOME>/pigpen-warden/config.json`, which a scenario cannot write; `/warden prefs` output and the `pi-warden-prefs`
  message of a project with earlier sessions of known content (the scan reads Pi's own session files, whose names and
  dates a scenario does not control; `/warden prefs` prints real dates). The fake-host tests
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
- **Equivalence** (`port/scenarios`, goldens recorded from the original under Pi 0.87.1; `pigeq check` 29 of 29 equal): ten scenarios for this slice. Offline: a token in a tool result is masked and the banner leads and trails it; a documented fixture key is left whole; two calls that print the same large output (the second becomes a duplicate note with a stored copy); a result that repeats a block the agent already has (one pointer line). Judged, against a fake judge both lanes call (G1 below): a build log compressed to its errors and summary with a stored copy and then a copy of it as a duplicate note (the request body of the output check is compared field by field); a compiler log recognised as `tsc` (exact error lines); a retention verdict under the confidence gate (output kept whole); an injection verdict (banner at both ends, no steer); a judge answering 503 (output kept whole); and the context filter on (the filter's chunk request and the kept passages). The scenario `normalize` rule masks `pi-warden-output-<random>` (the original itself randomizes it per run).
- **Blocked** (`port/scenarios-blocked`): `judged-relevance-compaction-replaces-the-summary` (manual `/compact` with relevance compaction on). The judge's requests, the kept and left-out units and the summary text are equal in the original under Pi, the original under PiG and the port; what differs is the host: the `compact` response carries `estimatedTokensAfter`, 12941 on PiG and 12938 on Pi for the same compaction (a host difference to report, not to normalize), and a random `firstKeptEntryId`.
- **Mutations**: layer 1 (`port/mutate.py`, 40 defects of this slice, `MUTATE_RUN` restricts the run to the slice's tests): 39 killed, 1 equivalent (`banner-only-on-first-block`, reason in the script). pigeq (`port/mutations.json`, 10 defects of this slice: masking skipped, banner not leading, notice dropped, duplicate-note and pointer-line wording, footer missing, confidence gate ignored, format parser never selected, repeated runs never cut, duplicates never noted): 10 of 10 killed by the scenarios.

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

## Re-verified on PiG 0.4.1 (porter-verify)

The golden traces were recorded again from the original under Pi 1.0.1 (host check: identical under PiG's Node runtime), with PiG 0.4.1 content (`5f948f86a`, `pig --version`
`0.3.1+1.0.1`) and normalizer v3, because Pi 1.0.x changed the trace format: a `prompt` response now carries
`data.disposition` (Pi 0.99.0, #9098). The bash tool's results now carry `structuredContent` (15 events), whose `wall_time_seconds` the harness normalizes (N6). The record reads `port/eq-entry.ts` with the modules it re-exports, so `sdk-gaps` checks the vendored original again (it said "not an extension" before). An event-by-event diff against the previous traces shows no other
difference, and `pigeq check` passes on the Go port. Details: `docs/plan/progress/porter-verify.md`.
