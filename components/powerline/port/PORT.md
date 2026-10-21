# Port record: pi-powerline-footer (everything except the editor features, which are the next slices; they are refused meanwhile)

| Input | Identity |
|---|---|
| Original | `pi-powerline-footer` 0.19.1 by Nico Bailon, https://github.com/nicobailon/pi-powerline-footer, commit `859dee671b633fb533b07ceba3e6c1ab1c43360a`. MIT as declared in its package.json; the repository ships no LICENSE file (the owner ruled: treat it as MIT, say so in CREDITS.md) |
| Oracle | Pi 1.0.0, Node 24.19.0, the unmodified package at `port/oracle` (dev dependencies `@earendil-works/pi-*` 0.84.4 from its own `package-lock.json`, used only by `port/drive/drive.mjs`; every render state gives the same output with Pi 1.0.0's own pi-tui, pi-ai and pi-coding-agent, see below) |
| Target | PiG 0.5.0 (Pi 1.3.0 build `1402973a`, the build `pigeq` pins), go1.27 |
| Original's tests | 249 cases in 26 files. Slice: 107 cases in 11 files, 82 exact twins and 25 named skips; the other 142 (15 files) are deferred by name in `port/slices.json` |
| Kind | 1: an extension, no CLI built-in, no provider |

## Scope: what is and is not ported

The original is an 8,690-line extension that replaces the editor, adds a bash mode, a prompt queue and a welcome screen, and draws
a status bar. **Ported: the status bar** (and everything it reads): the segments, the layout, presets, separators, colors and icons,
custom items, the settings and their warnings, `/powerline`, token and cost totals, context usage, git status and host icons,
currency conversion. **Not ported** (named in `port/slices.json`): the bash-mode editor, the prompt queue and its stash and delayed
send, the welcome header and overlay, the working-message vibes, quote reply, `/cd`, the editor chrome (borders, fast renderer,
shortcuts, autocomplete composition) and the editor profiler. The original's `shell_mode` and `subagents` segments are therefore
never visible (the first needs bash mode, the second renders nothing in the original either).

### Ported in the follow-up (version 0.2.0)

`auto-follow-up.ts` and the `input` handler of `index.ts` (Jev classifier: POST to `api.typesafe.ai/v1/systemone`, 1500 ms for the exchange, confidence at least 0.8, requests serialised, Esc during the call returns the messages to the editor, a session switch during the call routes nothing into the new session, any failure keeps the message as steering and says so) are in `follow_up.go`, `followup_ext.go` and `auto_follow_up.go`. `editor-performance.ts` (options, counters, report) and `editor-composition.ts` are ported as `editor_performance.go` and `editor_composition.go`; nothing calls them, because the call site is the editor setup (`setEditorComponent`), which the Go SDK cannot do (PiG item C3). `/powerline-perf` and `POWERLINE_DEBUG_PERF` stay refused. Evidence: 10 more exact twins (6 auto-follow-up, 2 editor-performance, 2 editor-composition), 3 scenarios recorded from the original under Pi 1402973a, 146 mutations killed.

### Ported in PR 3 (keys, version 0.3.0)

`shortcuts.ts`, the shortcut config of `index.ts`, the stash and restore, `/stash-history`, copy and cut, `render-scheduler.ts`, `lifecycle.ts` in `shortcuts.go`, `shortcut_config.go`, `keymatch.go`, `keys.go`, `keys_ext.go`, `stash_history.go`, `stash_reload.go`, `clipboard.go`, `render_scheduler.go`. The input listener is `ctx.OnTerminalInput` and takes no core lock. `ctrl+s` is handled by the original only inside the focused editor; a Go extension has no editor, so the listener leaves it to Pi (`TestTerminalInputLeavesCtrlS`). Key matching has no keybinding lookup, so Pi's default keys are used.

### Ported in PR 4 (bashmode, version 0.4.0)

the logic of `bash-mode/*` (`history.ts`, `transcript.ts`, `shell-session.ts`, `completion.ts`, the pure parts of `editor.ts`) in `bash_*.go`: the shell session over `os/exec` (process group signals, hidden console on Windows), the completion engine and its providers over the SDK's `AutocompleteProvider`, the transcript store and widget, the `shell_mode` segment. The editor (`BashModeEditor`) is the next slice (`setEditorComponent` is in PiG v0.5.0): `/bash-mode`, `/bash-reset` and the `bashMode` setting stay refused, and 28 twins of editor cases are named skips with their reasons.

### Ported in PR 5 (vibes, version 0.5.0)

`working-vibes.ts` and the vibe wiring of `index.ts` in `working_vibes*.go` (model registry `Complete`, `SetWorkingMessage`): manager, themes, file and generate modes, prompt template, rate limit, `parseVibeGenerateArgs`, `generateVibesBatch`, and the `before_agent_start`, `agent_start`, `tool_call`, `agent_end` handlers. 9 twins.

### Ported in PR 6 (cdquote, version 0.6.0)

`cd-command.ts` (target resolution, argument completions, the forked session file written byte for byte as `SessionManager.forkFrom` writes it, checked against a file Pi wrote; `SwitchSession` with the notification and the `pi - <dir>` title), `quote-reply.ts` (candidate list, `formatQuote`, the picker through `Custom` and the select fallback) and the last-prompt line, in `cd_command.go`, `quote_reply.go`, `quote_picker.go`, `cdquote_ext.go`. One difference: the original calls `process.chdir(nextCtx.cwd)`; a Go extension has no host process cwd to change (PiG core item C3; `TestCdDoesNotChangeTheProcessCwd`). PiG replaces the session's cwd on `switchSession`, so tools, the footer and `Context.Cwd()` follow the new directory.

### Ported in PR 7 (welcome, version 0.7.0)

`welcome.ts` and `index.ts` (1882-1890, 2184-2202, 3539-3666) in `welcome.go` and `welcome_ext.go`: the header through `SetHeaderRenderer`, the 30 second non-capturing overlay through `Custom` (the SDK v0.5.0 `OverlayHandle`, while `go.mod` still requires v0.4.1) with a terminal-input listener standing in for the editor's key forwarding, and the dismissals (`agent_start`, `tool_call`, `session_shutdown`, `/powerline` off, any typing). The lifecycle tests assert the welcome's own start and stop of watching the keyboard through a test hook, because the SDK shares one host subscription between the extension's listeners.

### Ported in PR 8 (queue, version 0.8.0)

`queue/store.ts`, `queue/types.ts` and the queue parts of `index.ts` in `queue_*.go`, `select_overlay.go`: the inbox store (aliases, ids, statuses, locking, delivery text, the `/compact <prompt>` parse), the `queue` segment and the preview and pending-send widgets, `/queue`, the picker overlay, and delivery after a compaction (timers, block on failure, 60 second requeue, session generation). Typing a prompt during a compaction and delayed send need the editor's submit path and stay refused (`sendDelayMs`). The scenario `queue-post-compaction` is parked in `port/scenarios-pending`: PiG's compaction request prompt says "pig" where Pi says "pi" (PiG core bug). The adapter sends a widget only when its lines or placement changed and reads the session id once per repaint.

### Refusal of what is not ported

Until the rest is ported, `refusal.go` refuses each unported command (`/bash-mode`, `/bash-reset`, `/powerline-perf`) with an error notification and each unported setting (see the README) with a
warning at session start, using the message `<package> (Go port) does not support <feature> yet. Install the original extension,
npm:pi-powerline-footer@0.19.1, which runs unchanged in PiG.` `refusal_test.go` has one test per refused command and setting.
The refusals live in the SDK adapter, not the core, so the render states and the Pi-recorded scenarios (which set none of these
options) are unchanged.

## Mapping

| ID | Original | Go | Checked by |
|---|---|---|---|
| M1 | `segments.ts`: 20 segments and custom segments | `segments.go` | 178 render states; the usage-display, thinking-segment and remaining-regressions twins (28) |
| M2 | `index.ts` layout: `computeResponsiveLayout`, `buildContentFromParts`, `mergeSegmentsWithCustomItems` | `core.go` | render states at 3 widths each |
| M3 | `powerline-config.ts` | `config.go` | 25 custom-items twins and 4 usage-display config twins; states with invalid settings |
| M4 | `presets.ts`, `icons.ts`, `theme.ts` tables, Pi 1.0.0's theme tokens | `tables.go` **generated** by `port/gen-tables.mjs` | everything that renders; `pi100_test.go` |
| M5 | `theme.ts`, `colors.ts`, `icons.ts`, `separators.ts` logic | `theme.go` | states (rainbow, hex, user theme.json, icon overrides, nerd fonts); the icons twin |
| M6 | `paths.ts`, `thinking-level.ts` | `env.go` | 6 twins (paths, thinking-level), `extras_test.go` |
| M7 | `token-stats.ts` | `tokenstats.go` | 4 twins; states with errored turns, subagent costs |
| M8 | `context-usage.ts` | `contextusage.go` | 9 twins; states with known, unknown and fallback usage |
| M9 | `git-status.ts` | `git.go` | 9 twins; 16 states with real repositories |
| M10 | `currency-rates.ts` | `currency.go` | 3 states with a cache file; `extras_test.go` |
| M11 | `index.ts` handlers, `/powerline`, settings read/write | `core.go`, `extension.go` | 6 Pi-recorded RPC scenarios; 21 states with commands or events |
| M12 | pi-tui `visibleWidth` | `text.go` (rune-based, not grapheme-based; tabs, control and format characters and escape codes measured as pi-tui 1.0.0 does) | layout at narrow widths; 19 bad-input states; `pi100_test.go` (widths printed by pi-tui 1.0.0) |
| M13 | the host side of `index.ts` (ctx, `ctx.ui`, footer data) | `extension.go` (SDK adapter) | `adapter_test.go` and `concurrency_test.go` (the Skill's fake host, interactive mode, `go test -race`) |

## Two oracles

1. **Six scenarios recorded from the original under Pi 1.0.0** (`port/scenarios`, `port/golden`): the host-visible behavior in RPC
   mode: the stash status clear, the seven widget clears, the notifications and warnings and their order, the persisted settings
   (`cmd-preset`, `cmd-placement`), and an ordinary turn. Pi drops component factories in RPC mode, so no bar rows are in these
   traces (the host check pig-ts vs pi-ts differs there: PiG's RPC emits factory-widget rows, G3).
2. **A render golden produced by the original's own code** (`port/drive/drive.mjs`): the original extension is imported and driven
   in-process by a scripted host (a fake `pi`, a ctx, a theme that reproduces Pi's token check, a frozen clock, `TZ=UTC`, a fixed
   host name). The widget and footer factories it registers are rendered at 3 widths. 178 states (`port/gen-scenarios.py`) record
   the notifications, the settings file after the run (text and parsed), which widgets are installed and every line rendered.
   `render_test.go` replays the same states through the Go core. States that read a repository build a real repository on both
   sides; states with a currency cache write the cache file. This is a layer the porter harness does not have; it exists because
   the bar is invisible over RPC. The driver imports the original's dev dependencies (0.84.4); with Pi 1.0.0's own pi-tui, pi-ai
   and pi-coding-agent linked in instead, all 178 states give the same output once the theme-token codes (an index into Pi's
   token list, which gained `scrollbarTrack` and `scrollbarThumb`) are mapped back to token names. 19 states put control
   characters and escape codes (tab, CR/LF, BEL, NUL, C1, OSC, APC, CSI) into the session name, the working directory and the
   model name; neither the original nor the port strips them, and the layout measures them as pi-tui does.

## Results

- `go test -race`: pass. 82 twins + 25 named skips (`pigeq twins check --files` with the 11 slice files: exit 0; without `--files` the
  142 deferred cases are listed as MISSING), 178 render states, the port's own `extras_test.go`, the SDK adapter in an interactive
  fake host (`adapter_test.go`) and the footer renderer racing the handlers (`concurrency_test.go`).
- `pigeq check` against the Pi 1.0.0 goldens: 6 of 6 scenarios + port-gaps + exec-coverage, repeated 3 times.
- `pigeq mutate --unit`: **152 mutations, all killed** (139, plus 13 added in review for the SDK adapter, the footer frame and the
  text widths; before the adapter tests, 6 adapter mutations survived because the scenarios run in RPC mode). The first run killed 90 of 144. The survivors showed that the first 101 states
  put segment options under a `segmentOptions` key the original does not read (so they tested nothing; the original and the port
  agreed on ignoring them), that no state read a git repository, and that /powerline's project-settings, malformed-file and
  key-order paths had no state. They were fixed by tests, not by changing the mutations. 4 invalid mutations were rebuilt and 5
  equivalent ones removed (the thinking text of "off" does not exist; a `www.` host is covered by the sub-domain rule; two porcelain
  cases that cannot occur; an unknown separator style cannot reach `getSeparator`).
- The states found two real defects the twins could not: the port had no currency rate source (now the cache file and a background
  refresh, like the original), and two states shared a name (the generator now asserts unique names).
- `pig install --validate-only --json`: valid (command `powerline`, 14 handlers). `pig package validate`: valid.
- `pigeq gaps`: 16 gaps, 2 blocking, both in code that is not ported (quote reply's overlay handle, `/cd`'s
  `expandPromptTemplates`). The port itself has 2 PARTIAL stand-ins (`GetModelInfo`, `SetWidget`).
- Piglet: `piglets/pig-popular` now selects three ports and `pig-0.4.0 piglet build` fuses them. The Binary's own traces equal the
  Pi goldens in every event outside the `llm` channel (6 of 6).
- tmux, detached, in an interactive pig 0.4.0: `port/demo/pane-*.txt`: the bar above the editor, a turn, `/powerline full`
  (the overflow lands in the footer row), `placement below`, and the toggle off. The review rerun also resized the window (110,
  60, 110 columns): the footer row is laid out again at each width.

## Findings (to file against PiG, and notes on the porter)

1. **G11 No footer data provider.** The original reads other extensions' statuses (`getExtensionStatuses`), the host's git branch and
   branch-change events through `ReadonlyFooterDataProvider`. The Go SDK has none, so the `extension_statuses` segment and custom
   items see only what this extension sets (nothing), and the branch comes from `git` itself. Needs `Context.ExtensionStatuses()`
   (and a change notification) and the footer branch.
2. **G12 No settings write API.** The original persists `/powerline` choices by writing `settings.json`. The port does the same
   against the Pi agent directory (`PI_CODING_AGENT_DIR`, default `~/.pi/agent`), which a PiG host does not read; the choice
   persists for this extension only. `ctx.GetSettings()` is read-only.
3. **G1 again: widgets are lines.** The primary bar and the notification row are widget lines laid out at the terminal width when the
   bar is refreshed (an event, a command, a width change), not factories re-laid-out by the host. The secondary row uses
   `SetFooterRenderer` and so follows resizes. The SDK calls the footer renderer on its own goroutine after a width change, so the
   renderer lays out an immutable frame the handlers publish (the first version read the core's fields there and could refresh
   from the host: a data race `go test -race` reports, fixed in review), and the theme and git caches it reaches are locked.
4. **G13 theme token check.** Pi's `Theme.fg` throws on an unknown token and the original relies on that to fall back to `text`;
   the SDK's `UITheme.Fg` leaves the text uncolored. The port wraps it with Pi 1.0.0's token list (generated from Pi 1.0.0's
   `theme.d.ts`; the first version read the original's 0.84.4 dev dependency and lacked `scrollbarTrack` and `scrollbarThumb`).
   A list in the SDK, or an error return, would remove the copy.
5. **G14 stale-context errors.** The original recognizes Pi's stale-ctx error text (`isStaleExtensionContextError`); the Go SDK's
   equivalent is unverified, so the 2 related twins are named skips.
6. **G2 again: no pi-tui text helpers.** `text.go` started as the todo port's rune-based `visibleWidth`, a third copy in this
   series; it now measures tabs (3 cells), control and format characters (none) and escape codes (CSI ending in m/G/K/H/J, OSC,
   APC) as pi-tui 1.0.0 does. It is still per rune: a ZWJ emoji sequence (one 2-cell grapheme to pi-tui) is wider here, so such a
   session name can move a segment. A grapheme-aware `visibleWidth` in the SDK would make the layout exact.
7. **Asynchronous original.** The original refreshes git and currency data in the background and repaints; the port reads them while
   it builds the snapshot (handlers only). The 7 stale-while-revalidate twins of `git-status` and the 8 identity-keyed cache twins of
   `token-stats` are named skips with their reasons.
8. **The oracle is timing-dependent.** Its git calls time out after 200 ms; on a loaded machine a state came out with no branch. The
   recorder (`port/record-render.py`) accepts a state when two runs agree and runs each git state in its own process, because
   `git-status.ts` and `currency-rates.ts` keep module-level caches that survive between scenarios. The port keeps the 200 ms and 500 ms limits (`gitBranchTimeout`, `gitRemoteTimeout`, `gitStatusTimeout`, pinned by `TestGitTimeoutsMatchTheOriginal`); its tests raise them to a minute, because on a loaded host a killed `git symbolic-ref` fell back to `rev-parse` and reported `<sha> (detached)` (`TestSlowSymbolicRefStillFindsTheBranchWithinTheTestTimeout` makes that deterministic with a git that answers after 400 ms).
9. **Missing license file.** Recorded as an owner ruling (E3): MIT as declared in package.json; `port/oracle/LICENSE` is a notice
   file this Package added, and is marked as such.
10. **Escape codes in host text reach the terminal.** A session name, directory or model name with OSC, CSI or BEL bytes is drawn
   as is, by the original and by the port (the bad-input render states show both emit the same bytes). Worth raising upstream and
   with PiG for widget and footer lines.
11. **Host traffic per event.** Each event repaints from a fresh snapshot: 12 host calls per `message_update` while streaming
   (model info twice, session reads, context usage, model registry, two widgets and the footer), where the original reads in
   process and only requests a render.
12. **Settings location.** The settings are read from and `/powerline` writes to Pi's agent directory (`PI_CODING_AGENT_DIR`,
   default `~/.pi/agent`), as the original does; a `powerline` key in PiG's own settings is not read (see G12).

## Where a generated skeleton (`pig-codegen extension`) would have saved hand work

- The module, `Extension()` and event wiring boilerplate (14 handlers) and the command registration.
- `tables.go` is already generated (icons, separators, presets, default colors, the Pi theme tokens): that is the pattern for any
  upstream constant table; a skeleton could offer it.
- JS helpers written by hand again: `jsTrim`, `jsNumber`, `toFixed` with ties, `Math.round`, ordered JSON objects with JavaScript's
  key order, `JSON.stringify(x, null, 2)`. Four ports now carry copies.
- The twin ledger: 107 titles by hand again.
- The scripted-host driver and the render golden are new here and reusable by any port whose output is invisible over RPC.
