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

- **The overlay is pre-rendered rows.** The Go SDK cannot express a component widget, so the rows are sent
  as a list the host shows (at most ten rows), laid out at the width the host reported. The row budget is
  kept inside that limit: with the default `maxWidgetLines` of 12 the overlay shows nine rows plus the
  heading and the blank line, not twelve. After a terminal resize the rows stay as they were until the next
  refresh.
- **No locales.** The original translates its text with `@juicesharp/rpiv-i18n` when that is installed.
  Without it, and in this port, the text is English.
- **Parallel calls.** Calls to `todo` in one model turn are applied in the order the model wrote them and
  report their results in that order, as they do in Pi.

The proof that it matches the original is in [port/PORT.md](port/PORT.md).
