# Credits

`pitui` is an adaptation of two MIT-licensed works; both licence texts travel with the Package in
[`licenses/`](licenses/) and [`port/oracle/`](port/oracle), and [LICENSE](LICENSE) lists all copyright holders.

- **pi-tui** (`@earendil-works/pi-tui` 1.2.0), the terminal UI library of **Pi**,
  <https://github.com/earendil-works/pi>, by **Mario Zechner** (MIT, Copyright (c) 2025 Mario Zechner;
  [port/oracle/LICENSE](port/oracle/LICENSE)), at Pi's v1.2.0 tag, commit
  `06a466935536f8c9bcacb2d5efc0fc021be1bdff` (the pi-tui release PiG 0.5.0 targets). The ported sources
  (`packages/tui/src`, and `DynamicBorder` from `packages/coding-agent`) are kept unmodified at
  [`port/oracle/pi/`](port/oracle/pi) with the licence at [`port/oracle/LICENSE`](port/oracle/LICENSE). `container.go`, `spacer.go`, `text.go`, `dynamic_border.go`
  (Pi's `DynamicBorder` from pi-coding-agent), `keys.go` (the `Key` object, `matchesKey`, `isKeyRepeat`),
  `hyperlink.go` (the hyperlink part of `terminal-image.ts`) and `sgr.go` (fake-cursor markers and
  `resolveFakeCursors`) are Go rewrites of those TypeScript files.
- **PiG** (`tui` and `internal/{jsstring,jsnumber,wordsegmenter,lazyregexp,latex}`),
  <https://github.com/MichaelKinsy/PiG>, a Go port of pi-tui (MIT, Copyright Hewlett Packard Enterprise
  Development LP; [licenses/PiG-LICENSE](licenses/PiG-LICENSE)), commit
  `de6604a5ea3e27ed73a05ee89358d3d82eea43b7`. Copied with import paths rewritten: `widthx/`, `jsstring/`,
  `jsnumber/`, `wordsegmenter/`, `lazyregexp/`, `latex/`, and from `tui/`: `editor.go`, `editor_segments.go`,
  `editor_state.go`, `graphemes.go`, `kill_ring.go`, `key_match.go`, `key_parse.go`, `keys_decode.go`,
  `keybindings.go`, `markdown*.go`, `word_navigation.go`, with their tests (`compat_test.go` holds shims).

Modified paths: every copied file listed above was edited only where [`doc.go`](doc.go) lists a deviation
(pruned application features, `EditorTheme`/`NewEditor`, fake-cursor output, `GetCursor`, `OnChange`
timing, `Keybindings`, markdown theme and hyperlink handling); all other lines are as in PiG. Pruned
tests are those of the removed features. The PiG checkout was behind origin/main. Planned, and part of the port pull request: re-sync every copied
file from PiG's **v0.5.0** release tag (commit `7a5422f66d2f7a73ed566abdb7c4b29bf1571e3d`, what Pigpen pins), reapplying the
pruning listed in `doc.go`. Until then this Package reflects PiG commit `de6604a5ea`, not v0.5.0.

It also depends on `github.com/clipperhouse/uax29/v2` (MIT) and `golang.org/x/text` (BSD-3-Clause), as PiG does.
