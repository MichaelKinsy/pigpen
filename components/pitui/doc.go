// Package tui is the subset of pi-tui that the ask-user-question questionnaire
// needs, as ported by PiG's tui package (v0.5.0) with PiG's application
// coupling removed, plus pi-tui's simple components rewritten from Pi.
// README.md and CREDITS.md give provenance and the file inventory.
//
// Components: Component, Container (AddChild/RemoveChild/Clear), Spacer, Text,
// DynamicBorder, Editor, Markdown. Keys: MatchesKey (and PiG's MatchesKeyID),
// IsKeyRelease, IsKeyRepeat, Key* constants and modifier helpers, Keybindings
// (interface), NewKeybindings, TUIKeybindingsManager, Set/GetTUIKeybindings (the
// global manager Editor reads), KB* ids (tui.* and app.editor.external).
// Other exported names: EditorTheme and SelectListTheme, MarkdownTheme
// (HighlightCode nil = plain), DefaultTextStyle, MarkdownOptions, CursorMarker,
// FakeCursorStart, FakeCursorEnd, RenderFakeCursor, ResolveFakeCursors,
// Hyperlink, HyperlinksSupported, SetHyperlinksSupported,
// SetKittyProtocolActive, IsKittyProtocolActive, FindWordForward and
// FindWordBackward with WordNavigationOptions, SGR* resets, FgClose, KillRing.
// Editor extras: SetRowsProvider, SetRequestRender, SetMaxVisibleLines.
// Syntax highlighting (highlight.js through internal/hljs): Highlight, HighlightMarkdownCode, HighlightOptions,
// HighlightTheme, HighlightFormatter, RenderHighlightedHtml, SupportsLanguage, LoadAllHighlightLanguages.
//
// # What PiG's public tui lacks or shapes differently
//
// PiG v0.5.0 has NewEditorWithTUI(ui, theme, options), GetMarkdownTheme,
// DynamicBorder, Spacer, IsKeyRepeat, MatchesKeyID and the widthx fake-cursor
// helpers. This package supplies the pi-tui shape the questionnaire's port is
// written against: NewEditor(EditorTheme) with GetText and GetCursor() returning
// (line, col); a Keybindings interface, NewKeybindings and app.editor.external;
// MatchesKey and the Key* constants; pi-tui's Text(text, paddingX, paddingY,
// bg), Container.AddChild/RemoveChild/Clear; a Markdown and an Editor without
// ActiveTheme, mouse, remote, status or autocomplete coupling. Delete this
// package when PiG exports those (core item C1).
//
// # Deviations from PiG v0.5.0
//
//   - Pruned: mouse events, StatusIndicator/Loader, autocomplete (providers,
//     async provider, SelectList popup), the remote/delegated editor
//     component, thinking-level and bash-mode borders, the global theme
//     (ActiveTheme, GetMarkdownTheme, Theme), async markdown transforms and
//     their generation tracking, alt-screen keybinding ids, keybinding hints,
//     PiG's KeyHelper/KeyID (replaced by the Key* constants in keys.go),
//     Editor.Clear and the Editor's host-visible BorderColor/ThinkingLevel
//     fields.
//   - Syntax highlighting is PiG's highlight.js port (highlight.go, internal/hljs, jsarray) without PiG's memo, resume
//     state, active theme and Piglet strip switch: HighlightMarkdownCode takes the theme's cli-highlight theme and
//     code block style as arguments and a caller sets MarkdownTheme.HighlightCode from it.
//   - NewEditor takes an EditorTheme; PiG's tui argument is replaced by
//     SetRowsProvider (visible-line cap max(5, floor(rows*0.3))) and
//     SetRequestRender (called by SetPaddingX).
//   - Editor.Render and Editor.HandleInput are pi-tui's, which Pi
//     1402973a shares (packages/tui/src is identical): Render emits the fake
//     cursor APC markers (hosts run ResolveFakeCursors), without PiG's row and
//     border caches; HandleInput dispatches in pi-tui's order (undo, deletion,
//     kill ring, history, cursor movement, newline, submit, ...) and ignores
//     Tab since there is no provider.
//   - Editor.OnChange fires once per HandleInput when the text changed (plus
//     explicitly from SetText, InsertTextAtCursor and history navigation);
//     submitValue reports "" before OnSubmit as pi-tui does.
//   - Editor.GetCursor returns (line, col) with col in UTF-16 units; the text is
//     GetText (PiG's Text() is gone).
//   - Markdown: a nil theme means identity styling (pi-tui requires a theme);
//     markdownPureStyles lists the plain theme's and the emphasis styles'
//     functions instead of PiG's active-theme functions, so the streaming memos
//     and resume apply to those themes and to themes registered the same way;
//     the transform cache is cleared by Invalidate; hyperlinks use
//     HyperlinksSupported (environment detection incl. the tmux probe) instead of
//     PiG's capability cache.
//   - jsnumber carries a copy of durable/core/jv.AppendNumber instead of
//     importing internal/jsparity; jsstring drops Stringify and its tests (they
//     need PiG's SDK JSON); wordsegmenter drops the pigstrip dictionary
//     switch and the dictionary-size budget test.
//   - DecodeKittyPrintable keeps PiG's Shift+letter fallback ("\x1b[97;2u" decodes to "A"); pi-tui's
//     decodeKittyPrintable yields the base key "a". This is PiG divergence D185 (docs/kitty-shift-letter-divergence), kept
//     here so pitui behaves as the PiG tui it is copied from.
//   - Text, Container, Spacer, DynamicBorder, keys.go, hyperlink.go and sgr.go
//     are pi-tui's, not PiG's.
//   - Tests: PiG's tests of the kept code are carried over; those that need
//     Node, Pi's oracle or PiG internals (the *_oracle tests, termsim grids,
//     testenv, pioracle), the pruned features, or PiG components (assistant and
//     user message blocks, TextInput) are not. compat_test.go supplies the
//     helpers and a test ActiveTheme.
package tui
