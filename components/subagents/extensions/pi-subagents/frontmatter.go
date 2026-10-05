package pi_subagents

import (
	"strings"
	"unicode/utf8"
)

// Frontmatter is the key/value block of an agent or chain file. Values are strings; a nested block is one string
// with embedded newlines. Entries come back in JavaScript's Object.keys order (integer-like keys first).
type Frontmatter struct{ o *jsObject }

// Get returns the value of k, or "" when it is absent (use Has to tell the two apart).
func (f Frontmatter) Get(k string) string {
	if f.o == nil {
		return ""
	}
	s, _ := f.o.str(k)
	return s
}

func (f Frontmatter) Has(k string) bool { return f.o != nil && f.o.has(k) }

// Keys returns the keys in Object.keys order.
func (f Frontmatter) Keys() []string {
	if f.o == nil {
		return nil
	}
	return f.o.order()
}

func isLineTerminator(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

// jsFirstNonSpace is `line.search(/\S|$/)` counted in UTF-16 units: the index of the first character that is not
// JavaScript white space, or the length.
func jsFirstNonSpace(line string) int {
	n := 0
	for _, r := range line {
		if !isJSSpace(r) {
			return n
		}
		n += utf16Len(r)
	}
	return n
}

func utf16Len(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// foldBlock folds a YAML folded block scalar while keeping more-indented lines and every blank-line separator.
func foldBlock(block string) string {
	var folded strings.Builder
	hasContent, previousMore := false, false
	blank := 0
	for _, line := range strings.Split(block, "\n") {
		current := strings.TrimRightFunc(line, isJSSpace)
		if jsTrim(current) == "" {
			if hasContent {
				blank++
			}
			continue
		}
		currentMore := len(current) > len(strings.TrimLeftFunc(current, isJSSpace))
		if hasContent {
			if blank > 0 {
				n := blank
				if previousMore || currentMore {
					n++
				}
				folded.WriteString(strings.Repeat("\n", n))
			} else if previousMore || currentMore {
				folded.WriteString("\n")
			} else {
				folded.WriteString(" ")
			}
		}
		folded.WriteString(current)
		hasContent = true
		previousMore = currentMore
		blank = 0
	}
	return jsTrim(folded.String())
}

// lineStarts returns the offsets at which a multiline `^` matches: 0 and after every line terminator.
func lineStarts(s string) []int {
	starts := []int{0}
	for i, r := range s {
		if isLineTerminator(r) {
			// "\r\n" is one terminator in the original's input only after normalization; none remains here
			starts = append(starts, i+utf8.RuneLen(r))
		}
	}
	return starts
}

// blockPrefix is `rawBlock.match(/^[ \t]+(?=\S)/m)?.[0]`: the indentation of the first indented line that has content.
func blockPrefix(raw string) string {
	for _, st := range lineStarts(raw) {
		i := st
		for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t') {
			i++
		}
		if i == st || i >= len(raw) {
			continue
		}
		if r, _ := utf8.DecodeRuneInString(raw[i:]); !isJSSpace(r) {
			return raw[st:i]
		}
	}
	return ""
}

func stripBlock(raw string, folded bool) string {
	prefix := blockPrefix(raw)
	stripped := raw
	if prefix != "" {
		var b strings.Builder
		prev := 0
		for _, st := range lineStarts(raw) {
			if st < prev {
				continue
			}
			b.WriteString(raw[prev:st])
			if strings.HasPrefix(raw[st:], prefix) {
				st += len(prefix)
			}
			prev = st
		}
		b.WriteString(raw[prev:])
		stripped = strings.TrimPrefix(b.String(), "\n")
	}
	if folded {
		return foldBlock(stripped)
	}
	return stripped
}

func isWordByte(c byte) bool {
	return c == '_' || c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// matchKeyLine is `line.match(/^([\w-]+):\s*(.*)$/)`.
func matchKeyLine(line string) (key, value string, ok bool) {
	i := 0
	for i < len(line) && isWordByte(line[i]) {
		i++
	}
	if i == 0 || i >= len(line) || line[i] != ':' {
		return "", "", false
	}
	rest := strings.TrimLeftFunc(line[i+1:], isJSSpace)
	if strings.ContainsAny(rest, "\n\r\u2028\u2029") {
		return "", "", false
	}
	return line[:i], rest, true
}

// ParseFrontmatter parses the YAML-ish header of an agent or chain file and returns it with the trimmed body.
func ParseFrontmatter(content string) (Frontmatter, string) {
	fm := Frontmatter{o: newObject()}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---") {
		return fm, normalized
	}
	end := strings.Index(normalized[3:], "\n---")
	if end == -1 {
		return fm, normalized
	}
	end += 3
	block := ""
	if end > 4 {
		block = normalized[4:end]
	}
	body := jsTrim(normalized[end+4:])

	var key string
	var blockLines []string
	inBlock, folded, literal := false, false, false
	currentIndent := 0
	flush := func() {
		fm.o.set(key, stripBlock(strings.Join(blockLines, "\n"), folded))
		inBlock, folded, literal = false, false, false
		blockLines = nil
	}
	for _, line := range strings.Split(block, "\n") {
		indent := jsFirstNonSpace(line)
		trimmed := jsTrim(line)
		if inBlock && (indent > currentIndent || ((folded || literal) && trimmed == "")) {
			blockLines = append(blockLines, line)
			continue
		}
		if inBlock {
			flush()
		}
		k, rawValue, ok := matchKeyLine(line)
		if !ok {
			continue
		}
		rawValue = jsTrim(rawValue)
		quoted := len(rawValue) >= 1 && ((rawValue[0] == '"' && rawValue[len(rawValue)-1] == '"') || (rawValue[0] == '\'' && rawValue[len(rawValue)-1] == '\''))
		value := rawValue
		if quoted {
			if len(rawValue) >= 2 {
				value = rawValue[1 : len(rawValue)-1]
			} else {
				value = ""
			}
		}
		isFolded := !quoted && (rawValue == ">" || rawValue == ">-")
		isLiteral := !quoted && (rawValue == "|" || rawValue == "|-")
		if value == "" || isFolded || isLiteral {
			key, inBlock, currentIndent, folded, literal = k, true, indent, isFolded, isLiteral
			blockLines = nil
		} else {
			fm.o.set(k, value)
		}
	}
	if inBlock {
		flush()
	}
	return fm, body
}

// ParseFrontmatterList normalizes a comma-separated or block-list value; nil stays nil (the key was absent).
func ParseFrontmatterList(raw *string) []string {
	if raw == nil {
		return nil
	}
	out := []string{}
	for _, line := range strings.Split(*raw, "\n") {
		value := jsTrim(line)
		if strings.HasPrefix(value, "-") {
			// /^-\s+(.+)$/: a dash, white space, then a non-empty rest without line terminators
			rest := value[1:]
			if r, _ := utf8.DecodeRuneInString(rest); rest != "" && isJSSpace(r) {
				if item := strings.TrimLeftFunc(rest, isJSSpace); item != "" && !strings.ContainsAny(item, "\n\r\u2028\u2029") {
					value = item
				}
			}
		}
		for _, part := range strings.Split(value, ",") {
			if p := jsTrim(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}
