# Port record: pitui

pitui is a library Package (no extension, so no golden traces). It is a Go adaptation of the part of Pi's pi-tui that the
`ask-user-question` questionnaire needs. It was made from PiG's `tui` package at the v0.5.0 tag
(`7a5422f66d2f7a73ed566abdb7c4b29bf1571e3d`), which is itself a Go port of pi-tui, with PiG's application features cut
out, plus Go rewrites of pi-tui's `Container`, `Spacer`, `Text`, `DynamicBorder`, key helpers, hyperlink and fake-cursor
handling. It is a stopgap until PiG ships `tui` as a standalone module with a pi-tui-shaped API (core item C1); at that
point the Package is deleted.

## The original

The Pi sources this Package follows are kept unmodified in `port/oracle/pi/` (`packages/tui/src` and `DynamicBorder`
from `packages/coding-agent`, Pi commit `1402973a9dbe8c627341e04c9744c54953e285ca`, the Pi that PiG v0.5.0 targets, with its
licence in `port/oracle/LICENSE`). PiG's code is not vendored as an oracle: it is the source of the copied files, and
`CREDITS.md` lists every file and where it came from.

## Evidence

| Check | Command | Result |
|---|---|---|
| Tests of the kept code | `go vet ./... && go test ./...` in the Package root | pass |
| PiG tests carried over | 74 root test files and the subpackage tests, from PiG v0.5.0 | pass |
| Pi behaviours pinned | `pi_parity_test.go` (bare `www.` autolink, indented code block, image alt text) | pass |
| Consumer | the `ask-user-question` extension (`internal/ui` and the frame harness against the unmodified TypeScript) | see that Package's `port/PORT.md` |

Tests that need Node, Pi's own oracle or PiG internals (the `*_oracle` tests, terminal-grid simulators, `testenv`,
`pioracle`), tests of the pruned features, and tests of PiG components that were not taken (assistant and user message
blocks, `TextInput`) are not carried over. `doc.go` lists the deviations from PiG v0.5.0 and from pi-tui.

## Not ported

Mouse events, the status indicator and loader, autocomplete, the remote and delegated editor component, thinking-level
and bash-mode borders, the global theme, asynchronous markdown transforms and the alt-screen keybinding ids. The
questionnaire does not use them.
