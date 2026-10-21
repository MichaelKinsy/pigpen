package tui

import "slices"

// GetCursor returns the logical cursor line and column. The column counts
// UTF-16 code units, like pi-tui's getCursor (cursorCol), not terminal cells.
func (e *Editor) GetCursor() (line, col int) { return e.cursor[0], e.cursor[1] }

// GetLines returns a copy of the editor's logical lines.
func (e *Editor) GetLines() []string { return slices.Clone(e.lines) }
