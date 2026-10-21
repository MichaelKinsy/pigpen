# Port record: @tintinweb/pi-subagents (partial)

| Input | Identity |
|---|---|
| Original | `@tintinweb/pi-subagents` 0.19.0 by tintinweb, https://github.com/tintinweb/pi-subagents, commit `4f572eaa04c09d3dbc16e4a5f13a16b295e84e14`, MIT |
| Oracle | Pi 1.0.3, Node 24.19.0, with the unmodified package at `port/oracle` (dependencies from npm: `croner`, `nanoid`, `typebox`) |
| Target | PiG 0.4.1+1.0.3 (release v0.4.1, commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`), go1.27.1 |
| Original's tests | 2029 cases in 105 files (`port/upstream-tests.json`). Twins: `agent-types` 46, `custom-agents` 76, the workflow slice (see "The SubagentWorkflow runtime") 361 exact + 9 named skips, and the scheduling slice `schedule` 28, `schedule-e2e` 6, `schedule-params-wiring` 9, `schedule-store` 13; the rest deferred by name in `port/slices.json`, each file with its reason |
| Kind | 1: an extension, no CLI built-in, no provider |
| Not this | `nicobailon/pi-subagents`, ported as `components/subagents`. A different project |

## What this port ships (the slice), and what it does not

The original is 21,000 lines: an in-process agent runtime on Pi's SDK, a JS workflow engine, a scheduler, worktree
isolation, `@mention` agents, persistent memory, and a TUI of widgets, a fleet view and a conversation viewer. A Go
extension cannot host a Pi session, so the shipped slice is the part that has a faithful Go form:

- the three tools `Agent`, `get_subagent_result`, `steer_subagent` with the original's text and schemas;
- agent types: the three defaults, custom agent files (three directories, strict mode, warn-once), settings
  (`maxConcurrent`, `defaultMaxTurns`, `fallbackSubagent`, `disableDefaultAgents`, `schedulingEnabled`), the spawn policy (#183);
- background by default, a concurrency limit and a FIFO queue, handles (`explore`, `explore-2`) and names, a held
  completion notification that a read of the result cancels, the event-bus protocol v2 `pi-tasks` speaks;
- scheduled runs: the `schedule` parameter of `Agent` (cron, interval, one-shot), the session-scoped job store, the
  scheduler, its events, and the data of the `/agents` Scheduled jobs menu (G2);
- `/agents`: the types, the agents and the scheduled jobs of the session.

Everything else is deferred, by name, in `port/slices.json` (reasons per file). The Agent tool is advertised **as the
original advertises it with workflows and worktree isolation switched off** (`.pi/subagents.json`: `workflowsEnabled`
and `worktreeIsolation` both `false`) and scheduling on, its default, which is exactly what this port offers.

## Mapping

| ID | Original | Go | Scenario / test |
|---|---|---|---|
| M1 | `index.ts`: the three tool definitions (descriptions, schemas, prompt snippets and guidelines) | `tools.go`, `agent_description.txt`, `tool_specs.json`, `agent_guidelines.json` (extracted from what Pi sends, `port/gen-spec.py`) | `tool-tools` (the request, system prompt included, equals Pi's); `extra_test.go` |
| M2 | `agent-types.ts`: registry, case-insensitive lookup, defaults, `resolveSpawnType` and `fallbackSubagent` | `agent_types.go` | 46 twins in `agent_types_twin_test.go`; `agent-unknown-type-strict` |
| M3 | `custom-agents.ts`, `agent-file-toggle.ts` (serializer): agent files, tools field, inherit fields, name rules, warnings | `agents.go`, `frontmatter.go`, `serialize.go` | 76 twins in `custom_agents_twin*_test.go` (the loader reads PiG's agent dir, D2) |
| M4 | `default-agents.ts`: the three default agents | `defaults_text.go` (extracted byte for byte) | `agent_types_twin_test.go`; `tool-tools` |
| M5 | `settings.ts`: `subagents.json`, global then project | `settings.go` (five fields) | `settings_test.go`, `turnlimit_test.go` |
| M6 | `prompts.ts`, `env.ts`: the agent's system prompt and environment block | `prompts.go` | `extra2_test.go` (exact strings), inserted text equal to Pi's in a recorded child run (see F2) |
| M7 | `agent-manager.ts`: records, limit, queue, handles, abort, steer, consume | `manager.go` | `manager_test.go`, `extra_test.go`, `extra2_test.go` |
| M8 | `agent-runner.ts`: an agent's run, the turn limit (steer at `maxTurns`, abort at `maxTurns + graceTurns`), `finalTurnError` | `runner.go`: a child `pig --mode rpc --no-session` | `runner_test.go`, `turnlimit_test.go`, `proc_unix_test.go` (a fake pig, the test binary re-executed) |
| M9 | `cross-extension-rpc.ts`: ping, spawn, stop, consume | `bus.go` | `bus_test.go` |
| M10 | completion events and the held notification (`index.ts` `formatTaskNotification`, `getStatusLabel`; `status-note.ts`; `xml.ts`) | `extension.go` | `tools_test.go`, `extra_test.go`, `turnlimit_test.go` |
| M11 | the Agent, result and steer tools' behavior and texts | `tools.go` | `agent-not-found`, `agent-unknown-type-strict`; `tools_test.go` |
| M12 | `schedule.ts`: `SubagentScheduler` (jobs, intervals, one-shots, cron; `addJob`, `removeJob`, `updateJob`, `getNextRun`, the fire path, `detectSchedule` and the parsers); `index.ts` `startScheduler`, the session hooks | `schedule.go`, `schedule_tool.go` | 28 twins of `schedule.test.ts` (`schedule_twin_test.go`), 6 of `schedule-e2e.test.ts`; `schedule_test.go`, `schedule_hardening_test.go` |
| M13 | `schedule-store.ts`: the per-session file, the PID lock, the atomic write | `schedule_store.go`, `pidalive_*.go` | 13 twins of `schedule-store.test.ts`; `schedule_test.go`, `schedule_hardening_test.go` (two processes, a damaged file) |
| M14 | croner 10.0.1 (`Cron`, `CronPattern`, `CronDate`) as `schedule.ts` uses it: a 6-field pattern, the next run, the 30 s re-arm | `cron.go` (a hand port, no Go dependency) | `cron_test.go` replays 5,000+ answers recorded from croner (`testdata/croner-vectors.json`, `port/record-croner.mjs`) in six time zones |
| M15 | `index.ts`: the `schedule` branch of `Agent`, `scheduleParamShape`, `scheduleGuideline`, `schedulingEnabled` | `tools.go`, `schedule_tool.go`, `schedule_feature.go` | 9 twins of `schedule-params-wiring.test.ts`; `schedule-tool-text`, `schedule-refusals`, `schedule-disabled` |
| M16 | `ui/schedule-menu.ts`: the job rows, the relative times, the details; the menu entry `Scheduled jobs (N)` | `schedule_menu_data.go`; `/agents` lists the rows | `TestScheduleMenuData`, `TestAgentsListsTheScheduledJobs` (the interactive menu and its 11 twins are deferred) |

## The SubagentWorkflow runtime (gap G1, the non-UI part)

The original runs a user-authored script in a Node worker thread inside a `vm` context. This port runs it in-process, in
Go, with no Node.

**Engine decision: [goja](https://github.com/dop251/goja)** (`github.com/dop251/goja v0.0.0-20260723142020-b4aef50fa347`,
MIT, the version `pig-music` already pins; with `dlclark/regexp2` BSD-3-Clause, `go-sourcemap/sourcemap` BSD-2-Clause,
`google/pprof` Apache-2.0, `golang.org/x/text` BSD-3-Clause as its requirements), a pure-Go ECMAScript engine, required in
`extensions/tintinweb-subagents/go.mod` with a plain `require` (no `replace`, as `components/a2a` does for a2a-go).
Spike, before any code (a scratch module, `async`/`await`, `Promise.all`, classes with private fields and statics,
`Map`/`Set`, `JSON`, optional chaining, object spread, named regex groups, `Array.flat`, `replaceAll`, `BigInt`,
generators, `class X extends Date` and `globalThis` all ran; `vm.Interrupt` stopped `while(true){}`). What the scripts and
the original's `worker-source.ts` need, and how each is met:

| Need | Original | Here |
|---|---|---|
| async/await, Promises, `Promise.all` | V8 | goja (ES2020); the job queue drains when a resolution returns to the realm |
| line numbers of errors equal the author's | prelude on its own line + `lineOffset: -1` | goja has no line offset: the prelude is on the first line of the body (`(async () => {PRELUDE` + body), so line N of the file is line N of the stack; only columns of line 1 differ |
| `Date.now`/`Math.random`/`new Date()` throw | prelude (`const Date = ...`) | the same prelude, byte for byte, in `workflow_engine.go` |
| `eval`/`Function(...)` throw `EvalError` | `codeGeneration: { strings: false }` | the realm's `eval`, `Function`, `Function.prototype.constructor` and the `AsyncFunction`/`GeneratorFunction` constructors are replaced by a function that throws `EvalError` (`workflow_worker.js banCodeGeneration`); a determinism boundary, as in the original, not a security one |
| kill a runaway script | `worker.terminate()` | `vm.Interrupt` from the host goroutine, then the worker goroutine ends (`workflowWorker.terminate`); a test checks no goja goroutine is left |
| the host API a script sees | `agent`, `parallel`, `pipeline`, `phase`, `log`, `workflow`, `budget`, `console`, `meta`, `args` | the same, from `workflow_worker.js`, a line-for-line port of `WORKER_SOURCE` (messages instead of `postMessage`, a `host` object instead of `require`) |
| JSON boundary (cycle, BigInt, Map, sparse array, NaN, function) | checked in the worker | the same checks, in the realm |

The worker is a goroutine with a task queue (the event loop); the run is the host half (`workflow_runtime.go`), a port of
`runtime.ts` that keeps the original's single-threaded discipline with a baton (`workflowRunState.mu`): a handler gives it
up only inside an `await` (a host call, the semaphore, a pause hold), so call order is the order the script made the calls.

Differences from the original (each deliberate or forced by the engine):

1. **No async generators.** goja 20260723 does not parse `async function*`/`for await` over one (`Async generators are not
   supported yet`). A script that uses one fails to compile with that message. Ordinary async functions, generators and
   Promises work.
2. **Syntax error text** is goja's (`Line 2:7 Unexpected token ;`), not V8's; a thrown error's `message` is the script's.
   Stack frames read `at fn (workflow.js:LINE:COL(pc))` (goja), where V8 prints `at fn (workflow.js:LINE:COL)`.
3. **Regular expressions** in a *script* are goja's (ECMAScript, with lookahead and back-references through regexp2); the
   `pattern` of an `agent({ schema })` is a Go RE2 expression (the JSON Schema validator is Go: below).
4. **JSON Schema validation is a Go validator** (`workflow_jsonschema.go`) of the keywords the original's `typebox` package
   checks, with typebox's English messages and its keyword order (`must have required properties a, b, c`, `must be >= 1`).
   A `$ref` other than a local pointer, `format`, `unevaluated*` and `$dynamicRef` are not validated; a lookahead pattern is
   refused at compile time with the usual "is not a schema this runtime can validate".
5. **`args` object key order is not kept** (the tool's parameters arrive as a Go map); a script iterating `Object.keys(args)`
   sees sorted keys. The script's *return value*, the journal and a schema keep the script's key order (they are serialized
   inside the realm).
6. **What a child process cannot do yet is refused, and fails the run.** `agent({ isolation: "worktree" })`,
   `agent({ schema })` (the `StructuredOutput` tool) and `agent({ resume })` of a finished agent's session fail the *run*
   with the refusal text (`workflowRefusalError`, which `startOwned` returns too, for an unported agent-file feature): the
   runtime fails the run directly (`finish`), not with a call error a script's `try/catch` could swallow, so a script
   must never be handed a `null` that means "not supported" nor carry on after catching it. The
   runtime, the host, the journal and the twins implement all three; the adapter over today's manager
   (`workflow_manager.go`) refuses them until the worktree, observability and sessions slices land (hooks assumed below).
7. **A child's effective model is not read back.** The original reads `record.invocation` (the model its session resolved
   to, the clamped thinking level). A child here is a process whose session is not observable, so a row keeps the model the
   script asked for. The host implements the read-back (`onResolved`) and is twinned with a manager that reports it.
8. **Worker crash.** `Workflow worker exited before completing.` only on a crash of the worker goroutine; like the
   original's worker (whose `parentPort` listener keeps it alive) a script awaiting a promise nothing settles stays parked
   until the run is stopped.
9. **Scripts are compiled sloppy**, like the original (`vm.Script`, `Function` and `meta` evaluation have no `"use strict"`):
   implicit globals, legacy octal and `this === globalThis` in a plain function behave as they do there (goja is asked for
   non-strict mode at the three compile sites).
10. **Stack overflow is not catchable in a script.** V8 throws a `RangeError` a `try/catch` can handle; goja raises an
    uncatchable `StackOverflowError` at 10,000 frames. The run fails with V8's message, `Maximum call stack size exceeded`,
    wherever it happens (before or after the first `await`); a script cannot recover from it. The text of other engine errors
    (a `TypeError` for calling a non-function, `x is not defined`) is goja's wording, not V8's, and reaches the model in
    `<status>` and a failed row.
11. **An unhandled promise rejection fails the run**, as the original's worker `error` does (goja's rejection tracker, checked
    after each task once its promise jobs have drained). An `Error` reports its message; any other reason reports Node's
    `ERR_UNHANDLED_REJECTION` sentence with `String(reason)` where Node prints `util.inspect(reason)`.
12. **No Workflows UI yet.** `/agents → Workflows` (list, dialog, stop, pause, skip, retry) is the UI slice's
    (`workflow-dialog`, `workflow-dialog-open-agent`, `workflow-render` in `slices.json`). `/agents` lists the runs and says
    the menu is refused with the unified refusal text, and the one model-visible text
    that sends the model there and that a run can reach, `Workflow "id" is still running. Stop it from /agents → Workflows
    before resuming it`, is reworded (`resolveResumeTarget`: `Resume it once it has finished.`). The tool *description*
    (`Use /agents → Workflows to watch live progress`, the nested-workflow group sentence, `stop it from /agents →
    Workflows first`) is kept as the original's, byte for byte: the scenarios compare the model request, so rewording it
    fails `pigeq check`. It therefore still names the menu, which `/agents` refuses until the UI slice; the `--subagents-workflow-file`
    warning (a user message, as `workflow-off` records it) still names `/agents → Settings → Workflows`. Until then a running workflow ends by finishing, by session shutdown or by
    the script's own failure; nothing stops one from the UI.
13. **A gate command is not killed on abort (SDK gap).** Pi's `pi.exec(command, args, { signal, timeout, cwd })` kills the
    command when the signal fires; the SDK's `ExecOptions` has only `Timeout` and `Cwd`, and the host runs the command, so
    `exec.CommandContext` cannot reach it. The port refuses to start a gate on an aborted run and otherwise leaves a running
    one to finish or hit `GateTimeout`. Needed: `ExecOptions.Signal`/context on the Go SDK.
14. **Small things.** A workflow's own children carry no `@mention` handle or alias (as in the original). The concurrency
    default counts every logical CPU (`/proc/stat`), as `os.cpus().length` does, not the process's affinity mask.
    `wfUTF16Prefix` cutting inside a surrogate pair keeps U+FFFD (what UTF-8 encoding makes of the lone surrogate JavaScript
    would leave). `decideWorkflowCollision` reads the tool's `sourceInfo.source` as a string: the SDK cannot tell an absent
    `source` from an empty one, which Pi never produces. `meta` keeps `null` rejected, `""` and `[]` kept, as the original.

**Dependency.** goja `v0.0.0-20260723142020-b4aef50fa347` is MIT; its requirements are `dlclark/regexp2` (BSD-3-Clause),
`go-sourcemap/sourcemap` (BSD-2-Clause), `google/pprof` (Apache-2.0) and `golang.org/x/text` (BSD-3-Clause), all recorded
in `go.sum`. The source is 2.2 MB (regexp2 7.9 MB); the cost in the built extension is measured below.

Manager hooks assumed (for the lane lead at stacking): `agentRecord.Owner` (a workflow's child is out of `list()`,
`resolve()` and `onSettle`, in no pool slot as a foreground spawn), `manager.spawnOwned`, `app.startOwned`,
`childResult.OutputTokens`; `workflowManager` (`SpawnAndWait`, `Abort`, `Resume`, `GetRecord`) with its options
(`WorkflowID`, `ThinkingLevel`, `Invocation`, `OnSessionCreated`, `StructuredOutput`, `Isolation`, `OnBeforeWorktreeCleanup`);
the model policy seam `workflowHostDeps.CheckScope` (the settings lane's `checkModelScope`) and `workflowModelRegistry`
(`wfResolveModel` duplicates `resolveModel` until the settings lane's lands); `withWorkflowChildBlock` splices the
`<workflow_child>` block into the prompt (becomes `extras.workflowChild` when the prompt builder takes extras).

| ID | Original | Go | Test |
|---|---|---|---|
| W1 | `workflow/worker-source.ts` | `workflow_worker.js`, `workflow_engine.go` | `workflow_runtime_twin_test.go` |
| W2 | `workflow/runtime.ts`: the run, caps, semaphore, pause/skip/retry, journal replay, gate, resume, unawaited launches | `workflow_runtime.go` | `workflow_runtime_twin_test.go`, `workflow_borrowed_twin_test.go`, `workflow_journal_twin_test.go` |
| W3 | `workflow/host.ts` | `workflow_host.go`, `workflow_manager.go`, `workflow_models.go` | `workflow_host_twin_test.go` |
| W4 | `workflow/journal.ts`, `meta.ts`, `json-schema.ts`, `progress.ts`, `task.ts`, `saved.ts`, `collisions.ts`, `entry.ts` | `workflow_journal.go`, `workflow_meta.go`, `workflow_jsonschema.go`, `workflow_progress.go`, `workflow_task.go`, `workflow_saved.go`, `workflow_collisions.go`, `workflow_entry.go` | one twin file each |
| W5 | `workflow/tool-description.ts` | `workflow_tooldesc.go` + `workflow_tool_description.txt` (extracted by `port/gen-workflow-text.mjs`) | `workflow_tooldesc_twin_test.go`; scenario `workflow-tools` |
| W6 | `index.ts`: the tool, flag, switch, collisions, notification | `workflow_tool.go` | `workflow_tool_twin_test.go`; the `workflow-*` scenarios |
| W7 | `ui/workflow-menu.ts` (data and actions only) | `workflow_menu.go` | `workflow_command_twin_test.go` |

Twins of the slice: `pigeq twins check --files workflow-borrowed,workflow-claude-code-compat,workflow-command,workflow-effective-config,workflow-examples,workflow-gate-worktree,workflow-journal,workflow-json-schema,workflow-meta,workflow-progress,workflow-runtime,workflow-task,workflow-tool,workflow-tool-description,e2e/workflow.e2e` prints `361 exact twins, 9 skipped`. The nine skips, each with its own reason in the test: the three live-LLM smokes and the `StructuredOutput` e2e (a real provider; structured output is the observability lane's), the overlay options (the UI lane), the fleet row and the UI capture (UI lane), the entry card (UI lane), and the real-git branch assertion (the worktree lane). The workflow UI (card, dialog, menu, renderers: `workflow-dialog`, `workflow-dialog-open-agent`, `workflow-render`) stays in `port/slices.json` for the UI lane, which consumes `workflowProgress` snapshots (`workflow_progress.go`), run records (`workflow_task.go`), the menu plan and dialog actions (`workflow_menu.go`).

## SDK gap check (`pigeq gaps --ts port/oracle/src`)

One `MISSING`: `ctx.ui.custom(options.onHandle)` (`ui/workflow-menu.ts`), the workflow menu of the deferred slice. It
is accepted in `port/accepted-gaps.json` with the approval it rests on: the lane lead's instruction of 2026-10-05 20:56
MDT to ship a tested slice and defer the rest by name. (The Go SDK of PiG 0.5.0 does have `RemoteOverlayOptions.OnHandle`
and `OverlayHandle`, which the display surfaces use to close an overlay from outside; its `pigeq gaps` table is stale for
Go.) The Agent tool's `renderCall`/`renderResult` and the notification renderer are ported (see "Display surfaces").

## Performance of scheduled runs

Measured on imladris (Xeon 6746E, `go test -bench`, 200 iterations; two runs agree within 30%). The baseline is a
session with nothing loaded: the scheduler is started over an empty store.

| Start-up with | time |
|---|---|
| no jobs (baseline) | 4 us |
| 10 jobs on disk (a third each cron, interval, one-shot) | 0.3 ms |
| 100 jobs on disk | 2-4 ms |

That is the cost of reading the session's file and arming one timer per enabled job; with no jobs nothing is armed and
no goroutine starts (`TestAnIdleSchedulerHoldsNoTimer`). Package initialisation adds a dozen compiled regular
expressions. croner's next-run search takes about 2 us for an ordinary pattern, and about 1 ms for a pattern that never
runs (the search to the year 3000), once when the job is added or armed. A cron job wakes every 30 s (as croner does)
to check the clock; an interval or one-shot wakes once per due time, at most once a day while waiting.

## Results (this revision)

- `go test -race ./...` passes, also `-count=24` (twice). A later `-count=24` run found a data race the first `-count=25` run had missed: `manager.run` set an agent's cancel func without the lock that `abort` reads it under (stopping an agent right after spawning it, as a session end does); fixed, with `TestAbortRightAfterSpawnIsRaceFree` red first. The runner tests re-execute the test binary as a fake `pig --mode rpc`.
- `pigeq check` against goldens **recorded from the original under Pi 1.0.3**: 3 of 3 scenarios, plus `port-gaps` and
  `exec-coverage`. `tool-tools` compares the whole model request, so the three tool definitions, the Agent guidelines in
  the system prompt and the tool list equal Pi's.
- Workflow scenarios, recorded under Pi `1.3.0.1402973a` (the pi-src pin): `workflow-tools`, `-bad-meta`, `-no-meta`, `-no-source`,
  `-unknown-name`, `-bad-name`, `-resume-unknown`, `-missing-script-path` and `-off` (the flag warning, the tool list and its
  order: `SubagentWorkflow` is registered between `Agent` and `get_subagent_result`, as the original does; a first check found
  the port registering it last). **No scenario starts a run**: a run's task id (`wf_` + random hex) and `duration_ms` reach the
  model in the tool result and the task notification, and pigeq's normalizer (N1-N6) has no rule for either, so such a scenario
  fails against its own golden. Eight were written and dropped for that reason (`gen-scenarios.py` says which); a bare
  `--subagents-workflow-file` cannot be recorded because Pi refuses to start (`Extension flag ... requires a value`). Those
  behaviors are covered by the twins. When those scenarios were recorded, the original run under PiG differed from Pi at the
  fleet `setWidget` (PiG emits it before `tool_execution_end`, Pi after): a PiG core difference, reported to the lane lead.
- `pigeq mutate --unit`: 81 mutations, all killed (the first run killed 58; 16 survived and 7 did not build; each
  survivor got a test in `extra_test.go` / `extra2_test.go` and each invalid mutation was rewritten). After the review
  fixes (finding 10): **101 mutations, all killed by the unit tests** (`--unit-only`); the first run after the fixes left
  three (a setting that never reached the child, a process-group signal the test waited long enough to miss, and a test
  whose failure text the runner read as a build failure), each closed by a test.
- `pig package validate components/tintinweb-subagents` and `pig install <extension dir> --validate-only --json`: valid.
- Live check of the child's protocol against `pig-0.4.1 --mode rpc`: `get_session_stats` returns `data.tokens.total` and
  `data.toolCalls`, `get_last_assistant_text` returns `data.text` (or `{}`), a `prompt` without a usable model
  answers `success: false` with the reason, which the runner reports as the agent's error.

## Deliberate differences and findings

1. **A child process, not an in-process session.** Each agent is `pig --mode rpc --no-session`; an agent costs a process
   start. The pig to run is found as described in the README. The child inherits the parent's environment (and so its
   credentials and settings) and, unless the call or file sets one, the parent's model and thinking level. A child
   registers nothing (`TINTINWEB_SUBAGENT_DEPTH`): no nesting. `Explore`'s `claude-haiku-4-5` default is not forced on the
   child (the original falls back to the parent's model when it is not available); it is shown in the type list.
2. **F2: a recorded child run cannot be compared whole.** A foreground `Plan` agent was recorded under Pi and run under
   PiG. The child's request carries the same user message and the same inserted system text (the agent header, the
   environment block and the agent's prompt), after a fix this comparison found (a blank line in the replace-mode header).
   The harness's `removed` part is the host's own default system prompt, which says `pi` under Pi and `pig` under PiG, so
   that comparison cannot pass and the scenario is not kept (`port/gen-scenarios.py` says so).
3. **Unsupported parameters of `Agent` are refused.** `inherit_context: true`, `resume` and
   `isolation: "worktree"` are tool errors with the message in the README (`refuse.go`; the first version answered
   `inherit_context` with plain text and emulated `resume` by starting a new agent with the old answer in its prompt, a
   partial run, now removed). `isolated` starts the child with `--no-extensions`. The tool text is the original's, so the
   model is told about these; a call that uses one gets the refusal.
4. **Agent files are read at start-up for the tool text** (the workspace of an extension process is fixed), and again when
   the session starts and when the workspace differs. A file added later reaches `/agents` and the `subagent_type`
   check on the next session event, not the tool description of the running session.
5. **Settings.** Six of `subagents.json`'s fields are honored (`maxConcurrent`, `defaultMaxTurns`, `graceTurns`,
   `fallbackSubagent`, `disableDefaultAgents`, `schedulingEnabled`). A value of any other field that asks for an unported feature is
   announced once when a session starts, with the refusal message (`refuse.go`, `settingRules`); a malformed value is
   ignored silently, as in the original. Agent files that use an unported field, and the `subagents:rpc:spawn` options the
   port does not honor, are refused the same way.
6. **Not ported** (named in `port/slices.json`): worktree isolation, `@mentions`,
   agent memory, nested delegation, output transcripts, structured output, model scope (the child
   resolves `--model`; the display only resolves a spelling against the available models to label it).
7. **Order of work.** The agent-type and agent-file twins were written after their Go code was drafted, so there is no red
   commit for this port; the substitute proof is the 122 twins (two of them found real defects: an empty frontmatter
   block panicked the parser, and a plain value with `: ` was accepted where the original refuses it), the recorded
   scenarios and the mutation run.
8. **Interop with the real `tintinweb-tasks` is checked by hand, not by a test.** The two ports are separate Go modules, so
   no test in either imports the other; this port is tested against the bus protocol (`bus_test.go`) and `tintinweb-tasks`
   against a fake pi-subagents. Building both into the `pig-essentials` Binary found one defect, in `tintinweb-tasks`
   (its ping during load deadlocked the host; fixed there, see its `PORT.md` G5), and then a scripted foreground `Plan`
   agent ran to completion in that Binary as a child process of the Binary (`pigeq record --self --builtin`; a
   regression baseline, not an equivalence claim). A scripted `TaskCreate` with an `agentType` and `TaskExecute` also ran through the
   Binary: the task became a child agent, and its completion reached the parent as a `<task-notification>`.
9. **Findings against PiG** (for the lane lead): the path of the pig that started an extension process is in
   `PIG_HARNESS_BINARY` (set by PiG 0.4.1 for every extension process, for its Node runtime's re-launch), but it is not a
   documented SDK contract; an SDK accessor would make the `/proc` and `PATH` probing unnecessary. `Context.GetSystemPrompt`
   and `ModelQualified` work as the port needs.
10. **Review fixes (rev-pig-essentials).** The first version (a) steered at the turn limit with its own text and never
   aborted, so an agent that ignored the steer ran unbounded; it had no `steered` or `aborted` status; (b) reported a run
   whose last turn was a provider error as `completed` with `No output.` (a real `pig --mode rpc` child with an
   unanswerable model showed it); (c) told the model its own wording where the original's texts are fixed: the
   notification's `<status>` label, the `stopped`/`aborted`/`steered` notes (the original's `status-note.ts` was never
   twinned: its file is deferred), `<tool-use-id>`, the fallback note, the partial-output suffix of a failure, the
   duration (`0.3s`, not `300ms`) and the quoting of an unknown id; (d) probed the parent process and `PATH` before
   `PIG_HARNESS_BINARY`, and the extension's own executable with the extension's socket in its environment; (e) had no
   test that a stop reaches the child's process group (a mutant that removed the signals survived). Each is fixed with a
   test (`turnlimit_test.go`, `proc_unix_test.go`, `runner_test.go`) and a mutant in `port/mutations.json`.
   The remaining differences of the notification: no `<output-file>`, `<context_percent>` or `<compactions>` (their
   features are deferred; `<estimated_cost_usd>` is ported, under `showCost` only), and a 500-character preview counted in code points where JavaScript counts UTF-16 units.
11. **Scheduled runs (G2).** Ported from `schedule.ts`, `schedule-store.ts`, `ui/schedule-menu.ts` (data only) and the
    `schedule` branch of `index.ts`; the Go matches the original except in these places, each deliberate:
    - **croner is hand-ported, not depended on.** `cron.go` follows croner 10.0.1 (MIT, Hexagon, https://github.com/Hexagon/croner)
      step by step, quirks included (day-of-month OR day-of-week unless the day-of-week field starts with `+`, a search that
      stops at the year 3000, February pinned to 28 days in the normalization, `L`, `LW`, `nW`, `n#k`, `nL`, names, `?`),
      in the process's local time zone, as `new Cron(expr, fn)` with no options does. No new Go dependency: croner is only
      the oracle that `port/record-croner.mjs` runs under Node to record `testdata/croner-vectors.json` (Node never runs
      at runtime), and the fixture is the only size added (see Results). croner's five- and seven-field forms, its options and
      its `@nickname` patterns cannot be reached from the original's own six-field gate and are not ported.
    - **A six-field string with a `:` after its first character** (owner decision pending on the remainder) is read by
      croner as a date (V8's `Date.parse`, a legacy parser) and becomes a one-shot Cron. `cron.go` reads the two six-word
      forms the original's gate lets through, `toString()` without its zone name (`Tue Jan 01 2030 10:00:00 GMT+0100`)
      and `toUTCString()` (`Tue, 01 Jan 2030 10:00:00 GMT`), and runs them once, as croner does (recorded against
      croner in `testdata/croner-vectors.json`). Any other shape V8's legacy parser would accept (a zone name such as
      `EST`, a two-digit year, a day that does not exist in the month) is refused as `Invalid ISO8601 passed to timezone
      parser.`; Go has no `Date.parse`. A one-shot ISO timestamp, the form the tool text advertises, is read as the
      ECMAScript date-time format (`parseJSDate`, recorded against Node for 70+ shapes).
    - **A delay above 2^31-1 ms (24.8 days) is 1 ms, as in Node** (matched; owner decision pending on whether to keep
      it). `setTimeout` and `setInterval` turn such a delay into 1 ms: `+30d` fires at once and `30d` fires every
      millisecond in the original, and so here (`nodeDelay`). The hazard is the original's: an interval job of more than
      24.8 days starts an agent per millisecond, past the concurrency limit. The wait itself is made in pieces of at
      most 24 hours (`rearmable`), so no timer is armed for longer.
    - **The model is resolved by the child.** The original resolves `job.model` against Pi's registry at fire time and falls
      back to the default when it does not resolve; here the string reaches `pig --model`, as for an immediate run (item 6).
      The conversation-viewer snapshot (`invocation`) is built and passed to the manager but not shown (the viewer is deferred).
    - **`isolation: "worktree"` is refused**, in a `schedule` call as in any other, and a stored job that carries it (a
      session of the original, resumed here) fires as an `error` event and `lastStatus: "error"`, without a spawn.
    - **The store.** Same path, JSON shape, key order, lock file (`<file>.lock`, a PID, 100 tries 50 ms apart, a dead
      holder's lock taken over) and temp-and-rename write. A job record in a file keeps no field the Go type does not know,
      and one with a field of the wrong type is skipped, where the original keeps the object as it parsed.
    - **Timers and goroutines.** One `time.AfterFunc` per armed job (a cron job re-arms every 30 s as croner does), none for
      a stopped one, none when there are no jobs; each fire runs in a recovered goroutine (a panic in the manager is a fire
      error, in a timer callback it is dropped), the one goroutine per fire waits for its agent and is bounded by the agents.
    - **`schedulingEnabled` is read when the extension loads (the tool text) and again when a session starts (the check and
      the scheduler)**, from `subagents.json`, project over global; the original applies its settings once, at load. The
      settings slice (branch `parity/tintinweb-subagents-settings`) holds the full reader and the tool-text assembly;
      `schedule_feature.go` is the temporary form of the `schedule` part and says what to delete when the two merge.
    - **A pattern that never runs, and whose search overflows V8's stack, is refused as croner's constructor refuses it**
      (owner decision pending: the limit is a measured median, not a rule).
      croner finds the first run when the Cron is built, by a recursion that makes one call per field it settles; for a
      pattern whose failure is decided at the day level (`0 0 8 27 * +6#3`: the 27th that is a third Saturday) that is
      thousands of calls up to the year 3000 and V8 throws `Maximum call stack size exceeded`, so the original answers
      `Invalid schedule`. `cron.go` counts the same calls (exactly: `testdata/croner-stack.json`) and refuses at
      `cronStackLimit` = 6227, the median depth at which croner overflowed in 75 probes (fresh process each, Node 24.19.0:
      6066 to 6810, a quarter between 6223 and 6502; completed searches up to 4867 calls and one of about 7100). V8's
      depth varies from run to run, so a pattern whose search lands between 6066 and about 7100 could go either way in
      the original; `cron_test.go` allows exactly that band and nothing else.
    - **`/agents` is a listing, not the original's menu**: it adds a `Scheduled jobs (N)` section with the original's rows,
      and cancelling a job says it is not supported yet (the refusal message). Until the menu slice lands, the jobs of a
      running session end with it (or with `/new`, which starts a session with none); one that should not come back on
      `/resume` is removed from the session's file while the session is not running.

## Display surfaces (widget, fleet list, conversation viewer, renderers, colors)

| ID | Original | Go | Proof |
|---|---|---|---|
| U1 | `ui/agent-widget.ts` AgentWidget, `widgetMode`, `showModel`, `showCost` | `agent_widget.go`, `ui_usage.go`, `ui_model.go` | 34 twins (`agent_widget_twin_test.go`), `ui_extra_test.go` |
| U2 | `ui/fleet-list.ts`, `fleetView`, key handling through `OnTerminalInput` | `fleet_list.go` | 50 twins, 2 `fleet-wiring` twins (layer 1) |
| U3 | `ui/conversation-viewer.ts`, `ui/viewer-keys.ts`, `viewerMarkdown`, `m` | `conversation_viewer.go`, `viewer_keys.go` | 61 + 8 twins |
| U4 | `agent-color.ts` | `agent_color.go` | 7 + 4 twins |
| U5 | the Agent tool `renderCall`/`renderResult`, `AgentDetails`, the notification renderer and `NotificationDetails` | `ui_render.go`, `ui_details.go`, `tools.go` | twins of `agent-tool-error-rendering`, `cost-display` (renderer group), layer 1 (`RenderTool`, `render_message`) |
| U6 | the invocation snapshot (`resolveAgentInvocationConfig` precedence, `resolveModel`, `describeModel`) | `ui_invocation.go`, `ui_observer.go` | `agent-model-display` twins, `ui_extra_test.go` |
| U7 | `ui/select-item.ts` | `select_item.go` | `ui_extra_test.go` |

How it differs from the original, and why:

- **The transcript is built from the child's rpc event stream** (`childSpec.Observer`, `ui_observer.go`): a message is
  one map updated in place while it streams, under the agent's lock. The viewer never sees that map: `Messages()` hands
  out deep copies (reused while a message is unchanged) that carry a stable id (`viewerIDKey`), which keys the viewer's
  per-message Markdown cache. (The original is single-threaded; here the child's reader goroutine writes while the
  viewer renders, and a concurrent map read and write is a fatal error no `recover` can catch.) Usage, tool use,
  turn count, compactions and the effective model come from the same stream (`get_state`, `get_session_stats`).
- **Widgets are drawn as pre-rendered rows at the terminal width** (a `kit.Lines` view), re-sent only when the rows change
  and on a resize. The original's component factory re-renders every frame; the SDK has no factory form across the
  process boundary. Under Pi's rpc mode a factory is ignored, so the widget produces no frame there; the status text
  (`setStatus("subagents", ...)`) is a string and does.
- **Focus.** Pi's fleet list asks the TUI which component is focused (an `Editor` means the prompt). The SDK exposes no
  focus accessor, so unknowable focus counts as the editor, as the original does when it has no TUI yet; the overlay
  the list opens is tracked by the list itself. The twins of the #123 cases drive a probe in place of `instanceof Editor`.
- **Keybindings.** `Context.Custom` does not hand the component the user's keybindings, so the viewer uses the defaults
  (`tui.select.*` as Pi ships them, plus k/j and shift+arrows); the twins drive a keybindings manager directly.
- **Markdown theme.** Pi's own markdown theme (syntax-highlighted fences) is not reachable; the viewer builds the
  original's fallback theme from the host theme, which the original does itself where Pi's theme is not initialised.
- **`perf/no-fs-on-render`** mocks `node:fs`; Go cannot interpose on `os.ReadFile`, so the twin is a static guard that
  the files a frame runs through import nothing that reaches the disk.
- **Agent file precedence.** The original lets an agent file's model, thinking level and turn limit outrank the call's
  (`resolveAgentInvocationConfig`); the port previously let the call win. `bus.go` now follows the original, and the
  model display discloses the call's value as `(asked X)`.
- **No panic leaves the extension** (`guard.go`). One unrecovered panic in any goroutine ends the process and every
  agent in it, so every entry the host or the runtime calls into (event handlers, the three tools, `/agents`, the
  renderers, bus handlers, terminal input, the overlay component, tickers, timers) and every goroutine recovers, logs
  the stack on stderr and tells the user once per place. The original has no such concern (a thrown error in a handler
  is Pi's to catch). A panic while a surface holds its own lock cannot be unwound, so the reads those surfaces make
  under a lock (`listAgents`, the workflow source) are guarded where they are made.
- **The steer composer is pi-tui's `Input`** (`viewer_input.go`): bracketed paste, Kitty CSI-u printable keys, undo,
  kill ring, word and grapheme movement, C1 rejection, the cursor marker and width-aware scrolling. pitui carries no
  `Input` yet, so the port lives here, built on pitui's keybindings, `KillRing`, word navigation, Kitty decoder and
  width helpers; it belongs in pitui when that package takes it (not ported: the placeholder and the mouse handler,
  which the viewer never uses).
- **Truncation is pi-tui's default `"..."`** everywhere the original calls `truncateToWidth` without an ellipsis.
- **`showCost`** adds `~$X` to the foreground result (after the tokens, comma-joined), `Cost: ~$X` to the
  `get_subagent_result` header, `~$X` to the steer reply and `<estimated_cost_usd>` to the notification, and only
  then (it is LLM context). The original's `Context:` and `Compactions:` fields that sit beside them are deferred
  with the observability slice.
- **`activity` follows the agent's life as in the original**: the widget and the list read the live activity, which is
  gone once the agent has settled (finished rows show no `↻N`); a viewer holds the activity it had when it opened,
  frozen at the last state when the agent settles. The final result and the notification still read the settled
  turn count.
- **The fleet list is bound again** on every session's first event after a shutdown (as `setUICtx` is on every
  `session_start` and `tool_execution_start`), and the widget is not disposed on shutdown.
- **A stop from the viewer** runs off the input path (the abort is a round trip to the child; the original's is
  synchronous and instant).
- **Packaging.** `go.work` uses `../../../pitui`, which PiG's resolver reads to add the require and replace; the
  component must be in the tree (`components/pitui`, branch `pitui-package-3` until it is merged).
- **Persisting `m`.** The viewer's `m` writes `viewerMarkdown` into the project `subagents.json` (the original writes the
  whole settings snapshot) and emits `subagents:settings_changed` with the five display settings and `persisted`; a
  failed write warns `Viewer markdown set to <mode> (session only; failed to persist)`. A settings writer that owns the
  snapshot replaces `persistViewerMarkdown`.
- **Equivalent mutant.** Clearing `active` in `fleetList.setEnabled(false)` cannot be told from the original: the
  `update()` it calls clears `active` whenever no rows are listed. The mutant is removed from `port/gen-mutations.py`.
- **Skipped twin:** "renders the reopened session's settings, not the resume call's": `resume` is refused by this port.

## Mutation status (`pigeq mutate --unit-only`), workflow runtime

Every mutant of the workflow runtime in `port/mutations.json` is killed by the unit tests except the ones below, which are removed from the list as
equivalent or unreachable through the extension's public surface:

- `journal-read-keeps-blank-line`: a blank line fails JSON parsing and the next guard skips it anyway.
- `progress-stats-complete-live`: `Complete` is `!anyLive && ... done+failed >= total`; a live agent means `done+failed < seen <= total`, so
  `done+failed >= total` already implies no live agent.
- `runtime-unknown-method-not-fatal`: the worker only ever sends `agent` and `workflow`; no script can produce another method.
- `runtime-already-aborted-runs`: a run whose context is already cancelled is killed by the abort watcher the same way.
- `runtime-gate-keeps-text`: `result.Text` is read only when the result is OK, and a failed gate makes it not OK.
- `runtime-retry-while-aborted`: needs an abort interleaved inside a retry's window; not reachable deterministically.
- `flag-relative-path`: the test rig changes into the project directory, so a relative flag path resolves the same either way.
- `host-unresolvable-agent-model-error`: the carry-on branch is for an agent file's own unresolvable model, which the rig's
  agent types cannot set without the agent-file model setting the settings slice owns.

## Menus and dialogs (`/agents`, settings, agent file toggles, workflow card and dialog, clear completed)

| ID | Original | Go | Proof |
|---|---|---|---|
| M1 | `agent-file-toggle.ts` (`locateAgentFile`, `disableInContent`, `enableInContent`, `isEmptyStub`, `buildNewAgentFile`; `serializeAgentFile` is `serialize.go`) | `agent_file_toggle.go` | 40 twins (`agent_file_toggle_twin_test.go`), 1 named skip |
| M2 | `index.ts` showAgentsMenu and what it opens (types list, detail, eject, disable, enable, create wizards, running agents) | `agents_menu.go`, `menu_ui.go` | `agents_menu_test.go` (the original has no test of the closure) |
| M3 | `ui/schedule-menu.ts` showSchedulesMenu | `agents_menu.go`, data layer `schedule_menu_data.go` | 11 twins (`schedule_menu_twin_test.go`) |
| M4 | `index.ts` showSettings, snapshotSettings, applyValue, notifyApplied; `settings.ts` saveAndEmitChanged | `settings_menu.go`, `settings_store.go`, `settings_list.go` | `agents_menu_test.go` |
| M5 | `ui/workflow-card.ts` | `workflow_card.go` | 39 twins (`workflow_render_twin_test.go`) |
| M6 | `ui/workflow-dialog.ts` | `workflow_dialog.go` | 84 twins (`workflow_dialog_twin_test.go`) |
| M7 | `ui/workflow-menu.ts` | `workflow_overlay.go` (the data and actions are `workflow_menu.go`) | 1 twin, `agents_menu_test.go` |
| M8 | `agent-manager.ts` clearCompleted on `session_start` / `session_before_switch` | `manager_clear.go` | 3 twins |

How it differs from the original, and why:

- **The menus run over `menuUI`** (`menu_ui.go`), which the command's context implements with every host call guarded;
  a UI that cannot be reached is a cancel. A panic in a menu is recovered at the command entry (`protectCommand`), in the
  overlay component (`overlayComponent`) and in the goroutines the dialog starts.
- **`SettingsList` is pi-tui's without the search box, the submenus and the mouse**: neither menu uses them, and a
  mouse never reaches an extension's component.
- **Settings the port has no feature for are held** (`heldSettings`): the menu shows them, a change is written like any
  other, and the menu says that the port does not support the value yet. They start at the value the port behaves as
  (the one `refuse.go` accepts), not the original's default, so writing the snapshot never announces a refusal for a
  default the user did not choose (`agentMentions` is `off`, `worktreeIsolation` `off`, `outputTranscript` `off`,
  `maxSubagentDepth` 1). Written only when chosen, as in the original: `workflowsEnabled` and `fallbackSubagent`. The
  settings slice (`settings.go` there) replaces the reader, the writer and the held values when it lands.
- **"Generate with Claude"** starts a foreground `general-purpose` agent (5 turns) and waits for it; the original passes
  no signal either, so Esc in the dialogs cannot cancel it. It ends with the session.
- **The workflow dialog follows the run** through a snapshot taken on every render under the task's lock (the progress
  log is append-only); the phase groups are cached against the log's revision, so a spinner frame costs the visible
  rows and not a fold of the whole log. The spinner is a recovered ticker goroutine that stops at `Dispose`.
- **`c` in the dialog** hides the dialog's own overlay through the SDK's `OverlayHandle` (`SetHidden`), opens the
  conversation in a goroutine and shows the dialog again when it closes. (G15's single-component workaround is not
  needed: the SDK has the handle.)
- **clearCompleted** drops the finished agents at the session boundaries and keeps a background result the model has
  not read; the original's ten-minute sweep and the tombstones for `@handle` are not ported here.
- **The old `/agents` text listing is gone** (it was a stand-in for the menu), with its refusal of the Workflows menu and
  of cancelling a scheduled job.
- **Skipped twin:** "leaves a file without a BOM exactly as the parser reads it": Go has one parser.
