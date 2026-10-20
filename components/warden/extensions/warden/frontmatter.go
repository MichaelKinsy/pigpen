package warden

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Skill frontmatter (pi-coding-agent utils/frontmatter.ts). The host reads the block between the first two `---`
// lines with a full YAML parser; load mode needs three top-level keys of it (name, description,
// disable-model-invocation), so this reads the top-level mapping of the YAML subset skill files use: plain, single
// and double quoted scalars (also across lines), `|` and `>` block scalars, comments, and nested or flow values
// (kept as non-string values, since only strings and `true` matter here). A document it cannot read is an error,
// as invalid YAML is for the host (load mode then reports load_failed).

var errFrontmatter = errors.New("invalid frontmatter")

// parseFrontmatter returns the top-level frontmatter values and the body, trimmed (extractFrontmatter).
func parseFrontmatter(content string) (map[string]any, string, error) {
	n := strings.TrimPrefix(content, "\uFEFF")
	n = strings.ReplaceAll(n, "\r\n", "\n")
	n = strings.ReplaceAll(n, "\r", "\n")
	if !strings.HasPrefix(n, "---") {
		return map[string]any{}, n, nil
	}
	end := strings.Index(n[3:], "\n---")
	if end < 0 {
		return map[string]any{}, n, nil
	}
	end += 3
	yamlString := n[4:max(4, end)]
	if end < 4 {
		yamlString = ""
	}
	body := jsTrim(n[end+4:])
	if yamlString == "" {
		return map[string]any{}, body, nil
	}
	fm, err := parseYAMLMapping(yamlString)
	return fm, body, err
}

var (
	reYAMLKey      = lazyRE(`^([^\s#'"\-?:\[\]{},&*!|>%@` + "`" + `][^:#]*?|"(?:[^"\\]|\\.)*"|'(?:[^']|'')*')\s*:(?:\s+(.*))?$`)
	reYAMLInt      = lazyRE(`^[-+]?(?:[0-9]+|0o[0-7]+|0x[0-9a-fA-F]+)$`)
	reYAMLFloat    = lazyRE(`^[-+]?(?:\.[0-9]+|[0-9]+(?:\.[0-9]*)?)(?:[eE][-+]?[0-9]+)?$`)
	reYAMLBlockHdr = lazyRE(`^([|>])([0-9]?[+-]?|[+-]?[0-9]?)(?:\s+#.*)?$`)
)

func yamlIndent(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

func yamlBlank(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// parseYAMLMapping reads the top-level `key: value` lines.
func parseYAMLMapping(src string) (map[string]any, error) {
	lines := strings.Split(src, "\n")
	out := map[string]any{}
	for i := 0; i < len(lines); {
		line := lines[i]
		if yamlBlank(line) {
			i++
			continue
		}
		if yamlIndent(line) != 0 || strings.ContainsRune(line, '\t') && strings.HasPrefix(line, "\t") {
			return nil, errFrontmatter
		}
		m := reYAMLKey.FindStringSubmatch(line)
		if m == nil {
			return nil, errFrontmatter
		}
		key, err := yamlKey(m[1])
		if err != nil {
			return nil, err
		}
		rest := strings.TrimSpace(m[2])
		i++
		// The lines that belong to this key: indented lines, blanks and comments inside, and `- ` items at column 0.
		start := i
		for i < len(lines) && (yamlBlank(lines[i]) || yamlIndent(lines[i]) > 0 || strings.HasPrefix(lines[i], "- ") || lines[i] == "-") {
			i++
		}
		end := i
		for end > start && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		block := lines[start:end]
		value, err := yamlValue(rest, block)
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}

func yamlKey(raw string) (string, error) {
	switch {
	case strings.HasPrefix(raw, `"`):
		return yamlDouble(raw)
	case strings.HasPrefix(raw, `'`):
		return strings.ReplaceAll(raw[1:len(raw)-1], "''", "'"), nil
	}
	return strings.TrimSpace(raw), nil
}

// yamlValue resolves one value: rest is what follows the colon, block the lines nested under the key.
func yamlValue(rest string, block []string) (any, error) {
	if rest == "" || strings.HasPrefix(rest, "#") {
		if hasContent(block) {
			return []any{}, nil // a nested mapping or list: not a string, not true
		}
		return nil, nil
	}
	if m := reYAMLBlockHdr.FindStringSubmatch(rest); m != nil {
		return yamlBlockScalar(m[1] == ">", m[2], block), nil
	}
	switch rest[0] {
	case '"':
		joined := rest
		for _, l := range block {
			joined += "\n" + l
		}
		s, tail, err := yamlDoubleFolded(joined)
		if err != nil || !yamlTailOK(tail) {
			return nil, errFrontmatter
		}
		return s, nil
	case '\'':
		joined := rest
		for _, l := range block {
			joined += "\n" + l
		}
		s, tail, ok := yamlSingleFolded(joined)
		if !ok || !yamlTailOK(tail) {
			return nil, errFrontmatter
		}
		return s, nil
	case '[', '{':
		return []any{}, nil
	case '&', '*', '!':
		return nil, errFrontmatter
	}
	if hasContent(block) && strings.HasPrefix(strings.TrimSpace(firstContent(block)), "- ") {
		return nil, errFrontmatter
	}
	// Plain scalar, folded over its continuation lines.
	parts := []string{stripYAMLComment(rest)}
	for _, l := range block {
		if yamlBlank(l) {
			parts = append(parts, "")
			continue
		}
		parts = append(parts, stripYAMLComment(strings.TrimSpace(l)))
	}
	text := foldLines(parts)
	return yamlPlain(text), nil
}

func hasContent(block []string) bool { return firstContent(block) != "" }

func firstContent(block []string) string {
	for _, l := range block {
		if !yamlBlank(l) {
			return l
		}
	}
	return ""
}

func yamlTailOK(tail string) bool {
	t := strings.TrimSpace(tail)
	return t == "" || strings.HasPrefix(t, "#")
}

// stripYAMLComment drops ` #...` from a plain scalar.
func stripYAMLComment(s string) string {
	if i := strings.Index(s, " #"); i >= 0 {
		return strings.TrimRight(s[:i], " \t")
	}
	if strings.HasPrefix(s, "#") {
		return ""
	}
	return strings.TrimRight(s, " \t")
}

// foldLines joins plain or quoted scalar lines the YAML way: a line break becomes a space, a blank line a newline.
func foldLines(parts []string) string {
	var b strings.Builder
	pendingBlank := 0
	for i, p := range parts {
		if p == "" && i > 0 {
			pendingBlank++
			continue
		}
		if i > 0 {
			if pendingBlank > 0 {
				b.WriteString(strings.Repeat("\n", pendingBlank))
			} else {
				b.WriteByte(' ')
			}
			pendingBlank = 0
		}
		b.WriteString(p)
	}
	return b.String()
}

// yamlPlain resolves a plain scalar to its YAML 1.2 core-schema type: only a string stays a string.
func yamlPlain(s string) any {
	switch s {
	case "", "~", "null", "Null", "NULL":
		return nil
	case "true", "True", "TRUE":
		return true
	case "false", "False", "FALSE":
		return false
	case ".inf", ".Inf", ".INF", "+.inf", "-.inf", ".nan", ".NaN", ".NAN":
		return 0.0
	}
	if reYAMLInt.MatchString(s) || reYAMLFloat.MatchString(s) {
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
	return s
}

// yamlDouble decodes a complete double-quoted scalar.
func yamlDouble(raw string) (string, error) {
	s, tail, err := yamlDoubleFolded(raw)
	if err != nil || strings.TrimSpace(tail) != "" {
		return "", errFrontmatter
	}
	return s, nil
}

// yamlDoubleFolded decodes the double-quoted scalar at the start of text (which may span lines) and returns the
// text after the closing quote.
func yamlDoubleFolded(text string) (string, string, error) {
	var b strings.Builder
	i := 1
	for i < len(text) {
		c := text[i]
		switch {
		case c == '"':
			return b.String(), text[i+1:], nil
		case c == '\n':
			i = foldBreak(text, i, &b)
		case c == '\\':
			if i+1 >= len(text) {
				return "", "", errFrontmatter
			}
			e := text[i+1]
			i += 2
			switch e {
			case '0':
				b.WriteByte(0)
			case 'a':
				b.WriteByte(7)
			case 'b':
				b.WriteByte(8)
			case 't', '\t':
				b.WriteByte('\t')
			case 'n':
				b.WriteByte('\n')
			case 'v':
				b.WriteByte(11)
			case 'f':
				b.WriteByte(12)
			case 'r':
				b.WriteByte('\r')
			case 'e':
				b.WriteByte(27)
			case ' ', '"', '/', '\\':
				b.WriteByte(e)
			case 'N':
				b.WriteString("\u0085")
			case '_':
				b.WriteString("\u00a0")
			case 'L':
				b.WriteString("\u2028")
			case 'P':
				b.WriteString("\u2029")
			case 'x', 'u', 'U':
				width := map[byte]int{'x': 2, 'u': 4, 'U': 8}[e]
				if i+width > len(text) {
					return "", "", errFrontmatter
				}
				v, err := strconv.ParseUint(text[i:i+width], 16, 32)
				if err != nil || !utf8.ValidRune(rune(v)) {
					return "", "", errFrontmatter
				}
				b.WriteRune(rune(v))
				i += width
			case '\n':
				// An escaped line break joins the lines with no space.
				for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
					i++
				}
			default:
				return "", "", errFrontmatter
			}
		default:
			b.WriteByte(c)
			i++
		}
	}
	return "", "", errFrontmatter
}

// foldBreak folds the raw line break at text[i] of a quoted scalar: one break is a space, each further blank line a
// newline; the white space around the break is dropped. It returns the index after the break and its indentation.
func foldBreak(text string, i int, b *strings.Builder) int {
	trimmed := strings.TrimRight(b.String(), " \t")
	b.Reset()
	b.WriteString(trimmed)
	breaks := 0
	for i < len(text) {
		switch text[i] {
		case '\n':
			breaks++
			i++
			continue
		case ' ', '\t':
			i++
			continue
		}
		break
	}
	if breaks <= 1 {
		b.WriteByte(' ')
	} else {
		b.WriteString(strings.Repeat("\n", breaks-1))
	}
	return i
}

// yamlSingleFolded decodes the single-quoted scalar at the start of text.
func yamlSingleFolded(text string) (string, string, bool) {
	var b strings.Builder
	for i := 1; i < len(text); i++ {
		if text[i] == '\'' {
			if i+1 < len(text) && text[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			return b.String(), text[i+1:], true
		}
		if text[i] == '\n' {
			i = foldBreak(text, i, &b) - 1
			continue
		}
		b.WriteByte(text[i])
	}
	return "", "", false
}

// yamlBlockScalar reads a `|` or `>` block: its lines lose the block's indentation; the chomping indicator decides
// the final line breaks.
func yamlBlockScalar(folded bool, indicators string, block []string) string {
	chomp := byte(0)
	for i := 0; i < len(indicators); i++ {
		if indicators[i] == '+' || indicators[i] == '-' {
			chomp = indicators[i]
		}
	}
	indent := -1
	for _, l := range block {
		if strings.TrimSpace(l) != "" {
			indent = yamlIndent(l)
			break
		}
	}
	if indent < 0 {
		if chomp == '+' {
			return strings.Repeat("\n", len(block))
		}
		return ""
	}
	lines := make([]string, len(block))
	for i, l := range block {
		if len(l) >= indent {
			lines[i] = l[indent:]
		} else {
			lines[i] = strings.TrimLeft(l, " ")
		}
	}
	trailing := 0
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
		trailing++
	}
	var text string
	if !folded {
		text = strings.Join(lines, "\n")
	} else {
		var b strings.Builder
		for i, l := range lines {
			if i > 0 {
				prev := lines[i-1]
				switch {
				case l == "":
					b.WriteByte('\n')
				case prev == "" || strings.HasPrefix(l, " ") || strings.HasPrefix(prev, " "):
					if prev != "" {
						b.WriteByte('\n')
					}
				default:
					b.WriteByte(' ')
				}
			}
			b.WriteString(l)
		}
		text = b.String()
	}
	switch chomp {
	case '-':
		return text
	case '+':
		return text + "\n" + strings.Repeat("\n", trailing)
	}
	return text + "\n"
}
