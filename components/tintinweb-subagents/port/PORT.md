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
- agent types: the three defaults, custom agent files (three directories, strict mode, warn-once), the spawn policy (#183);
- the settings store: all 24 keys of `subagents.json` parsed like the original's `sanitize`, global then project merged
  key by key, written back to the project file, applied through setter hooks; the keys of this slice are live
  (`maxConcurrent`, `maxConcurrentForeground`, `defaultMaxTurns`, `graceTurns`, `defaultJoinMode`, `backgroundByDefault`,
  `scopeModels`, `strictAgentFiles`, `toolDescriptionMode`, `fallbackSubagent`, `disableDefaultAgents`, `rememberAgents`), the others are
  stored and refused until their slice lands (`settingsView`);
- the Agent tool's text and schema as pure functions of `descriptionFlags` (scheduling, worktree isolation, workflows,
  mode), for all 8 combinations in each of `full`, `compact` and `custom` mode;
- model resolution (fuzzy match, `scopeModels`, the RPC's model override) and the invocation precedence (an agent
  file's fields outrank the call's);
- background by default, two concurrency pools (background and foreground) and a FIFO queue, record GC and tombstones,
  handles (`explore`, `explore-2`) and names, the join of completion notifications (`async`, `group`, `smart`), a held
  completion notification that a read of the result cancels, the manager registry, the event-bus protocol v2
  `pi-tasks` speaks (its listeners registered once the session has started);
- `/agents`: the types and the agents of the session;
- sessions: an agent's conversation is a pi session file (`rememberAgents`, `persist_session`, `session_dir`), `resume`
  continues the same conversation, `inherit_context` puts the parent conversation in front of the task, and the agent's
  skills, extensions, `ext:` tool selectors, `disallowed_tools` and `isolated` choose what its child loads;
- worktree isolation (`isolation: "worktree"` from the call, an agent file or the spawn RPC; strict, the branch commit) and
  persistent agent memory (`memory:` user, project, local, `MEMORY.md` injection, the legacy fallback), both twinned from
  the original's tests.

Everything else is deferred, by name, in `port/slices.json` (reasons per file). The Agent tool is advertised **as the
original advertises it with workflows and scheduling switched off** (`.pi/subagents.json`: `workflowsEnabled`,
`schedulingEnabled` both `false`; worktree isolation is on, as it is by default), which is what this port offers.

## Mapping

| ID | Original | Go | Scenario / test |
|---|---|---|---|
| M1 | `index.ts`: the three tool definitions (descriptions, schemas, prompt snippets and guidelines) | `tools.go`, `agent_description.txt`, `tool_specs.json`, `agent_guidelines.json` (extracted from what Pi sends, `port/gen-spec.py`) | `tool-tools` (the request, system prompt included, equals Pi's); `extra_test.go` |
| M2 | `agent-types.ts`: registry, case-insensitive lookup, defaults, `resolveSpawnType` and `fallbackSubagent` | `agent_types.go` | 46 twins in `agent_types_twin_test.go`; `agent-unknown-type-strict` |
| M3 | `custom-agents.ts`, `agent-file-toggle.ts` (serializer): agent files, tools field, inherit fields, name rules, warnings | `agents.go`, `frontmatter.go`, `serialize.go` | 76 twins in `custom_agents_twin*_test.go` (the loader reads PiG's agent dir, D2) |
| M4 | `default-agents.ts`: the three default agents | `defaults_text.go` (extracted byte for byte) | `agent_types_twin_test.go`; `tool-tools` |
| M5 | `settings.ts`: `subagents.json`, global then project, sanitize, save, apply, the events | `settings.go`, `settings_apply.go`, `features.go` | `settings_twin_test.go` (87 cases), `config_twin_test.go` (documented defaults), `settings_test.go`, `turnlimit_test.go` |
| M6 | `prompts.ts`, `env.ts`: the agent's system prompt and environment block | `prompts.go` | `extra2_test.go` (exact strings), inserted text equal to Pi's in a recorded child run (see F2) |
| M7 | `agent-manager.ts`: records, the two pools, queue, handles, tombstones, GC, abort, steer, consume, resume | `manager.go`, `registry.go`, `sessions.go` | `manager_twin_test.go` (foreground-concurrency, agent-manager-gc), `wiring_twin_test.go` (manager-registry-guard), `agent_manager_twin_test.go` (resume), `manager_test.go`, `lifecycle_test.go`, `sessions_test.go`, `extra_test.go`, `extra2_test.go` |
| M8 | `agent-runner.ts`: an agent's run, the turn limit (steer at `maxTurns`, abort at `maxTurns + graceTurns`), `finalTurnError`, `resumeAgent` | `runner.go`: a child `pig --mode rpc` (a session file, or `--no-session`) bound to a context the manager ends when it closes the child, kept alive after a run only when its conversation is in memory; a stop of such a child is the RPC `abort` alone | `runner_test.go`, `runner_twin_test.go`, `turnlimit_test.go`, `proc_unix_test.go`, `lifecycle_test.go` (a fake pig, the test binary re-executed) |
| M9 | `cross-extension-rpc.ts`: ping, spawn, stop, consume | `bus.go` | `bus_test.go` |
| M10 | completion events and the held notification (`index.ts` `formatTaskNotification`, `getStatusLabel`; `status-note.ts`; `xml.ts`) | `extension.go` | `tools_test.go`, `extra_test.go`, `turnlimit_test.go` |
| M11 | the Agent, result and steer tools' behavior and texts | `tools.go` | `agent-not-found`, `agent-unknown-type-strict`; `tools_test.go` |
| M12 | `index.ts` description builders, `invocation-config.ts` isolationParam, `workflow/tool-description.ts` (what it adds to the tool list) | `description.go`, `agent_text.json` (extracted by `gen-spec.py` from the source, checked against the golden) | `tooldesc_twin_test.go` (28 cases), `description_golden_test.go` (every combination against what Pi sent, `port/description-golden`), `tool-tools*` scenarios |
| M13 | `model-resolver.ts`, `model-scope.ts`, `enabled-models.ts`, `invocation-config.ts` | `models.go`, `modelscope.go`, `enabledmodels.go`, `invocation.go` | `models_twin_test.go` (70 cases), `config_twin_test.go`, `policy_test.go`, `agent-model-not-found` |
| M14 | `group-join.ts`, `index.ts` batch tracking and nudges | `groupjoin.go`, `joinmode.go` | `groupjoin_twin_test.go`, `policy_test.go` |
| M15 | the wiring tests of the original (`*-wiring`, `wait-queued`, `rpc-*`, `background-by-default`, ...) | `extension.go`, `tools.go`, `bus.go` | `wiring_twin_test.go` |
| M16 | `agent-manager.ts` resume, startResume, tombstones, cleanup (the 60 s timer), `clearCompleted`, `abortAll`, `dispose`, `resumeSessionFile`, `reclaim`, `bypassQueue`; `index.ts` Agent `resume` branch, `spawnTopLevel`, and the `session_start`, `session_before_switch` and `session_shutdown` handlers | `sessions.go`, `sessions_tool.go`, `extension.go` (timer, shutdown, session boundaries) | `agent_manager_twin_test.go`, `sessions_test.go`, `lifecycle_test.go`; `agent-resume-not-found`, `agent-resume-ignores-type` |
| M17 | `agent-runner.ts` extension and tool scope (`extensionCanonicalName(s)`, `parseExtensionsSpec`, `parseExtSelectors`, the loader filter, `inScope`), `persist_session`, session choice | `scope.go`, `discover.go` (the parent's tool registry and `pig status --json` stand for the loader's extension list) | `scope_twin_test.go`, `sessions_test.go`, `lifecycle_test.go` (the flags a scope becomes, asserted on `childArgs`) |
| M18 | `context.ts` (`inherit_context`), `skill-loader.ts` (`skills:`), `env.ts`, `prompts.ts` extras | `context.go`, `skills.go`, `skillfs.go`, `prompts.go` | `context_twin_test.go`, `skills_twin_test.go`, `env_twin_test.go`, `prompts_twin_test.go` |
| M19 | `output-file.ts`: the `.output` transcript, path rules, compaction handling | `output_file.go`, `observability_app.go` | 20 + 3 + 1 + 5 twins (`output_file_twin_test.go`); `obs-agent-file-output-transcript` |
| M20 | `structured-output.ts`, `workflow/json-schema.ts`: the StructuredOutput tool and its validator | `structured_output.go`, `workflow_jsonschema.go` (the one validator, shared with the workflow slice), `runner.go` (retry prompt, capture) | 11 twins (`structured_output_twin_test.go`), `workflow_jsonschema_twin_test.go`, `observability_test.go` |
| M21 | `usage.ts`, `index.ts`: usage accounting, `reportUsage`, `showCost` text, `<context_percent>`, `<compactions>`, cost, `subagents:started`/`compacted`, `get_subagent_result` verbose | `usage.go`, `usage_display.go`, `observability_app.go`, `extension.go`, `tools.go` | twins of `usage` (accumulator, pool), `usage-reporting`, `cost-display`, `status-note-wiring`, `subagent-error-status-e2e`, `agent-startup-error`; `obs-settings-honored` |

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
3. **Unsupported parameters of `Agent` are refused.** `schedule` is a tool error with the message in the README
   (`refuse.go`). `inherit_context`, `resume`, `skills`, `extensions`, `disallowed_tools`, `isolated` and `isolation` are
   ported (see 11 and 12); the first version answered `inherit_context` with plain text and emulated `resume` by
   starting a new agent with the old answer in its prompt, a partial run, now removed. The tool text is the original's, so
   the model is told about these; a call that uses an unported one gets the refusal.
4. **Agent files** are read at start-up (the workspace of an extension process is fixed; with `strictAgentFiles` an
   unreadable one aborts the load, as a throw from the factory does: here a panic out of `Extension()`, the way the SDK
   itself fails a bad registration), and again on every `Agent` call, as the original does, so a file added mid-session is
   usable at once. The tool *description* is fixed at registration, as in the original.
5. **Settings.** All 24 keys are parsed and stored (`rememberAgents` is applied by the sessions slice, `worktreeIsolation` by the worktree slice) (`settings.go`); `subagents.json` is read once at start-up (and again
   only when the session's workspace differs from it), as the original reads it once. The keys of other slices
   (`schedulingEnabled`, `workflowsEnabled`, `maxSubagentDepth`, `agentMentions`,
   `widgetMode`, `fleetView`, `showCost`, `showModel`, `viewerMarkdown`, `outputTranscript`, `reportUsage`) keep being
   refused by `settingRules` until their slice lands; they are read through `settingsView()`. A malformed value is
   ignored silently, as in the original; a malformed file warns on stderr. `subagents:settings_loaded` is emitted at the
   first `session_start` (the original emits it from the factory, where the SDK has no connection yet), before
   `subagents:ready`. `saveSettings` writes the project file for the settings menu (G5), which does not exist yet.
6. **Not ported** (named in `port/slices.json`): the workflow runtime, scheduling, `@mentions`,
   widget, fleet and conversation viewer, nested delegation, the settings menu, and the widgets that show
   cost and model. Output transcripts, structured output (`childSpec.Schema`, for the workflow slice), usage reporting and
   the cost texts are ported (M19-M21).
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
   The remaining difference of the notification: a 500-character preview counted in code points where JavaScript counts UTF-16 units.
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
12. **Worktree isolation and memory, differences.** Git runs through `os/exec` in the extension process (the original's
    `pi.exec`; the SDK has no exec call), killed by the same per-call timeouts; the twins replace that seam with a fake.
    The agent's system prompt is built when its run starts (`childSpec.Prepare`), after the worktree copy exists, so its
    environment block and its `memory:` directory are those of the copy (the original's `effectiveCwd` and `configCwd` of
    a worktree spawn). The extension and tool scope is planned before the copy exists, from the workspace (`childScope`'s
    `configCwd`); the original plans it in the copy. `MEMORY.md` is decoded like Node's utf-8 decoder, one U+FFFD for each
    maximal subpart of an ill-formed sequence (`decodeUTF8Lossy`); Go's `strings.ToValidUTF8` would replace a whole run of
    bad bytes with one. Not applicable because the feature is not ported: `SpawnOptions.cwd` and its `workPath` note,
    `onBeforeWorktreeCleanup` (workflows). A resume continues the conversation where it ran and makes no worktree.
13. **Worktree and memory: evidence and measurements.** `pigeq` scenarios exist for what is observable without a child
    model: the `isolation` parameter and guideline in the tool text (`tool-tools-isolation`) and the strict failure
    outside a git repository, from the call and from an agent file (`agent-isolation-not-git`,
    `agent-isolation-agent-file-not-git`). A scenario in which an isolated or memory agent runs a child model cannot match
    (F2: the child's host prompt names pig; a worktree path carries a random agent id), and an agent that fails reports
    `details` (agent id, duration) that this port's failure result does not carry (a base-slice difference, not
    specific to memory), so those behaviors are pinned by the twins and by `worktree_feature_test.go`. Measured on
    imladris (Xeon 6746E, `go test -bench`, real git 20 iterations): create, edit, commit to a branch and remove a worktree of a
    one-file repository 27.4 ms/op, 95 KB, 966 allocs; the memory block of a 300-line MEMORY.md 57 us/op, 33 KB, 28 allocs.
    The original runs only under Node (the same git calls through `pi.exec`), so no side-by-side figure is claimed. None of
    this is on the TUI input or render path: it runs in the agent's start and settle goroutines.


## Slice: settings store, dispatch policy, tool description modes (G10, G13, G14, G18, G19)

Behavior that changed against the first release, each because the original does it (twin or test in brackets):

- `get_subagent_result` and `steer_subagent` take an id or an agent's handle (its name's slug, else its type, `explore-2`),
  compared without case; they no longer take a prefix of an id or the raw name (`resolveAgentRef`; `TestResolveByIDAndHandle`).
- A cancelled `wait: true` fails that tool call and leaves the agent running and unconsumed (`wait-queued`); a queued agent
  reads as `No output.` until it starts, only a running one as "still running".
- Stopping an agent marks it `stopped` at once, a queued one leaves without a completion event, a foreground agent is never
  announced, an unconsumed record survives a session switch, and records are evicted ten minutes after they settle
  (`agent-manager-gc`, `clear-completed-wiring`, `foreground-result-retrieval`).
- The agent file's `model`, `thinking`, `max_turns`, `run_in_background` and `isolated` outrank the call's; the default
  `Explore` model (`anthropic/claude-haiku-4-5`) is used when it is an available model, else the parent's (the first
  release never forced it).
- The RPC listeners are registered once, at the first `session_start`, and removed at shutdown; the internal options
  (`parentAgentId`, `workflowId`, `depth`, `maxSubagentDepth`, `configCwd`, `rootSessionId`, `resumeSessionFile`, `reclaim`,
  `blocking`) are dropped from an RPC or registry spawn, as the original drops them, instead of being refused. `stop`
  and `consume` leave an owned child alone.
- An RPC or registry spawn reloads the agent files first (`spawnResolved`, `index.ts:666-674`), and without
  `isBackground: true` it is detached: it runs at once, holds no pool slot and is not announced to the model, as in
  `agent-manager.ts:520-540`, `:969` (`TestRPCSpawnReloadsAgentFiles`, `TestRPCSpawnWithoutIsBackgroundIsDetached`).
  `isBackground: false` is honored, no longer refused.
- A top-level agent's final record is appended to the session as a `subagents:record` entry (`index.ts:584-589`;
  `TestSubagentsRecordEntry`), `subagents:created` is emitted for background spawns only (`index.ts:2104`;
  `TestCreatedEventIsBackgroundOnly`), an unknown type falling back to a removed `general-purpose` still runs under the
  built-in definition with the note "using the fallback agent config" (`agent-runner.ts:675-683`; `index.ts:1797`), and a
  type's display name comes from `getConfig` (a disabled or removed type reads as the fallback's).
- The manager registry (`registry.go`) is the original's `Symbol.for("pi-subagents:manager")` for other packages of the
  same process (a fused Piglet Binary): the first activation owns it, only the owner's shutdown releases it.

Twins of the original's tests (`pigeq twins check` over the 22 files of this slice): see Results below. Named skips and
their reasons are in the twin files; the ones that have no Go form:

- `agent-manager-gc`: "closes the evicted session's extension lifecycle before disposing it" (an agent is a child process,
  there is no in-process session to dispose; the other eviction case asserts the eviction itself).
- `foreground-concurrency`: "rethrows a drain-time startup failure" (the failure is strict worktree creation, G3).
- `foreground-concurrency-wiring`: "says it is queued in the live tool result" (partial tool results for the renderer, G5).
- `fallback-subagent-wiring`: scheduled-job and resume cases (G2, G9: the call is refused before dispatch).
- `rpc-lifecycle-gating`: the two widget cases (G5).
- `status-note-wiring`: the three compaction cases (G12, G7); the nested-records case runs without its `resume` probe,
  which this port refuses before any lookup.
- `tool-description-mode`: the example template is not in the vendored oracle (it vendors `src` and `test`); the pinned
  commit's `examples/agent-tool-description.md` is kept byte for byte in `port/upstream-extra/` and the twin runs on it.

The `subagents:record` entry has no scenario: it exists only once an agent has run to completion, and a child run is not
comparable (F2), so the entry is proved on the host channel instead (`TestSubagentsRecordEntry` reads the `appendEntry` host
call; a nested child leaves none).

What the harness cannot show: a child run is not comparable (F2), so the join modes, the pools and the background default
are proved by their twins and `policy_test.go`, not by a scenario. Scenarios cover the model-facing text of every mode
(`tool-tools*`), the model error (`agent-model-not-found`) and, for every combination of the three optional features,
`port/description-golden` (recorded from the original under Pi, read by `description_golden_test.go`; the port refuses
those features, so `pigeq check` cannot run them).

## Observability slice (M19-M21): what differs from the original

- **Where the data comes from.** Usage is summed from each assistant `message_end` of the child's rpc stream (input,
  output, cacheWrite for the display total; cacheRead and cost for the reported `usage`), compactions from
  `compaction_end` (not aborted, with a result), the transcript from `message_end` flushed at `turn_end` and
  `compaction_start`. Pi's `session.messages` array has no Go form, so the stream follows the messages it has seen and
  keeps only the unwritten tail; a successful compaction drops what was seen since the last flush (the original's
  re-anchor to the rebuilt array). `get_subagent_result verbose` reads the child's `get_messages` for a running agent and
  the formatted conversation for a settled one.
- **The system message is a message (H1).** Under Pi 1402973a `session.messages` starts with `{"role":"system"}`
  (`packages/agent/src/agent.ts`; the `message_end` is in every golden of this port). The original's start index 1 skips
  it, writes the user prompt a second time after the initial entry, and types a later system message `toolResult`. The
  port does exactly that; `TestTranscriptOfPisEventOrderMatchesTheOriginal` replays the events of
  `golden/obs-agent-file-output-transcript.jsonl` (recorded under Pi) through the stream. PiG's stream carries the same
  system message, so there is no PiG-vs-Pi difference here. A whole child run cannot be a golden (F2 below).
- **Memory (H2).** The stream keeps only its unwritten tail and the record drops the stream when the run ends. The record
  keeps the conversation as the text `get_subagent_result verbose` prints (tool results already cut to 200 characters),
  not the raw messages, bounded to 1 MiB (`maxConversationBytes`; the oldest messages go first, marked
  `[earlier messages omitted]`). This bound is a difference from the original, which holds the whole session until the
  manager removes the record 10 minutes after it finished; that removal is gap G18, open, and its Go form belongs to
  the settings slice.
- **A stopped or failed agent (M1).** The original keeps `record.session`. Here the child's event stream builds the
  conversation as it goes, and `abort` reads `get_session_stats` (2 s limit) before the kill, so a stopped agent keeps
  its conversation and context percent. A child that dies on its own keeps its conversation but has no context percent
  (the process cannot be asked). A run that settles keeps `get_messages` as before (after any compaction).
- **Settings reader (L1).** `outputTranscript`, `reportUsage` and `showCost` are fields of `settings.go`, read with the
  other five.
- **`showCost` (L3).** The cost appears in the notification, the result and the steer texts; the widget's cost display is
  the UI slice's. The notice for `showCost: true` says so.
- **No session id (L7).** If the host cannot name the session, the agent runs without a transcript and a line goes to
  stderr (the original always has an id).
- **Rounding (L9).** `toFixed` is `jsToFixed`: an exact decimal tie takes the larger neighbor (`(1.25).toFixed(1)` is
  `1.3`), where Go's `%f` gives `1.2`. It is used for the cost, the duration and the token counts.
- **Resume.** A background resume of an agent that has a transcript streams the resumed run into the same `.output` file
  (`index.ts:1321` `onStarted`); `ensureOutputFile`'s other caller, the foreground resume, writes nothing, as in the original.
- **Harness limit.** A scenario that runs a child cannot be compared whole (F2: the child's request carries the host's own
  system prompt, which differs between Pi and PiG), and the scripted model reports 1 input and 1 output token and no
  prices, so usage, cost and context numbers are proven by the twins and by `observability_test.go` against a scripted
  child process, not by a golden. The scenarios `obs-settings-honored`, `obs-agent-file-output-transcript` and
  `agent-result-verbose-not-found` compare what the parent sees at session start.
- **JSON key order.** A transcript line is `JSON.stringify` in the original (keys in insertion order); Go marshals a map's
  keys sorted. The entries are equal as JSON.
- **StructuredOutput.** The canonical payload is the validated arguments with sorted keys. The validator is the
  workflow slice's `workflow_jsonschema.go` (`compileWorkflowSchema`, twinned against `workflow-json-schema.test.ts`):
  the original has one validator, `workflow/json-schema.ts`, and so does the port. The file is the same in both
  branches, so the stack merges it as one. In the original only the workflow host sets `schema` (`agent({ schema })`),
  so nothing in this slice's tools passes one; `childSpec.Schema` is the workflow slice's input. A schema-bound child
  cannot be isolated (its tool is an extension tool). The schema reaches the child in a private temp file named by
  `TINTINWEB_SUBAGENT_STRUCTURED_SCHEMA_FILE` (an environment string is capped at 32,767 characters on Windows and
  128 KiB on Linux); the parent removes it when the child exits. SDK gap: `sdk.ToolPrepareArgumentsFunc` is
  `func(map[string]any) (map[string]any, error)` while Pi's `ToolDefinition.prepareArguments(args: unknown)` also
  receives a payload a model sent as one JSON string, so `prepareStructuredArguments` is twinned but cannot be wired.
- **Transcript path errors.** A temp directory that cannot be created skips the transcript with a message on stderr; the
  original's call would throw.
- **Notification details.** The `details` object of the `subagent-notification` message (for the renderer) is the UI
  slice's; this slice changes its text (`<output-file>`, `<context_percent>`, `<compactions>`, cost, the footer).
- **Context percent.** The percentage is the child's `get_session_stats` `contextUsage.percent`, read live for a running
  agent and when the agent settles.

## Observability slice: verification record of the slice branch before the stack (superseded by the stack results below)

- `pigeq check`: 8 of 8 (port-gaps, exec-coverage and six scenarios), three runs in a row; the transcript goldens carry
  Pi 1.3.0.1402973a's system `message_end`. `agent-unknown-type-strict` failed in two earlier runs under heavy lane load
  (event order between the model request and a host event) and passed in the three final ones.
- `go test -race ./...` and `go vet` for linux, windows and darwin: exit 0 (the runner prints `GOOS evidence: requested
  windows -> go env GOOS prints windows`, likewise darwin and linux).
- `pigeq mutate --unit-only` at head 925c048: 271 mutations, 0 not killed (exit 0). One earlier attempt at the same head stopped
  because the unmutated baseline failed `TestBusProtocol` once (a timing flake under lane load; it then passed `-count=8` and the
  rerun).
- Latency at head 925c048 (median of 10 interleaved runs of the same `TestPerfReport`, this revision vs PR 1 d710533, one
  job): `agent-not-found` 61 us vs 64 us, `get_subagent_result` 65 us vs 64 us, `steer_subagent` 122 us vs 121 us,
  registration 1143 us vs 1170 us, all within 5%. Background start-and-finish is **714 us vs 416 us (+72%, 819 vs 670
  allocations)**, which exceeds 10% and is justified by behavior the original performs on every spawn and PR 1 did not:
  the `.output` transcript (directory check, initial entry file: `BenchmarkAttachTranscriptFiles` 95 us, 34 allocations,
  measured) and the `subagents:started` event (one more host round trip on the start path; the rest of the gap, about
  200 us, is [INFERENCE] this host call and the usage wrapper, not separately measured). Two avoidable costs were
  removed by profiling the path: the root directory mkdir and chmod (skipped when the root is already owner-only) and the
  per-spawn `getSessionID` host call (now once per session, cleared at `session_start`; 892 to 819 allocations).
  Benchmarks: streamed delta through the stream 39 ns, 0 allocations; transcript turn 33 us; usage hook 24 ns.
- `npm run check` and `npm run generate` (in a copy with full git history): both exit 0, no tracked file changed.
- The 5 mutants named above are killed in the final run (269 of 269 at ea1cb54; the two mutants for the session-id
  cache were added after).
