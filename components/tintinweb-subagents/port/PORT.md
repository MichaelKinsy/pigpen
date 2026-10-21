# Port record: @tintinweb/pi-subagents (partial)

| Input | Identity |
|---|---|
| Original | `@tintinweb/pi-subagents` 0.19.0 by tintinweb, https://github.com/tintinweb/pi-subagents, commit `4f572eaa04c09d3dbc16e4a5f13a16b295e84e14`, MIT |
| Oracle | Pi 1.0.3, Node 24.19.0, with the unmodified package at `port/oracle` (dependencies from npm: `croner`, `nanoid`, `typebox`) |
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
| M12 | `output-file.ts`: the `.output` transcript, path rules, compaction handling | `output_file.go`, `observability_app.go` | 20 + 3 + 1 + 5 twins (`output_file_twin_test.go`); `obs-agent-file-output-transcript` |
| M13 | `structured-output.ts`, `workflow/json-schema.ts`: the StructuredOutput tool and its validator | `structured_output.go`, `workflow_jsonschema.go` (the one validator, shared with the workflow slice), `runner.go` (retry prompt, capture) | 11 twins (`structured_output_twin_test.go`), `workflow_jsonschema_twin_test.go`, `observability_test.go` |
| M14 | `usage.ts`, `index.ts`: usage accounting, `reportUsage`, `showCost` text, `<context_percent>`, `<compactions>`, cost, `subagents:started`/`compacted`, `get_subagent_result` verbose | `usage.go`, `usage_display.go`, `observability_app.go`, `extension.go`, `tools.go` | twins of `usage` (accumulator, pool), `usage-reporting`, `cost-display`, `status-note-wiring`, `subagent-error-status-e2e`, `agent-startup-error`; `obs-settings-honored` |

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
   widget, fleet and conversation viewer, agent memory, nested delegation, the settings menu, the widgets that
   show cost and model, model scope and fuzzy model resolution (the child resolves `--model`). Output transcripts,
   structured output (`childSpec.Schema`, for the workflow slice), usage reporting and the cost texts are ported (M12-M14).
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

## Observability slice (M12-M14): what differs from the original

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
- **Resume.** `ensureOutputFile` is not ported here; its only caller is a resume, which this port refuses.
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

## Observability slice: verification record (review fixes)

- `pigeq check`: 8 of 8 (port-gaps, exec-coverage and six scenarios), three runs in a row; the transcript goldens carry
  Pi 1.3.0.1402973a's system `message_end`. `agent-unknown-type-strict` failed in two earlier runs under heavy lane load
  (event order between the model request and a host event) and passed in the three final ones.
- `go test -race ./...` and `go vet` for linux, windows and darwin: exit 0 (the runner prints `GOOS evidence: requested
  windows -> go env GOOS prints windows`, likewise darwin and linux).
- `pigeq mutate --unit-only`: 269 mutations. The last full run before the final test additions left 5 surviving
  (`result-consumed-not-marked`, `setting-notice-repeats`, `pool-kept-when-reporting-off`, `usage-pooled-when-off`,
  `usage-attached-when-off`) and 0 invalid; each has a test since (`observability_state_test.go`). That last test addition
  was not re-run under `mutate` at the time of writing.
- Latency (median of 3 runs, this revision vs PR 1 d710533, same `TestPerfReport`): registration 1.05 ms vs 1.05 ms,
  `get_subagent_result` 62 us vs 60 us, `agent-not-found` 57 us vs 55 us, `steer_subagent` 120 us vs 115 us,
  background start-and-finish 0.68 ms vs 0.40 ms (the transcript file: directory, chmod, open, initial entry; 900 vs 670
  allocations). Benchmarks: transcript turn 31 us, streamed delta through the stream 39 ns with no allocation, usage
  hook 24 ns, schema check 1.8 us.
- `npm run check` and `npm run generate` could not be run in the runner's copy without git history (the package records
  of other components name commits it lacks); a run in a copy with history was queued and had not started.
