package ui

// Theme is the part of pi-coding-agent's Theme the questionnaire uses: foreground and background colour by token,
// and bold. The root package adapts the host's sdk.UITheme to it. upstream: `Theme` in view and state files.
type Theme interface {
	Fg(color, text string) string
	Bg(color, text string) string
	Bold(text string) string
	Italic(text string) string
	Underline(text string) string
	Strikethrough(text string) string
	Inverse(text string) string
}

// TUI is the part of pi-tui's TUI the questionnaire uses. upstream: `TUI` (requestRender, terminal.columns,
// terminal.rows, and stop/start for the external editor).
type TUI interface {
	RequestRender()
	Columns() int
	Rows() int
}
