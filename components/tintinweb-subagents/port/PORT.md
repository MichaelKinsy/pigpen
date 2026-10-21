# Port record: @tintinweb/pi-subagents (partial)

| Input | Identity |
|---|---|
| Original | `@tintinweb/pi-subagents` 0.19.0 by tintinweb, https://github.com/tintinweb/pi-subagents, commit `4f572eaa04c09d3dbc16e4a5f13a16b295e84e14`, MIT |
| Oracle | Pi 1.0.3 (goldens `tool-tools`, `agent-not-found`, `agent-unknown-type-strict`) and Pi 1.3.0.1402973a (goldens `agent-resume-not-found`, `agent-resume-ignores-type`), Node 24.19.0, with the unmodified package at `port/oracle` (dependencies from npm: `croner`, `nanoid`, `typebox`) |
| Target | PiG 0.4.1+1.0.3 (release v0.4.1, commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`), go1.27.1 |
| Original's tests | 2029 cases in 105 files (`port/upstream-tests.json`). **122 exact twins** (`agent-types` 46, `custom-agents` 76), no skips; **1907 deferred by name** in `port/slices.json`, each file with its reason |
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
- `/agents`: the types and the agents of the session;
- sessions: an agent's conversation is a pi session file (`rememberAgents`, `persist_session`, `session_dir`), `resume`
  continues the same conversation, `inherit_context` puts the parent conversation in front of the task, and the agent's
  skills, extensions, `ext:` tool selectors, `disallowed_tools` and `isolated` choose what its child loads.

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
| M5 | `settings.ts`: `subagents.json`, global then project | `settings.go` (six fields) | `settings_test.go`, `turnlimit_test.go` |
| M6 | `prompts.ts`, `env.ts`: the agent's system prompt and environment block | `prompts.go` | `extra2_test.go` (exact strings), inserted text equal to Pi's in a recorded child run (see F2) |
| M7 | `agent-manager.ts`: records, limit, queue, handles, abort, steer, consume | `manager.go` | `manager_test.go`, `extra_test.go`, `extra2_test.go` |
| M8 | `agent-runner.ts`: an agent's run, the turn limit (steer at `maxTurns`, abort at `maxTurns + graceTurns`), `finalTurnError`, `resumeAgent` | `runner.go`: a child `pig --mode rpc` (a session file, or `--no-session`) bound to a context the manager ends when it closes the child, kept alive after a run only when its conversation is in memory; a stop of such a child is the RPC `abort` alone | `runner_test.go`, `runner_twin_test.go`, `turnlimit_test.go`, `proc_unix_test.go`, `lifecycle_test.go` (a fake pig, the test binary re-executed) |
| M9 | `cross-extension-rpc.ts`: ping, spawn, stop, consume | `bus.go` | `bus_test.go` |
| M10 | completion events and the held notification (`index.ts` `formatTaskNotification`, `getStatusLabel`; `status-note.ts`; `xml.ts`) | `extension.go` | `tools_test.go`, `extra_test.go`, `turnlimit_test.go` |
| M11 | the Agent, result and steer tools' behavior and texts | `tools.go` | `agent-not-found`, `agent-unknown-type-strict`; `tools_test.go` |
| M12 | `agent-manager.ts` resume, startResume, tombstones, cleanup (the 60 s timer), `clearCompleted`, `abortAll`, `dispose`, `resumeSessionFile`, `reclaim`, `bypassQueue`; `index.ts` Agent `resume` branch, `spawnTopLevel`, and the `session_start`, `session_before_switch` and `session_shutdown` handlers | `sessions.go`, `sessions_tool.go`, `extension.go` (timer, shutdown, session boundaries) | `agent_manager_twin_test.go`, `sessions_test.go`, `lifecycle_test.go`; `agent-resume-not-found`, `agent-resume-ignores-type` |
| M13 | `agent-runner.ts` extension and tool scope (`extensionCanonicalName(s)`, `parseExtensionsSpec`, `parseExtSelectors`, the loader filter, `inScope`), `persist_session`, session choice | `scope.go`, `discover.go` (the parent's tool registry and `pig status --json` stand for the loader's extension list) | `scope_twin_test.go`, `sessions_test.go`, `lifecycle_test.go` (the flags a scope becomes, asserted on `childArgs`) |
| M14 | `context.ts` (`inherit_context`), `skill-loader.ts` (`skills:`), `env.ts`, `prompts.ts` extras | `context.go`, `skills.go`, `skillfs.go`, `prompts.go` | `context_twin_test.go`, `skills_twin_test.go`, `env_twin_test.go`, `prompts_twin_test.go` |

## SDK gap check (`pigeq gaps --ts port/oracle/src`)

One `MISSING`: `ctx.ui.custom(options.onHandle)` (`ui/workflow-menu.ts`), the workflow menu of the deferred slice. It
is accepted in `port/accepted-gaps.json` with the approval it rests on: the lane lead's instruction of 2026-10-05 20:56
MDT to ship a tested slice and defer the rest by name. `PARTIAL tool.renderResult`: the original's renderers return TUI
components; this port registers none.

## Results (this revision)

- Scoped checks for the final head: `go vet` clean, `go test -race -count=1 ./...` passes; GOOS evidence from
  `scoped.mjs`: `requested windows -> go env GOOS prints windows`, `darwin -> darwin`, `linux -> linux`, each followed by
  `go vet ./...` exit 0. `npm run check` and `npm run generate` (no diff) pass.
- `go test -race ./...` passes, also `-count=24` (twice). A later `-count=24` run found a data race the first `-count=25` run had missed: `manager.run` set an agent's cancel func without the lock that `abort` reads it under (stopping an agent right after spawning it, as a session end does); fixed, with `TestAbortRightAfterSpawnIsRaceFree` red first. The runner tests re-execute the test binary as a fake `pig --mode rpc`.
- `pigeq check` against goldens **recorded from the original** (the first three under Pi 1.0.3, the two resume scenarios
  under Pi 1.3.0.1402973a): 5 of 5 scenarios, plus `port-gaps` and `exec-coverage`. `tool-tools` compares the whole model request, so the three tool definitions, the Agent guidelines in
  the system prompt and the tool list equal Pi's.
- `pigeq mutate --unit-only` after the sessions review fixes: **156 mutations**. Nine entries had gone stale when the
  sessions slice moved code (five refusal mutants of behavior that is ported now were dropped, four were re-targeted), four
  more did not build and were fixed; 52 new ones cover the lifecycle (a kept child and its context, a stop of an in-memory
  conversation, the cleanup timer, shutdown, session start and switch, `clearCompleted`), the scope flags, the session name,
  `APPEND_SYSTEM.md`, JS trim, rooted paths, the package manifest shape, tombstone order, mention matching, lazy
  `inherit_context`, the spawn RPC options and `rememberAgents`. The last full run killed 155 of 156 and left
  `setting-notice-repeats` (its test, `the same settings are announced once`, was added after that run and kills it).
  The earlier history: 81 mutations, all killed (the first run killed 58; 16 survived and 7 did not build; each
  survivor got a test in `extra_test.go` / `extra2_test.go` and each invalid mutation was rewritten). After the review
  fixes (finding 10): **101 mutations, all killed by the unit tests** (`--unit-only`); the first run after the fixes left
  three (a setting that never reached the child, a process-group signal the test waited long enough to miss, and a test
  whose failure text the runner read as a build failure), each closed by a test.
- `pig package validate components/tintinweb-subagents` and `pig install <extension dir> --validate-only --json`: valid.
- Live check of the child's protocol against `pig-0.4.1 --mode rpc`: `get_session_stats` returns `data.tokens.total` and
  `data.toolCalls`, `get_last_assistant_text` returns `data.text` (or `{}`), a `prompt` without a usable model
  answers `success: false` with the reason, which the runner reports as the agent's error.

## Deliberate differences and findings

1. **A child process, not an in-process session.** Each agent is a `pig --mode rpc` process (its conversation a session file by default,
   `--no-session` when it is in memory); an agent costs a process start. The pig to run is found as described in the README. The child inherits the parent's environment (and so its
   credentials and settings) and, unless the call or file sets one, the parent's model and thinking level. A child
   registers nothing (`TINTINWEB_SUBAGENT_DEPTH`): no nesting. `Explore`'s `claude-haiku-4-5` default is not forced on the
   child (the original falls back to the parent's model when it is not available); it is shown in the type list.
2. **F2: a recorded child run cannot be compared whole.** A foreground `Plan` agent was recorded under Pi and run under
   PiG. The child's request carries the same user message and the same inserted system text (the agent header, the
   environment block and the agent's prompt), after a fix this comparison found (a blank line in the replace-mode header).
   The harness's `removed` part is the host's own default system prompt, which says `pi` under Pi and `pig` under PiG, so
   that comparison cannot pass and the scenario is not kept (`port/gen-scenarios.py` says so).
3. **Unsupported parameters of `Agent` are refused.** `schedule` and `isolation: "worktree"` are tool errors with the
   message in the README (`refuse.go`). `inherit_context`, `resume`, `skills`, `extensions`, `disallowed_tools` and
   `isolated` are ported (see "Sessions" below); the first version answered `inherit_context` with plain text and
   emulated `resume` by starting a new agent with the old answer in its prompt, a partial run, now removed. The tool text
   is the original's, so the model is told about these; a call that uses an unported one gets the refusal.
4. **Agent files are read at start-up for the tool text** (the workspace of an extension process is fixed), and again when
   the session starts and when the workspace differs. A file added later reaches `/agents` and the `subagent_type`
   check on the next session event, not the tool description of the running session.
5. **Settings.** Six of `subagents.json`'s fields are honored (`maxConcurrent`, `defaultMaxTurns`, `graceTurns`,
   `fallbackSubagent`, `disableDefaultAgents`, `rememberAgents`). A value of any other field that asks for an unported feature is
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
11. **Sessions (reviewed by RvSessions, fixed in this revision).** What differs from the original on purpose or for want
    of a capability:
    - **Tool scope is applied at the child's start, over the tools the parent knows (M5).** `ext:` narrowing and
      `disallowed_tools` become `--exclude-tools` (or a `--tools` allowlist when no extension loads) for the tools the
      parent's registry lists. The original re-derives the scope on every turn and blocks an out-of-scope call in
      `beforeToolCall`; tools of an extension the child loads that the parent does not know (a path in `extensions:`,
      a project extension the parent lacks), or registered after the child started (MCP), are not excluded. A faithful
      form needs a child-side per-tool gate that PiG does not expose to an extension: the Pi API is
      `session.agent.beforeToolCall` with `setActiveToolsByName`. Reported to the lane lead.
    - **Tombstones are created and cleared, but not yet reopened by a tool (M6).** `removeRecord` leaves one for an agent
      with a handle and a session file; it keeps the handle reserved (`explore-2`), `clearCompleted` forgets it at a
      session boundary, and the cap and order match the original. `resolveMention`, `listTombstones`, `dropTombstone`,
      and `spawn` with `resumeSessionFile` and `reclaim` are implemented and tested, and are called by nothing in
      production until the `@handle` input hook, which is the mention slice (`port/slices.json`, `agent-mention-wiring`).
    - **A foreground resume of an agent that is still running** answers `Agent failed: Agent is already processing...`
      (pi's `session.prompt` message) and leaves the running agent alone; the original's rejected prompt also flips the
      live record to `error` as a side effect, which this port does not copy.
    - **A stop of an in-memory agent** sends the RPC `abort` and keeps the process, so the conversation stays
      resumable. If the run does not settle within ten seconds the process is ended (the original's `session.abort()` cannot
      hang), and the conversation with it.
    - **The session name** is set with `set_session_name` after `new_session`: PiG applies `--name` to the first session
      of a process only ("Replacements carry none", `cmd/pig/rpc_mode_runtime.go`), and the persisted child's first
      session is replaced by `new_session` so that it can record its parent.
    - **`APPEND_SYSTEM.md`** is kept out of the child with `--append-system-prompt ""`. That an explicit empty append
      suppresses discovery is read from PiG's `resolvePromptInputs` (`cmd/pig/resource_prompts.go`, covered by its
      own tests), not shown by a recorded run.
    - **The spawn RPC drops, as the original does, `parentAgentId`, `workflowId`, `depth`, `maxSubagentDepth`,
      `configCwd` and `rootSessionId`**; it refused them before. `isolated`, `inheritContext` and `bypassQueue` count when
      truthy.
    - **The cleanup timer starts at the first `session_start`** and stops at `session_shutdown`, because an extension
      process can outlive a session in PiG; the original starts it in the manager's constructor and stops it in `dispose`.
