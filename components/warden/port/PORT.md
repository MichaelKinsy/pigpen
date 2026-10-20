# Port record: warden (pi-warden)

| Input | Identity |
|---|---|
| Original | `DevMortimer/pi-warden` 0.80.0, commit `a12b2703b2c7ac32401fe13f4ce037d673a5f113`, MIT (Ryan Gapac). Kept unmodified at `port/oracle` |
| Judge seam | `DevMortimer/pi-typesafe` commit `ed439f834665ad6fc652787f5ba7c23852566d49` (`ask`, questions, `errors.ts`), MIT |
| Oracle | Pi 0.87.1 hosting the original; Node 24.19.0 |
| Target | PiG 0.3.0+0.87.1, SDK of a 0.3.0 pre-release build (`63c6ba456`) (host getters return `(value, error)`), go1.27.1; shared client `components/typesafe` |
| Original's tests | 1,264 cases in `port/oracle/tests`; 164 have a Go twin, 1,100 are named skipped subtests with a reason (see below) |

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

## Scope

Ported: the action guard (offline pattern floor, judged irreversible / off-task / off-plan / should-proceed / approval,
hold and release, sibling prejudging, user command and deny rules, exempt rules), the stuck guard (exact and
successful repeats, churn, quick repeat, judged loops, stuck diff), the done-check (evidence, UI proof, false claims),
the task spine, steer delivery with the per-run budget and repeat window, the trace widget, the project rules guard
(`pi-warden.md`: parsing, scoping, per-rule judged requests, scores, severity and soft tier, sensitive-path notes, the
rules log, `/warden rules` and its `check`, `audit`, `calibrate` and `tune`, `/warden report`, `/warden init`,
end-of-run turn rules, literal shell writes judged as writes), and the memory tools: the three
tools `warden_remember`, `warden_loops` and `warden_recall`, standing preferences (the scan of earlier sessions of the project
and its worktrees, the seven injection rules, the lesson store, forget), open loops (store, reminder at the end of a run, message on resume), the compaction evidence
appendix (`session_compact`), the recall footer (`recall.go`), and `/warden prefs [forget <n>]` and `/warden loops`.

Not ported (each upstream case is a named skipped subtest in `skipped_twins_test.go`, generated by
`port/gen-skipped-twins.py`; OWNER DECISION NEEDED to add any of them): slop and prose
guards, security masking and secret detection, runaway output, context saver and relevance compaction, conscience,
subagent supervision, call-waste, adaptive steering, path and arming rules, session scratch,
host paths, SQL target classification (`sql-delete-local`, `sql-hosted*`; the plain `sql-*` rules still apply),
the shell-write summary of `describeAction` (`shellWrites` is ported), the large-output steer, jev violation escalation (the `violation_i` questions), the hold
feedback database and regret question, learning, the config schema migration, and stuck evidence (runs as upstream's
documented `stuck.evidence: false`). The ledger of saved outputs and the hold records are interfaces here
(`outputLedger`, `holdRecords` in `compact.go`): until those modules land the appendix and `warden_recall` list no saved output and no held action.

Refused, not silent: the `/warden` actions `config`,
`audit`, `index`, `recommend`, `unmute` and `bench` answer with a command notification: "@pi-in-go/pigpen-warden
(Go port) does not support <feature> yet. Install the original extension, npm:pi-warden@0.80.0, which runs unchanged in
PiG." Also not yet ported: the trace panel shortcut and the upstream events with no Go handler (message_*,
input, agent_settled; the compaction relevance hook `session_before_compact`).

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

pigeq: 5 scenarios (`sensitive-path-note-steered` and `rules-file-offline-write-runs`, `rules-offline-fallback-document-write-runs`,
`rules-offline-heredoc-write-runs`, `rules-offline-turn-rules-without-git-run-quietly`), goldens recorded from the original;
Pi original == original under PiG Node == Go port on all five (`pigeq check`: 24 of 24 scenarios PASS, with the 19 earlier ones).
Mutations: 21 layer-1 rows (`port/mutate.py`, `port/mutation-results.txt`, all killed by the twins) and two pigeq rows
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

Stacking notes: when the supervision lane lands, register `w.sup.blockInput(w.InitBusy, "pi-warden is generating rules. Please wait...")`
in `newWarden` (first of init, audit, index), and set `ShellWriteTargets` (waste.go) from `shellWrites(command, ...).Writes`
paths. `discover.go` is a copy of the audit lane's until it lands (commit `c0a805e`): drop it on stacking.
