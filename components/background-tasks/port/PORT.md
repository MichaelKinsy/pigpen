# Port record: @vanillagreen/pi-background-tasks

| Input | Identity |
|---|---|
| Original | `@vanillagreen/pi-background-tasks` 2.2.0 by vanillagreen, https://github.com/vanillagreencom/kendex (`pi-extensions/pi-background-tasks`), commit `bd611b212d2bec33aac5b62642b6aba21100d7f8`, MIT. Its `extensions/` tree at that commit is byte-identical to the npm tarball (`sha512-wIpmFABX1fPKODRducUXXu+Au4mK6TJeKj/8Kl2U924ntd7BkqY31iWgvcKmSAD7GxPtQAC8mtz+Vu9+R9MAMA==`); the tests come from the same commit. Based on `@ifi/pi-background-tasks` (MIT, Ifiok Jr.): `THIRD_PARTY_NOTICES.md` kept. |
| Oracle | Pi 1.3.0+1402973a, Node 24.19.0, the unmodified package at `port/oracle` (`typebox`, `@earendil-works/pi-ai`, `pi-tui` and `pi-coding-agent` come from Pi's own tree) |
| Target | PiG 0.5.0+1.3.0.1402973a (SDK `extensions/sdk`), go1.27 |
| Original's tests | 99 `test` titles in 62 files (`port/upstream-tests.json`; `bun test` reports 102 because three `test.skipIf(baseline === undefined)` / `describe.skipIf` cases are not in the ledger). Baseline of the original on a Mac under load: 91 pass, 7 skip, 4 fail (30 s timeouts of must-fail source-mutation children in `restored-exit.test.ts`; re-run on the lane host for the record in a later slice) |
| Kind | 1: a Pi TypeScript extension, no CLI built-in, no provider |

## Slices

| Slice | Content | State |
|---|---|---|
| 1 | Inventory, honest-refusal skeleton, settings reader, package metadata | merged-ready (`parity/background-tasks-1`) |
| 2a | Leaf modules and their twins: format, wake events, snapshots and restore, lifecycle, orphan watcher, resource control, probes, log writer and tail, lanes, persistence, matcher, activity | this revision: 58 exact twins, 1 named skip (below); the tool, commands and shortcuts stay refused until 2b |
| 2b | The task runtime through the host: spawn, stop, wakes, restore, retention, auto-background; twins and mutations | this revision (0.2.0): 77 exact twins, 10 named skips; scenarios wait for slice 3 (see Slice 2b evidence) |
| 3 | Widget, dashboard, renderers, shortcuts | this revision: in the tree with twins and mutants; `tool-definitions`, `list-log-stop` and `fork-restore` equal to Pi; four scenarios pending (see Slice 3 evidence) |
| 4 | Process-tree cleanup on stop and shutdown on Linux and macOS (Windows by vet and reasoning), the performance table | cleanup evidence on Linux and macOS (below); performance table (below) |

## Inventory

Every behavior of the original, with its classification: **port** (Go on the public SDK), **SDK** (needs a PiG SDK
capability; reported to the Tern lead with the Pi API it mirrors), **owner** (needs an owner decision). The refusal
column is what a user sees until its slice lands (`refusal(feature)` in `refuse.go`).

| ID | Original (file:line) | Behavior | Class | Go | Refused as |
|---|---|---|---|---|---|
| M1 | `registrations.ts:42-197` | `bg_status` and `bg_task` tools: schemas, descriptions, `promptSnippet`, `promptGuidelines`, `renderShell: "self"`, results with `details` | port | `tooldefs.json` dumped from the unmodified package; `extension.go` | `the bg_task tool (action X)` |
| M2 | `background-tasks.ts:809-958` | `spawnTask`: trimmed command, cwd, id counter, lane directory and log file, shell from `getShellConfig`, `detached` process group, `taskEnv`, pipes, pid | port | `spawn.go`, `shell.go` | same |
| M3 | `background-tasks.ts:834-1002`, `log-writer.ts`, `format.ts` | chunk decoding, in-memory buffer cap, the batched log writer (flush window, write-now size, pending cap, producer hold, stall deadline, failure markers), pause and resume of the pipes | port | `logwriter.go`, `task.go` | same |
| M4 | `background-tasks.ts:419-571`, `wake-events.ts`, `format.ts:79-125` | output wakes: settle timer, matcher, notify modes, dedupe hash, wake budget and its notice, matcher-timeout notice, exit wakes (`followUp`), voiding on stop, wake records | port | `wakes.go`, `matcher.go` | same |
| M5 | `background-tasks.ts:590-770`, `lifecycle.ts` | stop, timeout, force-kill grace, shutdown kill, `terminationReason`, exit wake after the log flush, release of the child and the output | port | `lifecycle.go`, `proc_unix.go`, `proc_windows.go` | same |
| M6 | `resource-control.ts`, `probes.ts` | `nice`/`ionice`/`systemd-run --user` plans, mode resolution, helper lookup memo, user-manager reachability, unit stop and liveness, Windows warning | port | `resource.go`, `probes.go` | same |
| M7 | `persistence.ts`, `snapshot.ts`, `tool-result-details.ts`, `orphan-watcher.ts` | session entries (64 KiB cap, bounded manifest), the sidecar `state.json`, restore from the branch and tool-result details, process identity (`/proc`, `ps`), missed-exit replay, 30 s orphan watcher | port | `persistence.go`, `snapshot.go`, `orphan.go` | same |
| M8 | `scripts/lane-retention.ts`, `background-tasks.ts:216-262` | lane directories, the prune at `session_start` (5 days, gone worktrees, own directories only), the 50 finished-task bound | port | `lanes.go` | same |
| M9 | `background-tasks.ts:1044-1086`, `auto-background.ts` | `tool_call` bash rewriting to the acknowledgement, `user_bash` result, the forced window (`/bg next`, `alt+.`) | port | `autobg.go`, `extension.go` | bash command refused (see below) |
| M10 | `background-tasks.ts:352-405`, `stacked-widget.ts`, `widget-visibility.ts` | the mini-dashboard widget in the shared stack, placement, compact/expanded/hidden, finished-task expiry timer, clamp above the editor | port (kit view; slice 3) | `widget.go` | `the background task widget` |
| M11 | `dashboard.ts`, `render.ts` | the dashboard overlay: panes, scrolling, follow, `s` stop, `c` clear, `x` expand, async log tail with a cache | port (RemoteComponent through `ctx.Custom`; slice 3) | `dashboard.go` | `the background task dashboard` |
| M12 | `render.ts`, `registrations.ts` | tool renderers (`renderCall` empty, `renderResult`) and the `kendex-background-tasks:event` message renderer | port (`SetToolRenderers`, `MessageViewRenderer`; slice 3) | `render.go` | default card |
| M13 | `registrations.ts:201-322` | `/bg` and `/bg:*` commands with argument completions; four shortcuts | port | `commands.go` | notification |
| M14 | `package.json` `kendex.extensionManager` | the settings declaration for the `/extensions:settings` tab | n/a | carried unchanged in `package.json`; the tab belongs to `pi-extension-manager`, a separate extension | none |
| M15 | `activity.ts` | `bg_task.*` events to the broker at `globalThis[Symbol.for("kendex.pi.activity")]` (pi-session-bridge) | owner | the event builder is ported and twinned; publication has no target across the process boundary (see the table under Gaps) | |
| M16 | `render.ts:acquirekendexModalLock`, `stacked-widget.ts`, `package-config.ts` | process-global `Symbol.for("kendex.pi.*")` registries (modal lock, mini-dashboard stack, project trust) | owner | each is a Go-process registry of this extension (see the table under Gaps) | |
| M17 | `package.json` `pi.appendSystem`, `scripts/append-system.mjs`, `instructions.md` | the npm postinstall helper writes a marked block into `APPEND_SYSTEM.md` | port | `before_agent_start` puts the same marked block into the run's `appendSystemPrompt` unless the block is already there; the manifest declares `pi.appendSystem` as the original's does (Pi and PiG ignore the key; a Go package has no install hook). `TestInstructionsAreWhatTheOriginalsInstallStepWrites` runs the original's own script and requires byte-equal text, with and without other rules in the file, and no doubling | none |
| M18 | `settings.ts`, `package-config.ts` | settings: user and trusted-project files, 1 s memo, `PI_BG_TASK_DIR`, agent-directory `bin` on the task PATH | port | `config.go`, `settings.go` | none (done in slice 1) |
| M19 | `diagnostics.ts` | `PI_BG_TASK_DEBUG`, `PI_BG_TASK_DIAGNOSTICS`, `PI_BG_TASK_DIAGNOSTIC_LOG` | port | `diagnostics.go` | |
| M20 | `package-config.ts:installSettingsCacheRefresh` | memo cleared at `session_start`, `session_shutdown` and on `kendex:extension-settings-changed` | port | `extension.go` | settings cache cleared at session boundaries (done); the bus channel in slice 2b |

## Gaps: needs an SDK capability or an owner decision

- **Process-global interop (M15, M16): the facts for the owner.** The original reads or writes five `Symbol.for` globals.
  A Go extension runs in its own process (or fused into PiG's), so it can neither see nor set a JavaScript global; what each
  one drives, observably:

  | Symbol | Original use | What a user sees when it works | Go port today |
  |---|---|---|---|
  | `kendex.background-tasks.installed` (`constants.ts:7`) | set on the `pi` object itself as a double-registration guard | nothing cross-extension; a second load of the same `pi` object registers nothing | n/a (each factory call returns a new extension) |
  | `kendex.pi.activity` (`activity.ts:36`) | a broker object that pi-session-bridge installs; `publishBackgroundTaskStarted`/`publishBackgroundTaskActivity` call `broker.publish(event)` on task start, output match and exit (`bg_task.started`, `bg_task.output_matched`, `bg_task.completed`/`failed`/`timed_out`/`stopped`, with severity, importance, refs `bg_task_id`, details) | nothing in the chat (README: "these do not appear as chat messages"); with pi-session-bridge loaded, **external observers** of that bridge receive structured task events | not published; `buildBackgroundTaskActivity` is ported and twinned. Decision: accept, or define a `pi.events` channel (invented protocol) |
  | `kendex.pi.mini-dashboard-stack` (`stacked-widget.ts:42`) | one registry of widget entries ranked ORCH 10, TASKS 20, AGENTS 30, BACKGROUND_TASKS 40; sibling kendex extensions (orchestrator, pi-tasks, agents) add entries and **one** host widget per placement (`kendex-mini-dashboard-stack-above`, `-below`) draws them in rank order, sequence order for equal ranks | with siblings loaded, their mini-dashboards and this one share one framed region in rank order and a sibling's removal re-lays the rest out; alone, only this entry draws | the port keeps its own registry with the same host keys, so alone it is identical. **With a Node sibling loaded in PiG's Node runtime, both write the same host widget key and the later `setWidget` replaces the earlier** (no cross-process stack). Decision: keep the shared key (matches the original when alone, clobbers a sibling) or use a port-specific key (no clobbering, no stacking) |
  | `kendex.pi.modal-lock` (`constants.ts:10`, `render.ts:54`) | a depth counter raised while the dashboard overlay is open | sibling kendex extensions that read `depth > 0` hold back their own modals or notifications while the dashboard is up | dropped: nothing to lock against |
  | `kendex.pi.project-trust` (`package-config.ts:136`) | the trust answer recorded by whichever kendex package sees Pi's `isProjectTrusted()` first, read by every package | which packages read a project's `.pi/settings.json` | each process records its own answer from `ctx.IsProjectTrusted()` on the events the original records on; same file gating |

- **`PI_CODING_AGENT_DIR`.** The original resolves the user directory from `PI_CODING_AGENT_DIR` (when root-anchored) or
  `~/.pi/agent`. The port follows PiG's identity rule (divergence D2, as the tintinweb-tasks port; owner decision
  2026-10-20): PiG's agent directory (`sdk.AgentDir`, which honors `PIG_CODING_AGENT_DIR`, `PIG_HOME` and
  `PIG_USE_PI_DIRS=1` for Pi's). The sidecar state lives in `<agent dir>/kendex/sessions/<session>/pi-background-tasks/`.
  The project directory stays `.pi`, as in the original.
- **Install-time script (M17).** PiG packages have no postinstall; the extension appends the instructions itself.

## Deliberate differences (so far)

1. **Agent directory** (above).
2. **Auto-backgrounding in this release is a refusal, not a diversion** (slice 1 only): the matching bash command is
   blocked with the refusal text instead of running in the foreground.

## SDK gap check

`pigeq gaps --ts port/oracle/extensions --surface <PiG>/docs/extension-sdk-surface.md` (PiG 0.5.0+1.3.0.1402973a): **7 gaps, 0 blocking**,
all `PARTIAL` stand-ins:

| Gap | Where | Stand-in | Effect |
|---|---|---|---|
| `ctx.ui.custom` and its `overlayOptions` | `dashboard.ts:57` | `Context.Custom` with a `RemoteComponent` and `RemoteOverlayOptions` | the overlay's `width: 96`, `maxHeight: "75%"`, `anchor: "center"` are serializable fields; slice 3 |
| `ctx.ui.setWidget` with a factory | `stacked-widget.ts:115,119` | a `kit` view through `Context.SetWidget` | the widget is rows re-laid out on resize (as in tintinweb-tasks); slice 3 |
| `pi.registerMessageRenderer` | `background-tasks.ts:991` | `Extension.MessageViewRenderer` | lines, not a Component; slice 3 |
| `tool.renderCall`, `tool.renderResult` | `registrations.ts:77-78,168-169` | `ToolDefinition.RenderCall`/`RenderResult` (or `SetToolRenderers`) | lines, not a Component; slice 3 |
| `tool.promptSnippet` | `registrations.ts:89` | `ToolDefinition.PromptSnippet` | none: the gap note is stale. The `tool-definitions` golden (Pi) and the Go port (PiG) both carry `- bg_task: Spawn, inspect, and stop explicit non-blocking background shell tasks.` in the system prompt, and the original under PiG's Node runtime matches Pi |

`pigeq gaps --go` on the port is run with each slice.

## Slice 2a evidence (leaf modules)

- `pigeq twins check --ledger port/upstream-tests.json --go extensions/pi-background-tasks --deferred port/slices.json`:
  **58 exact twins, 1 skipped** (the log-tail must-fail row, tied to the seven `logtail-*` mutations of `port/mutations.json`).
  Cases of files whose runtime is not ported are listed as `DEFERRED` by `port/slices.json` (slice 2b: the task runtime; slice 3: widget,
  dashboard). `resource-systemd-probe` is a `skipIf` case the ledger does not list; its Go test is a plain subtest.
- Mutations (`pigeq mutate --unit-only`, unit twins as the judge): 7 of 7 killed (`logtail-*`, all by `TestTwinLogTail`).
- Runs on xd670 (tern-ci:v3, Linux, CPUs 64-95,160-191): `go test -race -count=12 ./...` green; `GOOS=linux|windows|darwin go vet ./...` clean. The
  Linux-only twins (`resource-systemd-probe`, `identity-probe-live`) run there, not skipped.

### Findings behind the two red twins of the work-in-progress branch

- **Log writer visit order.** A flush window and a drain visit the files in the order they were first appended (a JavaScript `Map`). The Go port
  held them in a `map`, whose iteration order is random, so the writes of two files came out in either order (the twin failed about one run in
  two, not only inside the full suite). `logWriter` now keeps an insertion-ordered list beside the map.
- **Log tail admission twin.** The original starts nine reads in one tick, so they reach the shared admission in call order. Goroutines do not;
  the twin now starts each read only once the one before holds a slot or waits for one (`admissions()`), and the duplicate read only once it
  waits on its task's read (`joins`, a counter in the reader). No expectation changed.
- **Systemd probe twin under load.** The stand-in `systemctl`/`systemd-run` were the race-instrumented test binary re-executed; its start-up
  alone can pass the one-second probe timeout on a loaded host. They are now `#!/bin/sh` scripts, as light as the original's Node stubs.


## Slice 2b evidence (task runtime)

- Twins: `pigeq twins check` reports 77 exact twins and 10 named skips, the remaining deferred files being slice 3 (widget, dashboard,
  benchmark, active context). New twin files: `twin_write_unix_test.go` (write-path, 9), `twin_exit_unix_test.go` (restored-exit,
  exit-reads), `twin_spawn_unix_test.go` (spawn-hardening, stop-content, wake-defaults, resource-stop, 10 rows), `twin_retention_unix_test.go`,
  `twin_matcher_unix_test.go`, `twin_toolbounds_test.go` (log tool bounds, orphan effects). The original fakes the child and the clock;
  the port runs real children (`cat` on a fifo, a flag-file loop) and drives its windows through a `Rig` (hand-fired timers, held log
  appends and opens, counted signals and systemctl calls), the seams being `export_test.go` only.
- Mutations (`pigeq mutate --unit-only`): 16 of 16 killed, the nine new ones being `restored-exit-*` (5) and `matcher-*` (4). Two are
  killed by the 3 minute test timeout (`matcher-timeout-rethrown`, `matcher-deadline-removed`), which is a kill, not a clean assertion.
  The must-fail titles of the original are named skips tied to these mutants.
- `-race -count=25` exit 137 was the docker-kill incident of the lane host, not a leak. A real flake in 2a was found and fixed: the 1 MB
  pathological regex took 321 ms under -race because regexp2 checks its timeout coarsely; the caller now enforces the 25 ms deadline, and the
  abandoned match ends on regexp2's own timeout (test: goroutine count returns to baseline).
- Deviations of the twins (each is in the test): the log write finishes on its own goroutine, so a stall deadline's clear and the drain's
  window clear are compared as a set; a pipe delivers 1 MB in pieces, so the output wakes before the last piece are "rescheduled" drops;
  the SIGKILL escalation line is counted by the stalled-log marker; `restore` probes count the identity and unit probes, not the systemctl
  availability probe (twinned in `TestTwinSystemdUnitProbe`); the Windows spawn row and the widget counts of the exit-held row wait for the
  Windows host and slice 3.
- **Open items of 2b, closed in slice 3.** (1) `exit-reads` row `mixed: delivered` is no longer skipped (see Slice 3 evidence: the original orders its
  tool read and its exit read deterministically, and the port now does the same). (2) The `tool-definitions` gate: with the installed state in
  `APPEND_SYSTEM.md` the original under PiG and under Pi agree on the whole `<addendum>`; the only difference is the `<docs>` block (D22), masked by a
  scenario rule that stops at `</docs>` (see below). Not a PiG bug, so no B-number.

## Slice 3 evidence (widget, dashboard, renderers, shortcuts)

- **Ported:** `widget.go` (visibility, expiry timer, the stack of mini-dashboards, `syncWidget` on every `refreshUi` and on `session_start`,
  `before_agent_start`, `session_tree`, `session_compact`; cleared at `session_shutdown`), `dashboard.go` (the overlay as a `RemoteComponent` through `ctx.Custom`, panes,
  scroll, follow, `s`, `c`, `x`, a read of the log tail that never blocks a frame, the command layout cache), `render.go` (frames, tool renderers, the
  `kendex-background-tasks:event` message renderer), `glyphs.go`, and the shortcuts and `/bg` entry points that open them. The shared pi-tui subset
  (`components/pitui`, `widthx`) comes in through `go.work` as in ask-user-question and goal.
- **Widget as the host sees it.** A kit view of rows laid out at the terminal's width (the kit form of the factory the original registers; it is laid
  out again on a width change, `OnWidthChange`), not cut at ten rows. In Pi's RPC mode a component widget is not rendered and the original's stack never
  marks itself active, so it sends nothing there, not even its removal; the port sends nothing in RPC mode either. The original under PiG's Node runtime
  still emits `widgetLines` there (core bug B2), so the host check of every scenario with a widget fails and the goldens are recorded with the host
  check ignored, as the tintinweb-tasks lane does.
- **Twins:** `widget-visibility` (2), `widget-expiry` (2), `active-context` (2), `widget-lifecycle` (the ten scenarios, on a pinned clock and the rig's timers, the
  stack's entries read through a test seam), `dashboard` (the cache and the late-read guard; the two must-fail titles are named skips tied to the mutants
  `dashboard-command-cache-bypassed` and `dashboard-selected-task-guard-dropped`), `component-benchmark` (a 50 MB log, 30+10+10+10+10+1+1 frames: opens,
  reads, bytes and command wraps per phase; its must-fail title is a named skip tied to the same cache mutant). Seven new mutants: the two above and `widget-expiry-*`,
  `widget-toggle-forgets-mode`, `widget-hidden-renders`. The original's twelve fixtures run with `showWidget: false`, so the rig's default config sets it; the widget twins turn it on.
- **`mixed: delivered` of exit-reads.** The original is single-threaded: `finalize` registers the task's exit read (it owns the task's tail and its place in the
  admission queue) in the same tick as the child's `close`, and a tool read of the same task started afterwards joins that read and opens the log once more only after
  it. The port started the exit read on a new goroutine, so the tool read could win. Now `taskOutputReader.start` decides the owner and the queue position
  synchronously, in call order, under the runtime lock (exit wake, output wake, tool read, dashboard read), and a slot of the shared limit is handed to the first
  queued read when one finishes (FIFO, as the original's queue). The twin row asserts the order (no second open of the task's log before its exit is delivered); the test hook releases
  held opens in arrival order. The mutant `exit-read-started-later` restores the old order. `Rig` opens are counted, not timed.
- **Scenarios (Pi against the port, pigeq 0.4.1 = N8 branch + main 101eaf99, rule N8 for the numbers the extension makes up):** `tool-definitions` (installed state),
  `list-log-stop` and `fork-restore` are equal to the original under Pi (`port: pig-go == pi-ts`, 10, 17 and 35 events). Four are not yet:
  `spawn-exit-wake` (the exit wake of a task that exits at once reaches the host before `agent_end` in the port and after it in Pi, where the identity probe `ps -o lstart=,comm=`
  of the gone pid also runs; the port reads `/proc`), `output-match-wake` (a difference inside the wake message, not yet isolated), `bg-commands` (the `nice`/`ionice` exec shim
  event lands two events later, and the exit wake of the stopped task starts its turn in step `03-stop` instead of `04-list-after-stop`) and `auto-background`
  (`procIdent.comm` of a `tail -f` task is `bash` or `tail` depending on whether the shell has exec'd yet when the identity is read). They stay in `port/scenarios-slice3/` so the gates stay green.
- **`<addendum>` and the tool-definitions gate.** The scenario holds the installed block in `APPEND_SYSTEM.md` (what the original's postinstall leaves). `pigeq lane` of the original under Pi and
  under PiG gives the same 3634 bytes from `<addendum>` to the end of the prompt, and the port adds nothing of its own. The `system` delta of the harness reaches back to the tool snippet once an addendum
  exists, so the `<docs>` block (D22: PiG names its docs bundle where Pi names its source tree) falls inside it; the scenario's rule `(?s)<docs>\n(?:PiG|Pi) documentation.*?(?:</docs>|$)` masks that block and only it.

## Slice 4 evidence (process cleanup, performance)

- `TestProcessTreeCleanup` (`cleanup_unix_test.go`): a task starts a tree of three descendants (two below a subshell); `stop` and `session_shutdown` each end the whole tree, also when the
  descendants ignore SIGTERM (the SIGKILL after `forceKillGraceMs`). Linux (xd670, tern-ci:v3): passes. macOS (Darwin arm64): the test binary was cross-compiled on xd670
  (`GOOS=darwin GOARCH=arm64 go test -c`), only that binary was copied to a scratch directory of the Mac, run three times (12 of 12 subtests pass) and deleted. Windows: `taskkill /T /F` by reasoning and `GOOS=windows go vet`.
- **Performance, chatty tasks** (`port/perf/perf-chatty.json`: four tasks of 3,000,000 lines each spawned in one turn, `showWidget` off, resource control off). xd670, tern-ci:v3, cores 0-63 under the perf lock,
  three runs each of one `pigeq lane -host pig` (the whole lane: pigeq, pig, the extension, the scripted model, shutdown). The original runs under PiG's Node runtime, the port as a Go extension. `peak RSS` is the
  largest single process the lane started (`RUSAGE_CHILDREN`), not a sum; the port's lane also includes a `go build` of the extension from a warm cache, so its CPU is an upper bound.

  | | wall (s) | CPU user+sys (s) | peak RSS of one process (MB) |
  |---|---|---|---|
  | original (Node runtime) | 5.26, 5.25, 5.54 | 2.94, 2.99, 2.70 | 119, 118, 116 |
  | port (Go) | 3.05, 3.06, 3.06 | 3.48, 3.12, 2.95 | 152, 142, 149 |

  Read it as: the port finishes the lane about 2 s sooner; CPU is the same within the noise of the build step; the largest process is bigger by about 30 MB (the Go host side plus the `go build`),
  which a per-process sampling run would separate. That separation was not done.
