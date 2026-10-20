# Credits

`pitui` is an adaptation of two MIT-licensed works. Their licence texts travel with the Package
([`port/oracle/LICENSE`](port/oracle/LICENSE) and [`licenses/PiG-LICENSE`](licenses/PiG-LICENSE)), and [LICENSE](LICENSE)
lists all copyright holders.

- **Pi** (pi-tui), the terminal UI library of the Pi coding agent, <https://github.com/earendil-works/pi>, by
  **Mario Zechner** (MIT, Copyright (c) 2025 Mario Zechner;
  [port/oracle/LICENSE](port/oracle/LICENSE)), at commit `1402973a9dbe8c627341e04c9744c54953e285ca`, the Pi
  that PiG v0.5.0 targets ("1.3.0+1402973a", 32 commits after the v1.3.0 tag, not published on npm). The sources
  this Package ports (`packages/tui/src`, and `DynamicBorder` from `packages/coding-agent`) are kept unmodified in
  [`port/oracle/pi/`](port/oracle/pi). `container.go`, `spacer.go`, `text.go`, `dynamic_border.go`, `keys.go`,
  `hyperlink.go` and `sgr.go` are Go rewrites of those TypeScript files. (`packages/tui/src` is identical at
  `06a466935536f8c9bcacb2d5efc0fc021be1bdff`, Pi v1.2.0, the version the first draft was checked against.)
- **PiG** (`tui` and `internal/{jsstring,jsnumber,wordsegmenter,lazyregexp,latex}`),
  <https://github.com/MichaelKinsy/PiG>, a Go port of pi-tui (MIT, Copyright Hewlett Packard Enterprise Development LP;
  [licenses/PiG-LICENSE](licenses/PiG-LICENSE)), at the v0.5.0 release tag, commit
  `7a5422f66d2f7a73ed566abdb7c4b29bf1571e3d`, the version Pigpen pins. The files listed below were copied from it
  with import paths rewritten and then pruned or adapted only where [`doc.go`](doc.go) lists a deviation; the PiG
  tests of the kept code came with them (`compat_test.go` holds the shims).

Modified paths: each copied file below, as `doc.go` describes (pruned application features, `EditorTheme` and
`NewEditor`, `GetCursor`, `OnChange` timing, `Keybindings`, markdown theme and hyperlink handling, the jsnumber/jsstring/
wordsegmenter dependency cuts). The Pi sources under `port/oracle/pi/` are not modified.

It also depends on `github.com/clipperhouse/uax29/v2` (MIT), `github.com/dlclark/regexp2` (MIT) and
`golang.org/x/text` (BSD-3-Clause), as PiG does.

## Files

Copied from PiG v0.5.0:

  - `editor.go` (1670 lines)
  - `editor_segments.go` (161 lines)
  - `editor_state.go` (10 lines)
  - `graphemes.go` (27 lines)
  - `kill_ring.go` (60 lines)
  - `key_match.go` (911 lines)
  - `key_parse.go` (295 lines)
  - `keys_decode.go` (224 lines)
  - `keybindings.go` (652 lines)
  - `word_navigation.go` (109 lines)
  - `markdown.go` (1530 lines)
  - `markdown_latex.go` (176 lines)
  - `markdown_list.go` (534 lines)
  - `markdown_marked.go` (1458 lines)
  - `markdown_marked_block.go` (420 lines)
  - `markdown_options.go` (42 lines)
  - `markdown_resume.go` (200 lines)
  - `markdown_spans.go` (456 lines)
  - `markdown_style.go` (120 lines)

Rewritten from Pi:

  - `container.go` (42 lines)
  - `spacer.go` (24 lines)
  - `text.go` (86 lines)
  - `dynamic_border.go` (23 lines)
  - `keys.go` (51 lines)
  - `hyperlink.go` (109 lines)
  - `sgr.go` (78 lines)
  - `base.go` (19 lines)

Root tests (PiG v0.5.0 tests of the kept code, adapted; `compat_test.go`, `extras_test.go` and `markdown_fixture_test.go` and `pi_parity_test.go` are written here):

  - `compat_test.go`
  - `components_grapheme_test.go`
  - `editor_border_color_test.go`
  - `editor_border_regression_test.go`
  - `editor_character_jump_upstream_test.go`
  - `editor_cursor_down_right_upstream_test.go`
  - `editor_edit_state_regression_test.go`
  - `editor_history_test.go`
  - `editor_input_copy_pi_test.go`
  - `editor_jump_keys_upstream_test.go`
  - `editor_kill_undo_upstream_test.go`
  - `editor_literal_input_upstream_test.go`
  - `editor_newline_test.go`
  - `editor_parity_test.go`
  - `editor_paste_marker_test.go`
  - `editor_scroll_test.go`
  - `editor_segments_regression_test.go`
  - `editor_shift_delete_pi_test.go`
  - `editor_sticky_paste_upstream_test.go`
  - `editor_typed_line_test.go`
  - `editor_utf16_test.go`
  - `editor_wide_grapheme_test.go`
  - `editor_word_segments_upstream_test.go`
  - `editor_wrapping_upstream_test.go`
  - `extras_test.go`
  - `key_cache_test.go`
  - `key_csiu_text_test.go`
  - `key_event_type_upstream_test.go`
  - `key_match_mode_test.go`
  - `key_match_test.go`
  - `key_parse_test.go`
  - `keybinding_definitions_upstream_test.go`
  - `keybindings_platform_test.go`
  - `keybindings_set_user_test.go`
  - `keybindings_test.go`
  - `keys_alternates_test.go`
  - `keys_decode_test.go`
  - `keys_shifted_symbols_upstream_test.go`
  - `keys_surrogate_test.go`
  - `kill_ring_test.go`
  - `markdown_autolink_test.go`
  - `markdown_blockquote_style_test.go`
  - `markdown_codeblock_wrap_test.go`
  - `markdown_fixture_test.go`
  - `markdown_generation_test.go`
  - `markdown_heading_bold_test.go`
  - `markdown_latex_test.go`
  - `markdown_latex_upstream_test.go`
  - `markdown_list_item_test.go`
  - `markdown_long_answer_test.go`
  - `markdown_marked_corpus_test.go`
  - `markdown_marked_test.go`
  - `markdown_pool_test.go`
  - `markdown_port_regressions_test.go`
  - `markdown_resume_soup_test.go`
  - `markdown_resume_test.go`
  - `markdown_row_invariant_test.go`
  - `markdown_spans_test.go`
  - `markdown_streaming_reuse_test.go`
  - `markdown_test.go`
  - `markdown_theme_test.go`
  - `markdown_upstream_test.go`
  - `markdown_width_test.go`
  - `pi_parity_test.go`
  - `restored_editor_border_regression_test.go`
  - `restored_editor_edit_state_regression_test.go`
  - `restored_editor_kill_undo_upstream_test.go`
  - `restored_editor_wrapping_upstream_test.go`
  - `restored_markdown_port_regressions_test.go`
  - `restored_markdown_resume_test.go`
  - `restored_markdown_streaming_reuse_test.go`
  - `undo_key_test.go`
  - `word_navigation_pi_test.go`
  - `word_navigation_sea_test.go`
  - `word_navigation_upstream_test.go`

Subpackage directories copied from PiG v0.5.0 (code, tests, data): `widthx/`, `jsstring/`, `jsnumber/`, `wordsegmenter/`, `lazyregexp/`, `latex/`.
Test data copied from PiG v0.5.0: `testdata/markdown-latex-corpus.txt`, `testdata/markdown-latex-golden.jsonl`, `testdata/markdown-marked-corpus.json`, `testdata/markdown-marked-golden.jsonl`.
Pi sources: every file under [`port/oracle/pi/`](port/oracle/pi) is the unmodified Pi file at the pinned commit.
