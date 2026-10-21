package pi_permission_system

import (
	"strings"
	"unicode/utf8"
)

// Port of src/tool-input/tool-input-preview.ts: the inline text helpers and the serialization of a tool input for a prompt (shown
// unredacted, so the approver sees what will run) and for the review log (sensitive-keyed values masked).

// ToolInputPreviewMaxLength and ToolTextSummaryMaxLength are the built-in preview limits.
const (
	ToolInputPreviewMaxLength = 200
	ToolTextSummaryMaxLength  = 80
)

// safeJSONStringify is the serializer the prompt preview delegates to (the original's imported safeJsonStringify); tests replace
// it where the original's tests mock that module.
var safeJSONStringify = SafeJSONStringify

// TruncateInlineText is truncateInlineText: the text unchanged when it is at most maxLength UTF-16 units, else its first maxLength
// units and an ellipsis.
func TruncateInlineText(value string, maxLength float64) string {
	n := utf16LenWTF(value)
	if float64(n) <= maxLength {
		return value
	}
	return toWellFormed(utf16Prefix(value, jsSliceEnd(maxLength, n)) + "\u2026")
}

// CountTextLines is countTextLines: 0 for an empty string, else the number of lines split on CRLF, CR or LF.
func CountTextLines(value string) int {
	if value == "" {
		return 0
	}
	lines := 1
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\r':
			if i+1 < len(value) && value[i+1] == '\n' {
				i++
			}
			lines++
		case '\n':
			lines++
		}
	}
	return lines
}

// FormatCount is formatCount: `1 line`, `0 lines`.
func FormatCount(value int, singular, plural string) string {
	word := plural
	if value == 1 {
		word = singular
	}
	return jsNumber(float64(value)) + " " + word
}

// SerializeToolInputPreview serializes tool input for display in a permission prompt, unredacted.
func SerializeToolInputPreview(input any) string {
	return normalizeSerializedPreview(safeJSONStringify(input))
}

// SerializeRedactedToolInputPreview serializes tool input for the review log, masking sensitive-keyed values. The log path needs
// its own entry point because the input is flattened to a string here: by the time it reaches the writer its keys are gone.
func SerializeRedactedToolInputPreview(input any) string {
	return normalizeSerializedPreview(RedactedJSONStringify(input))
}

func normalizeSerializedPreview(serialized string, ok bool) string {
	if !ok || serialized == "" || serialized == "{}" || serialized == "null" {
		return ""
	}
	return jsTrim(collapseJSWhitespace(serialized))
}

// collapseJSWhitespace is s.replace(/\s+/g, " "): every run of JavaScript whitespace becomes one space. A string with nothing to
// rewrite (no run but single spaces, the usual JSON preview) is returned as it is; otherwise the unchanged stretches are copied whole.
func collapseJSWhitespace(s string) string {
	var b strings.Builder
	copied := 0 // s[:copied] is already in b
	for i := 0; i < len(s); {
		c := s[i]
		if c > ' ' && c < utf8.RuneSelf { // a printable ASCII byte is never whitespace
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isJSSpace(r) {
			i += size
			continue
		}
		j := i + size
		for j < len(s) {
			r2, size2 := utf8.DecodeRuneInString(s[j:])
			if !isJSSpace(r2) {
				break
			}
			j += size2
		}
		if j-i == 1 && c == ' ' { // already one space
			i = j
			continue
		}
		if b.Cap() == 0 {
			b.Grow(len(s))
		}
		b.WriteString(s[copied:i])
		b.WriteByte(' ')
		copied, i = j, j
	}
	if copied == 0 {
		return s
	}
	b.WriteString(s[copied:])
	return b.String()
}
