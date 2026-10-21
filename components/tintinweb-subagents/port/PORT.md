# Port record: @tintinweb/pi-subagents (partial)

| Input | Identity |
|---|---|
| Original | `@tintinweb/pi-subagents` 0.19.0 by tintinweb, https://github.com/tintinweb/pi-subagents, commit `4f572eaa04c09d3dbc16e4a5f13a16b295e84e14`, MIT |
| Oracle | Pi 1.0.3, Node 24.19.0, with the unmodified package at `port/oracle` (dependencies from npm: `croner`, `nanoid`, `typebox`) |
| Target | PiG 0.4.1+1.0.3 (release v0.4.1, commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`), go1.27.1 |
| Original's tests | 2029 cases in 105 files (`port/upstream-tests.json`). **173 exact twins** (`agent-types` 46, `custom-agents` 76, and 51 about nested delegation across `nested-tools`, `nested-delegation-e2e`, `subagents-nested-print-mode-e2e`, `agent-manager`, `agent-runner`, `cross-extension-rpc`, `documented-defaults`, `foreground-concurrency`, `manager-registry-guard`, `settings` and `status-note-wiring`); **13 named skips** among the nested cases, each with its reason in the test (scopeModels, `output_transcript`, `persist_session` and `resume`, usage reporting, `disallowed_tools`, compaction events, tombstone reclaim: each follows the gap that ports it); **the rest of the 2029 deferred by name** in `port/slices.json`, each file with its reason |
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
- nested delegation: an agent file's `allowed_subagents` gives that agent nested `Agent`, `get_subagent_result` and
  `steer_subagent` tools, bounded by `maxSubagentDepth` (D6).

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
| M12 | `nested-tools.ts`, `agent-runner.ts:843-866`, `agent-manager.ts` (ownership, depth, `abortOwnedChildren`), `cross-extension-rpc.ts` stop ownership, `allowed_subagents`, `maxSubagentDepth` | `nested.go`, `manager.go`, `runner.go`, `bus.go`, `settings.go` | 51 twins and 13 named skips (`nested_tools_twin_test.go`, `nested_process_test.go`, `nested_manager_twin_test.go`, `nested_other_twin_test.go`), `nested_extension_test.go`, `nested_guard_test.go`, a fake pig two and three levels deep; scenario `nested-agent-file` |

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
- `pigeq mutate --unit`: 81 mutations, all killed (the first run killed 58; 16 survived and 7 did not build; each
  survivor got a test in `extra_test.go` / `extra2_test.go` and each invalid mutation was rewritten). After the review
  fixes (finding 10): **101 mutations, all killed by the unit tests** (`--unit-only`); the first run after the fixes left
  three (a setting that never reached the child, a process-group signal the test waited long enough to miss, and a test
  whose failure text the runner read as a build failure), each closed by a test.
- **Nested delegation (G7), imladris, container tern-ci:v2, `taskset` 96-151:** `go vet` and `go test -race -count=1 ./...`
  pass; `go vet` passes for `GOOS=windows`, `darwin` and `linux` (each run printed `go env GOOS` for the requested
  value, kept in the lane's log); the nested tests also pass `-race -count=10`. A `-race -count=6` run found one flake (the
  nested Agent tool answered "started in background" for a child that could not start, because the manager announced
  `started` before it recorded the failure); fixed, with a test that holds the manager's lock so the order is the test's,
  not the scheduler's. `pigeq check`: `port-gaps`, `exec-coverage` and the four scenarios pass, including the new
  `nested-agent-file` (an agent file with `allowed_subagents` and `maxSubagentDepth: 3` in `subagents.json`, equal to Pi's
  trace; recorded under Pi 1.3.0.1402973a). `pigeq mutate --unit-only`: 197 mutations, all killed (the first run left 8:
  four that did not build, three that survived for want of a test, and one `depth-default` that the tests' own reset
  hid; each got a test or a valid rewrite; the last of them, `start-announced-before-failure-recorded`, was rewritten after the full run and killed in a run of its own). `npm run check` and `npm run generate` (in a full-`.git` clone): check exit
  0, generate changes nothing. `pigeq twins check`: no nested title missing or unknown; the one `UNKNOWN` it prints
  (`accepts %s as a spelling of off`, `custom_agents_twin4_test.go`) and the 19 `MISSING` worktree cases are not nested.
- **Performance of the nested additions** (`nested_perf_test.go`, 5 runs, imladris host, medians): the environment of a
  child was 959 ns, 768 B, 2 allocs (the parent's environment plus the depth variable) and is 1.1 µs, 800 B, 3 allocs for a
  child that may not delegate (+15%, 0.15 µs, from dropping the stale `TINTINWEB_SUBAGENT_*` of a parent that is itself a
  child) and 1.9 µs, 1.07 kB, 10 allocs for one that may (the JSON of the allowance and five variables). A first version
  of that filter cost 3.6 µs; the shipped one is one prefix test per variable. The command line is 0.85 µs, 6 allocs.
  These are per-agent-start costs next to the start of a `pig` process (tens of milliseconds): under 0.01% of it, so the
  10% rule on per-call cost is reported to the lead rather than chased further. In a nested child, once at start:
  reading its place from the environment 7.2 µs; registering the three nested tools, four hooks and one read of the
  settings and agent files 73 µs, 23 kB, 190 allocs (a child with no allowance: 2.6 µs, registers nothing). Nothing
  runs on the TUI render path. The nested Agent tool re-reads the agent files per call, as the original does.
- `pig package validate components/tintinweb-subagents` and `pig install <extension dir> --validate-only --json`: valid.
- Live check of the child's protocol against `pig-0.4.1 --mode rpc`: `get_session_stats` returns `data.tokens.total` and
  `data.toolCalls`, `get_last_assistant_text` returns `data.text` (or `{}`), a `prompt` without a usable model
  answers `success: false` with the reason, which the runner reports as the agent's error.

## Deliberate differences and findings

1. **A child process, not an in-process session.** Each agent is `pig --mode rpc --no-session`; an agent costs a process
   start. The pig to run is found as described in the README. The child inherits the parent's environment (and so its
   credentials and settings) and, unless the call or file sets one, the parent's model and thinking level. A child
   registers no top-level tools (`TINTINWEB_SUBAGENT_DEPTH`) and nests only as D6 says. `Explore`'s `claude-haiku-4-5` default is not forced on the
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
   widget, fleet and conversation viewer, agent memory, output transcripts, structured output, the
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

## D6. Nested delegation across processes

The original keeps every agent in one in-process manager, so a nested tool reaches the record of any agent by id and
checks `record.parentAgentId`. Here every agent is a process, so each level has a manager of its own and the design
follows from that:

- **Who may delegate.** The runner starts a child with its place in the tree in the environment (`childEnv`):
  `TINTINWEB_SUBAGENT_DEPTH` (a top-level agent's child is 1), `..._MAX_DEPTH` (the branch's cap: `maxSubagentDepth` read
  when the first agent of the branch starts, so a later change reaches only new branches, as in the original),
  `..._ALLOWED` (the file's `allowed_subagents`: `"all"` or the list), `..._ID` (the agent's own id: the owner of its
  children), `..._CONFIG_CWD`, `..._USAGE_FILE`. The three nested variables are set only when the file allowed it, the
  agent is not `isolated` and `depth < cap`, the original's test (`agent-runner.ts:847-865`); a variable the parent was
  itself started with never reaches a grandchild.
- **What the child loads.** The same extension. `Extension()` sees the depth variable and, with an allowance below the
  cap, registers the three nested tools in place of the top-level ones, and no RPC, no `/agents`, no
  `subagents:ready`/`created`/`completed`/`failed` event and no notification: nested agents emit nothing, and the
  child's own event bus is not the parent's. The child's tool list admits the nested tools by name (`--tools`); for
  `extensions: false` (no other extensions, but the nested tools) the child is started without `--no-extensions` and the
  tool list holds only the built-ins and the nested tools, the original's `noExtensions` allowlist. This is the one place
  the child loads other extensions' hooks: their tools stay out, as in the original.
- **Ownership.** The nested tools act on the child's own manager. Every record in it has `ParentID` = the agent's id, so
  `ownsRecord` is the original's check and an id of another agent (another process) is simply not there; the texts are
  the original's. Owned records have no handle and take no pool slot (`occupiesPoolSlot`), `resolve` and `/agents`
  skip them (`isTopLevelAgent`), and `subagents:rpc:stop` refuses them. The RPC drops a caller's `parentAgentId`, `depth`,
  `maxSubagentDepth` and `configCwd` options, as `spawnTopLevel` does.
- **Foreground by default**, and the agent file's `run_in_background`, `model`, `thinking`, `max_turns` and `isolated`
  outrank the call's values (`resolveAgentInvocationConfig`), unlike the top-level tool of this port.
- **Stopping reaches down.** A parent's settle (the child's `agent_settled`) aborts the children it owns
  (`abortOwnedChildren`); a stop of the agent or the end of the session SIGTERMs the agent's process group, pig runs the
  extensions' `session_shutdown` on SIGTERM and at the end of its input, and the nested extension aborts everything it
  started: each of those is a process group the runner signals in turn, so a stop reaches any depth. A child that is
  SIGKILLed outright cannot do this itself, but its own children read their input from a pipe it held, so their input
  ends and their pig shuts down the same way; the stop path always sends SIGTERM first, five seconds before SIGKILL.
- **Spend.** The original's `onAssistantUsage` adds a nested child's usage to every ancestor. Here a settled child's
  tokens go to its owner's usage file; the owner's runner adds the file's total to the owner's own count when it
  settles, so each level's count includes everything below and the top-level record counts the tree.
- **The cap.** `maxSubagentDepth` is read from `subagents.json` (0 to 16, default 2) and floored; a branch's cap is passed
  down unchanged. The `Nested subagent call blocked (depth=N, max=M)` text exists for a tool that is reached at the cap; the
  runner never gives an agent at the cap the tools. **No chain of processes is longer than the ceiling (16)** whatever the
  environment or a setting says (`nestedActive` and `nestedConfigFromEnv` clamp it; `TestNoChainOfProcessesIsLongerThanTheCeiling`).
  As in the original, how many children one agent starts is the model's choice (nested children take no pool slot);
  only the depth is capped.
- **Panics.** Every goroutine this adds recovers: an agent's run (`safeRun`: a panic settles the record as an error,
  returns its pool slot, starts the next queued agent, stops its child and releases its waiters, so an owner waiting on it
  never hangs), the reader of a child's output (one message that panics is dropped) and the sender of the turn-limit
  commands. The SDK recovers the tool and hook handlers themselves.
- **Not carried over, refused.** `resume` and `inherit_context` of a nested call, and `isolation`, are refused with the
  port's one message (they follow their own gaps). The `scopeModels` check and output transcripts of nested spawns follow
  those gaps (scopeModels, `output_transcript` and the setting are refused today).
- **Faithfulness check.** Every model-facing string of the nested tools is a string literal of `nested-tools.ts`
  (`TestNestedToolTextIsTheOriginals`).
