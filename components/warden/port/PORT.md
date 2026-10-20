# Port record: warden (pi-warden)

| Input | Identity |
|---|---|
| Original | `DevMortimer/pi-warden` 0.80.0, commit `a12b2703b2c7ac32401fe13f4ce037d673a5f113`, MIT (Ryan Gapac). Kept unmodified at `port/oracle` |
| Judge seam | `DevMortimer/pi-typesafe` commit `ed439f834665ad6fc652787f5ba7c23852566d49` (`ask`, questions, `errors.ts`), MIT |
| Oracle | Pi 0.87.1 hosting the original; Node 24.19.0 |
| Target | PiG 0.3.0+0.87.1, SDK of a 0.3.0 pre-release build (`63c6ba456`) (host getters return `(value, error)`), go1.27.1; shared client `components/typesafe` |
| Original's tests | 1,264 cases in `port/oracle/tests`; 434 have a Go twin, 830 are named skipped subtests with a reason (see below; with the guard core lane stacked on the rules lane) |

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
| `redact.ts` `redact` | `redact.go` | `VALUE_AMPERSAND` lookahead written as a scan |
| `redact.ts` detection: `findSecrets`, `looksLikeSecretValue`, `syntheticish`, `partitionSecrets`, `maskSecrets`, `secretFingerprint`, `secretIds` | `redact_secrets.go` | the credential-assignment rule is a scan like `redact` (no lookahead); signed-URL spans are byte offsets, not UTF-16 units |
| `output.ts` security half: `evaluateOutput`, `buildOutputRequest`, `outputQuestions`, `securityNotice`, `OutputVerdict` | `output_security.go` | the retention and format half (`compressOutput`, `retentionQuestion`, learner) belongs to the context lane (`output_context.go`); `evaluateOutput` reaches it through `retentionQuestions`, `applyRetention`, `learnerSuggest` |
| `runaway.ts` | `runaway.go` | `RunawayMonitor` is used under one mutex by the message handlers; strings are measured in UTF-16 units |
| `prose.ts` (prose check, `ProseTrend`, `RestatementWindow`, `proseNudge`) | `prose.go` | the check runs in a goroutine beside the done check (upstream starts both promises, then awaits prose first) |
| `notify.ts` | `notify.go` | commands run through `os/exec` in the extension process, as upstream's `execFile` does (D12) |
| `extension.ts` `slopSteer`, `SLOP_FIXES`, the slop content note | `slop.go` | the note is a `callNotes` content note of the rules lane's combined steer |
| `extension.ts` security hooks (`message_start`/`message_update` runaway, `agent_end` slots 3, 5 and the prose check, `notifyDesktop`, the security-write steer) | `security_wiring.go` (one call each from `extension.go`, `results.go`, `toolcall.go`, `commands.go`) | the tool-output masking path (`tool_result`) is the context lane's `tool_output.go`, which calls `MaskSecrets`, `EvaluateOutput` and `SecurityNotice` |
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
preferences and loops, subagent supervision, call-waste, adaptive steering, the hold feedback database and the
settling of the regret question (the question itself is ported), learning, the config schema migration, and stuck
evidence (runs as upstream's documented `stuck.evidence: false`).

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
`audit`, `index`, `prefs`, `loops`, `recommend`, `unmute` and `bench` answer with a command notification: "@pi-in-go/pigpen-warden
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
| D12 | desktop notifier commands run with Node's `execFile` (title and body in `PI_WARDEN_TITLE` / `PI_WARDEN_BODY` for PowerShell and configured commands) | the same, through `os/exec` in the extension process; a configured command is spawned inside the hook and only the wait for its exit is off the handler, the probe chain and built-in notifiers run in a goroutine | the host's `Context.Exec` takes no environment (an SDK need: `ExecOptions.Env`), and `execFile` is what the original does |

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
- The `rules.*` hook `holds.recordRules` is the func variable `holdsRecordRules` (no-op until the learning lane lands).
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
- `scenarios-blocked/`: the real-git scenarios (host event ordering differs between Pi and PiG's Node runtime) and the
  config-dependent ones (a scenario cannot write `<PIG_HOME>/pigpen-warden/config.json`).
- Benchmarks (`guardcore_bench_test.go`, imladris container on cores 96-151, load average 63 over 56 cores at the time, so
  these are upper bounds; repeat when quieter), median of 10 runs: ordinary command 131 us/op, 6.4 KB, 419 allocs;
  the same call with three path rules 238 us/op, 12.5 KB, 777 allocs; a loopback `psql ... DELETE ... WHERE` 361 us/op,
  15.7 KB, 645 allocs; `ArmingTracker.CheckArmed` on an armed rule 8.5 us/op, 0.8 KB, 69 allocs. No baseline of the
  previous head and no binary-size or cold-start measurement yet (those need a built Binary; the package-init test bounds
  start-up allocation at 150 KB and passes).
- Not run here: `npm run generate/check/test` (an imladris copy has no git history; run from a bundle clone at stacking).
