package prompt

import (
	"regexp"
	"strings"
)

// formatPrompt reproduces omp's prompt.format(content, { renderPhase: "post-render" }).
// upstream: packages/utils/src/prompt.ts:192-308
func formatPrompt(content string) string {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))
	inCodeBlock := false

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t\r\n\f\v")
		s := 0
		for s < len(line) && (line[s] == ' ' || line[s] == '\t') {
			s++
		}

		if s < len(line) && (line[s] == '`' || line[s] == '~') &&
			(strings.HasPrefix(line[s:], "```") || strings.HasPrefix(line[s:], "~~~")) {
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
	return strings.TrimSpace(line) == ""
}

func compactTableRow(line string) string {
	cells := strings.Split(line, "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return strings.Join(cells, "|")
}

func compactTableSep(line string) string {
	rawCells := strings.Split(line, "|")
	normalized := make([]string, 0, len(rawCells))
	for _, c := range rawCells {
		trimmed := strings.TrimSpace(c)
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
