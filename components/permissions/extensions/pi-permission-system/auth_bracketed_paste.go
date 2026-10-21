package pi_permission_system

import "strings"

// Port of src/authority/bracketed-paste.ts: bracketed-paste normalization for the inline permission dialog's reason field. A terminal
// in bracketed-paste mode wraps pasted text in these markers, and the TUI hands the wrapped chunk to the focused component in a
// single call.

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// CollapsePastedNewlines collapses newline runs inside a bracketed-paste chunk to single spaces. The markers are preserved so the
// editor still recognizes the chunk as a paste, and anything that is not a complete paste chunk is returned unchanged.
func CollapsePastedNewlines(data string) string {
	start := strings.Index(data, pasteStart)
	if start == -1 {
		return data
	}
	contentStart := start + len(pasteStart)
	rel := strings.Index(data[contentStart:], pasteEnd)
	if rel == -1 {
		return data
	}
	contentEnd := contentStart + rel
	var b strings.Builder
	b.Grow(len(data))
	b.WriteString(data[:contentStart])
	content := data[contentStart:contentEnd]
	for i := 0; i < len(content); {
		if content[i] == '\r' || content[i] == '\n' {
			for i < len(content) && (content[i] == '\r' || content[i] == '\n') {
				i++
			}
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(content[i])
		i++
	}
	b.WriteString(data[contentEnd:])
	return b.String()
}
