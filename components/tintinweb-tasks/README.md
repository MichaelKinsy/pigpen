# tintinweb-tasks (Go port of @tintinweb/pi-tasks)

A Go extension for PiG that brings **Claude Code-style task tracking** to the agent: seven tools the model
calls to plan and run multi-step work, a `/tasks` menu, a live task list above the editor, periodic
reminders when the list goes stale, and (with [`tintinweb-subagents`](../tintinweb-subagents/README.md))
task execution by background subagents. It is a port of `@tintinweb/pi-tasks` 0.9.0 by tintinweb (see
[CREDITS.md](CREDITS.md)), made with the [extension porting Skill](../extension-port/README.md). It needs no
Node runtime, and starts no process and makes no network call at start-up.

```text
● 4 tasks (1 done, 1 in progress, 2 open)
  ✔ #1 Design the flux capacitor
  ✳ #2 Acquiring plutonium… (2m 49s · ↑ 4.1k ↓ 1.2k)
  ◻ #3 Install flux capacitor in DeLorean › blocked by #1
  ◻ #4 Test time travel at 88 mph › blocked by #2, #3
```

## Install

```sh
pig install ./components/tintinweb-tasks
```

Or take the [`pig-essentials`](../../piglets/pig-essentials/README.md) Piglet, which selects it.

## Tools

`TaskCreate`, `TaskList`, `TaskGet`, `TaskUpdate` (status, fields, metadata, `addBlocks` and `addBlockedBy`
with warnings for cycles, self-dependencies and unknown ids), `TaskOutput`, `TaskStop` and `TaskExecute`. Their
descriptions and schemas are the original's. A finished list is retired when the next batch of work starts,
and completed tasks are cleared a few turns after the list is done (`autoClearCompleted`).

## Storage and configuration

Each key of `taskScope` (`memory`, `session` (default), `session-global`, `project`) and the `PI_TASKS`
override (`off`, a list name under `<agent dir>/tasks/`, an absolute path, or a path relative to the workspace)
behave as in the original; shared lists use a lock file with stale-lock detection. Settings live in
`<agent dir>/tasks-config.json` (global) and `<workspace>/.pi/tasks-config.json` (project overrides, written by
`/tasks` → Settings): `taskScope`, `autoCascade`, `autoClearCompleted`, `collapseCompleted`, `showAll`,
`maxVisible`, `sortOrder` (a preset or a sort spec), `hiddenAt` and `glyphs`. Configuration is data, never code.

## Differences from the original

- **The widget is pre-rendered rows.** The Go SDK cannot register a component the host re-renders, so the rows
  are laid out at the width and theme the host reported and pushed on every change and on the spinner's
  150 ms tick. The host shows at most ten rows. In RPC mode the rows are not sent at all, as Pi's RPC mode drops
  a component widget.
- **`/tasks` → Settings is a chain of selects**, not a `SettingsList` panel: picking a row cycles its value and
  saves it.
- **Agent directory.** `<agent dir>` is PiG's (`PIG_CODING_AGENT_DIR`, else `<PIG_HOME or ~/.pig>/agent`;
  `PIG_USE_PI_DIRS=1` selects Pi's), not Pi's fixed `~/.pi/agent`. Named lists (`PI_TASKS=<name>`) live under `<agent dir>/tasks/`, where the original uses `~/.pi/tasks/`.
- **Pinging pi-subagents.** The original pings while it loads. Here the ping waits for the first `session_start`
  (a ping sent while the extension loads deadlocks the host when pi-subagents is built into the same Binary), and
  is repeated on the `subagents:ready` broadcast and when `TaskExecute` runs without having found it. The protocol (version 2) is unchanged.
- **Key order.** Task metadata is written with sorted keys (Go maps), where JavaScript keeps insertion order.

The proof that it matches the original is in [port/PORT.md](port/PORT.md).
