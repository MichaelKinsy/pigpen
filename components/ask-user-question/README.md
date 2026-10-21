# rpiv-ask-user-question (Go port)

A Go extension for PiG: the `ask_user_question` tool, which lets the model put up to four structured questions
(2-4 options each, single or multiple choice, an optional markdown preview per option) to you when it would otherwise guess.
In a terminal you get the original's interactive questionnaire: a tab per question and a Submit tab, per-option and global
notes (`n`), multi-select toggles, an inline "Type something." answer with paste and multi-line editing, a side-by-side
markdown preview pane with syntax highlighting, the collapse key, and nine locales (with `rpiv-i18n` installed, as in the original). On RPC hosts it asks with native select and input dialogs, as the
original does.

It is a port of `@juicesharp/rpiv-ask-user-question` 2.11.0 (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime. The terminal UI is built on the shared
[pitui](../pitui/README.md) Package (a Go port of pi-tui).

## What differs from the original

Each item names the capability the PiG SDK lacks; none is silent.

- **The collapse key hides the overlay only as a one-line row.** The original hides the overlay entirely and reopens it from a raw
  terminal listener (`ctx.ui.custom` `onHandle`: `setHidden`/`isHidden`/`isFocused`). The SDK has no overlay handle, so the port uses
  the original's own fallback for hosts without one: the visible one-line row, the same key expands it, Esc cancels.
- **The external editor key (Ctrl+G, `app.editor.external`) reports a failure.** The original stops the TUI and runs your editor;
  the SDK cannot stop and restart the host TUI. The key shows `External editor failed: ... does not support the external editor for
  custom answers yet. Install the original extension, npm:@juicesharp/rpiv-ask-user-question@2.11.0, which runs unchanged in PiG.`
  and keeps your draft.
- **No terminal bell** before the questionnaire waits (the SDK has no terminal write).
- **Keybinding overrides** are read from `keybindings.json` in the agent directory (the SDK offers no `KeybindingsManager`).
- **Locales: English unless `rpiv-i18n` is installed,** as in the original. The original asks the optional npm package
  `@juicesharp/rpiv-i18n` for its strings and falls back to English when the import fails. A Go extension cannot import it,
  so "installed" means that Pi's settings name it: a `packages` or `extensions` entry containing `rpiv-i18n` in the agent
  directory's `settings.json` or the project's `.pi/settings.json`. Only then does the port resolve a locale the way that
  package does: `$XDG_CONFIG_HOME/rpiv-i18n/locale.json`, then `LANG`, then `LC_ALL`.

**Refused, not trimmed.** A terminal host that cannot render the questionnaire (`ctx.ui.custom` resolves nothing) gets
the original's fallback, the select and input dialogs. What those dialogs cannot carry is refused, not trimmed or
ignored, with a tool error that names the working alternative:
`... (Go port) does not support <feature> yet. Install the original extension,
npm:@juicesharp/rpiv-ask-user-question@2.11.0, which runs unchanged in PiG.` They are an option preview longer than
600 characters (the dialogs cut it) and a `collapseKey` setting other than the default or `off` (the collapse key
belongs to the questionnaire). The questionnaire itself carries both, and on RPC hosts the original runs the dialogs,
cut and all, so nothing is refused there.

See [port/PORT.md](port/PORT.md) for the proof and the full list.

```sh
pig install ./components/ask-user-question
```
