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
| 3 | Widget, dashboard, renderers, shortcuts | this revision: in the tree with twins and mutants; six of the seven scenarios equal to Pi under PiG main (`bg-commands` waits for core bug B5; see Slice 3 evidence) |
| 4 | Process-tree cleanup on stop and shutdown on Linux and macOS (Windows by vet and reasoning), the performance table | cleanup evidence on Linux and macOS; per-process performance table (below) |

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
- **Scenarios against Pi** (original under Pi 1.3.0+1402973a against the port under PiG, `pigeq-merge run`, host check skipped for B2; rule N8 for the numbers the extension makes up).
  Four PiG builds were tried: `orig` = the v0.5.0 tag the earlier runs used; `main` = origin/main 3ee123c6 (the train); `D5` = `ordering/d5-timer-calls` 7109a92a; `comb` = D5 merged with `fix/custom-message-first-run-system-prompt` (B5; the merge conflicts only in `test/parity/normalization-inventory.json`, which is not code).

  | Scenario | orig | main | D5 | comb (D5 + B5) | Cause of a difference |
  |---|---|---|---|---|---|
  | `tool-definitions` | equal (10) | equal | equal | equal | |
  | `list-log-stop` | equal (17) | equal | equal | equal | |
  | `fork-restore` | equal (35) | equal | equal | equal | |
  | `spawn-exit-wake` | equal (24) | equal | equal | equal | |
  | `output-match-wake` | equal (24) | equal | equal | equal | |
  | `auto-background` | equal (36) | equal | equal | equal | |
  | `bg-commands` (pending, `port/scenarios-slice3/`) | differs | differs | differs | equal (19) | B5: the first run of the session is started by an extension's `sendMessage`; PiG sends it without the system prompt (`system.inserted` empty, `removed` the whole base prompt). Nothing before that request differs on any of the four |

  `check` against the recorded goldens passes six of six on `orig` and on `main` (the `bg-commands` golden waits for B5). What the earlier round called differences was, item by item:
  - **`output-match-wake`:** the wake message carries `details.deliveredAt`, a clock reading the scenario did not list as volatile. The generator now lists it (with the other clock and pid fields of listed tasks); nothing in the port changed for it.
  - **`auto-background`:** `procIdent.comm` of `tail -f` read `bash` in the port and `tail` in the original. See "When the original reads the identity" below; the port now reads at the same point. `procIdent.pid` and `procIdent.startToken` (the kernel's start time in ticks, kept as a string; the rule matches a string of seven or more digits and nothing else) are numbers the kernel makes up, listed as volatile; `comm` is compared.
  - **`spawn-exit-wake`:** the scenario as first written (`printf 'hello\n'`, the task ends within a millisecond of its spawn) is a race inside Pi itself. Eight lanes of the original under Pi gave eight different event orders: whether the exit wake joins the running turn or starts its own, and where the `ps -o lstart=,comm= -p <pid>` probe (the original's identity read hits ESRCH on the pid that is being reaped and falls through to `ps`) and its `exec_end` fall among `agent_end`, `agent_start` and the wake's `message_end`; eight lanes of the port under PiG gave one order. Such a scenario cannot be equal to a moving target, so the task now ends 0.5 s after its spawn (`sleep 0.5; printf 'hello\n'`, the step waits 1.5 s): the wake always starts a turn of its own after `agent_end`, and that order is equal on every build. The immediate-exit orders are pinned by the twins (`wake-defaults`, `restored-exit`, `exit-reads`), not by a scenario.
  - **`bg-commands`:** the earlier difference in the order of the `nice`/`ionice` exec shim event against the `ui` notification is two processes writing to the harness (README, Limits: "Parallel exec calls arrive in a nondeterministic order"): in five lanes of each side, Pi gave two orders (1 and 4) and the port three (3, 1, 1), the same orders on both. The scenario now runs with resource control off; the `nice`/`ionice`/`systemd-run` plans are pinned by the resource twins (`resource-control`, `resource-stop`, `systemd-probe`). The earlier "the stopped task's exit-wake turn lands one step early" is gone on every build: it was the `nice` shim path, not a port difference.
- **D5 (`ordering/d5-timer-calls`): the port deadlocked on it; fixed in the port.** Against D5 the lane of every scenario with a wake timed out at the first prompt (`timed out after 1m0s waiting for eq-2`, no stderr). A goroutine dump of the extension showed the `bg_task` handler in `settle()` waiting for the outbox, and the outbox's worker in `AppendEntry` waiting for the host. D5's SDK marks every host call made through the context of a request that has already returned (`CallPayload.Timer`) and the host holds it until Pi's event loop would turn (the loop turns at a model response, a built-in tool's run, a dialog, or the end of `agent_settled`). The port made the persist call of a tool handler through the context it keeps from an earlier request (`rt.last`, the original's `activeCtx`), which is such a request; the host held it, the handler waited for it, and the host waited for the handler to return. Now (`runtime.go`): a handler's host calls go through a request that is still running when the call is made (`rt.live`, registered by every handler of the SDK), and the host calls of a loop job (a wake, a persist, a refresh) go through a second queue (`timerOut`) that no handler waits for. Under D5 those calls are held until the loop turns, which is where Pi runs them. The change is invisible to older hosts (the unit suite and the six scenarios are equal before and after).
- **When the original reads the identity.** `spawnTask` calls `defaultReadProcessIdentity(pid)` in its synchronous part, but `readFile("/proc/<pid>/stat")` is four thread-pool calls (open, fstat, read, close), each carried on by one turn of Node's event loop, and the loop turns only after the handler's synchronous work and its result have gone back to Pi. Measured on xd670 (the original with the call timed, twelve `tail -f` spawns under Pi): the read resolved 2.1 ms to 22 ms after the spawn (median 16 ms; 37 ms to 51 ms for six `printf` spawns), after the shell had exec'd `tail`: `comm` was `tail` in 12 of 12, although `bash` was still the name at the spawn in 5 of 12. In a bare Node script with no handler to finish (40 spawns of the same shell) the read found `tail` 22 times and `bash` 18 times: the value is a race against the shell's `exec`, which the original wins because its loop is busy. The port read at once on a goroutine (`bash` in 3 of 15 lanes; Pi: `tail` in 15 of 15). It now starts the read when the handler that spawned the task returns, and no earlier than `identityReadFloor` (10 ms, below the smallest delay seen under Pi) after the spawn: `tail` in 25 of 25 lanes. A task that ends before the read gets `gone` (ENOENT) in the port; in Pi the same read ends in ESRCH and then in the `ps` probe about as often (see `spawn-exit-wake` above), which no scenario pins.
- **`<addendum>` and the tool-definitions gate.** The scenario holds the installed block in `APPEND_SYSTEM.md` (what the original's postinstall leaves). `pigeq lane` of the original under Pi and
  under PiG gives the same 3634 bytes from `<addendum>` to the end of the prompt, and the port adds nothing of its own. The `system` delta of the harness reaches back to the tool snippet once an addendum
  exists, so the `<docs>` block (D22: PiG names its docs bundle where Pi names its source tree) falls inside it; the scenario's rule `(?s)<docs>\n(?:PiG|Pi) documentation.*?(?:</docs>|$)` masks that block and only it.

## Slice 4 evidence (process cleanup, performance)

- `TestProcessTreeCleanup` (`cleanup_unix_test.go`): a task starts a tree of three descendants (two below a subshell); `stop` and `session_shutdown` each end the whole tree, also when the
  descendants ignore SIGTERM (the SIGKILL after `forceKillGraceMs`). Linux (xd670, tern-ci:v3): passes. macOS (Darwin arm64): the test binary was cross-compiled on xd670
  (`GOOS=darwin GOARCH=arm64 go test -c`), only that binary was copied to a scratch directory of the Mac, run three times (12 of 12 subtests pass) and deleted. Windows: `taskkill /T /F` by reasoning and `GOOS=windows go vet`.
- **Performance, chatty tasks, per process** (`port/perf/perf-chatty.json`: four tasks of `seq 1 3000000` spawned in one turn, `showWidget` off, resource control off). xd670, tern-ci:v3, cores 0-63 under the perf lock,
  PiG main 3ee123c6. One `pigeq lane -host pig` per run, five runs per side after one unmeasured warm-up of each, original and port alternating. A sampler reads `/proc/<pid>/stat` and `status`
  of every descendant of the lane every 20 ms (CPU = utime + stime at its last sample; RSS = VmRSS). The original runs as a Node extension in PiG's Node runtime (one `node` process); the
  port is a Go extension (one `runner` process). The `go build` of the lane's shim and of the extension (`go`, `compile`, `link`; 0.4-0.6 s CPU for the original's shim, 1.2-1.5 s for the shim plus the extension) is
  excluded. Per run:

  | | run | wall (s) | extension process CPU (s) | extension peak RSS (MB) | PiG host CPU (s) | PiG host peak RSS (MB) | task children CPU (s) |
  |---|---|---|---|---|---|---|---|
  | original (Node) | 1 | 4.05 | 3.81 | 464 | 0.97 | 136 | 0.15 |
  | | 2 | 4.38 | 4.25 | 466 | 1.25 | 136 | 0.13 |
  | | 3 | 4.46 | 4.20 | 473 | 1.19 | 136 | 0.12 |
  | | 4 | 4.27 | 3.93 | 469 | 1.40 | 136 | 0.15 |
  | | 5 | 4.12 | 3.84 | 467 | 1.21 | 138 | 0.13 |
  | port (Go) | 1 | 2.51 | 1.53 | 39 | 0.14 | 70 | 0.00 |
  | | 2 | 2.62 | 1.56 | 44 | 0.15 | 73 | 0.00 |
  | | 3 | 2.66 | 1.52 | 41 | 0.16 | 69 | 0.00 |
  | | 4 | 2.59 | 1.55 | 40 | 0.16 | 70 | 0.00 |
  | | 5 | 2.52 | 1.62 | 39 | 0.14 | 68 | 0.02 |
  | **median, original** | | 4.27 | 3.93 | 467 | 1.21 | 136 | 0.13 |
  | **median, port** | | 2.59 | 1.55 | 40 | 0.15 | 70 | 0.00 |

  Over time (run 1, the extension process; CPU in percent of one core per half second, RSS at the window's end): original 150%, 148%, 134%, 72%, 110%, 116% and 134, 197, 262, 333, 397, 432 MB;
  port 82%, 128% and 33, 33 MB. In the first 3 s the original's resident set grows by about 100 MB per second while the tasks write; the port's stays flat. The first
  attempt at this table (whole-lane `RUSAGE_CHILDREN`, 118 MB against 148 MB "largest process") understated the original by a factor of four; I did not establish why `RUSAGE_CHILDREN` missed the Node process (one possibility: a process still running when the lane ends is not counted until it is waited for). The extension process of the port uses 39% of the original's CPU and 9% of its memory; with the host
  side (the Node runtime's protocol work shows as 1.21 s of host CPU against 0.15 s) the lane costs 5.1 s of CPU against 1.7 s.

## Reliability (slice 4)

All on xd670, tern-ci:v3, cores 64-191, `nice -n 10`, `-p 8 -parallel 8`.

- **`go test -race -count=1 ./...`** over the whole module: green on the final head (246 s; 275 s on an earlier head of this round); `go test -count=1 ./...` 44 s; `go vet` clean for linux, windows and darwin; `pigeq twins check`: **86 exact twins, 13 skipped** (the thirteen must-fail titles, each tied to a mutant); `package validate`: valid.
- **The `mixed: delivered` row of exit-reads, repeated.** `go test -count=50 -run 'TestTwinExitReads/.*/mixed'` with 64 busy loops (`while :; do :; done`) in the container: the first batch of 50 had one failed run (I did not keep its log and could not tell its message), the next five batches of 50 gave 250 of 250.
  With 128 busy loops (one per CPU of the container) the row took 10-24 s a run and failed 2 of 98 runs with `timed out waiting for the first reads`, the 20 s bound of the test helper `eventually`; no assertion about the order of the reads or of the deliveries failed in
  those runs. The helper's bound is a hang detector, so it is now 60 s, and the same load gave 100 of 100. The race-instrumented row under 128 busy loops took about three minutes a run; that
  batch was stopped after 9 runs (all passed) and not repeated.
- **Mutants:** `pigeq mutate --unit-only` on the whole of `port/mutations.json`: **24 of 24 killed** (the 24th, `logtail-queued-validity`, which did not build in the previous run, is killed by `TestTwinLogTail`). `matcher-deadline-removed` is still killed by the 3 minute test timeout and
  `restored-exit-queue-unprotected` by a 60 s wait, which are kills, not clean assertions.
