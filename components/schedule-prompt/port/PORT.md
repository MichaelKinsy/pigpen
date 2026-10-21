# Port record: pi-schedule-prompt

| Input | Identity |
|---|---|
| Original | `pi-schedule-prompt` 0.4.1 ("Pi's Heartbeat") by tintinweb, https://github.com/tintinweb/pi-schedule-prompt, commit `5556775276202c26654ff9323541fe6983f6ee38`, MIT |
| Oracle | Pi `1402973a` (1.3.0), Node 24.19.0, with the unmodified package at `port/oracle` (dependencies `croner` 10.0.1 and `nanoid` 5 from npm; `@earendil-works/pi-ai`, `pi-coding-agent` and `pi-tui` are Pi's own) |
| Target | PiG 0.5.0 (+1.3.0.1402973a), go1.27 |
| Original's tests | 5 vitest files, 103 `it` cases (`port/upstream-tests.json`): **69 exact twins, 7 named skips** (slice 4: they pass `model`), 27 `subagent` cases and the 1 `cron-widget` case: the latter is a twin (counted in the 69 only if listed; see `pigeq twins check` output below) |
| Kind | 1: a Pi TypeScript extension, run under Pi as the oracle |
| Owner approval | 2026-10-21 (PROGRAM.md, "port two heartbeat/watcher extensions into Pigpen") |

## Slices

| Slice | Content | State |
|---|---|---|
| 1 | inventory and honest refusal | pushed (`port/schedule-prompt-1`, 0.1.0) |
| 2 | tool, scheduling, croner and V8 date semantics, storage, settings, firing, events, the widget, the message and tool renderers | pushed (`port/schedule-prompt-2`, 0.2.0) |
| 3 | `/schedule-prompt`: the Jobs overlay, the add flow and Settings (`Context.Custom` with `OnHandle`) | pushed (`port/schedule-prompt-3`, 0.3.0) |
| 4 | the per-task `model` mode (`model`, `notify`, `extensions`, `skills` on a subagent job) | this revision (0.4.0), on PiG's `Context.CreateAgentSession` (branch `feat/sdk-child-agent-sessions`, 8ecc5a4b) |

The widget is in slice 2 (not 3): in Pi's RPC mode the original's startup already calls `setWidget(key, undefined)`, so the widget's show and hide
logic is part of the observable trace of every scenario.

Slices 2 and 3 refused what was not ported yet with the program's refusal message (`/schedule-prompt`, a tool call with `model`, a stored job's model run); since slice 4 nothing is refused.

## Inventory

Every observable behaviour of the original, its source, the Go SDK operation it needs and the slice that ports it. Classes:
**Go** (portable with the public SDK), **SDK** (needs a PiG SDK capability; named, with the Pi API it mirrors), **owner** (needs a decision).

| ID | Original | Behaviour | Class | Slice |
|---|---|---|---|---|
| M1 | `types.ts:75-141`, `tool.ts:22-27` | the `schedule_prompt` tool definition: name, label, description, TypeBox parameters (`StringEnum` actions and type, `minLength` model, `anyOf` extensions and skills) | Go | 2 (the definition is already registered in slice 1) |
| M2 | `tool.ts:29-285` | actions `add`, `remove`, `list`, `enable`, `disable`, `update`, `cleanup`; errors come back as result text `✗ Error: ...` with `details.error`, not as a tool error | Go | 2 |
| M3 | `tool.ts:33-46` | recursion guard: an `add` is refused when one of the last 10 session entries is a custom `scheduled_prompt` message | Go (`Context.GetEntries`) | 2 |
| M4 | `scheduler.ts:362-433` | schedule parsing: relative `+Ns/m/h/d`, intervals `Ns/m/h/d`, ISO timestamps through `new Date(string)` (V8's parser: ISO and legacy formats), the past and the under-5-second checks, the 6-field cron check | Go (a V8-compatible `Date.parse`; proven by a differential test against Node) | 2 |
| M5 | `scheduler.ts:197-209`, `croner` 10.0.1 | cron validation errors (croner's own messages) and next-run times in the local time zone, DST included | Go (a port of croner's pattern, `CronDate` and `Cron`; proven by a differential test against croner 10.0.1) | 2 |
| M6 | `storage.ts` | `.pi/schedule-prompts.json`, `JSON.stringify(store, null, 2)`, temp file and rename, a re-read on every operation, key order as a JavaScript object keeps it | Go | 2 |
| M7 | `settings.ts` | two-layer settings (global `<agent dir>/schedule-prompts-settings.json`, project `<cwd>/.pi/schedule-prompts-settings.json`), `sanitize`, partial save | Go; `<agent dir>` is PiG's (owner rule of 2026-10-20 for tintinweb-tasks, D2) | 2 |
| M8 | `scheduler.ts:63-295` | session binding (`isLoadedFor`), `start()` with the stale-`running` sweep, the timers (cron, interval, once), the past-timestamp disable, auto-disable of once jobs | Go | 2 |
| M9 | `scheduler.ts:197-262` | firing: the empty-content marker, `sendUserMessage(prompt, {deliverAs: "followUp"})`, the statistics written back (`lastStatus`, `lastRun`, `runCount`, `nextRun`), error handling | Go (`Context.SendCustomMessage`, `Context.SendUserMessage` on a retained context) | 2 |
| M10 | `index.ts:105-145` | `session_start` (auto-cleanup of this session's disabled jobs unless `startup`, then a fresh scheduler), `session_shutdown` (auto-cleanup, stop) | Go | 2 |
| M11 | `index.ts:32-71` | the `scheduled_prompt` message renderer (inline, subagent start, done, error) | Go (`Extension.MessageRenderer`) | 2 |
| M12 | `tool.ts:288-377` | tool call and result renderers | Go (`Extension.ToolRenderer`) | 2 |
| M13 | `cron:change` on `pi.events` | change events, subscribed by the widget | Go (`Context.Events`) | 2, 3 |
| M14 | `ui/cron-widget.ts` | the widget below the editor: header, rows, relative times, a 30 s refresh, hide and show | Go (a `kit` lines view; `pitui` `Container`, `Text`, `DynamicBorder`) | 3 |
| M15 | `index.ts:150-195`, `ui/jobs-view.ts`, `ui/add-flow.ts` | `/schedule-prompt`: Jobs overlay (select, add, toggle, scope, remove, cleanup), the add flow, Settings | Go (`Context.Custom` with a `RemoteComponent` and `OnHandle`; `Select`, `Input`, `Confirm`) | 3 |
| M16 | `subagent.ts`, `scheduler.ts:264-358` | the per-task `model` mode: a fresh in-process agent session with the chosen model, `notify`, `extensions`, `skills`; start, done and error markers | **SDK**: C2 `createAgentSession`, see below | 4 |
| M17 | `scheduler.ts` (`console.*`) | log lines on stderr | Go | 2 |

### Upstream defects found while reading (not fixed, listed for the owner)

- **U1** (`scheduler.ts:155-170`, `scheduler.ts:127`): Node's `setTimeout` and `setInterval` take at most 2^31-1 ms (24.8 days). A `once` job more than 24.8 days away, or an `interval` job of 25 days or more (`+30d`, `30d`), makes Node print a `TimeoutOverflowWarning` and use 1 ms: the once job fires at once, the interval job fires every millisecond. Cron jobs are not affected (croner waits in 30 s steps).

### SDK capability needed for slice 4 (reported to the Tern lead)

- **C2 `createAgentSession`** (already item C2 in `core-items.md`): the original calls `createAgentSession({cwd, agentDir, sessionManager: SessionManager.inMemory(cwd), settingsManager, modelRegistry: ctx.modelRegistry, model, tools, resourceLoader})`, `session.bindExtensions({})`, `session.subscribe(...)`, `session.prompt(text)`, `session.abort()`, `session.messages`, and filters the loader with `extensionsOverride` and `skillsOverride`. The Go SDK has no child-session API. Until then the `model`, `notify`, `extensions` and `skills` parameters are refused in slice 2 with the program's message.

## Results (slice 2)

- `pigeq twins check --ledger port/upstream-tests.json --go extensions/schedule-prompt --deferred port/slices.json`: 69 exact twins, 7 skipped (each with its own reason, all slice 4), 27 deferred (`subagent`, slice 4).
- `go test -race` on the extension: pass (Mac, go1.27.2, `-count=2`); `go vet` pass.
- Differential tests against Node 24.19 (V8 13.6.233.17) and croner 10.0.1 (`port/diff`): `TestDateGolden` (21 554 generated date strings x 12 time zones, 15 000+ local-time constructor and getter rows around every DST transition 2019 to 2036) identical; `TestCronGolden` (1 200 expressions x 7 time zones incl. Lord Howe and Chatham, 10 instants x 2 chained runs, plus every validation error text) identical. A 40 000-expression corpus (`CRONER_GOLDEN=...`) found one difference class: croner throws `RangeError: Maximum call stack size exceeded` for patterns that match no date in every month of every year up to 3000 (for example `26 47 7-8 lw * +7-7`), which croner's recursion cannot finish; the port emulates the limit (`MaxRecurseDepth` 7300 steps; measured on Node 24: every pattern needing up to 6 824 steps is answered and every one needing 7 773 or more overflows, nothing in between in 46 000 patterns). A pattern in the 6 800 to 7 800 band may differ from Node (the real limit depends on the frames below and on JIT tiering).
- `pigeq record` + `pigeq check` (original under Pi 1402973a, port under PiG 0.5.0, `--skip-host-check`, xd670 tern-ci:v3, `nice -n 10`, cpus 64-95,160-191): 15 scenarios, goldens recorded from the original under Pi (`port/golden`). Three full `pigeq run` rounds before the goldens: round 2 identical for 10 of 15 (the rest were scenario defects: random ids in details, startup race of the every-second cron, script lengths), round 3 identical for 14 of 15. The last `check` against the freshly recorded goldens: 12 of 15 identical, and the three that differ (`add-variants`, `fire-cron`, `manage-seeded`) differ **only in the order of one event pair**: the port's next model request (`llm`) is recorded before the previous `tool_execution_end` or `turn_end`, which the same three scenarios did not show in the previous round and in the other twelve: the host-side ordering race already recorded by the tintinweb-tasks lane (finding 8) and PiG core item B10-class; it is not a content difference. Re-run counts to settle it are in the status file (pending).
- Evidence (`port/evidence`): `u1-long-once` recorded under Pi; `pigeq check` of the port against it differs, as D1 intends.
- `go test -race -count=2` of the extension and both internal packages: pass on xd670; `go vet` for linux, windows and darwin: clean (GOOS evidence lines in the run log `rec3.log`).
- Mutations: `port/mutations.json` (308 mutations, generator `gen-mutations.py` checks every `find` string is unique). First run, `pigeq mutate --unit-only --jobs 6` on xd670 (the port's own tests only, scenarios not used): **308 mutations, 210 killed, 98 not killed**. The survivors and invalid mutants are listed in `mutation-run-1.txt`. **Slice 2 is therefore not complete on the mutation bar.** Most survivors are wording of tool result texts and storage side effects (`tool-*`, `settings-*`, `subagent-*` text) that the scenarios compare against Pi but the unit tests do not; each needs a unit test or a scenario-level run (`pigeq mutate` without `--unit-only`, `--jobs 1`), and each INVALID mutant needs a rewrite. The croner and jsdate mutants are killed by the golden tests except `jsdate-parse-hour24` and the ones listed.

## Differences and findings

- **D1 (deliberate, lead decision 2026-10-21)** Long timers. Node's `setTimeout` and `setInterval` hold at most 2^31-1 ms; the original fires a one-shot more than 24.8 days away after 1 ms and an interval of 25 days or more every millisecond (U1). The port waits for the real delay (a Go timer takes any). Evidence: `port/evidence/scenarios/u1-long-once.json`, recorded under Pi. Upstream issue draft: `handoffs/pigpen-parity/upstream-issue-pi-schedule-prompt-long-timers.md` (not posted).
- **D2** The global settings file is `<PiG agent dir>/schedule-prompts-settings.json` (the host's `AgentDir`), not the fixed `~/.pi/agent`: owner rule of 2026-10-20 (D2, tintinweb-tasks).
- **D3** The original's `console.log` lines (`Executing scheduled prompt`, `Auto-cleanup`) are not printed; `console.error` and `console.warn` go to stderr.
- **D4** A store file that is JSON but not `{"jobs": [...]}` of objects is refused with a message (the original throws a TypeError from the first member it misses and never rewrites the file); an unparsable file reads as empty, as in the original.
- **D5** The widget is a `kit` view (host-rendered at the terminal width) pushed on change events, every 30 s and at mount; a theme change shows at the next push (SDK request C3+: `OnThemeChange`). In RPC mode nothing but the removal reaches the client, as in Pi.
- **D6** The change events reach the widget through the scheduler directly and are also emitted on the `cron:change` channel; the original's widget listens on the channel, so an extension emitting `cron:change` itself would refresh it there and not here.
- **D7** (slices 2 and 3 only, gone in slice 4) The per-task model refusal.
- **U2** (upstream defect, kept) The recursion guard looks for a session entry of type `custom` with customType `scheduled_prompt`, but `pi.sendMessage` writes a `custom_message` entry: the guard never trips on a real session. Scenario `recursion-after-fire` shows the original accepting an add after a scheduled prompt ran; the port does the same.
- **U3** (upstream, kept) `validateCronExpression` starts a croner timer loop for every validated expression and never stops it (a leaked no-op timer); the port validates without a timer.
- **Host check (original under PiG's Node runtime)** was skipped for the goldens: B2 (`core-items.md`): PiG's RPC mode emits `widgetLines` for a component widget where Pi emits nothing. Not yet run on the scenarios without a widget.
- **SDK table finding** `pigeq gaps --ts` reports `ctx.ui.custom(options.onHandle)` MISSING; the SDK source of 0.5.0 has `RemoteOverlayOptions.OnHandle` (`overlay_handle.go`). Accepted in `port/accepted-gaps.json` with that citation (not an owner approval); needed from slice 3.

## Slice 3: `/schedule-prompt` (0.3.0)

Ported: `index.ts` command handler, `ui/jobs-view.ts`, `ui/add-flow.ts` (M15) as `command.go`, `jobsview.go`, `addflow.go`. The Jobs overlay is an
`sdk.RemoteComponent` opened with `Context.Custom` (full width, up to full height, `OnHandle` hides it while the add flow's dialogs are open and
focuses it afterwards); the add flow and Settings use `Select`, `Input`, `Confirm` and `Notify`.

The original has **no tests** for any of this (its five test files cover the tool, the scheduler, the settings, the widget and the subagent runner), so the evidence is
built, not ported:

- **Frames** (`port/frames`, as in ask-user-question): `record.ts` runs the unmodified `JobsView` and `runAddFlow` under Pi 1402973a9d's pi-tui with a recording theme
  (`<fg:dim>text</fg>`, `<b>`: colour tokens and nesting are compared) and a scripted `ctx.ui`, for 38 scenarios (`gen-scenarios.py`): 19 overlay scenarios (empty, one
  group, two groups, navigation and its bounds, toggle, scope, remove and cleanup with every way out of the prompt, foreign rows read-only, quit keys, the add key with a
  runAdd that succeeds, adds a job or fails, unknown keys, missing fields), each rendered at widths 40, 90 and 120 after every key, with the store file and the calls to the scheduler;
  19 add-flow scenarios (every branch: cancel or empty at each prompt, duplicate name, re-asking the schedule for cron, interval and once, trimming, scope order from the default,
  declined, answers running out), with every dialog, notification, the store file and the scheduler call. `TestFramesAgainstOriginal` replays all 38 against the port: identical.
- **Scenarios** (`port/scenarios/cmd-*`, original under Pi against the port under PiG over RPC): the menu and Settings with the dialogs answered from the scenario, the settings file
  read back by the model: widget toggles, scope toggles, a failed write (`.pi` is a file), the global setting, cancel and empty answers, and `Jobs` (in RPC mode the host answers
  the component with nothing and the command ends, in both). 11 scenarios identical (recorded under Pi, `pigeq check`).
- `fire-cron` was reworked: its prompt step raced the job's first fire for the first model turn (2 of 30 reruns differed in the order of the widget removal and the tool result);
  the step is now a `get_state` wait, so only the fire drives the model.

Differences (all SDK-bound): **D8** the overlay's theme is the host's `UITheme` captured when the command runs (a theme change while the overlay is open shows at the next command; SDK
request C3+ `OnThemeChange`); **D9** the add flow runs on its own goroutine as the original's un-awaited promise does, and a failure is logged `[pi-schedule-prompt] add flow failed: <err>` (the
original prints the JavaScript error object).

## Slice 4: the per-task model mode (0.4.0)

`subagent.go` ports `subagent.ts` (`resolveModel`, `getLastAssistantText`, `describeAvailableModels`, `runSubagentOnce`) onto `sdk.Context.CreateAgentSession`
(PiG's counterpart of Pi's `createAgentSession`, C2): `CreateAgentSessionOptions{Cwd, Model: <the resolved model object>, InMemory: true, Tools: DEFAULT_TOOL_NAMES unless
extensions are on, Resources{NoExtensions, NoSkills, NoPromptTemplates, NoThemes, ExtensionNames, SkillNames}}`; `BindExtensions` when extensions are on; `Subscribe` collects the
`text_delta`s of the last message; `Prompt` with the abort signal as its context (an already aborted signal aborts the idle session, which does nothing, and the run still happens, as in the
original); the session is disposed afterwards (the original leaks it). The `model`, `notify`, `extensions` and `skills` parameters of `add` and `update` are accepted (the refusal, D7, is gone), the add result
carries the `Model:` line, and the scheduler's subagent path (ported in slice 2) now runs the real session. `extensionsOverride` and `skillsOverride` are the host's
`ExtensionNames` (path contains the name, case-insensitive) and `SkillNames` (exact name).

- **Twins**: `pigeq twins check`: all 103 upstream cases are twinned (`twin_subagent_test.go`: the 27 cases of `subagent.test.ts`; the 7 `tool.test.ts` cases that pass `model` are
  exact twins now). The mocks of the original (`createAgentSession`, `DefaultResourceLoader`) are the `subagentHost` seam: `loaderOptions.noExtensions` is `Resources.NoExtensions`,
  `sessionOptions.tools` is `Tools`. Two twins ("filters extensions", "filters skills") check that the name list reaches the session options; the filter itself runs in the host
  (`extensionsOverride`/`skillsOverride` are functions of the original's process), where PiG's own tests cover it.
- **Scenarios** (`port/scenarios/sub-*`): original under Pi against the port under a PiG built from `feat/sdk-child-agent-sessions`, the one-shots firing after the parent's turns: a text
  answer, notify (the parent wakes), an unknown model (the error lists the available models), a session that uses a tool, `extensions` and `skills` true, an empty answer, add/list/update with a
  model and the store, and the empty-model refusals: 8 scenarios identical.
- The model is resolved by the port against `ModelRegistry.Find` and `GetAvailable`, as `ctx.modelRegistry.find` and `getAvailable()`.
