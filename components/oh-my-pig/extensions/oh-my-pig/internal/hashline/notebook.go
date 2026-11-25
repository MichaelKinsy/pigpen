package hashline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Jupyter notebook (`.ipynb`) codec: projects cells onto an editable text
// form (`# %% [type] cell:N` markers) and serializes edits back to JSON
// with `JSON.stringify(nb, null, 1)`-identical bytes and preserved key order.
// upstream: crates/edit/src/notebook.rs

var (
	cellMarkerRE      = regexp.MustCompile(`^# %% \[(code|markdown|raw)\](?: cell:(\d+))?$`)
	escapableMarkerRE = regexp.MustCompile(`^# %%+ \[(?:code|markdown|raw)\](?: cell:\d+)?$`)
	escapedMarkerRE   = regexp.MustCompile(`^# %%%+ \[(?:code|markdown|raw)\](?: cell:\d+)?$`)
)

// NotebookCellType is a cell type supported by the editable notebook projection.
// upstream: crates/edit/src/notebook.rs:37
type NotebookCellType string

const (
	NotebookCellCode     NotebookCellType = "code"
	NotebookCellMarkdown NotebookCellType = "markdown"
	NotebookCellRaw      NotebookCellType = "raw"
)

func parseNotebookCellType(v string) (NotebookCellType, bool) {
	switch v {
	case "code":
		return NotebookCellCode, true
	case "markdown":
		return NotebookCellMarkdown, true
	case "raw":
		return NotebookCellRaw, true
	default:
		return "", false
	}
}

// ParsedVirtualCell is one cell parsed from the editable notebook projection.
// upstream: crates/edit/src/notebook.rs:67
type ParsedVirtualCell struct {
	CellType  NotebookCellType
	CellIndex int // -1 when the marker omitted cell:N
	Source    string
}

// IsNotebookPath reports whether path has a `.ipynb` extension (case-insensitive).
// upstream: crates/edit/src/notebook.rs:115
func IsNotebookPath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".ipynb")
}

// SplitNotebookSource splits source text into nbformat `source` lines (each keeps its
// trailing `\n`; a final unterminated line is kept as-is).
// upstream: crates/edit/src/notebook.rs:123
func SplitNotebookSource(content string) []string {
	if content == "" {
		return []string{}
	}
	var out []string
	start := 0
	for i := range len(content) {
		if content[i] == '\n' {
			out = append(out, content[start:i+1])
			start = i + 1
		}
	}
	if start < len(content) {
		out = append(out, content[start:])
	}
	return out
}

func escapeNotebookMarkerLines(source string) string {
	if !strings.Contains(source, "# %%") {
		return source
	}
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		if escapableMarkerRE.MatchString(line) {
			lines[i] = strings.Replace(line, "# %", "# %%", 1)
		}
	}
	return strings.Join(lines, "\n")
}

func unescapeNotebookMarkerLine(line string) string {
	if escapedMarkerRE.MatchString(line) {
		return strings.Replace(line, "# %%", "# %", 1)
	}
	return line
}

// orderedJSON preserves JSON object key insertion order and exact number tokens
// so round-tripped notebooks keep key order and `JSON.stringify(v, null, 1)` formatting.
type jsonKind int

const (
	jsonNull jsonKind = iota
	jsonBool
	jsonNumber
	jsonString
	jsonArray
	jsonObject
)

type jsonEntry struct {
	key string
	val *orderedJSON
}

type orderedJSON struct {
	kind   jsonKind
	b      bool
	num    json.Number
	str    string
	arr    []*orderedJSON
	obj    []jsonEntry
	objIdx map[string]int
}

func newJSONNull() *orderedJSON           { return &orderedJSON{kind: jsonNull} }
func newJSONString(s string) *orderedJSON { return &orderedJSON{kind: jsonString, str: s} }
func newJSONNumber(n string) *orderedJSON { return &orderedJSON{kind: jsonNumber, num: json.Number(n)} }
func newJSONArray(elems []*orderedJSON) *orderedJSON {
	if elems == nil {
		elems = []*orderedJSON{}
	}
	return &orderedJSON{kind: jsonArray, arr: elems}
}
func newJSONObject() *orderedJSON {
	return &orderedJSON{kind: jsonObject, objIdx: make(map[string]int)}
}

func (v *orderedJSON) get(key string) (*orderedJSON, bool) {
	if v == nil || v.kind != jsonObject {
		return nil, false
	}
	idx, ok := v.objIdx[key]
	if !ok {
		return nil, false
	}
	return v.obj[idx].val, true
}

func (v *orderedJSON) set(key string, val *orderedJSON) {
	if idx, ok := v.objIdx[key]; ok {
		v.obj[idx].val = val
		return
	}
	v.objIdx[key] = len(v.obj)
	v.obj = append(v.obj, jsonEntry{key: key, val: val})
}

// delete removes key using swap-remove (moving the last entry into the removed slot),
// matching serde_json's `preserve_order` (`IndexMap::swap_remove`) used by omp's notebook.rs.
// upstream: crates/edit/src/notebook.rs:308-309
func (v *orderedJSON) delete(key string) {
	idx, ok := v.objIdx[key]
	if !ok {
		return
	}
	last := len(v.obj) - 1
	if idx != last {
		moved := v.obj[last]
		v.obj[idx] = moved
		v.objIdx[moved.key] = idx
	}
	v.obj = v.obj[:last]
	delete(v.objIdx, key)
}

func (v *orderedJSON) clone() *orderedJSON {
	if v == nil {
		return nil
	}
	cp := &orderedJSON{
		kind: v.kind,
		b:    v.b,
		num:  v.num,
		str:  v.str,
	}
	if v.kind == jsonArray {
		cp.arr = make([]*orderedJSON, len(v.arr))
		for i, e := range v.arr {
			cp.arr[i] = e.clone()
		}
	} else if v.kind == jsonObject {
		cp.obj = make([]jsonEntry, len(v.obj))
		cp.objIdx = make(map[string]int, len(v.obj))
		for i, e := range v.obj {
			cp.obj[i] = jsonEntry{key: e.key, val: e.val.clone()}
			cp.objIdx[e.key] = i
		}
	}
	return cp
}

func parseOrderedJSON(raw string) (*orderedJSON, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	val, err := decodeOrderedValue(dec)
	if err != nil {
		return nil, err
	}
	// Ensure no trailing garbage.
	var extra json.Token
	if extra, err = dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing token %v", extra)
	}
	return val, nil
}

func decodeOrderedValue(dec *json.Decoder) (*orderedJSON, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return decodeOrderedToken(dec, tok)
}

func decodeOrderedToken(dec *json.Decoder, tok json.Token) (*orderedJSON, error) {
	switch t := tok.(type) {
	case nil:
		return newJSONNull(), nil
	case bool:
		return &orderedJSON{kind: jsonBool, b: t}, nil
	case json.Number:
		return &orderedJSON{kind: jsonNumber, num: t}, nil
	case string:
		return newJSONString(t), nil
	case json.Delim:
		switch t {
		case '[':
			arr := []*orderedJSON{}
			for dec.More() {
				elem, err := decodeOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, elem)
			}
			end, err := dec.Token()
			if err != nil {
				return nil, err
			}
			if end != json.Delim(']') {
				return nil, fmt.Errorf("expected ']'")
			}
			return newJSONArray(arr), nil
		case '{':
			obj := newJSONObject()
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fmt.Errorf("expected object key string")
				}
				val, err := decodeOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				obj.set(key, val)
			}
			end, err := dec.Token()
			if err != nil {
				return nil, err
			}
			if end != json.Delim('}') {
				return nil, fmt.Errorf("expected '}'")
			}
			return obj, nil
		default:
			return nil, fmt.Errorf("unexpected delimiter %q", t)
		}
	default:
		return nil, fmt.Errorf("unexpected token %T", tok)
	}
}

func validateOrderedNotebook(v *orderedJSON, display string) (*orderedJSON, []*orderedJSON, error) {
	if v == nil || v.kind != jsonObject {
		return nil, nil, fmt.Errorf("Invalid notebook structure (expected object): %s", display)
	}
	cellsVal, ok := v.get("cells")
	if !ok || cellsVal.kind != jsonArray {
		return nil, nil, fmt.Errorf("Invalid notebook structure (missing cells array): %s", display)
	}
	for i, cell := range cellsVal.arr {
		if cell == nil || cell.kind != jsonObject {
			return nil, nil, fmt.Errorf("Invalid notebook cell %d in %s", i, display)
		}
		ctVal, ok := cell.get("cell_type")
		if !ok || ctVal.kind != jsonString {
			return nil, nil, fmt.Errorf("Invalid notebook cell %d in %s", i, display)
		}
		if _, valid := parseNotebookCellType(ctVal.str); !valid {
			return nil, nil, fmt.Errorf("Invalid notebook cell %d in %s", i, display)
		}
	}
	return v, cellsVal.arr, nil
}

func orderedSourceToText(v *orderedJSON) string {
	if v == nil {
		return ""
	}
	switch v.kind {
	case jsonString:
		return v.str
	case jsonArray:
		var b strings.Builder
		for _, elem := range v.arr {
			if elem != nil && elem.kind == jsonString {
				b.WriteString(elem.str)
			}
		}
		return b.String()
	default:
		return ""
	}
}

// NotebookToEditableText decodes Jupyter `.ipynb` JSON into omp's 0-based editable cell form.
// upstream: crates/edit/src/notebook.rs:166
func NotebookToEditableText(rawJSON, display string) (string, error) {
	trimmed := strings.TrimPrefix(rawJSON, BOM)
	doc, err := parseOrderedJSON(trimmed)
	if err != nil {
		return "", fmt.Errorf("Invalid JSON in notebook: %s", display)
	}
	_, cells, err := validateOrderedNotebook(doc, display)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(cells))
	for i, cell := range cells {
		ctVal, _ := cell.get("cell_type")
		srcVal, _ := cell.get("source")
		src := escapeNotebookMarkerLines(orderedSourceToText(srcVal))
		if src == "" {
			parts = append(parts, fmt.Sprintf("# %%%% [%s] cell:%d", ctVal.str, i))
		} else {
			parts = append(parts, fmt.Sprintf("# %%%% [%s] cell:%d\n%s", ctVal.str, i, src))
		}
	}
	return strings.Join(parts, "\n"), nil
}

// ParseNotebookEditableText parses an editable notebook projection into virtual cells.
// upstream: crates/edit/src/notebook.rs:196
func ParseNotebookEditableText(text, display string) ([]ParsedVirtualCell, error) {
	var cells []ParsedVirtualCell
	type curCell struct {
		cellType  NotebookCellType
		cellIndex int
		lines     []string
	}
	var current *curCell
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	for _, line := range lines {
		if m := cellMarkerRE.FindStringSubmatch(line); m != nil {
			if current != nil {
				cells = append(cells, ParsedVirtualCell{
					CellType:  current.cellType,
					CellIndex: current.cellIndex,
					Source:    strings.Join(current.lines, "\n"),
				})
			}
			ct, _ := parseNotebookCellType(m[1])
			idx := -1
			if m[2] != "" {
				if parsed, err := strconv.Atoi(m[2]); err == nil && parsed >= 0 {
					idx = parsed
				}
			}
			current = &curCell{cellType: ct, cellIndex: idx}
		} else if current != nil {
			current.lines = append(current.lines, unescapeNotebookMarkerLine(line))
		} else {
			return nil, fmt.Errorf(
				`Invalid notebook editable representation for %s: expected first line to be "# %%%% [code] cell:0", "# %%%% [markdown] cell:0", or "# %%%% [raw] cell:0".`,
				display,
			)
		}
	}
	if current != nil {
		cells = append(cells, ParsedVirtualCell{
			CellType:  current.cellType,
			CellIndex: current.cellIndex,
			Source:    strings.Join(current.lines, "\n"),
		})
	}
	return cells, nil
}

func makeNotebookSourceArray(source string) *orderedJSON {
	lines := SplitNotebookSource(source)
	elems := make([]*orderedJSON, len(lines))
	for i, line := range lines {
		elems[i] = newJSONString(line)
	}
	return newJSONArray(elems)
}

func createNotebookCell(cellType NotebookCellType, source string) *orderedJSON {
	cell := newJSONObject()
	cell.set("cell_type", newJSONString(string(cellType)))
	cell.set("metadata", newJSONObject())
	cell.set("source", makeNotebookSourceArray(source))
	if cellType == NotebookCellCode {
		cell.set("execution_count", newJSONNull())
		cell.set("outputs", newJSONArray(nil))
	}
	return cell
}

func createEmptyNotebook() *orderedJSON {
	nb := newJSONObject()
	nb.set("cells", newJSONArray(nil))
	nb.set("metadata", newJSONObject())
	nb.set("nbformat", newJSONNumber("4"))
	nb.set("nbformat_minor", newJSONNumber("5"))
	return nb
}

// SerializeEditedNotebookText applies edited cell-projection text back onto originalJSON
// (empty string when hasOriginal is false creates an empty nbformat 4.5 notebook)
// and serializes with `JSON.stringify(nb, null, 1)`-identical formatting.
// upstream: crates/edit/src/notebook.rs:325
func SerializeEditedNotebookText(originalJSON string, hasOriginal bool, text, display string) (string, error) {
	var doc *orderedJSON
	if hasOriginal {
		trimmed := strings.TrimPrefix(originalJSON, BOM)
		var err error
		doc, err = parseOrderedJSON(trimmed)
		if err != nil {
			return "", fmt.Errorf("Invalid JSON in notebook: %s", display)
		}
	} else {
		doc = createEmptyNotebook()
	}
	obj, origCells, err := validateOrderedNotebook(doc, display)
	if err != nil {
		return "", err
	}
	parsedCells, err := ParseNotebookEditableText(text, display)
	if err != nil {
		return "", err
	}

	used := make(map[int]bool, len(parsedCells))
	nextCells := make([]*orderedJSON, 0, len(parsedCells))
	for _, parsed := range parsedCells {
		var orig *orderedJSON
		if parsed.CellIndex >= 0 && parsed.CellIndex < len(origCells) && !used[parsed.CellIndex] {
			used[parsed.CellIndex] = true
			orig = origCells[parsed.CellIndex]
		}
		if orig != nil {
			cell := orig.clone()
			cell.set("cell_type", newJSONString(string(parsed.CellType)))
			cell.set("source", makeNotebookSourceArray(parsed.Source))
			if parsed.CellType == NotebookCellCode {
				if ec, ok := cell.get("execution_count"); !ok || ec.kind == jsonNull {
					cell.set("execution_count", newJSONNull())
				}
				if out, ok := cell.get("outputs"); !ok || out.kind == jsonNull {
					cell.set("outputs", newJSONArray(nil))
				}
			} else {
				cell.delete("execution_count")
				cell.delete("outputs")
			}
			nextCells = append(nextCells, cell)
		} else {
			nextCells = append(nextCells, createNotebookCell(parsed.CellType, parsed.Source))
		}
	}
	nextDoc := obj.clone()
	nextDoc.set("cells", newJSONArray(nextCells))
	return stringifyIndent1(nextDoc), nil
}

// jsNumberToString formats a JSON number identically to ECMAScript Number.prototype.toString.
// upstream: crates/edit/src/notebook.rs:342
func jsNumberToString(num json.Number) string {
	s := string(num)
	if !strings.ContainsAny(s, ".eE") {
		if iv, err := strconv.ParseInt(s, 10, 64); err == nil {
			if iv == 0 {
				return "0"
			}
			return strconv.FormatInt(iv, 10)
		}
		if uv, err := strconv.ParseUint(s, 10, 64); err == nil {
			return strconv.FormatUint(uv, 10)
		}
	}
	val, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) {
		return s
	}
	if val == 0 {
		return "0"
	}
	neg := math.Signbit(val)
	absVal := math.Abs(val)
	// Shortest round-trip decimal representation in scientific form: d.dddde±xx
	rendered := strconv.FormatFloat(absVal, 'e', -1, 64)
	mantissa, expStr, _ := strings.Cut(rendered, "e")
	exp, _ := strconv.Atoi(expStr)
	digits := strings.ReplaceAll(mantissa, ".", "")
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		digits = "0"
	}
	// decimalPos is the number of digits before the decimal point (1-based).
	decimalPos := exp + 1
	var out strings.Builder
	if neg {
		out.WriteByte('-')
	}
	if decimalPos > 0 && decimalPos <= 21 {
		if decimalPos >= len(digits) {
			out.WriteString(digits)
			out.WriteString(strings.Repeat("0", decimalPos-len(digits)))
		} else {
			out.WriteString(digits[:decimalPos])
			out.WriteByte('.')
			out.WriteString(digits[decimalPos:])
		}
	} else if decimalPos <= 0 && decimalPos > -6 {
		out.WriteString("0.")
		out.WriteString(strings.Repeat("0", -decimalPos))
		out.WriteString(digits)
	} else {
		out.WriteByte(digits[0])
		if len(digits) > 1 {
			out.WriteByte('.')
			out.WriteString(digits[1:])
		}
		out.WriteByte('e')
		if exp >= 0 {
			out.WriteByte('+')
		}
		out.WriteString(strconv.Itoa(exp))
	}
	return out.String()
}

func writeJSONStringIndent1(out *bytes.Buffer, value string) {
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r >= 0 && r < 0x20 {
				fmt.Fprintf(out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
}

func writeOrderedJSON(out *bytes.Buffer, v *orderedJSON, depth int) {
	switch v.kind {
	case jsonNull:
		out.WriteString("null")
	case jsonBool:
		if v.b {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case jsonNumber:
		out.WriteString(jsNumberToString(v.num))
	case jsonString:
		writeJSONStringIndent1(out, v.str)
	case jsonArray:
		if len(v.arr) == 0 {
			out.WriteString("[]")
			return
		}
		out.WriteString("[\n")
		for i, elem := range v.arr {
			for range depth + 1 {
				out.WriteByte(' ')
			}
			writeOrderedJSON(out, elem, depth+1)
			if i+1 != len(v.arr) {
				out.WriteByte(',')
			}
			out.WriteByte('\n')
		}
		for range depth {
			out.WriteByte(' ')
		}
		out.WriteByte(']')
	case jsonObject:
		if len(v.obj) == 0 {
			out.WriteString("{}")
			return
		}
		out.WriteString("{\n")
		for i, entry := range v.obj {
			for range depth + 1 {
				out.WriteByte(' ')
			}
			writeJSONStringIndent1(out, entry.key)
			out.WriteString(": ")
			writeOrderedJSON(out, entry.val, depth+1)
			if i+1 != len(v.obj) {
				out.WriteByte(',')
			}
			out.WriteByte('\n')
		}
		for range depth {
			out.WriteByte(' ')
		}
		out.WriteByte('}')
	}
}

func stringifyIndent1(v *orderedJSON) string {
	var out bytes.Buffer
	writeOrderedJSON(&out, v, 0)
	return out.String()
}
