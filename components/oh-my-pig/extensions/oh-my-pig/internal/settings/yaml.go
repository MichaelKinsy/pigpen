package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// OrderedMap preserves YAML/JSON insertion order of record keys so that
// key-order-sensitive settings (`retry.fallbackChains`, `edit.modelVariants`,
// `systemPromptTemplates`, `bashInterceptor.patterns` rule fields, etc.)
// serialize and display in the exact order authored or defined by omp.
type OrderedMap struct {
	Keys   []string
	Values map[string]any
}

// NewOrderedMap creates an empty OrderedMap.
func NewOrderedMap() *OrderedMap {
	return &OrderedMap{Values: make(map[string]any)}
}

// Set inserts or updates k, preserving existing key position if already present.
func (o *OrderedMap) Set(k string, v any) {
	if o.Values == nil {
		o.Values = make(map[string]any)
	}
	if _, exists := o.Values[k]; !exists {
		o.Keys = append(o.Keys, k)
	}
	o.Values[k] = v
}

// Get returns the value for k.
func (o *OrderedMap) Get(k string) (any, bool) {
	if o == nil || o.Values == nil {
		return nil, false
	}
	v, ok := o.Values[k]
	return v, ok
}

// Delete removes k while preserving order of remaining keys.
func (o *OrderedMap) Delete(k string) {
	if o == nil || o.Values == nil {
		return
	}
	if _, exists := o.Values[k]; !exists {
		return
	}
	delete(o.Values, k)
	for i, existing := range o.Keys {
		if existing == k {
			o.Keys = append(o.Keys[:i], o.Keys[i+1:]...)
			break
		}
	}
}

// Len returns the number of entries.
func (o *OrderedMap) Len() int {
	if o == nil {
		return 0
	}
	return len(o.Keys)
}

// Clone returns a deep copy preserving key order.
func (o *OrderedMap) Clone() *OrderedMap {
	if o == nil {
		return NewOrderedMap()
	}
	out := &OrderedMap{
		Keys:   make([]string, len(o.Keys)),
		Values: make(map[string]any, len(o.Keys)),
	}
	copy(out.Keys, o.Keys)
	for _, k := range o.Keys {
		out.Values[k] = deepCloneOrdered(o.Values[k])
	}
	return out
}

// ToPlainMap recursively converts an OrderedMap tree into plain map[string]any
// for Go callers expecting standard maps (e.g. Store.Any).
func (o *OrderedMap) ToPlainMap() map[string]any {
	if o == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(o.Keys))
	for _, k := range o.Keys {
		out[k] = toPlainValue(o.Values[k])
	}
	return out
}

func toPlainValue(v any) any {
	switch x := v.(type) {
	case *OrderedMap:
		return x.ToPlainMap()
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = toPlainValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = toPlainValue(item)
		}
		return out
	default:
		return v
	}
}

func toOrderedValue(v any) any {
	switch x := v.(type) {
	case *OrderedMap:
		return x.Clone()
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		om := &OrderedMap{Keys: keys, Values: make(map[string]any, len(keys))}
		for _, k := range keys {
			om.Values[k] = toOrderedValue(x[k])
		}
		return om
	case map[string]string:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		om := &OrderedMap{Keys: keys, Values: make(map[string]any, len(keys))}
		for _, k := range keys {
			om.Values[k] = x[k]
		}
		return om
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = toOrderedValue(item)
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = item
		}
		return out
	default:
		return canonicalValue(v)
	}
}

func deepCloneOrdered(v any) any {
	switch x := v.(type) {
	case *OrderedMap:
		return x.Clone()
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = deepCloneOrdered(item)
		}
		return out
	case map[string]any:
		return toOrderedValue(x)
	default:
		return v
	}
}

// MarshalJSON writes the OrderedMap as a JSON object in exact key order without
// HTML-escaping `<`, `>`, `&` (matching JavaScript JSON.stringify).
func (o *OrderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	if err := writeJSONValue(&buf, o); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// marshalJSJSON serializes any value like JavaScript JSON.stringify:
//   - preserves OrderedMap key order
//   - does not HTML-escape <, >, &
//   - formats numbers with JS-style precision (scientific notation for >=1e21)
func marshalJSJSON(v any) (string, error) {
	var buf bytes.Buffer
	if err := writeJSONValue(&buf, v); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func writeJSONValue(buf *bytes.Buffer, v any) error {
	if v == nil {
		buf.WriteString("null")
		return nil
	}
	switch x := v.(type) {
	case *OrderedMap:
		buf.WriteByte('{')
		for i, k := range x.Keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJSONString(buf, k)
			buf.WriteByte(':')
			if err := writeJSONValue(buf, x.Values[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJSONString(buf, k)
			buf.WriteByte(':')
			if err := writeJSONValue(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case []any:
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeJSONValue(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case float64:
		buf.WriteString(formatJSNumber(x))
		return nil
	case int:
		buf.WriteString(strconv.Itoa(x))
		return nil
	case int64:
		buf.WriteString(strconv.FormatInt(x, 10))
		return nil
	case string:
		writeJSONString(buf, x)
		return nil
	default:
		var tmp bytes.Buffer
		enc := json.NewEncoder(&tmp)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(x); err != nil {
			return err
		}
		buf.Write(bytes.TrimSuffix(tmp.Bytes(), []byte{'\n'}))
		return nil
	}
}

func writeJSONString(buf *bytes.Buffer, s string) {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	buf.Write(bytes.TrimSuffix(tmp.Bytes(), []byte{'\n'}))
}

// formatJSNumber mirrors ECMAScript Number.prototype.toString / JSON.stringify
// for finite numbers (e.g. 1e21 -> "1e+21", 0.95 -> "0.95", 300 -> "300").
func formatJSNumber(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "null"
	}
	if f == 0 {
		return "0"
	}
	abs := math.Abs(f)
	if abs >= 1e21 || abs < 1e-6 {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		// Normalize exponent: Go writes "1e+07" or "1e-07"; JS writes "1e+7" / "1e-7".
		if idx := strings.IndexByte(s, 'e'); idx >= 0 && idx+2 < len(s) {
			mantissa := s[:idx]
			sign := s[idx+1]
			exp := strings.TrimLeft(s[idx+2:], "0")
			if exp == "" {
				exp = "0"
			}
			return mantissa + "e" + string(sign) + exp
		}
		return s
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// decodeOrderedJSON parses a JSON value preserving object key order.
func decodeOrderedJSON(src string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(src))
	dec.UseNumber()
	val, err := readOrderedJSONValue(dec)
	if err != nil {
		return nil, err
	}
	if tok, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("unexpected trailing JSON token %v", tok)
	}
	return val, nil
}

func readOrderedJSONValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return readOrderedJSONFromToken(dec, tok)
}

func readOrderedJSONFromToken(dec *json.Decoder, tok json.Token) (any, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			om := NewOrderedMap()
			for dec.More() {
				kTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kTok.(string)
				if !ok {
					return nil, fmt.Errorf("expected JSON object key, got %T", kTok)
				}
				v, err := readOrderedJSONValue(dec)
				if err != nil {
					return nil, err
				}
				om.Set(key, v)
			}
			// Consume closing '}'
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return om, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := readOrderedJSONValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		default:
			return nil, fmt.Errorf("unexpected delimiter %q", t)
		}
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return nil, err
		}
		return f, nil
	default:
		return t, nil
	}
}

// orderedValuesEqual compares two values including OrderedMap key order
// (matching omp's settingValuesEqual at config/registry.ts:349).
func orderedValuesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	oa, okA := a.(*OrderedMap)
	ob, okB := b.(*OrderedMap)
	if okA && okB {
		if len(oa.Keys) != len(ob.Keys) {
			return false
		}
		for i, k := range oa.Keys {
			if k != ob.Keys[i] || !orderedValuesEqual(oa.Values[k], ob.Values[k]) {
				return false
			}
		}
		return true
	}
	if okA || okB {
		return reflect.DeepEqual(toPlainValue(a), toPlainValue(b))
	}
	aa, isArrA := a.([]any)
	ab, isArrB := b.([]any)
	if isArrA && isArrB {
		if len(aa) != len(ab) {
			return false
		}
		for i := range aa {
			if !orderedValuesEqual(aa[i], ab[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(canonicalValue(a), canonicalValue(b))
}

// ── YAML subset parser & serializer ──────────────────────────────────────────

const (
	maxYAMLBytes     = 1 << 20 // 1 MiB cap for config files (B7)
	maxYAMLFlowDepth = 64      // nesting limit for flow collections (B7)
)

// ParseYAML parses the YAML subset used by omp's config.yml files using only
// the Go standard library, returning a plain map[string]any.
func ParseYAML(src string) (map[string]any, error) {
	om, err := ParseYAMLOrdered(src)
	if err != nil {
		return nil, err
	}
	return om.ToPlainMap(), nil
}

// ParseYAMLOrdered parses the YAML subset preserving mapping key insertion order.
// An empty or null YAML document returns an empty OrderedMap. A non-mapping
// top-level document returns an error.
func ParseYAMLOrdered(src string) (*OrderedMap, error) {
	if len(src) > maxYAMLBytes {
		return nil, fmt.Errorf("yaml: input exceeds %d byte limit", maxYAMLBytes)
	}
	val, err := parseYAMLValueOrdered(src)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return NewOrderedMap(), nil
	}
	om, ok := val.(*OrderedMap)
	if !ok {
		return nil, fmt.Errorf("YAML root must be a mapping, got %T", val)
	}
	return om, nil
}

type yamlLine struct {
	num    int // 1-based line number
	indent int
	text   string // trimmed of leading ASCII spaces and trailing comments/ASCII whitespace
}

func trimASCIISpace(s string) string {
	return strings.Trim(s, " \t")
}

func trimRightASCIISpace(s string) string {
	return strings.TrimRight(s, " \t")
}

func normalizeYAMLLineBreaks(src string) string {
	// Treat \r\n and lone \r as line breaks, matching YAML 1.2 / Bun.YAML (fixes T5).
	return strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n")
}

func parseYAMLValueOrdered(src string) (any, error) {
	// Strip UTF-8 BOM if present.
	src = strings.TrimPrefix(src, "\uFEFF")
	rawLines := strings.Split(normalizeYAMLLineBreaks(src), "\n")
	lines := make([]yamlLine, 0, len(rawLines))
	seenDocStart := false
	seenContent := false
	seenDocEnd := false
	for i, raw := range rawLines {
		lineNum := i + 1
		indent := 0
		for indent < len(raw) && raw[indent] == ' ' {
			indent++
		}
		if indent < len(raw) && raw[indent] == '\t' {
			return nil, fmt.Errorf("yaml line %d: tabs are not allowed for indentation", lineNum)
		}
		rest := stripYAMLComment(raw[indent:])
		rest = trimRightASCIISpace(rest)
		if rest == "" {
			continue
		}
		if indent == 0 && (rest == "---" || strings.HasPrefix(rest, "--- ")) {
			if seenContent || seenDocStart || seenDocEnd {
				return nil, fmt.Errorf("yaml line %d: multiple YAML documents are not supported", lineNum)
			}
			seenDocStart = true
			after := trimASCIISpace(rest[3:])
			if after != "" {
				seenContent = true
				lines = append(lines, yamlLine{num: lineNum, indent: 0, text: after})
			}
			continue
		}
		if indent == 0 && rest == "..." {
			seenDocEnd = true
			continue
		}
		if seenDocEnd {
			return nil, fmt.Errorf("yaml line %d: unexpected content after document end marker '...'", lineNum)
		}
		seenContent = true
		lines = append(lines, yamlLine{num: lineNum, indent: indent, text: rest})
	}
	if len(lines) == 0 {
		return nil, nil
	}
	p := &yamlParser{lines: lines}
	val, err := p.parseBlock(lines[0].indent)
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.lines) {
		return nil, fmt.Errorf("yaml line %d: unexpected trailing content %q", p.lines[p.pos].num, p.lines[p.pos].text)
	}
	return val, nil
}

// stripYAMLComment removes an unquoted `# ...` comment from s.
// Quotes only open a quoted string when they appear at the start of a token
// (index 0, after whitespace, or after a flow delimiter `[`, `{`, `,`, `:`),
// so an apostrophe inside a plain scalar (`echo don't # note`) does not swallow
// the trailing comment (fixes B6).
func stripYAMLComment(s string) string {
	inSingle := false
	inDouble := false
	escaped := false
	canOpenQuote := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inDouble {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inDouble = false
				canOpenQuote = false
			}
			continue
		}
		if inSingle {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
				canOpenQuote = false
			}
			continue
		}
		switch c {
		case '"':
			if canOpenQuote {
				inDouble = true
			}
			canOpenQuote = false
		case '\'':
			if canOpenQuote {
				inSingle = true
			}
			canOpenQuote = false
		case '#':
			if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
				return s[:i]
			}
			canOpenQuote = false
		case ' ', '\t', '[', '{', ',', ':':
			canOpenQuote = true
		default:
			canOpenQuote = false
		}
	}
	return s
}

type yamlParser struct {
	lines []yamlLine
	pos   int
	depth int
}

func (p *yamlParser) parseBlock(minIndent int) (any, error) {
	if p.depth > maxYAMLFlowDepth {
		lineNum := 0
		if p.pos < len(p.lines) {
			lineNum = p.lines[p.pos].num
		}
		return nil, fmt.Errorf("yaml line %d: nesting exceeds maximum depth %d", lineNum, maxYAMLFlowDepth)
	}
	if p.pos >= len(p.lines) {
		return nil, nil
	}
	line := p.lines[p.pos]
	if line.indent < minIndent {
		return nil, nil
	}
	if isSequenceItem(line.text) {
		return p.parseSequence(line.indent)
	}
	if _, _, ok := splitMappingLine(line.text); ok {
		return p.parseMapping(line.indent)
	}
	p.pos++
	return parseYAMLScalarOrFlow(line.text, line.num, 0)
}

func isSequenceItem(text string) bool {
	return text == "-" || strings.HasPrefix(text, "- ")
}

func (p *yamlParser) parseSequence(indent int) ([]any, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxYAMLFlowDepth {
		lineNum := 0
		if p.pos < len(p.lines) {
			lineNum = p.lines[p.pos].num
		}
		return nil, fmt.Errorf("yaml line %d: sequence nesting exceeds maximum depth %d", lineNum, maxYAMLFlowDepth)
	}
	var out []any
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, fmt.Errorf("yaml line %d: unexpected indentation %d (expected %d)", line.num, line.indent, indent)
		}
		if !isSequenceItem(line.text) {
			break
		}
		p.pos++
		after := ""
		if len(line.text) > 1 {
			after = strings.TrimLeft(line.text[1:], " ")
		}
		if after == "" {
			if p.pos < len(p.lines) && p.lines[p.pos].indent > indent {
				item, err := p.parseBlock(p.lines[p.pos].indent)
				if err != nil {
					return nil, err
				}
				out = append(out, item)
			} else {
				out = append(out, nil)
			}
			continue
		}
		// Check nested sequence BEFORE splitMappingLine so `- - - ...` cannot
		// scan the rest of the line quadratically at each level (fixes B7 residual).
		if isSequenceItem(after) {
			itemIndent := line.indent + len(line.text) - len(after)
			sub := yamlLine{num: line.num, indent: itemIndent, text: after}
			saved := p.lines
			tail := make([]yamlLine, 0, len(p.lines)-p.pos+1)
			tail = append(tail, sub)
			tail = append(tail, p.lines[p.pos:]...)
			p.lines = tail
			p.pos = 0
			item, err := p.parseSequence(itemIndent)
			if err != nil {
				return nil, err
			}
			consumed := p.pos - 1
			p.lines = saved
			p.pos = len(saved) - (len(tail) - 1) + consumed
			out = append(out, item)
			continue
		}
		// Inline mapping starting on the `- ` line (e.g. `- match: "go test*"`).
		if key, rest, ok := splitMappingLine(after); ok {
			itemIndent := line.indent + len(line.text) - len(after)
			m := NewOrderedMap()
			if err := p.setMappingEntry(m, key, rest, line.num, itemIndent); err != nil {
				return nil, err
			}
			for p.pos < len(p.lines) && p.lines[p.pos].indent == itemIndent {
				next := p.lines[p.pos]
				if isSequenceItem(next.text) {
					break
				}
				k, r, ok := splitMappingLine(next.text)
				if !ok {
					return nil, fmt.Errorf("yaml line %d: expected mapping entry, got %q", next.num, next.text)
				}
				p.pos++
				if err := p.setMappingEntry(m, k, r, next.num, itemIndent); err != nil {
					return nil, err
				}
			}
			out = append(out, m)
			continue
		}
		val, err := parseYAMLScalarOrFlow(after, line.num, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, val)
	}
	if out == nil {
		out = []any{}
	}
	return out, nil
}

func (p *yamlParser) parseMapping(indent int) (*OrderedMap, error) {
	out := NewOrderedMap()
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, fmt.Errorf("yaml line %d: unexpected indentation %d (expected %d)", line.num, line.indent, indent)
		}
		if isSequenceItem(line.text) {
			break
		}
		key, rest, ok := splitMappingLine(line.text)
		if !ok {
			return nil, fmt.Errorf("yaml line %d: expected 'key: value', got %q", line.num, line.text)
		}
		p.pos++
		if err := p.setMappingEntry(out, key, rest, line.num, indent); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p *yamlParser) setMappingEntry(m *OrderedMap, rawKey, rest string, lineNum, parentIndent int) error {
	key, err := parseYAMLKey(rawKey, lineNum)
	if err != nil {
		return err
	}
	if rest != "" {
		val, err := parseYAMLScalarOrFlow(rest, lineNum, 0)
		if err != nil {
			return err
		}
		m.Set(key, val)
		return nil
	}
	if p.pos < len(p.lines) {
		next := p.lines[p.pos]
		if next.indent > parentIndent || (next.indent == parentIndent && isSequenceItem(next.text)) {
			val, err := p.parseBlock(next.indent)
			if err != nil {
				return err
			}
			m.Set(key, val)
			return nil
		}
	}
	m.Set(key, nil)
	return nil
}

// splitMappingLine splits `key: value` outside quoted keys and flow brackets.
// Only treats quotes as delimiters when starting a key or flow token so plain
// keys containing apostrophes (`it's: allow`) still split cleanly (fixes B6).
func splitMappingLine(s string) (key, rest string, ok bool) {
	inSingle := false
	inDouble := false
	escaped := false
	depth := 0
	canOpenQuote := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inDouble {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inDouble = false
				canOpenQuote = false
			}
			continue
		}
		if inSingle {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
				canOpenQuote = false
			}
			continue
		}
		switch c {
		case '"':
			if canOpenQuote {
				inDouble = true
			}
			canOpenQuote = false
		case '\'':
			if canOpenQuote {
				inSingle = true
			}
			canOpenQuote = false
		case '[', '{':
			depth++
			canOpenQuote = true
		case ']', '}':
			if depth > 0 {
				depth--
			}
			canOpenQuote = false
		case ':':
			if depth == 0 && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t') {
				k := trimASCIISpace(s[:i])
				if k == "" {
					return "", "", false
				}
				r := ""
				if i+1 < len(s) {
					r = trimASCIISpace(s[i+1:])
				}
				return k, r, true
			}
			canOpenQuote = false
		case ' ', '\t', ',':
			canOpenQuote = depth > 0
		default:
			canOpenQuote = false
		}
	}
	return "", "", false
}

func parseYAMLKey(raw string, lineNum int) (string, error) {
	if len(raw) >= 1 && raw[0] == '"' {
		return parseDoubleQuoted(raw, lineNum)
	}
	if len(raw) >= 1 && raw[0] == '\'' {
		return parseSingleQuoted(raw, lineNum)
	}
	if len(raw) > 0 && (raw[0] == '&' || raw[0] == '*' || raw[0] == '!' || raw[0] == '?') {
		return "", fmt.Errorf("yaml line %d: unsupported YAML syntax %q", lineNum, raw)
	}
	return raw, nil
}

func parseYAMLScalarOrFlow(s string, lineNum, depth int) (any, error) {
	s = trimASCIISpace(s)
	if s == "" {
		return nil, nil
	}
	if depth > maxYAMLFlowDepth {
		return nil, fmt.Errorf("yaml line %d: flow nesting exceeds maximum depth %d", lineNum, maxYAMLFlowDepth)
	}
	switch s[0] {
	case '[':
		return parseFlowSequence(s, lineNum, depth+1)
	case '{':
		return parseFlowMapping(s, lineNum, depth+1)
	case '"':
		return parseDoubleQuoted(s, lineNum)
	case '\'':
		return parseSingleQuoted(s, lineNum)
	case '&', '*', '!':
		return nil, fmt.Errorf("yaml line %d: YAML anchors, aliases, and tags (%q) are not supported", lineNum, s)
	case '|', '>':
		return nil, fmt.Errorf("yaml line %d: YAML block scalars (%q) are not supported", lineNum, s)
	}
	return parsePlainScalar(s), nil
}

func parseDoubleQuoted(s string, lineNum int) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("yaml line %d: unterminated double-quoted string", lineNum)
	}
	var b strings.Builder
	b.Grow(len(s) - 2)
	end := len(s) - 1
	for i := 1; i < end; i++ {
		c := s[i]
		if c == '"' {
			return "", fmt.Errorf("yaml line %d: unexpected quote inside double-quoted string", lineNum)
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= end {
			return "", fmt.Errorf("yaml line %d: dangling backslash in string", lineNum)
		}
		switch s[i] {
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		case '/':
			b.WriteByte('/')
		case 'a':
			b.WriteByte('\a')
		case 'e':
			b.WriteByte(0x1b)
		case 'v':
			b.WriteByte('\v')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case '0':
			b.WriteByte(0)
		case ' ':
			b.WriteByte(' ')
		case 'x':
			if i+2 >= end {
				return "", fmt.Errorf("yaml line %d: invalid \\x escape", lineNum)
			}
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				return "", fmt.Errorf("yaml line %d: invalid \\x escape: %w", lineNum, err)
			}
			b.WriteRune(rune(v))
			i += 2
		case 'u':
			if i+4 >= end {
				return "", fmt.Errorf("yaml line %d: invalid \\u escape", lineNum)
			}
			v, err := strconv.ParseUint(s[i+1:i+5], 16, 32)
			if err != nil {
				return "", fmt.Errorf("yaml line %d: invalid \\u escape: %w", lineNum, err)
			}
			r := rune(v)
			// Combine UTF-16 surrogate pairs (\uD83D\uDE00 -> U+1F600) instead of emitting mojibake (fixes B6).
			if utf16.IsSurrogate(r) && r >= 0xD800 && r <= 0xDBFF && i+10 < end && s[i+5] == '\\' && s[i+6] == 'u' {
				v2, err2 := strconv.ParseUint(s[i+7:i+11], 16, 32)
				if err2 == nil && v2 >= 0xDC00 && v2 <= 0xDFFF {
					b.WriteRune(utf16.DecodeRune(r, rune(v2)))
					i += 10
					continue
				}
			}
			b.WriteRune(r)
			i += 4
		case 'U':
			if i+8 >= end {
				return "", fmt.Errorf("yaml line %d: invalid \\U escape", lineNum)
			}
			v, err := strconv.ParseUint(s[i+1:i+9], 16, 32)
			if err != nil {
				return "", fmt.Errorf("yaml line %d: invalid \\U escape: %w", lineNum, err)
			}
			b.WriteRune(rune(v))
			i += 8
		default:
			return "", fmt.Errorf("yaml line %d: unknown escape \\%c", lineNum, s[i])
		}
	}
	return b.String(), nil
}

func parseSingleQuoted(s string, lineNum int) (string, error) {
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return "", fmt.Errorf("yaml line %d: unterminated single-quoted string", lineNum)
	}
	inner := s[1 : len(s)-1]
	var b strings.Builder
	b.Grow(len(inner))
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\'' {
			if i+1 < len(inner) && inner[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			return "", fmt.Errorf("yaml line %d: unescaped single quote in string", lineNum)
		}
		b.WriteByte(inner[i])
	}
	return b.String(), nil
}

func parsePlainScalar(s string) any {
	switch s {
	case "null", "Null", "NULL", "~":
		return nil
	case "true", "True", "TRUE":
		return true
	case "false", "False", "FALSE":
		return false
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		if n, err := strconv.ParseInt(s[2:], 16, 64); err == nil {
			return float64(n)
		}
	}
	if strings.HasPrefix(s, "0o") || strings.HasPrefix(s, "0O") {
		if n, err := strconv.ParseInt(s[2:], 8, 64); err == nil {
			return float64(n)
		}
	}
	if looksLikeYAMLNumber(s) {
		if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	}
	return s
}

func looksLikeYAMLNumber(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[i] == '+' || s[i] == '-' {
		i++
		if i >= len(s) {
			return false
		}
	}
	hasDigit := false
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		hasDigit = true
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			hasDigit = true
			i++
		}
	}
	if !hasDigit {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		expDigits := false
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			expDigits = true
			i++
		}
		if !expDigits {
			return false
		}
	}
	return i == len(s)
}

func parseFlowSequence(s string, lineNum, depth int) ([]any, error) {
	if depth > maxYAMLFlowDepth {
		return nil, fmt.Errorf("yaml line %d: flow nesting exceeds maximum depth %d", lineNum, maxYAMLFlowDepth)
	}
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, fmt.Errorf("yaml line %d: malformed flow sequence %q", lineNum, s)
	}
	inner := trimASCIISpace(s[1 : len(s)-1])
	if inner == "" {
		return []any{}, nil
	}
	parts, err := splitFlowItems(inner, lineNum)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(parts))
	for _, part := range parts {
		v, err := parseYAMLScalarOrFlow(part, lineNum, depth)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func parseFlowMapping(s string, lineNum, depth int) (*OrderedMap, error) {
	if depth > maxYAMLFlowDepth {
		return nil, fmt.Errorf("yaml line %d: flow nesting exceeds maximum depth %d", lineNum, maxYAMLFlowDepth)
	}
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil, fmt.Errorf("yaml line %d: malformed flow mapping %q", lineNum, s)
	}
	inner := trimASCIISpace(s[1 : len(s)-1])
	out := NewOrderedMap()
	if inner == "" {
		return out, nil
	}
	parts, err := splitFlowItems(inner, lineNum)
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		k, v, ok := splitFlowKV(part)
		if !ok {
			return nil, fmt.Errorf("yaml line %d: malformed flow mapping entry %q", lineNum, part)
		}
		key, err := parseYAMLKey(k, lineNum)
		if err != nil {
			return nil, err
		}
		val, err := parseYAMLScalarOrFlow(v, lineNum, depth)
		if err != nil {
			return nil, err
		}
		out.Set(key, val)
	}
	return out, nil
}

func splitFlowItems(s string, lineNum int) ([]string, error) {
	var parts []string
	start := 0
	depth := 0
	inSingle := false
	inDouble := false
	escaped := false
	canOpenQuote := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inDouble {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inDouble = false
				canOpenQuote = false
			}
			continue
		}
		if inSingle {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
				canOpenQuote = false
			}
			continue
		}
		switch c {
		case '"':
			if canOpenQuote {
				inDouble = true
			}
			canOpenQuote = false
		case '\'':
			if canOpenQuote {
				inSingle = true
			}
			canOpenQuote = false
		case '[', '{':
			depth++
			if depth > maxYAMLFlowDepth {
				return nil, fmt.Errorf("yaml line %d: flow nesting exceeds maximum depth %d", lineNum, maxYAMLFlowDepth)
			}
			canOpenQuote = true
		case ']', '}':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("yaml line %d: unmatched closing %c", lineNum, c)
			}
			canOpenQuote = false
		case ',':
			if depth == 0 {
				part := trimASCIISpace(s[start:i])
				if part != "" {
					parts = append(parts, part)
				}
				start = i + 1
			}
			canOpenQuote = true
		case ' ', '\t', ':':
			canOpenQuote = true
		default:
			canOpenQuote = false
		}
	}
	if depth != 0 || inSingle || inDouble {
		return nil, fmt.Errorf("yaml line %d: unterminated flow collection or quote", lineNum)
	}
	if tail := trimASCIISpace(s[start:]); tail != "" {
		parts = append(parts, tail)
	}
	return parts, nil
}

func splitFlowKV(s string) (key, val string, ok bool) {
	inSingle := false
	inDouble := false
	escaped := false
	depth := 0
	canOpenQuote := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inDouble {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inDouble = false
				canOpenQuote = false
			}
			continue
		}
		if inSingle {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
				canOpenQuote = false
			}
			continue
		}
		switch c {
		case '"':
			if canOpenQuote {
				inDouble = true
			}
			canOpenQuote = false
		case '\'':
			if canOpenQuote {
				inSingle = true
			}
			canOpenQuote = false
		case '[', '{':
			depth++
			canOpenQuote = true
		case ']', '}':
			if depth > 0 {
				depth--
			}
			canOpenQuote = false
		case ':':
			if depth == 0 {
				k := trimASCIISpace(s[:i])
				if k == "" {
					return "", "", false
				}
				return k, trimASCIISpace(s[i+1:]), true
			}
			canOpenQuote = true
		case ' ', '\t', ',':
			canOpenQuote = true
		default:
			canOpenQuote = false
		}
	}
	return "", "", false
}

// MarshalYAML renders a settings map into clean, canonical 2-space-indented YAML
// matching omp's stringifyYamlConfig style (no trailing spaces on mapping headers).
// upstream: packages/utils/src/yaml-config.ts stringifyYamlConfig
func MarshalYAML(m map[string]any) (string, error) {
	om, _ := toOrderedValue(m).(*OrderedMap)
	return MarshalYAMLOrdered(om)
}

// MarshalYAMLOrdered serializes an OrderedMap preserving key insertion order.
func MarshalYAMLOrdered(om *OrderedMap) (string, error) {
	if om == nil || om.Len() == 0 {
		return "{}\n", nil
	}
	var b strings.Builder
	writeYAMLMappingOrdered(&b, om, 0)
	return b.String(), nil
}

func writeYAMLMappingOrdered(b *strings.Builder, om *OrderedMap, indent int) {
	prefix := strings.Repeat(" ", indent)
	for _, k := range om.Keys {
		v := om.Values[k]
		fk := formatYAMLKey(k)
		switch x := v.(type) {
		case *OrderedMap:
			if x.Len() == 0 {
				b.WriteString(prefix)
				b.WriteString(fk)
				b.WriteString(": {}\n")
			} else {
				b.WriteString(prefix)
				b.WriteString(fk)
				b.WriteString(":\n")
				writeYAMLMappingOrdered(b, x, indent+2)
			}
		case map[string]any:
			sub, _ := toOrderedValue(x).(*OrderedMap)
			if sub.Len() == 0 {
				b.WriteString(prefix)
				b.WriteString(fk)
				b.WriteString(": {}\n")
			} else {
				b.WriteString(prefix)
				b.WriteString(fk)
				b.WriteString(":\n")
				writeYAMLMappingOrdered(b, sub, indent+2)
			}
		case []any:
			if len(x) == 0 {
				b.WriteString(prefix)
				b.WriteString(fk)
				b.WriteString(": []\n")
			} else {
				b.WriteString(prefix)
				b.WriteString(fk)
				b.WriteString(":\n")
				writeYAMLSequenceOrdered(b, x, indent+2)
			}
		default:
			b.WriteString(prefix)
			b.WriteString(fk)
			b.WriteString(": ")
			b.WriteString(formatYAMLScalar(v))
			b.WriteByte('\n')
		}
	}
}

func writeYAMLSequenceOrdered(b *strings.Builder, arr []any, indent int) {
	prefix := strings.Repeat(" ", indent)
	for _, item := range arr {
		switch x := item.(type) {
		case *OrderedMap:
			writeYAMLSeqMap(b, x, prefix, indent)
		case map[string]any:
			sub, _ := toOrderedValue(x).(*OrderedMap)
			writeYAMLSeqMap(b, sub, prefix, indent)
		case []any:
			if len(x) == 0 {
				b.WriteString(prefix)
				b.WriteString("- []\n")
			} else {
				b.WriteString(prefix)
				b.WriteString("-\n")
				writeYAMLSequenceOrdered(b, x, indent+2)
			}
		default:
			b.WriteString(prefix)
			b.WriteString("- ")
			b.WriteString(formatYAMLScalar(item))
			b.WriteByte('\n')
		}
	}
}

func writeYAMLSeqMap(b *strings.Builder, om *OrderedMap, prefix string, indent int) {
	if om == nil || om.Len() == 0 {
		b.WriteString(prefix)
		b.WriteString("- {}\n")
		return
	}
	for i, k := range om.Keys {
		if i == 0 {
			b.WriteString(prefix)
			b.WriteString("- ")
		} else {
			b.WriteString(prefix)
			b.WriteString("  ")
		}
		fk := formatYAMLKey(k)
		val := om.Values[k]
		switch mv := val.(type) {
		case *OrderedMap:
			if mv.Len() == 0 {
				b.WriteString(fk)
				b.WriteString(": {}\n")
			} else {
				b.WriteString(fk)
				b.WriteString(":\n")
				writeYAMLMappingOrdered(b, mv, indent+4)
			}
		case map[string]any:
			sub, _ := toOrderedValue(mv).(*OrderedMap)
			if sub.Len() == 0 {
				b.WriteString(fk)
				b.WriteString(": {}\n")
			} else {
				b.WriteString(fk)
				b.WriteString(":\n")
				writeYAMLMappingOrdered(b, sub, indent+4)
			}
		case []any:
			if len(mv) == 0 {
				b.WriteString(fk)
				b.WriteString(": []\n")
			} else {
				b.WriteString(fk)
				b.WriteString(":\n")
				writeYAMLSequenceOrdered(b, mv, indent+4)
			}
		default:
			b.WriteString(fk)
			b.WriteString(": ")
			b.WriteString(formatYAMLScalar(val))
			b.WriteByte('\n')
		}
	}
}

func formatYAMLKey(k string) string {
	if needsYAMLQuoting(k, true) {
		return quoteYAMLString(k)
	}
	return k
}

func formatYAMLScalar(v any) string {
	if v == nil {
		return "null"
	}
	switch x := v.(type) {
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if math.Trunc(x) == x && !math.IsInf(x, 0) && !math.IsNaN(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return formatJSNumber(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case string:
		if needsYAMLQuoting(x, false) {
			return quoteYAMLString(x)
		}
		return x
	default:
		return quoteYAMLString(fmt.Sprint(v))
	}
}

func needsYAMLQuoting(s string, isKey bool) bool {
	if s == "" {
		return true
	}
	if s[0] == ' ' || s[len(s)-1] == ' ' || s[0] == '\t' || s[len(s)-1] == '\t' {
		return true
	}
	switch s {
	case "null", "Null", "NULL", "~",
		"true", "True", "TRUE",
		"false", "False", "FALSE":
		return true
	}
	if looksLikeYAMLNumber(s) || strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") || strings.HasPrefix(s, "0o") || strings.HasPrefix(s, "0O") {
		return true
	}
	if strings.HasPrefix(s, "\uFEFF") {
		return true
	}
	switch s[0] {
	case '-', '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`':
		return true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f || c == '\n' || c == '\r' || c == '\t' {
			return true
		}
		if c == ':' && (isKey || i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t') {
			return true
		}
		if c == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
			return true
		}
		// Quote keys containing quotes or flow delimiters so serialization always round-trips (fixes B6).
		if isKey && (c == '\'' || c == '"' || c == ',' || c == '[' || c == ']' || c == '{' || c == '}') {
			return true
		}
	}
	return false
}

func quoteYAMLString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&b, `\x%02x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
