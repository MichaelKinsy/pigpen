package hashline

import (
	"fmt"
	"strings"
)

// BOM is the UTF-8 byte order mark.
// upstream: crates/edit/src/text.rs:57
const BOM = "\uFEFF"

// LineEnding is the line-ending style of a file.
// upstream: crates/edit/src/text.rs:12
type LineEnding int

const (
	LineEndingLF LineEnding = iota
	LineEndingCRLF
)

// DetectLineEnding detects the first line ending style in content; LF when neither is present.
// upstream: crates/edit/src/text.rs:28
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

// NormalizeToLF normalizes every \r\n to \n. A lone \r (e.g. progress-bar logs)
// stays content, matching grep, wc -l, and omp v19.1.1.
// upstream: crates/edit/src/text.rs:41, packages/coding-agent/src/edit/normalize.ts:12
func NormalizeToLF(text string) string {
	if !strings.Contains(text, "\r\n") {
		return text
	}
	return strings.ReplaceAll(text, "\r\n", "\n")
}

// RestoreLineEndings re-encodes LF text with the requested line ending.
// upstream: crates/edit/src/text.rs:49
func RestoreLineEndings(text string, ending LineEnding) string {
	if ending == LineEndingCRLF {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

// StripBOM splits a leading UTF-8 BOM off content.
// upstream: crates/edit/src/text.rs:60
func StripBOM(content string) (bom, rest string) {
	if strings.HasPrefix(content, BOM) {
		return BOM, content[len(BOM):]
	}
	return "", content
}

// FileHash computes the four-hex uppercase hashline content tag.
// It hashes text with trailing spaces, tabs, and CRs stripped from every line.
// upstream: crates/edit/src/store.rs:87
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

// utf16Len returns the length in UTF-16 code units (JS string.length).
// upstream: crates/edit/src/text.rs:98
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// similarity computes Levenshtein similarity in [0, 1] over Unicode scalar values.
// upstream: crates/edit/src/fuzzy.rs:434
func similarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if a == "" || b == "" {
		return 0.0
	}
	ar := []rune(a)
	br := []rune(b)
	maxLen := len(ar)
	if len(br) > maxLen {
		maxLen = len(br)
	}
	dist := levenshteinRunes(ar, br)
	return 1.0 - float64(dist)/float64(maxLen)
}

// levenshteinRunes matches crates/edit/src/fuzzy.rs:206.
func levenshteinRunes(a, b []rune) int {
	start := 0
	limit := min(len(a), len(b))
	for start < limit && a[start] == b[start] {
		start++
	}
	aEnd := len(a)
	bEnd := len(b)
	for aEnd > start && bEnd > start && a[aEnd-1] == b[bEnd-1] {
		aEnd--
		bEnd--
	}
	longer := a[start:aEnd]
	shorter := b[start:bEnd]
	if len(longer) == 0 {
		return len(shorter)
	}
	if len(shorter) == 0 {
		return len(longer)
	}
	if len(shorter) > len(longer) {
		longer, shorter = shorter, longer
	}
	row := make([]int, len(shorter)+1)
	for i := range row {
		row[i] = i
	}
	for i, ae := range longer {
		diag := row[0]
		row[0] = i + 1
		for j, be := range shorter {
			cell := j + 1
			above := row[cell]
			if ae == be {
				row[cell] = diag
			} else {
				m := above + 1
				if row[cell-1]+1 < m {
					m = row[cell-1] + 1
				}
				if diag+1 < m {
					m = diag + 1
				}
				row[cell] = m
			}
			diag = above
		}
	}
	return row[len(shorter)]
}
