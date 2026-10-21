# Port record: @tintinweb/pi-subagents (partial)

| Input | Identity |
|---|---|
| Original | `@tintinweb/pi-subagents` 0.19.0 by tintinweb, https://github.com/tintinweb/pi-subagents, commit `4f572eaa04c09d3dbc16e4a5f13a16b295e84e14`, MIT |
| Oracle | Pi 1.0.3, Node 24.19.0, with the unmodified package at `port/oracle` (dependencies from npm: `croner`, `nanoid`, `typebox`) |
| Target | PiG 0.4.1+1.0.3 (release v0.4.1, commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`), go1.27.1 |
| Original's tests | 2029 cases in 105 files (`port/upstream-tests.json`). Twins: `agent-types` 46, `custom-agents` 76, and the workflow slice (see "The SubagentWorkflow runtime") 361 exact + 9 named skips; the rest deferred by name in `port/slices.json`, each file with its reason |
| Kind | 1: an extension, no CLI built-in, no provider |
| Not this | `nicobailon/pi-subagents`, ported as `components/subagents`. A different project |

## What this port ships (the slice), and what it does not

The original is 21,000 lines: an in-process agent runtime on Pi's SDK, a JS workflow engine, a scheduler, worktree
isolation, `@mention` agents, persistent memory, and a TUI of widgets, a fleet view and a conversation viewer. A Go
extension cannot host a Pi session, so the shipped slice is the part that has a faithful Go form:

- the three tools `Agent`, `get_subagent_result`, `steer_subagent` with the original's text and schemas;
- agent types: the three defaults, custom agent files (three directories, strict mode, warn-once), settings
  (`maxConcurrent`, `defaultMaxTurns`, `fallbackSubagent`, `disableDefaultAgents`), the spawn policy (#183);
- background by default, a concurrency limit and a FIFO queue, handles (`explore`, `explore-2`) and names, a held
  completion notification that a read of the result cancels, the event-bus protocol v2 `pi-tasks` speaks;
- `/agents`: the types and the agents of the session.

Everything else is deferred, by name, in `port/slices.json` (reasons per file). The Agent tool is advertised **as the
original advertises it with workflows, scheduling and worktree isolation switched off** (`.pi/subagents.json`:
`workflowsEnabled`, `schedulingEnabled`, `worktreeIsolation` all `false`), which is exactly what this port offers.

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
in `go.sum`. The source is 2.2 MB (regexp2 7.9 MB). **Cost in the built extension**, measured on imladris (linux/amd64, go1.27.1,
`CGO_ENABLED=0`, `pig piglet build` of a one-extension Piglet, PiG 0.5.0 source 7a5422f6; the same Piglet built from this
branch and from the base without the workflow package, `origin/parity/tintinweb-subagents-refusal` at 7dc3f789): Piglet Binary 80,920,736 bytes with the workflow package against
71,049,376 without, +9.9 MB (+13.9%), the workflow package with goja and its requirements. The extension alone (`go build`, `-s -w`): 15.4 MB against 5.7 MB; unstripped 21.7 MB against 8.3 MB. Cold start
to registration: `Extension()` builds in 0.51 ms against 0.41 ms (in process, median of 55); `GODEBUG=inittrace=1` shows the
workflow package's added init at 0.5 ms (`google/pprof/profile` 0.36 ms, `goja/parser` 0.12 ms, both from goja's imports); a run of the
extension binary from exec to exit takes 5.3 ms against 4.3 ms; in `pig` (`scripts/perf/measure.mjs startup --mode trace --n 20`,
cpus 100-103) the extension's spawn-to-handshake takes 4 ms against 3 ms and `extensions-loaded` comes at 31 ms against 29 ms.
The tools are registered at start; the engine only runs when a workflow does. License: goja is MIT, module version
`v0.0.0-20260723142020-b4aef50fa347`, the one `pig-music` pins; no new module outside that set.

Building the extension's Piglet: `npm run build:piglet -- pig-essentials` (`scripts/build-piglet.mjs`, `PIG_BIN` and a git checkout of the PiG source at `scripts/pig-requirement.json`'s `release.commit` as `PIG_SOURCE_ROOT`) is the repository's documented command; the per-extension Piglets used above are what `scripts/perf/build-singles.mjs` makes. Offline, the build needs `GOSUMDB=off` for the goja requirement `github.com/google/pprof` (the checksum database is unreachable); the fused-member vet notes that the five external modules were not vetted for process-global hazards (regexp2, goja, sourcemap, pprof, x/text).

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
MDT to ship a tested slice and defer the rest by name. `PARTIAL tool.renderResult`: the original's renderers return TUI
components; this port registers none.

## Results (this revision)

- `go test -race ./...` passes, also `-count=24` (twice). A later `-count=24` run found a data race the first `-count=25` run had missed: `manager.run` set an agent's cancel func without the lock that `abort` reads it under (stopping an agent right after spawning it, as a session end does); fixed, with `TestAbortRightAfterSpawnIsRaceFree` red first. The runner tests re-execute the test binary as a fake `pig --mode rpc`.
- `pigeq check` against goldens **recorded from the original under Pi 1.0.3**: 3 of 3 scenarios, plus `port-gaps` and
  `exec-coverage`. `tool-tools` compares the whole model request, so the three tool definitions, the Agent guidelines in
  the system prompt and the tool list equal Pi's.
- Workflow scenarios, recorded under Pi `1.3.0.1402973a` (the pi-src pin): `workflow-tools`, `-bad-meta`, `-no-meta`, `-no-source`,
  `-unknown-name`, `-bad-name`, `-resume-unknown`, `-missing-script-path` and `-off` (the flag warning, the tool list and its
  order: `SubagentWorkflow` is registered between `Agent` and `get_subagent_result`, as the original does; a first check found
  the port registering it last). **The eight run-starting scenarios** (`workflow-run-inline`, `-run-throws`, `-determinism`, `-unknown-option`, `-saved-name`,
  `-title-ignored`, `-nested`, `-flag`) are written by `gen-scenarios.py` into `port/scenarios-pending/` and recorded under Pi
  1.3.0+1402973a into `port/golden-pending/`, not into `scenarios/`/`golden/`. A run's task id (`wf_` + random hex), its
  `duration_ms`, `details.durationMs` and the directory an inline script is saved in (`pi-subagents-<uid>/<cwd slug>/`) are
  different every run; the scenarios normalize them with pigeq's N8/N9 (task ids and `<duration_ms>`, branch `pigeq-normalize-wf`),
  the scenario rules `volatileNumbers: ["message.details.durationMs"]` (pigeq `pigeq-normalize-numbers`) and one `normalize` rule
  for the directory. Recorded and compared with a pigeq built from `pigeq-normalize-wf` + `fix/pigeq-channel-order` + that
  numbers rule. Result: the Go port under PiG equals Pi for every event up to the session shutdown (events 0-22 of the 25 of
  `workflow-run-inline`, 0-10 of the 13 of `workflow-flag`: the task id, the task notification, the next turn). The one difference is
  at `99-shutdown`: Pi shows `setWidget` for key `fleet` with no content (the original's fleet list clears its widget when
  the session ends) and the port, which has no fleet list yet (the UI lane's G5), shows nothing. That is a missing feature of
  another lane, not a bug of what is ported here, so the eight stay in `scenarios-pending/` (a red `pigeq check` would hide
  real regressions) and move to `scenarios/` and `golden/` with the fleet list (`mv` both directories' files; regenerate
  nothing). The original under PiG differs from the original under Pi in one more event: an extra `setWidget` for `fleet`
  with `widgetLines` before `tool_execution_end` (PiG's Node runtime sends the factory widget over RPC, which Pi drops): B2
  in `core-items.md`, with the event dump under B10; no host event is reordered. `pigeq record` therefore prints "host check:
  pig-ts differs from pi-ts" for these eight until B2 lands; the goldens are Pi's. A bare `--subagents-workflow-file` cannot be
  recorded because Pi refuses to start (`Extension flag ... requires a value`). The `workflow-resume-unknown` scenario names a
  run id that is not 12 hex digits (`wf_nosuchrun`), so it does not depend on N8. The behaviors are also covered by the twins
  (`workflow_tool_twin_test.go`).
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
3. **Unsupported parameters of `Agent` are refused.** `inherit_context: true`, `resume`, `schedule` and
   `isolation: "worktree"` are tool errors with the message in the README (`refuse.go`; the first version answered
   `inherit_context` with plain text and emulated `resume` by starting a new agent with the old answer in its prompt, a
   partial run, now removed). `isolated` starts the child with `--no-extensions`. The tool text is the original's, so the
   model is told about these; a call that uses one gets the refusal.
4. **Agent files are read at start-up for the tool text** (the workspace of an extension process is fixed), and again when
   the session starts and when the workspace differs. A file added later reaches `/agents` and the `subagent_type`
   check on the next session event, not the tool description of the running session.
5. **Settings.** Five of `subagents.json`'s fields are honored (`maxConcurrent`, `defaultMaxTurns`, `graceTurns`,
   `fallbackSubagent`, `disableDefaultAgents`). A value of any other field that asks for an unported feature is
   announced once when a session starts, with the refusal message (`refuse.go`, `settingRules`); a malformed value is
   ignored silently, as in the original. Agent files that use an unported field, and the `subagents:rpc:spawn` options the
   port does not honor, are refused the same way.
6. **Not ported** (named in `port/slices.json`): the workflow runtime, scheduling, worktree isolation, `@mentions`,
   widget, fleet and conversation viewer, agent memory, nested delegation, output transcripts, structured output, the
   settings menu, usage and cost display, model scope and fuzzy model resolution (the child resolves `--model`).
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
   The remaining differences of the notification: no `<output-file>`, `<context_percent>`, `<compactions>` or cost (their
   features are deferred), and a 500-character preview counted in code points where JavaScript counts UTF-16 units.

## Mutation status (`pigeq mutate --unit-only`)

Every mutant of `port/mutations.json` is killed by the unit tests except the ones below, which are removed from the list as
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
