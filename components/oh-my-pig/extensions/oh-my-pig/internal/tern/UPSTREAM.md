# Upstream Tracking (`internal/tern`)

- **Upstream release**: `v19.1.1` (`31ffee513f`)
- **Upstream files followed**:
  - `packages/tui/src/terminal-capabilities.ts` (`detectTerminalId`, `NotifyProtocol`, `isNotificationSuppressed`, `notificationToLine`)
  - `packages/tui/src/terminal.ts` (`isTernPane`)
  - `packages/utils/src/env.ts` (`isWsl`)
  - `packages/coding-agent/src/tools/browser/tern/kind.ts` (`resolveTernPane`)
  - `packages/coding-agent/src/tools/browser/terminal-owner.ts` (`TERMINAL_OWNER_ENV`, `claimTerminal`)
  - `packages/coding-agent/src/tools/tern-media.ts` (`ternShowsMedia`, `TERN_MEDIA_EXTENSIONS`, `TERN_MODEL_GZ_EXTENSIONS`)
  - `packages/coding-agent/src/tools/read.ts` (`mediaPath` attachment on whole-file reads, `:2611-2615`, standardized on macOS via `dirs.StandardizeMacOSPath` matching `packages/utils/src/dirs.ts:148-158`)
  - `packages/coding-agent/src/tools/read-selector.ts` (`parseSel` for `:raw` and `:img`)
  - `packages/coding-agent/src/utils/title-card.ts` (`formatCardTitle`, `splitCardTitle`, `keepTitleCard`, `parseCardTitleReply`, `parseCardReply`, `isCardIcon`)
  - `packages/coding-agent/src/tiny/text.ts` (`normalizeGeneratedTitle`, `isNoTitleSentinel`, `reconcileTitleCasing`)
  - `packages/coding-agent/src/utils/title-generator.ts` (`buildNativeTerminalTitle`, `buildTerminalTitleWithState`, `sanitizeTerminalTitlePart`, `initTerminalTitleState`, `disposeTerminalTitleState`, `setTerminalTitleState`, `setTerminalTitleStateEnabled`, `setTerminalTitleSpinnerStyle`, `setTerminalTitleSpinnerInterval`, `setTerminalTitlePullRequest`)
  - `packages/coding-agent/src/utils/run-status.ts` (`shownStatus`, `holdBlockedStatus`)
  - `packages/coding-agent/src/modes/controllers/event-controller.ts` (`sendCompletionNotification`, `sendErrorNotification`)
  - `packages/coding-agent/src/modes/settings.ts` (`completion.notify`, `error.notify`, `ask.notify`, `pty.notify`)
  - `packages/coding-agent/src/tools/ask.ts` (`#sendAskNotification`)
- **Upstream tests ported**:
  - `packages/coding-agent/test/tools/read-media-path.test.ts`: 3 cases (`mirrors Tern's viewer extensions`, whole-file viewable reads, sidecars/partial reads)
  - `packages/coding-agent/test/title-card.test.ts`: all 18 test cases (including table-driven subcases across `parseCardTitleReply`, `splitCardTitle`, `parseCardReply`, `keepTitleCard`)
  - `packages/coding-agent/test/terminal-title-state.test.ts`: 19 cases (all 10 `buildTerminalTitleWithState` cases + 9 `disposeTerminalTitleState` spinner/latch/style/interval cases; Windows Win32 console-title and OSC 7/pop cases are not ported because the host owns the console)
  - `packages/tui/test/terminal-capabilities.test.ts`: Tern recognition case + non-Tern terminal identity cases
  - Plus Go integration tests for TUI vs RPC gating, `PI_TERMINAL_OWNER_PID` claim, dialog `!` hold across `agent_end` + `Init` reset, `ask` single-bell gating, `error.notify` default-off, `tern.Setting` hook wiring, and `read` mediaPath with `details.resolvedPath`, `details.isDirectory` (directory named `icons.svg`), non-existent files, quotes/`@`/`file://`/literal colon files.

## Settings read by omp

Settings are read at use via package variable `tern.Setting func(ctx sdk.Context, key string) any` (assigned by the integrator; `nil` uses omp's defaults).

| Setting | Default | Honoured here | Notes |
|---|---|---|---|
| `title.icons` | `"nf+emoji"` | No (parameter only) | Accepted by `ParseCardTitleReply` / `ParseCardReply`; automatic title generation is not ported. |
| `tui.titleState` | `true` | Yes (via `tern.Setting`) | Read at use; disables `π > / ⠋ / !` run-state separator when `false`. |
| `tui.titleSpinner` | `"braille"` | Yes (via `tern.Setting`) | Read at use; selects `braille`, `pulse`, `dots`, or `line`. |
| `tui.spinnerInterval` | `80` ms | Yes (via `tern.Setting`) | Read at use (ms); `0` pins static `:` and stops the spinner goroutine. |
| `terminal.showProgress` | `false` | No (SDK gap) | PiG's host honours `terminal.showTerminalProgress`; `pig-tern/frontend/progress.go` emits OSC 9;4 unconditionally while native TSP is live. |
| `completion.notify` | `"on"` | Yes (via `tern.Setting`) | Read at use (`"on"`/`"off"`). |
| `error.notify` | `"off"` | Yes (via `tern.Setting`) | Read at use (`"on"`/`"off"`). |
| `ask.notify` | `"on"` | Yes (via `tern.Setting`) | Read at use (`"on"`/`"off"`). |
| `pty.notify` | `"on"` | No | Interactive PTY console-wait watcher is not ported (see Not ported). |
| `PI_NOTIFICATIONS` (env) | unset | Yes | `"off"`, `"0"`, `"false"` suppress notifications. |
| `TERM_PROGRAM` (env) | unset | Yes | `"tern"` (case-insensitive) selects native title format. |
| `TERN_PANE_SOCKET`, `TERN_PANE` (env) | unset | Yes (`Pane` / `PaneFromEnv`) | Delegates to `internal/ternwire`. |
| `TERN_BLOB_DIR` (env) | unset | Library helper (`HasBlobDir`) | Indicates local Tern blob cache availability. |
| `PI_TERMINAL_OWNER_PID` (env) | unset | Yes (written on TUI start) | Claimed on TUI `session_start` so child processes (`internal/browser`) know this process owns the pane. |
## Deliberate differences & Not ported

| Feature | Reason |
|---|---|
| Native Tern tab title & fallback notification titles (`oh-my-pig` vs omp's `omp`) | On purpose: the Piglet does not present itself as `omp`, so `NativeTerminalTitle` (`title.go`) and the fallback titles in `NotifyAgentEnd`/`NotifyAsk` (`notify.go`) use `"oh-my-pig"` rather than `"omp"` when a session is unnamed. Tern does not key session handling, restore, or tab icons on the OSC 0 title (its `Agent` sparkle icon comes from opening a `Chat: true` TSP surface, and session restore/transcript hooks key on `omp.session` / `omp_session_file`). |
| `/fork` into a Tern split pane (`wire.ts` `fork` op) and `flushToDisk` before Tern fork | On purpose (integrator decision): Tern decides the program and session from `omp_session_file`; a Tern split-pane fork has no correct success for this Binary and can start the real `omp`. omp's `/fork` (whole-session fork) behaviour is PiG's built-in `/clone`; PiG's built-in `/fork` is its user-message picker. |
| `omp_restore` / `omp_session_file` emission (OSC 1337 `SetUserVar`) | On purpose + SDK gap: Tern would type `omp --restore\|--resume <file>` in a shell pane (handing a PiG session file to `omp`), and in an agent pane PiG has no `--restore` flag and its `--resume` takes no path. PiG's SDK also has no raw OSC 1337 write call. |
| Native vs classic title switching on live TSP surface (`isNativeRendering` / `onNativeRenderingChange`) | SDK gap: PiG's extension SDK does not inform extensions whether a native TSP frontend is active or fell back to ANSI, so `TitleController` switches on `TERM_PROGRAM=tern`. |
| Extension title ownership over host resets (`title-generator.ts:849-858`) | SDK/host gap: PiG's interactive host unconditionally writes `pig - <dir>` after `session_start` and on session rename with no way for an extension to claim exclusive title ownership. `TitleController` re-emits on each state/session transition instead of deduping non-tick writes. |
| OSC 99 / OSC 9 structured desktop notifications and OSC 99 capability probe (`tui/src/terminal.ts:2020`) | SDK gap: PiG's extension SDK exposes `ctx.Bell()` and in-TUI `ctx.Notify()`, not raw OSC 99 / OSC 9 terminal writes or stdin capability probes. Degrades to `ctx.Bell()` in TUI mode. |
| Linux `libnotify` fallback (`terminal-capabilities.ts:193-199`) | Out of scope for a cross-platform subprocess extension without external CLI/D-Bus dependencies; degrades to `ctx.Bell()`. |
| Multiplexer notification routing (`terminal-capabilities.ts:181-187`) | Requires raw PTY passthrough sequences (tmux DCS, screen, etc.) that PiG's extension SDK does not expose. |
| Warp OSC 777 notification suppression (`event-controller.ts:2985-2987`) | PiG does not negotiate Warp's OSC 777 CLI agent protocol, so Warp double-toast suppression does not apply. |
| Direct OSC 9;4 emission from extension | SDK gap: PiG's extension SDK has no `SetProgress(bool)` call. In Tern TSP sessions, `pig-tern/frontend/progress.go` emits OSC 9;4;3 / OSC 9;4;0 directly; outside TSP, PiG's host controls OSC 9;4 via `terminal.showTerminalProgress`. |
| Console-waiting notification (`pty.notify`) and its `!` state (`tools/bash-interactive.ts:112-121`) | `internal/shell` does not implement omp's interactive PTY overlay (`bash-interactive.ts`), so there is no console input-wait watcher to trigger it. |
| `!` attention state during plan review (`modes/interactive-mode.ts:8236`) | Plan review UI is owned by `internal/modes`; it does not open a `ui_prompt_start` event unless wired to `TitleController.HoldBlocked`. |
| Pull-request suffix wiring (`interactive-mode.ts:2557`) | `TitleController.SetPullRequest` is implemented, but PiG's SDK does not push active git/PR status from the status line to extensions. |
| `read` tool emitting `details.resolvedPath` for all file reads (`internal/files/read.go`) | `internal/files` is owned by another lane. `internal/tern`'s `OnToolResult` hook skips `details.isDirectory`, prefers `details["resolvedPath"]` when present, falls back to omp's path expansion and literal-preferring selector splitting, checks `ShowsMedia` first, and verifies `os.Stat` is a regular file. Internal URLs resolving to files require `details.resolvedPath` from `internal/files`. |
| OSC 0 title stack push/pop (`CSI 22;2t` / `CSI 23;2t`), Win32 `SetConsoleTitleW` and Windows native-title failure latch (`title-generator.ts:709-739`) | PiG's host owns the terminal and console title lifecycle; extensions set titles only through `ctx.SetTitle`. |
| Retry-pending suppression on error notifications (`event-controller.ts:2983`) | PiG's SDK does not emit a retry-pending state on `agent_end` (and `error.notify` defaults to `"off"`). |
| Bundled 10,766-entry Nerd Fonts glyph catalog (`nerd-font-glyphs.txt`) | `ParseCardTitleReply` / `ParseCardReply` accept an injectable `nfResolver func(string) string` and degrade to the emoji when nil, avoiding a ~250KB embedded catalog until title generation is wired. |
| Non-Tern multiplexer `/fork pane\|window\|tab` launcher (`subprocess/terminal-launch.ts`) | Not ported: requires spawning external multiplexer CLIs (tmux/wezterm/cmux/herdr) and relaunching this Binary with `--resume`/`--fork`. |
| Automatic LLM session title generation (`title-generator.ts` `generateTitle` / `title-drift-classifier.ts`) | Model-driven title generation belongs to session/modes, not Tern connections; `internal/tern` provides the card parser/formatter and terminal title controller. |
| Managed install detection (`cli/managed-install.ts`) | Specific to `omp update`, omp's auto-updater, setup wizard, and WebKit engine installer; PiG/Piglet binaries are managed by Pigpen. |
| Native TSP drawing (`tray.ts`, `btw-controller.ts`, `apps/*`, `blobcache`, `cwd` OSC 7, `program_status` OSC 7501, `[NO-TERN]` tips, forced nerd preset, extension link claims, streaming pacing) | Owned by the frontend lane (`piglets/pig-tern/frontend/`). |
| Tern browser PiPs, `/stats` ("beside"), and `omp stats` ("over") | Owned by the ternwire/browser lane (`internal/ternwire`, `internal/browser`). |
