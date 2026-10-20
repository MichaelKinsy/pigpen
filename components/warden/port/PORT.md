# Port record: warden (pi-warden)

| Input | Identity |
|---|---|
| Original | `DevMortimer/pi-warden` 0.80.0, commit `a12b2703b2c7ac32401fe13f4ce037d673a5f113`, MIT (Ryan Gapac). Kept unmodified at `port/oracle` |
| Judge seam | `DevMortimer/pi-typesafe` commit `ed439f834665ad6fc652787f5ba7c23852566d49` (`ask`, questions, `errors.ts`), MIT |
| Oracle | Pi 0.87.1 hosting the original; Node 24.19.0 |
| Target | PiG 0.3.0+0.87.1, SDK of a 0.3.0 pre-release build (`63c6ba456`) (host getters return `(value, error)`), go1.27.1; shared client `components/typesafe` |
| Original's tests | 1,264 cases in `port/oracle/tests`; 90 have a Go twin, 1,174 are named skipped subtests with a reason (see below) |

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

## Scope

Ported: the action guard (offline pattern floor, judged irreversible / off-task / off-plan / should-proceed / approval,
hold and release, sibling prejudging, user command and deny rules, exempt rules), the stuck guard (exact and
successful repeats, churn, quick repeat, judged loops, stuck diff), the done-check (evidence, UI proof, false claims),
the task spine, steer delivery with the per-run budget and repeat window, the trace widget, the configuration loader
(user file, trusted project file, validation, warnings, reload on change), `/warden config get|set|editor`, the trace
sidebar and the config overlay with the `widget.shortcut` toggle, the status widget's rendering (stack and live bars,
placement), the optional trace file, and the config schema completion.

Not ported (each upstream case is a named skipped subtest in `skipped_twins_test.go`, generated by
`port/gen-skipped-twins.py`; OWNER DECISION NEEDED to add any of them): the rules guard (`pi-warden.md`), slop and prose
guards, security masking and secret detection, runaway output, context saver and relevance compaction, conscience,
preferences and loops, subagent supervision, call-waste, adaptive steering, path and arming rules, session scratch,
host paths, SQL target classification (`sql-delete-local`, `sql-hosted*`; the plain `sql-*` rules still apply),
shell-write summaries, the large-output steer, jev violation escalation (the `violation_i` questions), the hold
feedback database and regret question, learning, and stuck evidence (runs as upstream's
documented `stuck.evidence: false`).

Refused, not silent: the `/warden` actions `rules` (and `rules check|audit|calibrate|tune`), `report`, `init`,
`audit`, `index`, `prefs`, `loops`, `recommend`, `unmute` and `bench` answer with a command notification: "@pi-in-go/pigpen-warden
(Go port) does not support <feature> yet. Install the original extension, npm:pi-warden@0.80.0, which runs unchanged in
PiG." The tools `warden_remember`, `warden_loops` and `warden_recall` are not registered (not yet ported), so the model is
not offered them. Also not yet ported: the upstream events with no Go handler (message_*, input, compaction,
agent_settled).

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
