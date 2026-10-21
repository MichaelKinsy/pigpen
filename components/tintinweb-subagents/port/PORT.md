# Port record: @tintinweb/pi-subagents (partial)

| Input | Identity |
|---|---|
| Original | `@tintinweb/pi-subagents` 0.19.0 by tintinweb, https://github.com/tintinweb/pi-subagents, commit `4f572eaa04c09d3dbc16e4a5f13a16b295e84e14`, MIT |
| Oracle | Pi 1.0.3, Node 24.19.0, with the unmodified package at `port/oracle` (dependencies from npm: `croner`, `nanoid`, `typebox`) |
| Target | PiG 0.4.1+1.0.3 (release v0.4.1, commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`), go1.27.1 |
| Original's tests | 2029 cases in 105 files (`port/upstream-tests.json`). **178 exact twins** (`agent-types` 46, `custom-agents` 76, `schedule` 28, `schedule-e2e` 6, `schedule-params-wiring` 9, `schedule-store` 13), no skips; **1851 deferred by name** in `port/slices.json`, each file with its reason (`schedule-menu`, 11 cases, tests the interactive menu, which is not ported; its data layer has tests of its own) |
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

## SDK gap check (`pigeq gaps --ts port/oracle/src`)

One `MISSING`: `ctx.ui.custom(options.onHandle)` (`ui/workflow-menu.ts`), the workflow menu of the deferred slice. It
is accepted in `port/accepted-gaps.json` with the approval it rests on: the lane lead's instruction of 2026-10-05 20:56
MDT to ship a tested slice and defer the rest by name. `PARTIAL tool.renderResult`: the original's renderers return TUI
components; this port registers none.

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
6. **Not ported** (named in `port/slices.json`): the workflow runtime, the interactive Scheduled jobs menu, worktree isolation, `@mentions`,
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
