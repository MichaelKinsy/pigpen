package debug

import (
	"fmt"
	"strconv"
	"strings"
)

// parseSimpleYAML parses the subset of YAML used in dap.yaml / dap.yml config
// files (nested mappings, block/flow sequences, scalars, comments) using only
// the Go standard library.
func parseSimpleYAML(input string) (any, error) {
	rawLines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	type yamlLine struct {
		indent int
		text   string
	}
	var lines []yamlLine
	for _, raw := range rawLines {
		cleaned := stripYAMLComment(raw)
		trimmed := strings.TrimSpace(cleaned)
		if trimmed == "" || trimmed == "---" || trimmed == "..." {
			continue
		}
		indent := 0
		for indent < len(cleaned) && cleaned[indent] == ' ' {
			indent++
		}
		if indent < len(cleaned) && cleaned[indent] == '\t' {
			return nil, fmt.Errorf("tabs are not supported for YAML indentation")
		}
		lines = append(lines, yamlLine{indent: indent, text: trimmed})
	}
	if len(lines) == 0 {
		return map[string]any{}, nil
	}

	var parseBlock func(idx int, minIndent int) (any, int, error)
	parseBlock = func(idx int, minIndent int) (any, int, error) {
		if idx >= len(lines) {
			return nil, idx, nil
		}
		baseIndent := lines[idx].indent
		if baseIndent < minIndent {
			return nil, idx, nil
		}
		if strings.HasPrefix(lines[idx].text, "- ") || lines[idx].text == "-" {
			var seq []any
			for idx < len(lines) && lines[idx].indent == baseIndent && (strings.HasPrefix(lines[idx].text, "- ") || lines[idx].text == "-") {
				rest := ""
				if len(lines[idx].text) > 1 {
					rest = strings.TrimSpace(lines[idx].text[1:])
				}
				idx++
				if rest == "" {
					if idx < len(lines) && lines[idx].indent > baseIndent {
						val, nextIdx, err := parseBlock(idx, baseIndent+1)
						if err != nil {
							return nil, idx, err
						}
						seq = append(seq, val)
						idx = nextIdx
					} else {
						seq = append(seq, nil)
					}
					continue
				}
				seq = append(seq, parseYAMLScalarOrFlow(rest))
			}
			return seq, idx, nil
		}

		obj := make(map[string]any)
		for idx < len(lines) && lines[idx].indent == baseIndent {
			line := lines[idx].text
			if strings.HasPrefix(line, "- ") || line == "-" {
				break
			}
			key, rest, ok := splitYAMLKeyValue(line)
			if !ok {
				return nil, idx, fmt.Errorf("invalid YAML mapping entry: %q", line)
			}
			idx++
			if rest == "" {
				if idx < len(lines) && lines[idx].indent > baseIndent {
					val, nextIdx, err := parseBlock(idx, baseIndent+1)
					if err != nil {
						return nil, idx, err
					}
					obj[key] = val
					idx = nextIdx
				} else {
					obj[key] = nil
				}
				continue
			}
			obj[key] = parseYAMLScalarOrFlow(rest)
		}
		return obj, idx, nil
	}

	val, _, err := parseBlock(0, 0)
	if err != nil {
		return nil, err
	}
	return val, nil
}

func stripYAMLComment(line string) string {
	inSingle := false
	inDouble := false
	escaped := false
	for i := range len(line) {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if inDouble && ch == '\\' {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if ch == '#' && !inSingle && !inDouble {
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				return line[:i]
			}
		}
	}
	return line
}

func splitYAMLKeyValue(line string) (key, rest string, ok bool) {
	inSingle := false
	inDouble := false
	escaped := false
	for i := range len(line) {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if inDouble && ch == '\\' {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if ch == ':' && !inSingle && !inDouble {
			if i+1 == len(line) || line[i+1] == ' ' || line[i+1] == '\t' {
				rawKey := strings.TrimSpace(line[:i])
				key = unquoteYAMLScalar(rawKey)
				rest = strings.TrimSpace(line[i+1:])
				return key, rest, true
			}
		}
	}
	return "", "", false
}

func parseYAMLScalarOrFlow(s string) any {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		inner := strings.TrimSpace(s[1 : len(s)-1])
		if inner == "" {
			return []any{}
		}
		parts := splitYAMLFlowItems(inner)
		out := make([]any, 0, len(parts))
		for _, p := range parts {
			out = append(out, parseYAMLScalarOrFlow(p))
		}
		return out
	}
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		inner := strings.TrimSpace(s[1 : len(s)-1])
		out := make(map[string]any)
		if inner == "" {
			return out
		}
		for _, item := range splitYAMLFlowItems(inner) {
			if k, v, ok := splitYAMLKeyValue(item); ok {
				out[k] = parseYAMLScalarOrFlow(v)
			}
		}
		return out
	}
	if (strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2) ||
		(strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2) {
		return unquoteYAMLScalar(s)
	}
	switch s {
	case "true", "True", "TRUE":
		return true
	case "false", "False", "FALSE":
		return false
	case "null", "Null", "NULL", "~":
		return nil
	}
	if iv, err := strconv.ParseInt(s, 10, 64); err == nil {
		return float64(iv)
	}
	if fv, err := strconv.ParseFloat(s, 64); err == nil {
		return fv
	}
	return s
}

func splitYAMLFlowItems(s string) []string {
	var parts []string
	inSingle := false
	inDouble := false
	escaped := false
	depth := 0
	start := 0
	for i := range len(s) {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if inDouble && ch == '\\' {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
		} else if ch == '"' && !inSingle {
			inDouble = !inDouble
		} else if !inSingle && !inDouble {
			switch ch {
			case '[', '{':
				depth++
			case ']', '}':
				if depth > 0 {
					depth--
				}
			case ',':
				if depth == 0 {
					parts = append(parts, strings.TrimSpace(s[start:i]))
					start = i + 1
				}
			}
		}
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	return parts
}

func unquoteYAMLScalar(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if unq, err := strconv.Unquote(s); err == nil {
			return unq
		}
		return s[1 : len(s)-1]
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}
