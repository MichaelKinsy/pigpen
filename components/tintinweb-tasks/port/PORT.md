# Port record: @tintinweb/pi-tasks

| Input | Identity |
|---|---|
| Original | `@tintinweb/pi-tasks` 0.9.0 by tintinweb, https://github.com/tintinweb/pi-tasks, commit `00ecbd8110f1a4e267791f9cecb2612144c78f6e`, MIT |
| Oracle | Pi 1.0.3, Node 24.19.0, with the unmodified package at `port/oracle` (dependency `typebox` 1.x from npm; `@earendil-works/pi-coding-agent` and `pi-tui` are Pi's own) |
| Target | PiG 0.4.1+1.0.3 (release v0.4.1, commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`), go1.27.1 |
| Original's tests | 389 `it` cases in 19 vitest files; 386 titles are in the ledger (`port/upstream-tests.json`; the two `it.each` cases of `task-sort.test.ts` have no literal title and are ported as ordinary subtests): **386 exact twins, 3 named skips** |
| Kind | 1: an extension, no CLI built-in, no provider |

## Mapping

| ID | Original | Go | Scenario / test |
|---|---|---|---|
| M1 | `index.ts`: seven tools with TypeBox schemas, descriptions (Claude Code's specs), `promptGuidelines` | `tools.go`, `descriptions.go` (extracted byte for byte) | `tool-definitions` and the `llm` event of every scenario (full tool definitions in the request); `integration_test.go` |
| M2 | `task-store.ts`: CRUD, bidirectional edges and their warnings, metadata merge, file lock with stale detection, atomic write, legacy-record normalization | `store.go`, `proc_unix.go`, `proc_windows.go` | `create-update-list`, `dependencies`, `delete-and-metadata`; `store_test.go`, `store_shared_test.go`, `extra_test.go` |
| M3 | `task-sort.ts` (presets, sort specs) | `sort.go` | `config-glyphs` (a malformed `sortOrder`); `sort_test.go` |
| M4 | `task-glyphs.ts` | `glyphs.go` | `config-glyphs`; `glyphs_test.go`, `widget_test.go` |
| M5 | `tasks-config.ts`, `task-paths.ts` (global and project config, `session-global` paths) | `config.go`, `paths.go` | `config-glyphs`; `config_test.go`, `paths_test.go`, `scope_test.go` |
| M6 | `reminder-cadence.ts`, the `context`, `tool_result`, `turn_end` hooks, the reminder text and echo | `cadence.go`, `extension.go` | `reminder-stale`, `reminder-sanitized`, `reminder-cap`; `cadence_test.go`, `stale_test.go` |
| M7 | `auto-clear.ts` and its use (`agent_settled`, `turn_start`, TaskCreate, resume) | `autoclear.go`, `extension.go`, `tools.go` | `auto-clear-new-batch`; `autoclear_test.go`, `lifecycle_test.go` |
| M8 | `ui/task-widget.ts`: rows, spinner timer, token and time stats, collapse, limits | `widget.go` | `widget_test.go` (a live-render twin per case); the scenarios show the widget is quiet in RPC mode |
| M9 | `/tasks` menu, detail actions, create, clear | `command.go` | `command-menu`, `command-clear`; `command_test.go` |
| M10 | `ui/settings-menu.ts` (`SettingsList`) | `command.go` (chained selects) | `settings`-related twin is a named skip (G6) |
| M11 | `process-tracker.ts` | `tracker.go` | `tracker_test.go` (not reachable from a tool call in the original either) |
| M12 | session hooks (`session_start` with its `reason`, `before_agent_start`, `tool_execution_start`), store re-pointing, fork seeding, reattaching agents after a reload | `extension.go` | `new-session`; `integration_test.go`, `reattach_test.go`, `scope_test.go` |
| M13 | pi-subagents protocol over `pi.events`: ping (`subagents:rpc:ping`) and `subagents:ready`, `spawn`, `stop`, `consume` RPCs on scoped reply channels, `subagents:completed` and `subagents:failed`, cascade, `TaskOutput` joining and consuming a result | `extension.go`, `tools.go` | `task-execute-unavailable`; `cascade_test.go`, `output_stop_test.go`, `integration_test.go` with a fake pi-subagents on a scripted bus |

## SDK gap check (`pigeq gaps --ts port/oracle/src`)

`PARTIAL ctx.model` only (index.ts lines 326, 1182, 1198: the `ctx.model` of the test mocks' shape; the original reads it
for nothing observable). No `MISSING`. For the Go source, `pigeq gaps --go` reports the `PARTIAL Context.SetWidget` stand-in (G1). The original registers no renderer, so PiG 0.4.1's `Extension.ToolRenderer` is not needed. `pi.events` is
bridged in PiG 0.4 (`Context.Events()`).

## Results (this revision)

- Red commit `386c37e4`: 387 failing, 45 passing (zero-value stubs), 3 skipped; `port/red-run.log` lists both.
- `go test -race ./...`: pass (also `-count=6`).
- `pigeq check` against goldens **recorded from the original under Pi 1.0.3**: 15 of 15 scenarios, plus `port-gaps` and
  `exec-coverage`. Three scenarios cover the model request, so the seven tool definitions equal Pi's.
- `pigeq mutate`: 117 mutations, all killed: 111 by the port's own tests alone (`--unit-only`), and the other 6 only by
  scenarios, each re-run alone (`--jobs 1`) and killed with a difference that belongs to the mutated text. The first
  run killed 93; each survivor got a test in `extra_test.go` / `extra_ext_test.go`; each invalid mutation was
  rewritten. **A kill by scenario under load is not trusted:** a full run with four mutants at once (on a host with a
  load average above 70) reported three mutants killed by `parallel-create` / `reminder-cap` that no scenario can
  tell apart from the original (finding 8); they had no test of their own until `TestLaunchResultAndCadenceEdges`,
  `TestTaskPaths`, `TestTaskExecuteRefusesAMissingBlocker` and the `/tasks` auto-clear case were written.
  `port/gen-mutations.py` writes `port/mutations.json` and checks every `find` string is unique.
- `pig package validate components/tintinweb-tasks` and `pig install <extension dir> --validate-only --json` (`form:
  factory`, `language: go`): valid.

## Differences and findings

**Deliberate differences (also in README):** the widget is pre-rendered rows; `/tasks` Settings is a chain of selects;
`<agent dir>` follows PiG's rule (D2), and named lists (`PI_TASKS=<name>`) live under `<agent dir>/tasks/` instead of `~/.pi/tasks/`; the ping to pi-subagents is sent on the first `session_start`, on `subagents:ready` and from
`TaskExecute`; task metadata keys are sorted.

**Named skips (3):**
1. `opens the settings panel and returns to the main menu afterwards`: it counts `ui.custom()` calls of a `SettingsList`
   component (G1). The Go behavior is covered by `command_test.go` and `extra_ext_test.go`.
2. `stops waiting when the tool call is aborted`: the fake host has no frame that cancels one request. The wait selects on
   `ctx.Done()`, which the SDK closes when the host cancels.
3. `keeps an in-memory store when the context cwd changes`: the Go SDK fixes the workspace of an extension process.

**Host and SDK findings (to file against PiG):**
1. **G1 No component widget.** `SetWidget` takes lines (the host shows ten). A `SetWidgetRenderer` like
   `SetFooterRenderer` is needed.
2. **G2 `Context.Input` always sends a `placeholder`.** In RPC mode the request carries `placeholder: ""` where Pi's
   `ui.input(title)` has none, so the menu's *Create task* cannot be a scenario (a layer-1 twin covers it).
3. **G3 PiG's RPC mode emits a factory widget's rows** (`widgetLines: []`) where Pi emits nothing. The host check
   (original under PiG's Node runtime against the original under Pi) therefore fails at the first widget on 14 of 15
   scenarios (`tool-definitions` passes). The Pi trace is the oracle, and the port draws nothing in RPC mode.
4. **G4 PiG's request body escapes `<`, `>`, `&`** in tool-call arguments as `\u003c` where Pi does not, so the
   scenario for the reminder's tag stripping (`reminder-sanitized`) carries a newline only; the tag case is the twin
   `sanitizes task subjects so they cannot break out of the reminder block`.
5. **G5 The event bus is reachable only after the connection.** The original pings in its factory. The SDK returns
   `errBusNotConnected` before then. A first version pinged from a goroutine as soon as the connection existed; that
   **deadlocked the host** once `tintinweb-subagents` was loaded in the same host (found building the `pig-essentials`
   Binary: the reply is dispatched to an extension that has not finished loading; one extension per lane never shows
   it). The ping now waits for the first `session_start` (once), and is repeated from `subagents:ready` and
   `TaskExecute`. Load order still does not matter. Twin and tests: `TestPingWaitsForTheFirstSession`.
6. **G6 No `ui.custom` for a Go extension** (the settings panel).
7. **Stale context.** Retained contexts outlive their request; the spinner timer stops when `Context.Err()` is non-nil.
8. **Parallel tool calls finish in no fixed order in PiG.** Ten `TaskCreate` calls of one model turn run concurrently
   in the SDK's request goroutines, so their `tool_execution_end` events (and which call takes `#1`) vary from run to
   run; Pi runs the same synchronous tools in call order. A scenario with ten creates in one turn failed on about one
   run in three under load, so `parallel-create` became `sequential-create` (one call per turn) and `reminder-cap`
   creates its twelve tasks one per turn; both goldens were re-recorded from the original under Pi 1.0.3 (the host
   check of the original under PiG was skipped for them: under load it reports a `setWidget` event ahead of
   `tool_execution_end`, a second ordering race, in the *original* running on PiG). `TestConcurrentCreatesAreAllKept`
   covers the concurrency itself: ten simultaneous creates are all kept and numbered once.

## Run in PiG (tmux)

Not run for this port. The widget and the menus are exercised by the layer-1 twins and, for the menus, by the
Pi-recorded scenarios; a live interactive run of the widget is open.

## Piglet build

`piglets/pig-essentials` selects this Package. `npm run build:piglet -- pig-essentials` fuses it (see that
Piglet's README and the progress note for the build and cold-start numbers).
