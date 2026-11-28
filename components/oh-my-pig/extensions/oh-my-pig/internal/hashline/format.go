package hashline

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Hashline syntax constants and display helpers.
// upstream: crates/edit/src/modes/hashline/format.rs

const (
	HLFilePrefix     = "["
	HLFileSuffix     = "]"
	HLPayloadReplace = "+"
	HLPutKeyword     = "PUT"
	HLCutKeyword     = "CUT"
	HLRemKeyword     = "REM"
	HLMoveKeyword    = "MV"
	HLHeaderColon    = ":"
	HLGapBefore      = "<"
	HLGapAfter       = ">"
	HLBlockSuffix    = "*"
	HLEOFAnchor      = "$"
	HLRegisterSigil  = "@"
	HLFileHashSep    = "#"
	HLRangeSep       = ".="
	HLLineBodySep    = ":"
	HLFileHashLength = 4
)

// HLFileHashExamples are representative snapshot tags used in diagnostics.
// upstream: crates/edit/src/modes/hashline/format.rs:52
var HLFileHashExamples = [3]string{"1A2B", "3C4D", "9F3E"}

// FormatReplaceHeader formats a concrete replacement hunk header.
// upstream: crates/edit/src/modes/hashline/format.rs:55
func FormatReplaceHeader(start, end uint32) string {
	return fmt.Sprintf("PUT %d.=%d:", start, end)
}

// FormatCutHeader formats a concrete cut hunk header.
// upstream: crates/edit/src/modes/hashline/format.rs:59
func FormatCutHeader(start, end uint32) string {
	return fmt.Sprintf("CUT %d.=%d", start, end)
}

// FormatGapLocator formats a gap locator.
// upstream: crates/edit/src/modes/hashline/format.rs:63
func FormatGapLocator(cursor Cursor) string {
	switch cursor.Kind {
	case CursorBof:
		return "<1"
	case CursorEof:
		return ">$"
	case CursorBeforeAnchor:
		return fmt.Sprintf("<%d", cursor.Anchor.Line)
	case CursorAfterAnchor:
		return fmt.Sprintf(">%d", cursor.Anchor.Line)
	default:
		return ""
	}
}

// FormatInsertHeader formats an insertion hunk header.
// upstream: crates/edit/src/modes/hashline/format.rs:72
func FormatInsertHeader(cursor Cursor) string {
	return "PUT " + FormatGapLocator(cursor) + ":"
}

// FormatRegister formats a named clipboard register.
// upstream: crates/edit/src/modes/hashline/format.rs:76
func FormatRegister(name string) string {
	return "@" + name
}

// DescribeAnchorExamples formats representative line anchors for a diagnostic.
// upstream: crates/edit/src/modes/hashline/format.rs:80
func DescribeAnchorExamples(linePrefix string) string {
	var examples [3]string
	if linePrefix == "" {
		examples = [3]string{"160", "42", "7"}
	} else {
		runes := []rune(linePrefix)
		shortened := "4"
		if len(runes) > 1 {
			shortened = string(runes[:len(runes)-1])
		}
		examples = [3]string{linePrefix, shortened + "2", "7"}
	}
	return fmt.Sprintf("%q, %q, %q", examples[0], examples[1], examples[2])
}

// FormatHeader formats a hashline file section header: [path#TAG].
// upstream: crates/edit/src/modes/hashline/format.rs:97
func FormatHeader(path, tag string) string {
	return "[" + path + "#" + tag + "]"
}

// FormatNumberedLine formats one numbered source line: N:line.
// upstream: crates/edit/src/modes/hashline/format.rs:101
func FormatNumberedLine(lineNumber int, line string) string {
	return strconv.Itoa(lineNumber) + ":" + line
}

// formatNumberedLineU32 formats one uint32 numbered source line.
// upstream: crates/edit/src/modes/hashline/format.rs:108
func formatNumberedLineU32(lineNumber uint32, line string) string {
	return strconv.FormatUint(uint64(lineNumber), 10) + ":" + line
}

// SplitAddressableFileLines splits text into lines addressable by hashline anchors.
// A trailing newline does not produce a phantom blank line.
// upstream: crates/edit/src/modes/hashline/format.rs:112
func SplitAddressableFileLines(text string) []string {
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// FormatNumberedLines formats every LF-delimited row, including a terminal blank sentinel.
// upstream: crates/edit/src/modes/hashline/format.rs:120
func FormatNumberedLines(text string, startLine int) string {
	var start uint32
	if startLine > 0 {
		if uint64(startLine) > math.MaxUint32 {
			start = math.MaxUint32
		} else {
			start = uint32(startLine)
		}
	}
	return FormatNumberedLinesU32(text, start)
}

// FormatNumberedLinesU32 matches crates/edit/src/modes/hashline/format.rs:120 with u32 saturation.
func FormatNumberedLinesU32(text string, startLine uint32) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder
	b.Grow(len(text) + len(lines)*6)
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		var num uint32
		if uint64(startLine)+uint64(i) > math.MaxUint32 {
			num = math.MaxUint32
		} else {
			num = startLine + uint32(i)
		}
		b.WriteString(strconv.FormatUint(uint64(num), 10))
		b.WriteByte(':')
		b.WriteString(line)
	}
	return b.String()
}
