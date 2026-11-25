package prompt

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// isECMAWhitespace reports whether r is stripped by ECMAScript String.prototype.trimEnd
// / trimStart / \s (WhiteSpace + LineTerminator + BOM). Go's unicode.IsSpace includes
// U+0085 (NEL), which ECMAScript does not treat as whitespace.
func isECMAWhitespace(r rune) bool {
	if r <= 0x7f {
		switch r {
		case ' ', '\t', '\n', '\v', '\f', '\r':
			return true
		default:
			return false
		}
	}
	if r == 0x85 {
		return false
	}
	return r == 0xfeff || unicode.IsSpace(r)
}

func trimEndECMA(s string) string {
	return strings.TrimRightFunc(s, isECMAWhitespace)
}

func trimStartECMA(s string) string {
	return strings.TrimLeftFunc(s, isECMAWhitespace)
}

// lineIndent mirrors packages/utils/src/prompt.ts:203-209.
func lineIndent(line string) int {
	s := 0
	for s < len(line) && (line[s] == ' ' || line[s] == '\t') {
		s++
	}
	if s < len(line) && line[s] >= utf8.RuneSelf {
		return len(line) - len(trimStartECMA(line))
	}
	return s
}

func isFenceLine(line string, indent int) bool {
	if indent >= len(line) {
		return false
	}
	rest := line[indent:]
	return strings.HasPrefix(rest, "```") || strings.HasPrefix(rest, "~~~")
}

// CloseCodeFence appends a closing fence when text ends inside a code fence by
// formatPrompt's rule, so an unclosed fence in a context file never leaves the
// prompt after it unformatted.
// upstream: packages/utils/src/prompt.ts:220-228
func CloseCodeFence(text string) string {
	open := ""
	for _, line := range strings.Split(text, "\n") {
		indent := lineIndent(line)
		if isFenceLine(line, indent) {
			if open != "" {
				open = ""
			} else {
				open = line[indent : indent+3]
			}
		}
	}
	if open == "" {
		return text
	}
	if strings.HasSuffix(text, "\n") {
		return text + open
	}
	return text + "\n" + open
}

// formatPrompt reproduces omp's prompt.format(content, { renderPhase: "post-render" }).
// upstream: packages/utils/src/prompt.ts:230-350
func formatPrompt(content string) string {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))
	inCodeBlock := false

	for i := 0; i < len(lines); i++ {
		line := trimEndECMA(lines[i])
		s := lineIndent(line)

		if s < len(line) && (line[s] == '`' || line[s] == '~') && isFenceLine(line, s) {
			inCodeBlock = !inCodeBlock
			result = append(result, line)
			continue
		}

		if inCodeBlock {
			result = append(result, line)
			continue
		}

		isClosingLine := false
		if s < len(line) && line[s] == '<' {
			trimmedStart := line[s:]
			if strings.HasPrefix(trimmedStart, "</") && closingTagName(trimmedStart) != "" {
				isClosingLine = true
			}
		} else if s < len(line) && line[s] == '|' {
			trimmedStart := line[s:]
			if tableSepRe.MatchString(trimmedStart) {
				line = line[:s] + compactTableSep(trimmedStart)
			} else if tableRowRe.MatchString(trimmedStart) {
				line = line[:s] + compactTableRow(trimmedStart)
			}
		}

		if s >= len(line) {
			// Blank line: strip runs of 2+ consecutive blank lines entirely; preserve a single blank.
			if i+1 >= len(lines) || isBlankLine(lines[i+1]) {
				for len(result) > 0 && len(result[len(result)-1]) == 0 {
					result = result[:len(result)-1]
				}
				j := i + 1
				for j < len(lines) && isBlankLine(lines[j]) {
					j++
				}
				i = j - 1
				continue
			}
			if len(result) == 0 || len(result[len(result)-1]) == 0 {
				continue
			}
		}

		if isClosingLine {
			for len(result) > 0 && len(result[len(result)-1]) == 0 {
				result = result[:len(result)-1]
			}
		}

		result = append(result, line)
	}

	for len(result) > 0 && len(result[len(result)-1]) == 0 {
		result = result[:len(result)-1]
	}

	return strings.Join(result, "\n")
}

var (
	tableRowRe = regexp.MustCompile(`^\|.*\|$`)
	tableSepRe = regexp.MustCompile(`^\|[-:\s|]+\|$`)
)

// closingTagName mirrors packages/utils/src/prompt.ts:23-31.
func closingTagName(s string) string {
	n := len(s)
	if n < 4 || s[n-1] != '>' || !strings.HasPrefix(s, "</") {
		return ""
	}
	for j := 2; j < n-1; j++ {
		c := s[j]
		if !((c >= 'a' && c <= 'z') || c == '-' || c == '_') {
			return ""
		}
	}
	return s[2 : n-1]
}

func isBlankLine(line string) bool {
	return trimStartECMA(line) == ""
}

func compactTableRow(line string) string {
	cells := strings.Split(line, "|")
	for i, c := range cells {
		cells[i] = strings.TrimFunc(c, isECMAWhitespace)
	}
	return strings.Join(cells, "|")
}

func compactTableSep(line string) string {
	rawCells := strings.Split(line, "|")
	normalized := make([]string, 0, len(rawCells))
	for _, c := range rawCells {
		trimmed := strings.TrimFunc(c, isECMAWhitespace)
		if trimmed == "" {
			continue
		}
		left := strings.HasPrefix(trimmed, ":")
		right := strings.HasSuffix(trimmed, ":")
		switch {
		case left && right:
			normalized = append(normalized, ":---:")
		case left:
			normalized = append(normalized, ":---")
		case right:
			normalized = append(normalized, "---:")
		default:
			normalized = append(normalized, "---")
		}
	}
	return "|" + strings.Join(normalized, "|") + "|"
}
