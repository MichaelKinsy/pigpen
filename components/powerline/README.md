# powerline-footer (Go port, every feature except those that need the editor, ported)

A Go extension for PiG: a powerline-style status bar. A row of segments (model, thinking level, path, git branch and counts, token
and cost totals, context usage, session, host, time, other extensions' notification statuses) is laid out to the terminal width,
with what does not fit moving to a second row, and styled by presets (`default`, `minimal`, `compact`, `full`, `nerd`, `ascii`),
separators, custom items and your own colors and icons.

It is a port of `pi-powerline-footer` 0.19.1 (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime.

## Use

`/powerline` toggles the bar, `/powerline <preset>` picks a preset, `/powerline placement above|below|toggle` moves the primary
bar. The settings are the original's: the `powerline` key of `settings.json` (under `$PI_CODING_AGENT_DIR`, default
`~/.pi/agent`, and `.pi/settings.json` of the project), and `extensions/powerline-footer/theme.json` there for colors and icons.

```sh
pig install ./components/powerline
```

## What is ported

The segments, the layout, presets and separators, custom items, the settings and their warnings, the persisted `/powerline`
changes, token and cost totals (subagent costs included), context usage, git status and host icons, currency conversion, and the
Nerd Font detection. In RPC mode it clears the same widgets and status Pi does and draws nothing.

**Also ported:** auto follow-up (`powerline.autoFollowUp` with `TYPESAFE_API_KEY`: while the agent works, a plain Enter is sent to the Jev classifier and moves to the follow-up queue when Jev is confident it can wait), the pure parts of the editor profiler and the editor composition helpers (not wired in, see below), the prompt stash (`alt+s`, restore, the `stash` status, carried over a reload), `/stash-history` with its picker and `ctrl+alt+h`, recent project prompts, the copy and cut shortcuts, the shortcut settings (`powerlineShortcuts`, `powerline.stashSharpSShortcut`) with their conflict rules, and the render scheduler and lifecycle helpers; the logic of bash mode: command history, the shell session, the completion engine (paths, git, history), the transcript store and widget rows, the `shell_mode` segment data and the settings parse; working vibes: `/vibe`, the generated working message, themes, file and generate modes, the rate limit and the `workingVibes` and `workingVibe` settings; `/cd` (the session is forked into the new directory and replaced), `/reply` (quote a message into the editor) and the last-prompt line (`showLastPrompt`); the startup welcome: the header, the 30 second overlay, recent sessions and the loaded counts, and every dismissal the original wires; the prompt queue: `/queue` (send, retry, clear, target, alias), the queue picker (`ctrl+alt+q`), the queue segment and the preview and pending-send widgets, and delivery after a compaction; the powerline editor chrome and the fast renderer for very large drafts, and `ctrl+s` inside the editor (installed through `Context.SetEditorComponent`, so the editor wraps Pi's own; printable keys go straight through); bash mode: `/bash-mode`, `/bash-reset`, the `!` one-off commands, the shell prompt (`$`), shell history browsing with up and down, the ghost suggestion (Tab or Right accepts it), the transcript widget below the editor, the mode-aware autocomplete provider, the `shell_mode` segment, the toggle shortcut and the `bashMode` settings; delayed send (`powerline.sendDelayMs`: Esc takes a waiting prompt back, Enter on an empty editor sends it now) and the capture of prompts typed during a compaction (`/compact <prompt>`, and any prompt, are queued for after it); the queue picker shortcut (`ctrl+alt+q`) and the reply shortcut.

**Not ported yet, next slices:** PiG's Go SDK can host an editor since v0.5.0 (`Context.SetEditorComponent`, C3), so the features that need one are still to be ported: and the editor profiler (`/powerline-perf`). Until then they are refused, see below. **Genuinely missing in the SDK** (reported in the PiG SDK drift inventory): keybinding lookup (E10, `keybindings.matches`), `tui.hasOverlay()` and a host working-directory change after a session switch. Things differ because the Go SDK lacks them or an equivalent: the editor draws the border in the colour of the default editor's own border row and assumes a 24-row terminal and a focused editor in the fast renderer (it cannot see `tui.terminal.rows` or a paste in progress); also other extensions' statuses are not visible to the bar (it only draws its own), and the primary bar is widget lines laid out at the terminal width when the bar is refreshed. Shortcuts follow Pi's default key bindings (the SDK cannot read the user's), and `/cd` does not change the host process's working directory (the session replacement carries the new directory). See [port/PORT.md](port/PORT.md).

Nothing unported is silently accepted. The command the original adds that is not ported (`/powerline-perf`) answer with an error naming the feature, and an environment variable that turns an unported feature on (`POWERLINE_DEBUG_PERF=1`) gets a warning at session start:

> @pi-in-go/pigpen-powerline (Go port) does not support &lt;feature&gt; yet. Install the original extension,
> npm:pi-powerline-footer@0.19.1, which runs unchanged in PiG.
