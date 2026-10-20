// Package tui is the subset of pi-tui (as ported by PiG's tui package) that
// the ask-user-question questionnaire needs, with the PiG application
// coupling removed. See NOTICE for provenance.
//
// Components: Component, Container (AddChild/RemoveChild/Clear), Spacer, Text,
// DynamicBorder, Editor, Markdown. Keys: MatchesKey, IsKeyRelease,
// IsKeyRepeat, Key* constants and modifier helpers, Keybindings (interface),
// NewKeybindings, TUIKeybindingsManager, Set/GetTUIKeybindings (the global
// manager Editor reads), KB* ids (tui.* and app.editor.external).
// Other exported names: EditorTheme, MarkdownTheme (HighlightCode nil =
// plain), DefaultTextStyle, MarkdownOptions, CursorMarker, FakeCursorStart,
// FakeCursorEnd, RenderFakeCursor, ResolveFakeCursors, Hyperlink,
// HyperlinksSupported, SetHyperlinksSupported, SetKittyProtocolActive,
// IsKittyProtocolActive, FindWordForward/FindWordBackward,
// WordNavigationOptions, SGR* resets, FgClose, KillRing.
// Editor extras: SetRowsProvider, SetRequestRender, SetMaxVisibleLines.
//
// Differences from PiG's public tui (origin/main, which has NewEditorWithTUI
// with EditorTheme/EditorOptions, widthx fake-cursor helpers, GetMarkdownTheme,
// DynamicBorder, Spacer, IsKeyRepeat and MatchesKeyID) that this package
// papers over so the questionnaire gets the pi-tui shape: Text()/GetText,
// GetCursor() returning (line, col) instead of EditorCursor, a Keybindings
// interface + NewKeybindings + app.editor.external, MatchesKey and the Key*
// constants, pi-tui's Text(text, paddingX, paddingY, bg) and Container
// AddChild/RemoveChild/Clear, a Markdown without global-theme coupling, and an
// Editor without ActiveTheme/mouse/remote/status/autocomplete coupling.
// Delete this package when PiG exports those (core item C1); see README.md.
//
// Deviations from pi-tui 1.2.0 and PiG
//
//   - Pruned: mouse events, StatusIndicator/Loader, async and slash/file
//     autocomplete (SelectList popup, providers), remote/extension editor,
//     thinking-level/bash border colours, global theme (ActiveTheme),
//     async markdown transforms, alt-screen keybinding ids, input-history
//     key defaults beyond the tui.* table, Editor.Clear, the PiG TUI.
//   - NewEditor takes an EditorTheme (PiG's NewEditorWithTUI takes a TUI and
//     options); the tui argument is replaced by SetRowsProvider (visible-line cap max(5, floor(rows*0.3))) and
//     SetRequestRender (called by SetPaddingX).
//   - Editor.Render emits pi-tui 1.2.0's fake-cursor APC markers instead of
//     PiG's inline "\x1b[7m...\x1b[0m"; hosts run ResolveFakeCursors (which
//     yields "\x1b[7m...\x1b[27m" like TUI.resolveFakeCursors). PiG's D66 width-1
//     cursor divergence is removed.
//   - Editor.OnChange fires once per HandleInput when the text changed (plus
//     explicitly from SetText/InsertTextAtCursor/history), not after every
//     internal edit step; submitValue reports "" before OnSubmit as pi-tui does.
//   - Editor.GetCursor returns (line, col) with col in UTF-16 units.
//   - Tab is ignored (no provider), Editor.HandleInput follows pi-tui's
//     dispatch order exactly (undo, kill, history, cursor, newline, submit...).
//   - Markdown: nil theme means identity styling (pi-tui requires a theme);
//     the render cache is cleared by Invalidate; hyperlinks use
//     HyperlinksSupported (env detection incl. tmux probe) instead of PiG's
//     capability cache.
//   - Text/Container/Spacer are pi-tui's versions, not PiG's.
package tui
