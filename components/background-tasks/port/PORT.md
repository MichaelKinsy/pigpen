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
| 1 | Inventory, honest-refusal skeleton, settings reader, package metadata | this revision |
| 2a | Leaf modules and their twins: format, wake events, snapshots and restore, lifecycle, orphan watcher, resource control, probes, log writer and tail, lanes, persistence, matcher, activity | refused until then |
| 2b | The task runtime through the host: spawn, stop, wakes, restore, retention, auto-background; scenarios and mutations | refused until then |
| 3 | Widget, dashboard, renderers, shortcuts | refused until then |
| 4 | Process-tree cleanup on stop and shutdown on Linux and macOS (Windows by vet and reasoning), the performance table | |

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
| M15 | `activity.ts` | `bg_task.*` events to the broker at `globalThis[Symbol.for("kendex.pi.activity")]` (pi-session-bridge) | owner | the event builder is ported and twinned; publication has no target across the process boundary (see Gaps) | |
| M16 | `render.ts:acquirekendexModalLock`, `stacked-widget.ts`, `package-config.ts` | process-global `Symbol.for("kendex.pi.*")` registries (modal lock, mini-dashboard stack, project trust) | owner | each is a Go-process registry of this extension (see Gaps) | |
| M17 | `package.json` `pi.appendSystem`, `scripts/append-system.mjs`, `instructions.md` | the npm postinstall helper writes a marked block into `APPEND_SYSTEM.md` | port | the extension appends `instructions.md` to the system prompt unless the block is already there (slice 2b) | none |
| M18 | `settings.ts`, `package-config.ts` | settings: user and trusted-project files, 1 s memo, `PI_BG_TASK_DIR`, agent-directory `bin` on the task PATH | port | `config.go`, `settings.go` | none (done in slice 1) |
| M19 | `diagnostics.ts` | `PI_BG_TASK_DEBUG`, `PI_BG_TASK_DIAGNOSTICS`, `PI_BG_TASK_DIAGNOSTIC_LOG` | port | `diagnostics.go` | |
| M20 | `package-config.ts:installSettingsCacheRefresh` | memo cleared at `session_start`, `session_shutdown` and on `kendex:extension-settings-changed` | port | `extension.go` | settings cache cleared at session boundaries (done); the bus channel in slice 2b |

## Gaps: needs an SDK capability or an owner decision

- **Activity broker (M15).** The original publishes through a JavaScript global that another extension in the same Node
  process (pi-session-bridge) installs. A Go extension is another process; nothing can install or read that global.
  Decision needed: either accept that no `bg_task.*` activity is published, or define a `pi.events` channel for it (an invented
  protocol, so not done without approval). The event builder (`buildBackgroundTaskActivity`) is ported and twinned either way.
- **Shared registries (M16).** The mini-dashboard stack lets pi-tasks, orchestrators and agents widgets share one host
  widget. Across Node extensions it is a global; for Go extensions fused into one Binary a Go-process registry could be
  shared, but no other Pigpen extension uses it, so this package keeps its own registry and the same host widget keys
  (`kendex-mini-dashboard-stack-above` and `-below`). The modal lock is dropped with nothing to lock against.
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
| `tool.promptSnippet` | `registrations.ts:89` | `ToolDefinition.PromptSnippet` | the host does not yet list extension tools' snippets in PiG's system prompt (a host gap shared with in-process extensions); recorded when the scenarios run |

`pigeq gaps --go` on the port is run with each slice.
