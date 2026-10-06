package tintinweb_subagents

import (
	"fmt"
	"strconv"
	"strings"
)

// parseFrontmatter reads the YAML frontmatter of an agent file: a mapping at the top of the file between two
// `---` lines. It supports the YAML the original's agent files use — plain, single- and double-quoted scalars,
// booleans, numbers and null, flow lists, block lists and block scalars (`|` and `>`) — and fails on anything
// else, as Pi's parser fails on bad YAML. upstream: pi-coding-agent parseFrontmatter (utils/frontmatter).
func parseFrontmatter(content string) (map[string]any, string, error) {
	content = strings.TrimPrefix(content, "\ufeff")
	norm := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	if !strings.HasPrefix(norm, "---") {
		return map[string]any{}, norm, nil
	}
	end := strings.Index(norm[3:], "\n---")
	if end < 0 {
		return map[string]any{}, norm, nil
	}
	yamlText := "" // JS slice(4, 3) of an empty block is ""
	if end+3 > 4 {
		yamlText = norm[4 : end+3]
	}
	body := strings.TrimSpace(norm[end+3+4:])
	m, err := parseYAMLMapping(yamlText)
	if err != nil {
		return nil, "", err
	}
	return m, body, nil
}

func parseYAMLMapping(text string) (map[string]any, error) {
	out := map[string]any{}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			return nil, fmt.Errorf("unexpected indentation at line %d", i+1)
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			return nil, fmt.Errorf("bad mapping line %d: %q", i+1, line)
		}
		key := strings.TrimSpace(line[:colon])
		if q := unquoteKey(key); q != "" {
			key = q
		}
		rest := strings.TrimSpace(line[colon+1:])
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		switch {
		case rest == "" || strings.HasPrefix(rest, "#"):
			// A block list or a nested value follows, or the value is null.
			var items []any
			j := i + 1
			for ; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if t == "" || strings.HasPrefix(t, "#") {
					continue
				}
				if (lines[j][0] == ' ' || lines[j][0] == '\t' || strings.HasPrefix(lines[j], "- ")) && strings.HasPrefix(t, "- ") {
					v, err := parseScalar(strings.TrimSpace(t[2:]))
					if err != nil {
						return nil, err
					}
					items = append(items, v)
					continue
				}
				if lines[j][0] == ' ' || lines[j][0] == '\t' {
					return nil, fmt.Errorf("nested mappings are not supported (line %d)", j+1)
				}
				break
			}
			if items != nil {
				out[key] = items
			} else {
				out[key] = nil
			}
			i = j - 1
		case rest[0] == '|' || rest[0] == '>':
			folded := rest[0] == '>'
			chomp := strings.Trim(rest[1:], " ")
			var block []string
			j := i + 1
			for ; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) != "" && lines[j][0] != ' ' && lines[j][0] != '\t' {
					break
				}
				block = append(block, lines[j])
			}
			out[key] = blockScalar(block, folded, chomp)
			i = j - 1
		default:
			if c := rest[0]; c != '"' && c != '\'' && c != '[' && strings.Contains(rest, ": ") {
				// A plain scalar cannot hold ": ": the yaml library reads it as a nested mapping and refuses.
				col := len(line) - len(strings.TrimLeft(line[colon+1:], " ")) + 1
				return nil, fmt.Errorf("Nested mappings are not allowed in compact mappings at line %d, column %d:\n\n%s\n", i+1, col, line)
			}
			v, err := parseScalar(rest)
			if err != nil {
				return nil, err
			}
			out[key] = v
		}
	}
	return out, nil
}

func unquoteKey(k string) string {
	if len(k) >= 2 && (k[0] == '"' && k[len(k)-1] == '"' || k[0] == '\'' && k[len(k)-1] == '\'') {
		return k[1 : len(k)-1]
	}
	return ""
}

func blockScalar(block []string, folded bool, chomp string) string {
	indent := -1
	for _, l := range block {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	var lines []string
	for _, l := range block {
		if len(l) >= indent && indent >= 0 {
			lines = append(lines, l[indent:])
		} else {
			lines = append(lines, "")
		}
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	text := strings.Join(lines, "\n")
	if folded {
		var b strings.Builder
		for i, l := range lines {
			if i > 0 {
				if l == "" || lines[i-1] == "" {
					b.WriteByte('\n')
				} else {
					b.WriteByte(' ')
				}
			}
			b.WriteString(l)
		}
		text = b.String()
	}
	if chomp == "-" {
		return text
	}
	return text + "\n"
}

func parseScalar(s string) (any, error) {
	if s == "" {
		return nil, nil
	}
	switch s[0] {
	case '"':
		end := strings.LastIndex(s, `"`)
		if end <= 0 {
			return nil, fmt.Errorf("unterminated string %s", s)
		}
		u, err := strconv.Unquote(s[:end+1])
		if err != nil {
			return nil, fmt.Errorf("bad double-quoted string %s", s)
		}
		return u, nil
	case '\'':
		end := strings.LastIndex(s, "'")
		if end <= 0 {
			return nil, fmt.Errorf("unterminated string %s", s)
		}
		return strings.ReplaceAll(s[1:end], "''", "'"), nil
	case '[':
		if !strings.HasSuffix(s, "]") {
			return nil, fmt.Errorf("unterminated list %s", s)
		}
		inner := strings.TrimSpace(s[1 : len(s)-1])
		items := []any{}
		if inner == "" {
			return items, nil
		}
		for _, part := range splitFlow(inner) {
			v, err := parseScalar(strings.TrimSpace(part))
			if err != nil {
				return nil, err
			}
			items = append(items, v)
		}
		return items, nil
	case '{', '&', '*', '!', '%', '@', '`':
		return nil, fmt.Errorf("unsupported YAML %s", s)
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	switch s {
	case "true", "True", "TRUE":
		return true, nil
	case "false", "False", "FALSE":
		return false, nil
	case "null", "Null", "NULL", "~":
		return nil, nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil && !strings.ContainsAny(s, "xXoObBeE_ ") {
		return n, nil
	}
	return s, nil
}

// splitFlow splits a flow list on top-level commas, keeping quoted commas.
func splitFlow(s string) []string {
	var parts []string
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == ',':
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}
