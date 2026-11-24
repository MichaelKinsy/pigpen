package hashline

import (
	"fmt"
	"strconv"
	"strings"
)

// BOM is the UTF-8 byte order mark.
// upstream: crates/pi-edit/src/text.rs:67
const BOM = "\uFEFF"

// LineEnding is the line-ending style of a file.
// upstream: crates/pi-edit/src/text.rs:12
type LineEnding int

const (
	LineEndingLF LineEnding = iota
	LineEndingCRLF
)

// DetectLineEnding detects the first line ending style in content; LF when neither is present.
// upstream: crates/pi-edit/src/text.rs:28
func DetectLineEnding(content string) LineEnding {
	lf := strings.IndexByte(content, '\n')
	if lf < 0 {
		return LineEndingLF
	}
	crlf := strings.Index(content, "\r\n")
	if crlf >= 0 && crlf < lf {
		return LineEndingCRLF
	}
	return LineEndingLF
}

// NormalizeToLF converts every \r\n and lone \r to \n.
// upstream: crates/pi-edit/src/text.rs:39
func NormalizeToLF(text string) string {
	if !strings.ContainsRune(text, '\r') {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] == '\r' {
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			b.WriteByte('\n')
		} else {
			b.WriteByte(text[i])
		}
	}
	return b.String()
}

// RestoreLineEndings re-encodes LF text with the requested line ending.
// upstream: crates/pi-edit/src/text.rs:59
func RestoreLineEndings(text string, ending LineEnding) string {
	if ending == LineEndingCRLF {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

// StripBOM splits a leading UTF-8 BOM off content.
// upstream: crates/pi-edit/src/text.rs:70
func StripBOM(content string) (bom, rest string) {
	if strings.HasPrefix(content, BOM) {
		return BOM, content[len(BOM):]
	}
	return "", content
}

// FileHash computes the four-hex uppercase hashline content tag.
// It hashes text with trailing spaces, tabs, and CRs stripped from every line.
// upstream: crates/pi-edit/src/store.rs:79
func FileHash(text string) string {
	h := newXXH32(0)
	runStart := 0
	lineStart := 0
	for lineStart < len(text) {
		nl := strings.IndexByte(text[lineStart:], '\n')
		var segEnd, lineEnd int
		if nl >= 0 {
			lineEnd = lineStart + nl
			segEnd = lineEnd + 1
		} else {
			lineEnd = len(text)
			segEnd = len(text)
		}
		kept := lineEnd
		for kept > lineStart {
			c := text[kept-1]
			if c == ' ' || c == '\t' || c == '\r' {
				kept--
			} else {
				break
			}
		}
		if kept < lineEnd {
			h.update([]byte(text[runStart:kept]))
			runStart = lineEnd
		}
		lineStart = segEnd
	}
	h.update([]byte(text[runStart:]))
	return fmt.Sprintf("%04X", h.digest()&0xffff)
}

// FormatHeader formats a hashline file section header: [path#TAG].
// upstream: crates/pi-edit/src/modes/hashline/format.rs:97
func FormatHeader(path, tag string) string {
	return "[" + path + "#" + tag + "]"
}

// FormatNumberedLine formats one numbered source line: N:line.
// upstream: crates/pi-edit/src/modes/hashline/format.rs:101
func FormatNumberedLine(lineNumber int, line string) string {
	return strconv.Itoa(lineNumber) + ":" + line
}

// FormatNumberedLines formats every LF-delimited row starting at startLine.
// upstream: crates/pi-edit/src/modes/hashline/format.rs:120
func FormatNumberedLines(text string, startLine int) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder
	b.Grow(len(text) + len(lines)*6)
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(startLine + i))
		b.WriteByte(':')
		b.WriteString(line)
	}
	return b.String()
}

// SplitAddressableFileLines splits text into lines addressable by hashline anchors.
// A trailing newline does not produce a phantom blank line.
// upstream: crates/pi-edit/src/modes/hashline/format.rs:112
func SplitAddressableFileLines(text string) []string {
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}
