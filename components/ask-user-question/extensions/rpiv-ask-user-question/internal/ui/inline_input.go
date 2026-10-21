package ui

import (
	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Grapheme-aware extraction at the cursor: pi-tui's Editor reports UTF-16 line/column positions, so the cursor
// can land between code units of one cluster (emoji, ZWJ, combining marks). Slicing by one code unit would split
// the cluster across the SGR 7/27 boundary. Both single- and multi-select views share this cursor-building core.
// upstream: view/components/inline-input.ts.

// RenderInlineInputOptions are the inputs of RenderInlineInputRow.
type RenderInlineInputOptions struct {
	// Buffer is the live inline-input buffer.
	Buffer string
	// CursorOffset is the cursor offset in UTF-16 code units; nil or out of range falls back to end-of-buffer.
	CursorOffset *int
	// RowPrefix is the prefix of the first emitted line (e.g. `❯ 4. `).
	RowPrefix string
	// ContinuationPrefix is the prefix of continuation lines (whitespace of equal visible width).
	ContinuationPrefix string
	// ContentWidth is the visible columns available for the buffer content (width minus prefix width).
	ContentWidth int
	// SelectedText styles each emitted line.
	SelectedText func(string) string
}

// resolveCursorOffset resolves the cursor offset, falling back to end-of-buffer for nil or out-of-range.
func resolveCursorOffset(buffer string, requested *int) int {
	n := jsstring.Length(buffer)
	if requested != nil && *requested >= 0 && *requested <= n {
		return *requested
	}
	return n
}

// buildCursorRaw builds the cursor-marked raw string for the whole buffer: before, CURSOR_MARKER, an SGR-7
// reverse-video cell, SGR-27, after. The cell under the cursor is the single grapheme at the offset (or U+00A0 at
// end-of-buffer or on a literal space: NBSP is wrap-safe where a literal space would tokenize as a wrap break).
func buildCursorRaw(buffer string, offset int) string {
	before := jsstring.Slice(buffer, 0, offset)
	rawAt, _ := widthx.FirstGrapheme(jsstring.Slice(buffer, offset))
	// A logical newline has no visible cell. Draw the cursor on an NBSP immediately before it and leave the
	// newline unconsumed so the next logical line still renders.
	cursorAtLineEnd := rawAt == "\n"
	atCursor := rawAt
	if rawAt == "" || rawAt == " " || cursorAtLineEnd {
		atCursor = "\u00a0"
	}
	consumed := jsstring.Length(rawAt)
	if cursorAtLineEnd {
		consumed = 0
	}
	after := jsstring.Slice(buffer, offset+consumed)
	return before + widthx.CursorMarker + "\x1b[7m" + atCursor + "\x1b[27m" + after
}

// RenderInlineInputRow renders the inline editor across logical and visually wrapped lines. Cursor visualization
// follows Pi's editor pattern: reverse-video on the cell at the cursor, with a non-breaking-space cell at
// end-of-line or end-of-buffer.
func RenderInlineInputRow(opts RenderInlineInputOptions) []string {
	raw := buildCursorRaw(opts.Buffer, resolveCursorOffset(opts.Buffer, opts.CursorOffset))
	wrapped := widthx.WrapTextWithAnsi(raw, opts.ContentWidth)
	out := make([]string, len(wrapped))
	for index, segment := range wrapped {
		prefix := opts.ContinuationPrefix
		if index == 0 {
			prefix = opts.RowPrefix
		}
		out[index] = opts.SelectedText(prefix + segment)
	}
	return out
}
