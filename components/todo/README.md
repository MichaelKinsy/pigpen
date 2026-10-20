# rpiv-todo (Go port)

A Go extension for PiG: a `todo` tool the model uses to plan and track multi-step work, the `/todos` command
that lists the tasks by status, and a live overlay above the editor that follows the list. The list is rebuilt
from the session on start, reload, compaction and tree navigation, so it survives all of them.

```text
● Todos (1/3)
├─ ✓ Write tests
├─ ◐ Implement (implementing)
└─ ○ Release
```

It is a port of `@juicesharp/rpiv-todo` 2.11.0 (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime.

## Use

```sh
pig install ./components/todo
```

The tool is `todo` (actions `create`, `update`, `list`, `get`, `delete`, `clear`; statuses `pending`,
`in_progress`, `completed`, plus a `deleted` tombstone; `blockedBy` dependencies with cycle rejection).
`/todos` shows the list. The overlay collapses with `ctrl+shift+t`.

Configuration is `~/.config/rpiv-todo/config.json` (or `$XDG_CONFIG_HOME/rpiv-todo/config.json`), read on
every use: `maxWidgetLines` (default 12, at least 3), `collapseKey` (a key like `alt+o`, or `off`), and
`guidance.promptSnippet` and `guidance.promptGuidelines` to replace the tool's prompt copy.

## Differences from the original

- **The overlay follows the terminal size on resize, not on every frame.** It is a component widget (a kit
  view of rows), so the host draws every row the budget allows, with no cap, and a terminal resize lays the
  rows out again. Pi redraws the original's widget on every frame, so a change of the theme or of Pi's
  tool-output expansion (the expand key) shows in it at once; here it shows at the next refresh (the next
  `todo` call, turn or resize), because the SDK sends an extension neither change.
- **Locales: installed-gated, no live switch.** Like the original, the port is English unless
  `@juicesharp/rpiv-i18n` is installed; "installed" is read from Pi's settings (a `packages` or `extensions`
  entry naming `rpiv-i18n` in the agent directory's `settings.json` or the project's `.pi/settings.json`). Then
  the nine locale files of the original are used, the locale coming from `~/.config/rpiv-i18n/locale.json`
  (under `$XDG_CONFIG_HOME` when set), then `LANG`, then `LC_ALL`, as the original's SDK resolves it. Two things
  need in-process JavaScript state a Go process cannot reach and stay documented: the live `/languages` switch
  and the `--locale` command-line flag (the host does not hand flags to an extension process).
- **Parallel calls.** Calls to `todo` in one model turn are applied in the order the model wrote them and
  report their results in that order, as they do in Pi.

The proof that it matches the original is in [port/PORT.md](port/PORT.md).
