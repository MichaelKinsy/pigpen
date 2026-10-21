# background-tasks (Go port of @vanillagreen/pi-background-tasks)

> **Status: task runtime (0.2.0).** `bg_task`, `bg_status`, the `/bg` commands, wakes, restore, retention and the
> bash auto-background diversion run. The mini-dashboard widget, the dashboard overlay, the tool and message renderers and the
> dashboard and widget shortcuts are refused with a notification that names the working alternative,
> [`@vanillagreen/pi-background-tasks`](https://www.npmjs.com/package/@vanillagreen/pi-background-tasks) 2.2.0, until slice 3
> (see [port/PORT.md](port/PORT.md) and [port/slices.json](port/slices.json)).

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

- `bg_task` (spawn, list, log, stop, clear) and `bg_status` with the original's exact schemas, in their own process group, with
  a batched log, output and exit wakes, the notify pattern deadline, resource controls, restore from the session branch and the
  sidecar, the orphan watcher, the 50-task bound and lane retention.
- `/bg`, `/bg:list`, `/bg:next`, `/bg:run`, `/bg:stop`, `/bg:clear` and the arm-next-bash shortcut. `/bg` and `/bg watch` (the
  dashboard) and the dashboard and widget shortcuts answer with a refusal until slice 3.
- A bash command the original would move into a background task (`watch`, `tail -f`, a polling loop, your own patterns) is
  moved, in the agent's bash tool and in `!command`.
- The instructions of `instructions.md` reach the system prompt exactly as the original's install step puts them in
  `APPEND_SYSTEM.md` (see PORT.md, M17).

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
