# powerline-footer (Go port, partial)

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

**Not ported:** the bash-mode editor, the prompt queue and stash, the welcome header, the working-message vibes, quote reply,
`/cd`, the editor chrome and shortcuts. Two things differ because the Go SDK lacks them: other extensions' statuses are not
visible to the bar (it only draws its own), and the primary bar is widget lines laid out at the terminal width when the bar is
refreshed. See [port/PORT.md](port/PORT.md).
