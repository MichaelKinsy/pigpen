# pitui

The subset of **pi-tui** that Pigpen extensions need, as a shared Go library (package `tui`, module
`github.com/MichaelKinsy/pigpen/components/pitui`). It is adapted from PiG's `tui` package (v0.5.0, commit `7a5422f66d`), which ports
Pi's pi-tui, with PiG's application features removed; the Pi it ports is commit `1402973a9d` ("1.3.0+1402973a"). It has no extension of its own.

Used by `ask-user-question` (the interactive questionnaire: `Editor`, `Markdown`, `Text`, `Container`,
`Spacer`, `DynamicBorder`, keys and keybindings, `widthx`). Other Packages that need pi-tui components may
import it the same way (`use ( . ../../../pitui )` in the extension's `go.work`, `require … v0.0.0`).

**Stopgap.** This is a copy, not a fork to maintain: it exists until PiG ships `tui` as a standalone module
with a pi-tui-shaped API (core item C1), at which point this Package is deleted and importers switch.

## What is in it

`Component`, `Container`, `Spacer`, `Text`, `DynamicBorder`, `Editor` (`NewEditor(EditorTheme)`, pi-tui
dispatch order, paste markers, undo, kill ring, UTF-16 cursor), `Markdown`, `MatchesKey` / `IsKeyRelease` /
`IsKeyRepeat` / `Key*`, `Keybindings` / `NewKeybindings` (tui.* ids and `app.editor.external`), the
fake-cursor markers with `ResolveFakeCursors`, and the subpackages `widthx`, `jsstring`, `jsnumber`,
`wordsegmenter`, `lazyregexp`, `latex`. [doc.go](doc.go) lists every exported name and every deviation
from Pi and PiG v0.5.0; in short: no mouse, status indicator, autocomplete, remote editor, thinking/bash
borders, global theme, async markdown or alt-screen keys; `Editor.Render` emits pi-tui's fake-cursor APC
markers; `OnChange` fires once per input chunk.

## Tests

`go vet ./... && go test ./...` (tests are PiG's, adapted; see [CREDITS.md](CREDITS.md)).

## File groups

| Group | Files | Lines |
|---|---|---|
| Root code, copied from PiG v0.5.0 and pruned/adapted | 20 files | 9465 |
| Root code, rewritten from pi-tui / pi-coding-agent | 8 files | 432 |
| Root tests (PiG v0.5.0 tests, adapted) + shims | 76 files | 9919 |
| `widthx/` code (PiG v0.5.0 copy) | 12 files | 5506 |
| `widthx/` tests | 25 files | 2193 |
| `widthx/` data, generators and notes | 24 files | 5762 |
| `jsstring/` code (PiG v0.5.0 copy) | 11 files | 782 |
| `jsstring/` tests | 14 files | 629 |
| `jsnumber/` code (PiG v0.5.0 copy) | 2 files | 364 |
| `jsnumber/` tests | 3 files | 252 |
| `jsnumber/` data, generators and notes | 1 files | 24 |
| `wordsegmenter/` code (PiG v0.5.0 copy) | 7 files | 2139 |
| `wordsegmenter/` tests | 2 files | 71 |
| `wordsegmenter/` data, generators and notes | 11 files | 42875 |
| `lazyregexp/` code (PiG v0.5.0 copy) | 1 files | 357 |
| `lazyregexp/` tests | 2 files | 233 |
| `latex/` code (PiG v0.5.0 copy) | 4 files | 2031 |
| `latex/` tests | 3 files | 900 |
| `latex/` data, generators and notes | 4 files | 1047 |
| `jsarray/` code (PiG v0.5.0 copy) | 1 files | 492 |
| `jsarray/` tests | 1 files | 45 |
| `jsarray/` data, generators and notes | 1 files | 1 |
| `internal/hljs/` code (PiG v0.5.0 copy) | 10 files | 4363 |
| `internal/hljs/` tests | 7 files | 939 |
| `internal/hljs/` data, generators and notes | 4 files | 1961 |
| `testdata/` (PiG v0.5.0 goldens the kept tests read) | 4 files | 2985 |
| `port/oracle/pi/` (Pi sources, unmodified) | 47 files | 19565 |

Every file by name is in [CREDITS.md](CREDITS.md) and [doc.go](doc.go).
