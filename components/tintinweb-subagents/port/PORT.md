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

## SDK gap check (`pigeq gaps --ts port/oracle/src`)

One `MISSING`: `ctx.ui.custom(options.onHandle)` (`ui/workflow-menu.ts`), the workflow menu of the deferred slice. It
is accepted in `port/accepted-gaps.json` with the approval it rests on: the lane lead's instruction of 2026-10-05 20:56
MDT to ship a tested slice and defer the rest by name. (The Go SDK of PiG 0.5.0 does have `RemoteOverlayOptions.OnHandle`
and `OverlayHandle`, which the display surfaces use to close an overlay from outside; its `pigeq gaps` table is stale for
Go.) The Agent tool's `renderCall`/`renderResult` and the notification renderer are ported (see "Display surfaces").

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
   agent memory, nested delegation, output transcripts, structured output, the settings menu, model scope (the child
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
