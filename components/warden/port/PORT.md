# Port record: warden (pi-warden)

| Input | Identity |
|---|---|
| Original | `DevMortimer/pi-warden` 0.80.0, commit `a12b2703b2c7ac32401fe13f4ce037d673a5f113`, MIT (Ryan Gapac). Kept unmodified at `port/oracle` |
| Judge seam | `DevMortimer/pi-typesafe` commit `ed439f834665ad6fc652787f5ba7c23852566d49` (`ask`, questions, `errors.ts`), MIT |
| Oracle | Pi 0.87.1 hosting the original; Node 24.19.0 |
| Target | PiG 0.3.0+0.87.1, SDK of a 0.3.0 pre-release build (`63c6ba456`) (host getters return `(value, error)`), go1.27.1; shared client `components/typesafe` |
| Original's tests | 1,264 cases in `port/oracle/tests`; 511 have a Go twin, 753 are named skipped subtests with a reason (see below; with the learning lane stacked on the guard core and rules lanes) |

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

## Scope

Ported: the action guard (offline pattern floor, judged irreversible / off-task / off-plan / should-proceed / approval,
hold and release, sibling prejudging, user command and deny rules, exempt rules), the stuck guard (exact and
successful repeats, churn, quick repeat, judged loops, stuck diff), the done-check (evidence, UI proof, false claims),
the task spine, steer delivery with the per-run budget and repeat window, the trace widget, and the project rules guard
(`pi-warden.md`: parsing, scoping, per-rule judged requests, scores, severity and soft tier, sensitive-path notes, the
rules log, `/warden rules` and its `check`, `audit`, `calibrate` and `tune`, `/warden report`, `/warden init`,
end-of-run turn rules, literal shell writes judged as writes).

Not ported (each upstream case is a named skipped subtest in `skipped_twins_test.go`, generated by
`port/gen-skipped-twins.py`; OWNER DECISION NEEDED to add any of them): slop and prose
guards, security masking and secret detection, runaway output, context saver and relevance compaction, conscience,
preferences and loops, subagent supervision, call-waste, the config schema migration, and stuck
evidence (runs as upstream's documented `stuck.evidence: false`). Ported by the learning lane: the hold feedback loop,
the learning database, per-model steer adaptation, the judge cooldown, `/warden recommend` and `/warden unmute`.

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

Refused, not silent: the `/warden` actions `config`,
`audit`, `index`, `prefs`, `loops` and `bench` answer with a command notification: "@pi-in-go/pigpen-warden
(Go port) does not support <feature> yet. Install the original extension, npm:pi-warden@0.80.0, which runs unchanged in
PiG." The tools `warden_remember`, `warden_loops` and `warden_recall` are not registered (not yet ported), so the model is
not offered them. Also not yet ported: the trace panel shortcut and the upstream events with no Go handler (message_*,
input, compaction, agent_settled).

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

`port/scenarios` holds 19 offline scenarios, `port/golden` their traces recorded **from the original under Pi 0.87.1**
(`port/eq-entry.ts` loads the vendored, unmodified `port/oracle` from source; `npm ci --ignore-scripts` in
`port/oracle` first). Every scenario sets `"env": {"PIGPEN_WARDEN_ENABLED": "1", "PIGPEN_WARDEN_STEER_VISIBLE": "0"}` (the
per-scenario `env` map of pigeq; the original ignores both), declares `git` as missing and passes
`--tools read,bash,edit,write`. Held: force push (`--force` and `-f`), `rm -rf ~/...`, `chmod -R 777 /`, `DROP DATABASE`,
`curl | sh`, a hard reset; a sibling pair with one held; a hold released by a user reply that approves it; a repeated
failing call steered (quick repeat, then the stuck steer); identical calls; a changing failure and a repeated
mutating call (both quiet); read-only and ordinary commands, secrets and writes (allowed offline).
Results: **19 of 19 scenarios: the original under Pi == the original under PiG's Node runtime (host check) == the Go
port under PiG (Go directory) == the built `pig-warden` Binary (`check --builtin`)**, event for event.

What the scenarios deliberately do not pin, and why (each is the parity claim's edge, not a normalizer change):

- `capture` leaves out `extension_ui_request` (gap G2, deviation D4). Everything the agent and the model see is compared: the
  tool result text of a held call, the steer messages, the model requests, the lifecycle, the ordering.
- `PIGPEN_WARDEN_STEER_VISIBLE=0` matches the original's default (`display:false`); the port's default is visible
  (D4). The steer's `customType` is the original's `pi-warden-steer` so a session reads the same on either.
- `--tools read,bash,edit,write` keeps the model's tool list equal: the original registers `warden_remember`,
  `warden_loops` and `warden_recall` (deferred: prefs, loops, recall) and PiG's default set adds `powershell`, `grep`, `find`,
  `ls` (a host difference).
- The port is off unless enabled (D1); the disabled default is proved by `TestWardenIsOffUntilYouEnableIt`.

Mutations (`port/mutations.json`, 16 defects; `pigeq mutate`): 12 killed by scenarios. Four (`quick-repeat-*`,
`differing-output-still-repeat`) survive at the scenario layer and are killed by the stuck twins in layer 1
(`port/mutate.py`, `port/mutation-results.txt`); pigeq's `--unit` cannot run them because its temporary `go.work` does
not include the shared `components/typesafe` library module (finding below).

## Named gaps

| # | Gap | Why | Closes when |
|---|---|---|---|
| G1 | Judged behavior is compared to the original only through the pinned question texts and the fake-server tests, not live against Pi | the original cannot be pointed at a fake TypeSafe server from the hermetic host | a pigeq fixture serves one fake judge to both lanes |
| G2 | The **UI** the two sides draw (status line, widget, notifications) is not compared live | deliberate UX deviation (D4): the port shows a status line, a trace widget and a notice per finding, the original draws its own widget and rules-file warnings; scenarios therefore leave `extension_ui_request` out of `capture` | the UI is proved by the fake-host tests and the Binary smoke (`port/live-smoke/smoke.sh`) |
| G3 | Not compared live: `hasUI == false`, the judged (TypeSafe / own-model) path, `/warden` commands, the done-check (it needs a judge to score a claim) | the original cannot be pointed at a fake TypeSafe server from the hermetic host; its `/warden` differs (D5) | fake-host and fake-server tests, the pinned question texts, the differential corpus |

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


## Rules lane (port/warden-rules-1)

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

Not done in this PR (honest list): pigeq scenarios and goldens for the rules behaviours, the layer-1 and pigeq mutation
rows, the performance section's measurements, and the `go test -race -count=24` run; see the status file.

## Learning lane (port/warden-learning-1)

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

Wiring facts other lanes need:

- `guardcoreState.previousActions` is set by `installLearning`; the regret answer is settled by `settleRegretFromVerdict`.
- The tools lane's `holdRecords` adapter: `HoldLedger.HeldCalls()` returns `{Tool, Preview, Outcome}` of the held records
  (the adapter converts them to its `HeldEntry`): `w.tools.holds = ...` at stacking.
- `message_end` is registered here (`learnMessageEnd`); with the supervision lane stacked it becomes
  `w.sup.onMessageEnd(w.learnMessageEnd)` and the direct `e.OnEvent(sdk.EventMessageEnd, ...)` line goes.
- `adaptiveSend` and `watchSteer` now gate and watch the action notes (intent-mismatch, off-task, should-proceed) in
  `toolcall.go`, with the `OffPlan`, `OffPlanTraceOnly` and `OffTask` counters; the supervision lane gates stuck and repeat.
- Every `Judge` the extension hands out is wrapped by the cooldown (`watchJudge`); tests that inject `Options.NewJudge` get it too.

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

Results of the learning lane (imladris, tern-ci:v2, go1.27.2, linux/amd64):

- **Twins**: holds 10 of 10, learning 31 of 31, learning-dirs 3 of 3, adaptive 12 of 12, judge-cooldown 6 of 6, ask 2 of 2,
  sqlite-adapter 1 of 10 (the real-engine case; the nine others test the bun:sqlite wrapper and stay named skips), the
  `extension.test.ts` cases on adaptive steers, hold feedback (offline, record-time outcome, with Jev, confirm mode) and the judge
  cooldown (seven) 12 of 12. Named skips with per-case reasons: sqlite-adapter 9 and learning-driver 2 (they test which JavaScript
  module opened the database). 511 twins and 753 named skips in all (1,264 upstream cases).
- **Red first**: the tests alone (commit `59fd381`) fail to build with 167 undefined symbols (the first is `undefined: SteerStats`).
- **Mutations** (`port/mutate.py`, rows `holds-*`, `learning-*`, `adaptive-*`, `cooldown-*`, `learn-*`): 143 rows, 138 killed, 5 equivalent
  mutants named in `EQUIVALENT` with their reasons (a redundant outer floor, a limit that cannot be reached, a defensive guard).
  pigeq (`port/mutations.json`): 3 rows (`learn-regret-candidates-never-offered`, `learn-judge-cooldown-ignored`,
  `learn-answer-check-skipped`), 3 killed by the judge-server scenarios.
- **pigeq**: three scenarios, `judge-regret-question-rides-the-first-request-after-a-reply` (42 events),
  `judge-auth-failure-pauses-the-judge` (25) and `judge-answers-that-do-not-match-the-questions-fail-the-request` (41): the original
  under Pi == the original under PiG's Node runtime == the Go port, event for event, including every request to the fake judge. Goldens
  are recorded from the original. Under host load (load average 100 to 200) four scenarios of the stack differed once at event 10 or 11
  (`llm` before `turn_end`, the harness's event order); each passed three further runs.
- **Shared file**: the port's `holds.db` and the original's are one file format: the original wrote six rows with `node:sqlite`
  and the port read them (stats, smart history), and the port wrote six rows and the original read them (stats, history,
  recommendations, steer report): `port/perf/learning-cross.mts` with `TestLearningCrossCheck` (needs `WARDEN_LEARNING_CROSS`).
- **Performance** (median; 4 CPUs of the shared host, `taskset 100-103`, load average 90 to 200, so differences under 10% are noise):

  | | previous release (main 0.1.1) | guard core lane | this lane |
  |---|---|---|---|
  | single-extension Binary | 71,536,800 bytes | 72,294,560 | **76,554,400** (+4.26 MB, +5.9% over the guard core lane, +7.0% over the release; the engine) |
  | startup, 41 interleaved runs, `-p` through the fused Binary, warden off | 71.1 ms | 62.7 ms | 74.3 ms (seed-check control 70.8 ms) |

  The warden package's own init is 0.9 ms and 245 KB (the engine adds 0.5 to 0.9 ms of package init in the Binary). An off warden
  opens no database (`learnEnsure`): before that rule the first-use cost of the engine (open, WAL, schema) added about 11 ms to a run.
  Per operation, 500 rows of one project, Go 4 CPUs against the original under Node 24 (`node:sqlite`) on the same CPUs:
  `recordHold` 173 us (40 allocs, 2.4 KB) against 165 us; `generateRecommendations` 800 us (104 allocs) against 490 us;
  `analyzeSteerEffectivenessReport` 330 us against 185 us; `holdStats` 495 us (54 allocs) against 298 us. The pure-Go engine is
  about 1.6 times slower on the reads. They run in `/warden recommend` and `/warden status` (sub-millisecond), never on the tool-call
  path; the per-call work is the ledger (630 ns, 12 allocs, 888 B) and an enqueue, and the database write runs in the background
  (173 us). The steer counts write 3 KB in 170 us per observed steer. This is the >10% regression the quality bar names: the owner
  decision is the dependency itself (no cgo, one file format); a faster path would need a cgo SQLite.
  The original under PiG's Node runtime was not measured for startup: it is on by default and its first-run judgment notice is a
  message the faux model of the perf harness refuses (`unhandled request`), so a headless run of the original ends with an error.
