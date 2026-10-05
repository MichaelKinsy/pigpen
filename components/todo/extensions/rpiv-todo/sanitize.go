package rpiv_todo

import "regexp"

// The patterns are the original's, in the same order. Go's regexp has no lookahead, and none is used.
var (
	// CSI sequences, via both ESC-[ and the C1 single-byte introducer.
	csiPattern = regexp.MustCompile("(?:\u001b\\[|\u009b)[0-?]*[ -/]*[@-~]")
	// OSC sequences with their payload; an unterminated OSC swallows the rest of the string.
	oscPattern = regexp.MustCompile("(?:\u001b\\]|\u009d)[^\u0007\u009c\u001b]*(?:\u0007|\u009c|\u001b\\\\)?")
	// Any remaining two-character ESC sequence. JavaScript's `.` (no s flag) does not match a line
	// terminator, so an ESC before \n, \r, U+2028 or U+2029 is left for the passes below.
	escPattern = regexp.MustCompile("\u001b[^\n\r\u2028\u2029]")
	// Unicode line and paragraph separators join lines like \n does below.
	separatorPattern = regexp.MustCompile("[\u2028\u2029]")
	controlPattern   = regexp.MustCompile("[\u0000-\u001f\u007f-\u009f]")
	// Bidi embedding, override and isolate controls and the LRM and RLM marks.
	bidiPattern = regexp.MustCompile("[\u200e\u200f\u202a-\u202e\u2066-\u2069]")
)

// sanitizeTerminalText removes terminal control characters from model-controlled task text before it
// reaches the terminal: complete CSI and OSC sequences are dropped whole, newlines and tabs become spaces
// so a field cannot change the layout, and bidi controls are removed. upstream: tool/sanitize.ts:9-26.
func sanitizeTerminalText(value string) string {
	value = csiPattern.ReplaceAllString(value, "")
	value = oscPattern.ReplaceAllString(value, "")
	value = escPattern.ReplaceAllString(value, "")
	value = separatorPattern.ReplaceAllString(value, " ")
	value = controlPattern.ReplaceAllStringFunc(value, func(c string) string {
		if c == "\n" || c == "\r" || c == "\t" {
			return " "
		}
		return ""
	})
	return bidiPattern.ReplaceAllString(value, "")
}
