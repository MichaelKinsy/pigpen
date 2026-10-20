# pitui

The subset of **pi-tui** that Pigpen extensions need, as a shared Go library (package `tui`, module
`github.com/MichaelKinsy/pigpen/components/pitui`). It is adapted from PiG's `tui` package, which ports
Pi's pi-tui, with PiG's application features removed. It has no extension of its own.

Used by `ask-user-question` (the interactive questionnaire: `Editor`, `Markdown`, `Text`, `Container`,
`Spacer`, `DynamicBorder`, keys and keybindings, `widthx`). Other Packages that need pi-tui components may
import it the same way (`use ( . ../../../pitui )` in the extension's `go.work`, `require … v0.0.0`).

**Re-sync pending.** The copied code is PiG `de6604a5ea`, not yet PiG v0.5.0 (`7a5422f66d`, what Pigpen pins); re-syncing is part of the port pull request (see [CREDITS.md](CREDITS.md)).

**Stopgap.** This is a copy, not a fork to maintain: it exists until PiG ships `tui` as a standalone module
with a pi-tui-shaped API (core item C1), at which point this Package is deleted and importers switch.

## What is in it

`Component`, `Container`, `Spacer`, `Text`, `DynamicBorder`, `Editor` (`NewEditor(EditorTheme)`, pi-tui
dispatch order, paste markers, undo, kill ring, UTF-16 cursor), `Markdown`, `MatchesKey` / `IsKeyRelease` /
`IsKeyRepeat` / `Key*`, `Keybindings` / `NewKeybindings` (tui.* ids and `app.editor.external`), the
fake-cursor markers with `ResolveFakeCursors`, and the subpackages `widthx`, `jsstring`, `jsnumber`,
`wordsegmenter`, `lazyregexp`, `latex`. [doc.go](doc.go) lists every exported name and every deviation
from pi-tui 1.2.0 and PiG; in short: no mouse, status indicator, autocomplete, remote editor, thinking/bash
borders, global theme, async markdown or alt-screen keys; `Editor.Render` emits pi-tui's fake-cursor APC
markers; `OnChange` fires once per input chunk.

## Tests

`go vet ./... && go test ./...` (tests are PiG's, adapted; see [CREDITS.md](CREDITS.md)).
