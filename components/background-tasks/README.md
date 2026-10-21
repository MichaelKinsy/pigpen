# background-tasks (Go port of @vanillagreen/pi-background-tasks)

> **Status: refusal skeleton (0.1.0).** The package registers the original's tools, commands and shortcuts and reads its
> settings, but it does not run background tasks yet: every task action is refused with a tool error or a notification that
> names the working alternative, [`@vanillagreen/pi-background-tasks`](https://www.npmjs.com/package/@vanillagreen/pi-background-tasks) 2.2.0,
> which runs unchanged in PiG:
>
> `@pi-in-go/pigpen-background-tasks (Go port) does not support <feature> yet. Install the original extension, npm:@vanillagreen/pi-background-tasks@2.2.0, which runs unchanged in PiG.`
>
> The port follows in slices (see [port/PORT.md](port/PORT.md) and [port/slices.json](port/slices.json)).

A Go extension for PiG for shell commands that run while the conversation continues: builds, development servers and log
monitors. The agent starts a command with `bg_task`; the extension runs it in its own process group, saves its output to a
log, shows its status beside the editor and wakes the agent when the command exits or prints something it asked about.
It is a port of `@vanillagreen/pi-background-tasks` 2.2.0 by vanillagreen (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime.

## Install

```sh
pig install ./components/background-tasks
```

## What works in this release

- The package registers `bg_task` and `bg_status` with the original's exact descriptions, schemas and prompt guidelines,
  the `/bg` command family (`/bg`, `/bg:list`, `/bg:next`, `/bg:run`, `/bg:stop`, `/bg:clear`) and the shortcuts
  (`dashboardShortcut`, `f5`, `backgroundBashShortcut`, `widgetToggleShortcut`), from the settings read at load.
- Settings are read exactly as the original reads them: `kendex.extensionManager.config["@vanillagreen/pi-background-tasks"]`
  in the user settings file and, once the project is trusted, in `.pi/settings.json`; `PI_BG_TASK_DIR` overrides `taskDir`;
  `enabled: false` registers nothing.
- A bash command that the original would move into a background task (`watch`, `tail -f`, `journalctl -f`, a delayed
  tmux/session monitor, a polling loop) is refused instead of running in the foreground, as is any bash command while
  `autoBackgroundPatterns` holds a pattern.

Everything else is refused until its slice lands; the README is updated with each slice.

## Settings and the `/extensions:settings` tab

The settings file is `<agent dir>/settings.json` (PiG's agent directory: divergence D2, see PORT.md) and, for a trusted
project, `<workspace>/.pi/settings.json`. The `kendex.extensionManager` declaration of the original's `package.json`
(every key with its label, type, default and category) is carried unchanged in this package's `package.json`. The
`/extensions:settings` tab is drawn by the separate `pi-extension-manager` extension from that declaration; this package
neither provides nor needs it.

## Differences from the original

Listed in [port/PORT.md](port/PORT.md) under "Deliberate differences" and kept current with each slice.

## Credits and license

MIT. Original by vanillagreen, based on `@ifi/pi-background-tasks` by Ifiok Jr.; see [CREDITS.md](CREDITS.md),
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and [port/oracle/LICENSE](port/oracle/LICENSE).
