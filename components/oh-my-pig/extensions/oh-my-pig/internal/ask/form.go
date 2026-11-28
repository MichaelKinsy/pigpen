package ask

import (
	"bytes"
	"encoding/json"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// FormSpanStyle is inline emphasis kept from <code>, <strong>/<b>, <em>/<i>.
// upstream: packages/tui/src/html-form/parse.ts:24
type FormSpanStyle string

const (
	SpanStyleCode   FormSpanStyle = "code"
	SpanStyleStrong FormSpanStyle = "strong"
	SpanStyleEm     FormSpanStyle = "em"
)

// FormSpan is a run of inline text; "\n" is a hard line break (<br>).
// upstream: packages/tui/src/html-form/parse.ts:27-30
type FormSpan struct {
	Text  string        `json:"text"`
	Style FormSpanStyle `json:"style,omitempty"`
}

// ChoiceOption is one option of a ChoiceField.
// upstream: packages/tui/src/html-form/parse.ts:33-38
type ChoiceOption struct {
	Value    string     `json:"value"`
	Label    []FormSpan `json:"label"`
	Checked  bool       `json:"checked"`
	Disabled bool       `json:"disabled"`
}

// FormField represents either a ChoiceField (Kind == "choice") or TextField (Kind == "text").
// upstream: packages/tui/src/html-form/parse.ts:41-75
type FormField struct {
	Kind string `json:"kind"` // "choice" | "text"
	Name string `json:"name"`

	// ChoiceField fields
	Multi   bool           `json:"multi,omitempty"`
	Select  bool           `json:"select,omitempty"`
	Other   bool           `json:"other,omitempty"`
	Options []ChoiceOption `json:"options,omitempty"`

	// TextField fields
	Type        string   `json:"type,omitempty"`
	Multiline   bool     `json:"multiline,omitempty"`
	Value       string   `json:"value,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
	MinLength   *int     `json:"minLength,omitempty"`
	MaxLength   *int     `json:"maxLength,omitempty"`
	Rows        int      `json:"rows,omitempty"`

	// Shared
	Label    []FormSpan `json:"label"`
	Required bool       `json:"required"`
}

// FormBlock is a renderable unit of a section in document order.
// upstream: packages/tui/src/html-form/parse.ts:78-94
type FormBlock struct {
	Kind       string         `json:"kind"` // "text" | "list" | "pre" | "table" | "image" | "svg" | "field"
	Spans      []FormSpan     `json:"spans,omitempty"`
	Heading    bool           `json:"heading,omitempty"`
	Items      [][]FormSpan   `json:"items,omitempty"`
	Ordered    bool           `json:"ordered,omitempty"`
	Text       string         `json:"text,omitempty"`
	Rows       [][][]FormSpan `json:"rows,omitempty"`
	HeaderRows int            `json:"headerRows,omitempty"`
	Src        string         `json:"src,omitempty"`
	Alt        string         `json:"alt,omitempty"`
	Markup     string         `json:"markup,omitempty"`
	Field      *FormField     `json:"field,omitempty"`
	OptionsEnd int            `json:"optionsEnd,omitempty"`
}

// FormSection is a <fieldset> (titled by <legend>) or loose content between fieldsets.
// upstream: packages/tui/src/html-form/parse.ts:97-102
type FormSection struct {
	Title    []FormSpan  `json:"title"`
	Blocks   []FormBlock `json:"blocks"`
	Fieldset bool        `json:"fieldset"`
}

// HtmlForm is the parsed form structure.
// upstream: packages/tui/src/html-form/parse.ts:105-109
type HtmlForm struct {
	Sections []FormSection `json:"sections"`
	Fields   []*FormField  `json:"fields"`
	Hidden   [][2]string   `json:"hidden,omitempty"`
}

// FormAnswer is one field as a question and its answer, both plain text.
// upstream: packages/tui/src/html-form/data.ts:24-28
type FormAnswer struct {
	Name     string `json:"name"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// OptionNote is a note attached to one checked choice option.
// upstream: packages/tui/src/html-form/data.ts:31-45
type OptionNote struct {
	Name     string `json:"name"`
	Question string `json:"question"`
	Option   string `json:"option"`
	Value    string `json:"value"`
	Text     string `json:"text"`
}

// FormDataEntry is one key/value pair in an insertion-ordered FormData object.
type FormDataEntry struct {
	Key   string
	Value any
}

// FormData is an insertion-ordered map of submitted form values that serializes
// in JS object key order (array-index keys in ascending numeric order first, then
// remaining keys in insertion order) and without HTML escaping, matching
// JSON.stringify on the object built by packages/tui/src/html-form/data.ts:123-135.
type FormData struct {
	entries []FormDataEntry
	index   map[string]int
}

// NewFormData creates an empty ordered FormData.
func NewFormData() *FormData {
	return &FormData{index: make(map[string]int)}
}

// Set inserts or updates key, preserving first-insertion position.
func (d *FormData) Set(key string, val any) {
	if d.index == nil {
		d.index = make(map[string]int)
	}
	if i, ok := d.index[key]; ok {
		d.entries[i].Value = val
		return
	}
	d.index[key] = len(d.entries)
	d.entries = append(d.entries, FormDataEntry{Key: key, Value: val})
}

// Delete removes key if present.
func (d *FormData) Delete(key string) {
	if d.index == nil {
		return
	}
	i, ok := d.index[key]
	if !ok {
		return
	}
	d.entries = append(d.entries[:i], d.entries[i+1:]...)
	delete(d.index, key)
	for j := i; j < len(d.entries); j++ {
		d.index[d.entries[j].Key] = j
	}
}

// Get retrieves key and whether it is present.
func (d *FormData) Get(key string) (any, bool) {
	if d == nil || d.index == nil {
		return nil, false
	}
	i, ok := d.index[key]
	if !ok {
		return nil, false
	}
	return d.entries[i].Value, true
}

// Entries returns a copy of entries in JS property iteration order
// (canonical uint32 array indices < 2^32-1 ascending, then remaining keys in insertion order).
func (d *FormData) Entries() []FormDataEntry {
	if d == nil || len(d.entries) == 0 {
		return nil
	}
	type idxEntry struct {
		num   uint32
		entry FormDataEntry
	}
	var numeric []idxEntry
	var rest []FormDataEntry
	for _, e := range d.entries {
		if n, ok := parseJSArrayIndex(e.Key); ok {
			numeric = append(numeric, idxEntry{num: n, entry: e})
		} else {
			rest = append(rest, e)
		}
	}
	sort.Slice(numeric, func(i, j int) bool { return numeric[i].num < numeric[j].num })
	out := make([]FormDataEntry, 0, len(d.entries))
	for _, ne := range numeric {
		out = append(out, ne.entry)
	}
	out = append(out, rest...)
	return out
}

func parseJSArrayIndex(s string) (uint32, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil || v == math.MaxUint32 {
		return 0, false
	}
	return uint32(v), true
}

// MarshalJSONNoEscape encodes FormData as pretty-printed JSON (2-space indent when
// pretty is true, compact otherwise) without escaping <, >, &.
func (d *FormData) MarshalJSONString(pretty bool) string {
	entries := d.Entries()
	if len(entries) == 0 {
		return "{}"
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range entries {
		if i > 0 {
			buf.WriteByte(',')
		}
		if pretty {
			buf.WriteString("\n  ")
		}
		buf.WriteString(jsonStringNoEscape(e.Key))
		if pretty {
			buf.WriteString(": ")
		} else {
			buf.WriteByte(':')
		}
		buf.WriteString(jsonValueNoEscape(e.Value, pretty))
	}
	if pretty {
		buf.WriteByte('\n')
	}
	buf.WriteByte('}')
	return buf.String()
}

// MarshalJSON implements json.Marshaler preserving key order without HTML escaping.
func (d *FormData) MarshalJSON() ([]byte, error) {
	return []byte(d.MarshalJSONString(false)), nil
}

func jsonStringNoEscape(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

func jsonValueNoEscape(v any, pretty bool) string {
	if arr, ok := v.([]string); ok {
		if len(arr) == 0 {
			return "[]"
		}
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, item := range arr {
			if i > 0 {
				buf.WriteByte(',')
			}
			if pretty {
				buf.WriteString("\n    ")
			}
			buf.WriteString(jsonStringNoEscape(item))
		}
		if pretty {
			buf.WriteString("\n  ")
		}
		buf.WriteByte(']')
		return buf.String()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

// OtherLabel is the label of the automatic "Other" free-text answer.
// upstream: packages/tui/src/html-form/data.ts:48
const OtherLabel = "Other"

var (
	skippedTags = map[string]bool{
		"button": true, "head": true, "iframe": true, "link": true, "meta": true,
		"noscript": true, "script": true, "style": true, "template": true, "title": true,
	}
	blockTags = map[string]bool{
		"address": true, "article": true, "aside": true, "blockquote": true, "dd": true,
		"details": true, "div": true, "dl": true, "dt": true, "figure": true,
		"figcaption": true, "footer": true, "header": true, "li": true, "main": true,
		"nav": true, "p": true, "section": true,
	}
	headingTags = map[string]bool{
		"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "summary": true,
	}
	styleOfTag = map[string]FormSpanStyle{
		"b": SpanStyleStrong, "code": SpanStyleCode, "em": SpanStyleEm, "i": SpanStyleEm,
		"kbd": SpanStyleCode, "samp": SpanStyleCode, "strong": SpanStyleStrong, "tt": SpanStyleCode,
	}
	ignoredInputTypes = map[string]bool{
		"button": true, "file": true, "image": true, "reset": true, "submit": true,
	}

	otherPrefixRe  = regexp.MustCompile(`(?i)^\s*other\b`)
	emailShapeRe   = regexp.MustCompile(`^[^\s@]+@[^\s@]+$`)
	nameLabelSepRe = regexp.MustCompile(`[_-]+`)
)

// ParseHtmlForm parses agent-written form HTML; content outside the first <form> is ignored when one exists.
// upstream: packages/tui/src/html-form/parse.ts:160-163
func ParseHtmlForm(raw string) *HtmlForm {
	doc := parseHTMLDocument(raw)
	root := doc.querySelector("form")
	if root == nil {
		root = doc
	}
	b := &formBuilder{
		root:     root,
		groups:   make(map[string]*choiceGroupRef),
		labelFor: make(map[string]*htmlNode),
	}
	b.sections = append(b.sections, FormSection{Title: []FormSpan{}, Blocks: []FormBlock{}, Fieldset: false})
	b.curSection = 0
	return b.build()
}

// SpansText returns the plain text of spans.
// upstream: packages/tui/src/html-form/parse.ts:187-189
func SpansText(spans []FormSpan) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

func appendSpan(spans *[]FormSpan, text string, style FormSpanStyle) {
	if text == "" {
		return
	}
	if n := len(*spans); n > 0 && (*spans)[n-1].Style == style {
		(*spans)[n-1].Text += text
		return
	}
	*spans = append(*spans, FormSpan{Text: text, Style: style})
}

// collapseNonNewlineJSSpace collapses runs of JS whitespace (except '\n') into a single
// ASCII space and removes spaces adjacent to '\n', matching packages/tui/src/html-form/parse.ts:176-184.
func collapseNonNewlineJSSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == '\n' {
			inSpace = false
			b.WriteByte('\n')
			continue
		}
		if isJSSpace(r) {
			inSpace = true
			continue
		}
		if inSpace {
			if b.Len() > 0 && b.String()[b.Len()-1] != '\n' {
				b.WriteByte(' ')
			} else if b.Len() == 0 {
				b.WriteByte(' ')
			}
			inSpace = false
		}
		b.WriteRune(r)
	}
	if inSpace && (b.Len() == 0 || b.String()[b.Len()-1] != '\n') {
		b.WriteByte(' ')
	}
	return b.String()
}

func normalizeSpans(spans []FormSpan) []FormSpan {
	var out []FormSpan
	for _, s := range spans {
		t := collapseNonNewlineJSSpace(s.Text)
		appendSpan(&out, t, s.Style)
	}
	if len(out) > 0 {
		out[0].Text = strings.TrimLeftFunc(out[0].Text, isJSSpace)
	}
	if len(out) > 0 {
		out[len(out)-1].Text = strings.TrimRightFunc(out[len(out)-1].Text, isJSSpace)
	}
	filtered := out[:0]
	for _, s := range out {
		if s.Text != "" {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func trimJSSpace(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

func svgTitle(svg *htmlNode) string {
	if label, ok := svg.getAttr("aria-label"); ok && trimJSSpace(label) != "" {
		return trimJSSpace(label)
	}
	for _, c := range svg.childElements() {
		if c.tag == "title" {
			return oneLine(c.textContent())
		}
	}
	return ""
}

// parseJSNumber mirrors JavaScript's Number(raw) for finite numbers, including
// 0x/0o/0b integer literals and decimal floats.
func parseJSNumber(raw string) (float64, bool) {
	s := trimJSSpace(raw)
	if s == "" || strings.ContainsRune(s, '_') {
		return 0, false
	}
	if len(s) > 2 && s[0] == '0' {
		switch s[1] {
		case 'x', 'X':
			if u, err := strconv.ParseUint(s[2:], 16, 64); err == nil {
				return float64(u), true
			}
			return 0, false
		case 'b', 'B':
			if u, err := strconv.ParseUint(s[2:], 2, 64); err == nil {
				return float64(u), true
			}
			return 0, false
		case 'o', 'O':
			if u, err := strconv.ParseUint(s[2:], 8, 64); err == nil {
				return float64(u), true
			}
			return 0, false
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func floatAttr(el *htmlNode, name string) *float64 {
	raw, ok := el.getAttr(name)
	if !ok {
		return nil
	}
	v, valid := parseJSNumber(raw)
	if !valid {
		return nil
	}
	return &v
}

func intAttr(el *htmlNode, name string) *int {
	raw, ok := el.getAttr(name)
	if !ok {
		return nil
	}
	v, valid := parseJSNumber(raw)
	if !valid {
		return nil
	}
	iv := int(v)
	return &iv
}

type choiceGroupRef struct {
	field      *FormField
	sectionIdx int
	blockIdx   int
}

type formBuilder struct {
	root          *htmlNode
	sections      []FormSection
	curSection    int
	fields        []*FormField
	hidden        [][2]string
	groups        map[string]*choiceGroupRef
	labelFor      map[string]*htmlNode
	inline        []FormSpan
	fieldsetDepth int
}

func (b *formBuilder) section() *FormSection {
	return &b.sections[b.curSection]
}

func (b *formBuilder) build() *HtmlForm {
	ids := make(map[string]struct{})
	for _, ctrl := range b.root.querySelectorAll("input", "select", "textarea") {
		if id := ctrl.id(); id != "" {
			ids[id] = struct{}{}
		}
	}
	for _, lbl := range b.root.querySelectorAll("label") {
		if target, ok := lbl.getAttr("for"); ok && target != "" {
			if _, exists := ids[target]; exists {
				if _, seen := b.labelFor[target]; !seen {
					b.labelFor[target] = lbl
				}
			}
		}
	}
	b.walkChildren(b.root, "")
	b.flush(false)
	for _, f := range b.fields {
		if f.Kind != "choice" || f.Select || len(f.Options) < 2 {
			continue
		}
		hasOther := false
		for _, opt := range f.Options {
			if otherPrefixRe.MatchString(SpansText(opt.Label)) {
				hasOther = true
				break
			}
		}
		f.Other = !hasOther
	}
	var kept []FormSection
	for _, s := range b.sections {
		if len(s.Blocks) > 0 || len(s.Title) > 0 {
			kept = append(kept, s)
		}
	}
	return &HtmlForm{
		Sections: kept,
		Fields:   b.fields,
		Hidden:   b.hidden,
	}
}

func (b *formBuilder) walkChildren(n *htmlNode, style FormSpanStyle) {
	for _, c := range n.children {
		b.walk(c, style)
	}
}

func (b *formBuilder) walk(n *htmlNode, style FormSpanStyle) {
	if n.kind == nodeComment {
		return
	}
	if n.kind == nodeText {
		appendSpan(&b.inline, n.text, style)
		return
	}
	if n.kind != nodeElement {
		return
	}
	tag := n.tag
	if skippedTags[tag] {
		return
	}
	switch tag {
	case "br":
		appendSpan(&b.inline, "\n", "")
		return
	case "hr":
		b.flush(false)
		return
	case "input", "select", "textarea":
		b.control(n, nil)
		return
	case "label":
		b.label(n, style)
		return
	case "fieldset":
		b.fieldset(n)
		return
	case "legend":
		b.flush(false)
		b.walkChildren(n, style)
		b.flush(true)
		return
	case "ul", "ol":
		b.list(n, tag == "ol")
		return
	case "pre":
		b.flush(false)
		txt := strings.TrimPrefix(n.textContent(), "\n")
		txt = strings.TrimRightFunc(txt, isJSSpace)
		b.section().Blocks = append(b.section().Blocks, FormBlock{Kind: "pre", Text: txt})
		return
	case "table":
		b.table(n)
		return
	case "img":
		src, _ := n.getAttr("src")
		src = trimJSSpace(src)
		if src == "" {
			return
		}
		alt, _ := n.getAttr("alt")
		b.flush(false)
		b.section().Blocks = append(b.section().Blocks, FormBlock{Kind: "image", Src: src, Alt: trimJSSpace(alt)})
		return
	case "svg":
		b.flush(false)
		b.section().Blocks = append(b.section().Blocks, FormBlock{Kind: "svg", Markup: n.rawHTML, Alt: svgTitle(n)})
		return
	}
	if headingTags[tag] {
		b.flush(false)
		b.walkChildren(n, style)
		b.flush(true)
		return
	}
	if blockTags[tag] {
		b.flush(false)
		b.walkChildren(n, style)
		b.flush(false)
		return
	}
	nextStyle := style
	if s, ok := styleOfTag[tag]; ok {
		nextStyle = s
	}
	b.walkChildren(n, nextStyle)
}

func (b *formBuilder) flush(heading bool) {
	spans := normalizeSpans(b.inline)
	b.inline = nil
	if len(spans) > 0 {
		b.section().Blocks = append(b.section().Blocks, FormBlock{Kind: "text", Spans: spans, Heading: heading})
	}
}

func (b *formBuilder) spansOf(el *htmlNode) []FormSpan {
	var spans []FormSpan
	var visit func(*htmlNode, FormSpanStyle)
	visit = func(n *htmlNode, style FormSpanStyle) {
		if n.kind == nodeComment {
			return
		}
		if n.kind == nodeText {
			appendSpan(&spans, n.text, style)
			return
		}
		if n.kind != nodeElement {
			return
		}
		tag := n.tag
		if skippedTags[tag] || tag == "input" || tag == "select" || tag == "textarea" || tag == "svg" {
			return
		}
		if tag == "br" {
			appendSpan(&spans, "\n", "")
			return
		}
		if tag == "img" {
			alt, _ := n.getAttr("alt")
			appendSpan(&spans, alt, style)
			return
		}
		if blockTags[tag] || headingTags[tag] {
			appendSpan(&spans, " ", "")
		}
		nextStyle := style
		if s, ok := styleOfTag[tag]; ok {
			nextStyle = s
		}
		for _, c := range n.children {
			visit(c, nextStyle)
		}
	}
	for _, c := range el.children {
		visit(c, "")
	}
	return normalizeSpans(spans)
}

func (b *formBuilder) label(lbl *htmlNode, style FormSpanStyle) {
	if target, ok := lbl.getAttr("for"); ok && target != "" && b.labelFor[target] == lbl {
		return
	}
	if !lbl.hasControl {
		b.walkChildren(lbl, style)
		return
	}
	controls := lbl.querySelectorAll("input", "select", "textarea")
	b.flush(false)
	spans := b.spansOf(lbl)
	for _, ctrl := range controls {
		b.control(ctrl, spans)
	}
}

func (b *formBuilder) fieldset(fs *htmlNode) {
	b.flush(false)
	var legend *htmlNode
	for _, c := range fs.childElements() {
		if c.tag == "legend" {
			legend = c
			break
		}
	}
	if b.fieldsetDepth > 0 {
		if legend != nil {
			b.walk(legend, "")
		}
	} else {
		var title []FormSpan
		if legend != nil {
			title = b.spansOf(legend)
		}
		b.startSection(title, true)
	}
	b.fieldsetDepth++
	for _, c := range fs.children {
		if c != legend {
			b.walk(c, "")
		}
	}
	b.flush(false)
	b.fieldsetDepth--
	if b.fieldsetDepth == 0 {
		b.startSection(nil, false)
	}
}

func (b *formBuilder) startSection(title []FormSpan, fieldset bool) {
	if title == nil {
		title = []FormSpan{}
	}
	cur := b.section()
	if len(cur.Blocks) == 0 && len(cur.Title) == 0 {
		cur.Title = title
		cur.Fieldset = fieldset
		return
	}
	b.sections = append(b.sections, FormSection{Title: title, Blocks: []FormBlock{}, Fieldset: fieldset})
	b.curSection = len(b.sections) - 1
}

func (b *formBuilder) list(lst *htmlNode, ordered bool) {
	b.flush(false)
	if lst.hasControl {
		b.walkChildren(lst, "")
		b.flush(false)
		return
	}
	var items [][]FormSpan
	for _, c := range lst.childElements() {
		if c.tag == "li" {
			if sp := b.spansOf(c); len(sp) > 0 {
				items = append(items, sp)
			}
		}
	}
	if len(items) > 0 {
		b.section().Blocks = append(b.section().Blocks, FormBlock{Kind: "list", Items: items, Ordered: ordered})
	}
}

func (b *formBuilder) table(tbl *htmlNode) {
	b.flush(false)
	var rows []*htmlNode
	for _, tr := range tbl.querySelectorAll("tr") {
		if tr.closest("table") == tbl {
			rows = append(rows, tr)
		}
	}
	cellsOf := func(tr *htmlNode) []*htmlNode {
		var out []*htmlNode
		for _, c := range tr.childElements() {
			if c.tag == "td" || c.tag == "th" {
				out = append(out, c)
			}
		}
		return out
	}
	if !tbl.hasControl {
		cells := make([][][]FormSpan, len(rows))
		hasAny := false
		for i, tr := range rows {
			cs := cellsOf(tr)
			cells[i] = make([][]FormSpan, len(cs))
			for j, c := range cs {
				cells[i][j] = b.spansOf(c)
			}
			if len(cs) > 0 {
				hasAny = true
			}
		}
		headerRows := 0
		for headerRows < len(rows) {
			tr := rows[headerRows]
			cs := cellsOf(tr)
			allTH := len(cs) > 0
			for _, c := range cs {
				if c.tag != "th" {
					allTH = false
					break
				}
			}
			if tr.closest("thead") != nil || allTH {
				headerRows++
			} else {
				break
			}
		}
		if hasAny {
			b.section().Blocks = append(b.section().Blocks, FormBlock{Kind: "table", Rows: cells, HeaderRows: headerRows})
		}
		return
	}
	var header [][]FormSpan
	for _, tr := range rows {
		cs := cellsOf(tr)
		allTH := len(cs) > 0
		for _, c := range cs {
			if c.tag != "th" {
				allTH = false
				break
			}
		}
		if allTH && !tr.hasControl {
			header = make([][]FormSpan, len(cs))
			for j, c := range cs {
				header[j] = b.spansOf(c)
			}
			continue
		}
		var text []FormSpan
		for _, c := range cs {
			if c.hasControl {
				continue
			}
			sp := b.spansOf(c)
			if len(sp) == 0 {
				continue
			}
			if len(text) > 0 {
				appendSpan(&text, " · ", "")
			}
			for _, s := range sp {
				appendSpan(&text, s.Text, s.Style)
			}
		}
		if len(text) > 0 {
			b.section().Blocks = append(b.section().Blocks, FormBlock{Kind: "text", Spans: text, Heading: false})
		}
		for col, c := range cs {
			if !c.hasControl {
				continue
			}
			for _, ctrl := range c.querySelectorAll("input", "select", "textarea") {
				own := ctrl.closest("label")
				var sp []FormSpan
				if own != nil && c.contains(own) {
					sp = b.spansOf(own)
				} else {
					sp = b.spansOf(c)
				}
				if len(sp) == 0 && col < len(header) {
					sp = header[col]
				}
				b.control(ctrl, sp)
			}
		}
	}
}

func (b *formBuilder) detachedLabel(ctrl *htmlNode) []FormSpan {
	if id := ctrl.id(); id != "" {
		if lbl, ok := b.labelFor[id]; ok {
			return b.spansOf(lbl)
		}
	}
	if v, ok := ctrl.getAttr("aria-label"); ok {
		return normalizeSpans([]FormSpan{{Text: v}})
	}
	if v, ok := ctrl.getAttr("title"); ok {
		return normalizeSpans([]FormSpan{{Text: v}})
	}
	return nil
}

func nameLabel(name string) []FormSpan {
	if name == "" {
		return []FormSpan{}
	}
	return []FormSpan{{Text: nameLabelSepRe.ReplaceAllString(name, " ")}}
}

func (b *formBuilder) addField(f *FormField) int {
	b.fields = append(b.fields, f)
	b.section().Blocks = append(b.section().Blocks, FormBlock{Kind: "field", Field: f})
	return len(b.section().Blocks) - 1
}

func (b *formBuilder) control(ctrl *htmlNode, wrapping []FormSpan) {
	b.flush(false)
	label := wrapping
	if len(label) == 0 {
		label = b.detachedLabel(ctrl)
	}
	if label == nil {
		label = []FormSpan{}
	}
	name, _ := ctrl.getAttr("name")
	required := ctrl.hasAttr("required")
	tag := ctrl.tag

	if tag == "select" {
		multi := ctrl.hasAttr("multiple")
		optNodes := ctrl.querySelectorAll("option")
		options := make([]ChoiceOption, 0, len(optNodes))
		hasChecked := false
		for _, opt := range optNodes {
			txt := oneLine(opt.textContent())
			val, hasVal := opt.getAttr("value")
			if !hasVal {
				val = txt
			}
			checked := opt.hasAttr("selected")
			if checked {
				hasChecked = true
			}
			options = append(options, ChoiceOption{
				Value:    val,
				Label:    []FormSpan{{Text: txt}},
				Checked:  checked,
				Disabled: opt.hasAttr("disabled"),
			})
		}
		if !multi && !hasChecked {
			for i := range options {
				if !options[i].Disabled {
					options[i].Checked = true
					break
				}
			}
		}
		b.addField(&FormField{
			Kind:     "choice",
			Name:     name,
			Multi:    multi,
			Select:   true,
			Other:    false,
			Label:    label,
			Options:  options,
			Required: required,
		})
		return
	}

	if tag == "textarea" {
		lbl := label
		if len(lbl) == 0 {
			lbl = nameLabel(name)
		}
		val := ctrl.textContent()
		val = strings.TrimPrefix(val, "\r\n")
		val = strings.TrimPrefix(val, "\n")
		ph, _ := ctrl.getAttr("placeholder")
		rows := 4
		if r := intAttr(ctrl, "rows"); r != nil {
			rows = *r
		}
		b.addField(&FormField{
			Kind:        "text",
			Name:        name,
			Type:        "textarea",
			Multiline:   true,
			Label:       lbl,
			Value:       val,
			Placeholder: ph,
			Required:    required,
			MinLength:   intAttr(ctrl, "minlength"),
			MaxLength:   intAttr(ctrl, "maxlength"),
			Rows:        rows,
		})
		return
	}

	inType, hasType := ctrl.getAttr("type")
	if !hasType || inType == "" {
		inType = "text"
	} else {
		inType = strings.ToLower(inType)
	}
	if ignoredInputTypes[inType] {
		return
	}
	val, hasVal := ctrl.getAttr("value")
	if inType == "hidden" {
		if name != "" {
			b.hidden = append(b.hidden, [2]string{name, val})
		}
		return
	}
	if inType == "radio" || inType == "checkbox" {
		optVal := "on"
		if hasVal {
			optVal = val
		}
		optLabel := label
		if len(optLabel) == 0 {
			fallback := name
			if hasVal {
				fallback = val
			}
			optLabel = []FormSpan{{Text: fallback}}
		}
		opt := ChoiceOption{
			Value:    optVal,
			Label:    optLabel,
			Checked:  ctrl.hasAttr("checked"),
			Disabled: ctrl.hasAttr("disabled"),
		}
		key := inType + "\x00" + name
		if grp, ok := b.groups[key]; ok {
			grp.field.Options = append(grp.field.Options, opt)
			grp.field.Required = grp.field.Required || required
			if grp.sectionIdx == b.curSection && len(b.section().Blocks)-1 != grp.blockIdx {
				b.section().Blocks[grp.blockIdx].OptionsEnd = len(b.section().Blocks)
			}
			return
		}
		f := &FormField{
			Kind:     "choice",
			Name:     name,
			Multi:    inType == "checkbox",
			Select:   false,
			Other:    false,
			Label:    []FormSpan{},
			Options:  []ChoiceOption{opt},
			Required: required,
		}
		idx := b.addField(f)
		b.groups[key] = &choiceGroupRef{field: f, sectionIdx: b.curSection, blockIdx: idx}
		return
	}

	lbl := label
	if len(lbl) == 0 {
		lbl = nameLabel(name)
	}
	ph, _ := ctrl.getAttr("placeholder")
	pat, _ := ctrl.getAttr("pattern")
	b.addField(&FormField{
		Kind:        "text",
		Name:        name,
		Type:        inType,
		Multiline:   false,
		Label:       lbl,
		Value:       val,
		Placeholder: ph,
		Required:    required,
		Min:         floatAttr(ctrl, "min"),
		Max:         floatAttr(ctrl, "max"),
		Pattern:     pat,
		MinLength:   intAttr(ctrl, "minlength"),
		MaxLength:   intAttr(ctrl, "maxlength"),
		Rows:        1,
	})
}

// =============================================================================
// Form data, validation, questions, contexts (port of html-form/data.ts)
// =============================================================================

// oneLine collapses all JS whitespace (including Unicode space such as NBSP) into a single space.
func oneLine(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if isJSSpace(r) {
			inSpace = true
			continue
		}
		if inSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// IsArrayChoice reports whether a choice field reports an array (checkbox group or multi-select).
// upstream: packages/tui/src/html-form/data.ts:51-53
func IsArrayChoice(f *FormField) bool {
	return f.Multi && len(f.Options) > 1
}

// ChoiceValue computes a choice field's submitted value.
// upstream: packages/tui/src/html-form/data.ts:61-72
func ChoiceValue(f *FormField, checked map[int]bool, other string) (any, bool) {
	typed := ""
	if f.Other {
		typed = trimJSSpace(other)
	}
	var values []string
	for i, opt := range f.Options {
		if checked[i] && !opt.Disabled {
			values = append(values, opt.Value)
		}
	}
	if IsArrayChoice(f) {
		if typed != "" {
			values = append(values, typed)
		}
		if values == nil {
			values = []string{}
		}
		return values, true
	}
	if typed != "" {
		return typed, true
	}
	if len(values) > 0 {
		return values[0], true
	}
	return nil, false
}

// TextValue computes a text field's submitted value (number for numeric number/range inputs, else string).
// upstream: packages/tui/src/html-form/data.ts:75-81
func TextValue(f *FormField, text string) any {
	if (f.Type == "number" || f.Type == "range") && trimJSSpace(text) != "" {
		if n, ok := parseJSNumber(text); ok {
			return n
		}
	}
	return text
}

// canParseURL approximates WHATWG URL.canParse(text): a valid scheme followed by ":"
// (e.g. "https://example.com", "file:///tmp/x", "a:") with no ASCII control chars.
func canParseURL(raw string) bool {
	s := trimJSSpace(raw)
	if s == "" {
		return false
	}
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return false
	}
	scheme := s[:colon]
	for i := range len(scheme) {
		c := scheme[i]
		if i == 0 {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
				return false
			}
		} else if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.') {
			return false
		}
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ws", "wss", "ftp":
		return u.Host != ""
	default:
		return true
	}
}

// FieldError returns a validation error message for value, or "" if valid.
// upstream: packages/tui/src/html-form/data.ts:89-111
func FieldError(f *FormField, value any, present bool) string {
	if f.Kind == "choice" {
		empty := !present || value == nil
		if arr, ok := value.([]string); ok && len(arr) == 0 {
			empty = true
		}
		if !f.Required || !empty {
			return ""
		}
		if IsArrayChoice(f) {
			return "Select at least one"
		}
		return "Required"
	}
	text := ""
	if present && value != nil {
		switch v := value.(type) {
		case string:
			text = v
		case float64:
			text = strconv.FormatFloat(v, 'f', -1, 64)
		default:
			text = ""
		}
	}
	if trimJSSpace(text) == "" {
		if f.Required {
			return "Required"
		}
		return ""
	}
	if f.Type == "number" || f.Type == "range" {
		n, ok := parseJSNumber(text)
		if !ok {
			return "Enter a number"
		}
		if f.Min != nil && n < *f.Min {
			return "Must be at least " + strconv.FormatFloat(*f.Min, 'f', -1, 64)
		}
		if f.Max != nil && n > *f.Max {
			return "Must be at most " + strconv.FormatFloat(*f.Max, 'f', -1, 64)
		}
	}
	if f.MinLength != nil && len([]rune(text)) < *f.MinLength {
		return "Needs at least " + strconv.Itoa(*f.MinLength) + " characters"
	}
	if f.MaxLength != nil && len([]rune(text)) > *f.MaxLength {
		return "At most " + strconv.Itoa(*f.MaxLength) + " characters"
	}
	if f.Type == "email" && !emailShapeRe.MatchString(text) {
		return "Enter an email address"
	}
	if f.Type == "url" && !canParseURL(text) {
		return "Enter a full URL"
	}
	if f.Pattern != "" {
		if re, err := regexp.Compile("^(?:" + f.Pattern + ")$"); err == nil {
			if !re.MatchString(text) {
				pat := f.Pattern
				runes := []rune(pat)
				if len(runes) > 40 {
					pat = string(runes[:39]) + "…"
				}
				return "Must match " + pat
			}
		}
	}
	return ""
}

// DefaultFormData returns the ordered data the form submits untouched: hidden
// inputs first, then fields in document order.
// upstream: packages/tui/src/html-form/data.ts:123-135
func DefaultFormData(form *HtmlForm) *FormData {
	data := NewFormData()
	for _, h := range form.Hidden {
		data.Set(h[0], h[1])
	}
	for _, f := range form.Fields {
		if f.Name == "" {
			continue
		}
		if f.Kind == "text" {
			data.Set(f.Name, TextValue(f, f.Value))
			continue
		}
		checked := make(map[int]bool)
		for i, opt := range f.Options {
			if opt.Checked {
				checked[i] = true
			}
		}
		if val, ok := ChoiceValue(f, checked, ""); ok {
			data.Set(f.Name, val)
		}
	}
	return data
}

// FieldQuestions maps each field to its question text.
// upstream: packages/tui/src/html-form/data.ts:142-160
func FieldQuestions(form *HtmlForm) map[*FormField]string {
	questions := make(map[*FormField]string, len(form.Fields))
	for _, s := range form.Sections {
		title := SpansText(s.Title)
		caption := ""
		for _, blk := range s.Blocks {
			if blk.Kind == "field" && blk.Field != nil {
				f := blk.Field
				own := SpansText(f.Label)
				lone := ""
				if f.Kind == "choice" && len(f.Options) == 1 {
					lone = SpansText(f.Options[0].Label)
				}
				q := own
				if q == "" {
					q = caption
				}
				if q == "" {
					q = title
				}
				if q == "" {
					q = lone
				}
				if q == "" {
					q = f.Name
				}
				questions[f] = oneLine(q)
				caption = ""
				continue
			}
			if blk.Kind == "text" {
				caption = SpansText(blk.Spans)
			} else {
				caption = ""
			}
		}
	}
	return questions
}

// BlockText returns plain text of a context block, or "" for fields/pictures.
// upstream: packages/tui/src/html-form/data.ts:167-182
func BlockText(blk FormBlock) string {
	switch blk.Kind {
	case "text":
		return SpansText(blk.Spans)
	case "pre":
		return blk.Text
	case "list":
		lines := make([]string, len(blk.Items))
		for i, item := range blk.Items {
			prefix := "- "
			if blk.Ordered {
				prefix = strconv.Itoa(i+1) + ". "
			}
			lines[i] = prefix + SpansText(item)
		}
		return strings.Join(lines, "\n")
	case "table":
		lines := make([]string, len(blk.Rows))
		for i, row := range blk.Rows {
			cells := make([]string, len(row))
			for j, cell := range row {
				cells[j] = SpansText(cell)
			}
			lines[i] = strings.Join(cells, " | ")
		}
		return strings.Join(lines, "\n")
	default:
		return ""
	}
}

// FieldContexts computes the form context text carried by each named field's prompt.
// upstream: packages/tui/src/html-form/data.ts:210-250
func FieldContexts(form *HtmlForm) map[*FormField]string {
	questions := FieldQuestions(form)
	parts := make(map[*FormField]*[]string)
	var pending []string
	var last *[]string
	for _, s := range form.Sections {
		if title := trimJSSpace(SpansText(s.Title)); title != "" {
			pending = append(pending, title)
		}
		var owner *[]string
		ownerEnd := 0
		for idx, blk := range s.Blocks {
			if blk.Kind == "field" && blk.Field != nil {
				if blk.Field.Name == "" {
					continue
				}
				q := questions[blk.Field]
				own := make([]string, 0, len(pending))
				for _, p := range pending {
					if oneLine(p) != q {
						own = append(own, p)
					}
				}
				ownPtr := &own
				parts[blk.Field] = ownPtr
				pending = nil
				last = ownPtr
				owner = ownPtr
				end := blk.OptionsEnd
				intervening := false
				for at := idx + 1; at < end && at < len(s.Blocks); at++ {
					if s.Blocks[at].Kind == "field" && s.Blocks[at].Field != nil && s.Blocks[at].Field.Name != "" {
						intervening = true
						break
					}
				}
				if intervening {
					ownerEnd = 0
				} else {
					ownerEnd = end
				}
				continue
			}
			txt := BlockText(blk)
			if trimJSSpace(txt) == "" {
				continue
			}
			if owner != nil && idx < ownerEnd {
				*owner = append(*owner, txt)
			} else {
				pending = append(pending, txt)
			}
		}
	}
	if last != nil {
		*last = append(*last, pending...)
	}
	out := make(map[*FormField]string, len(parts))
	for f, p := range parts {
		out[f] = strings.Join(*p, "\n\n")
	}
	return out
}

// FormAnswers produces the ordered list of question/answer pairs for AskToolDetails.
// upstream: packages/tui/src/html-form/data.ts:253-266
func FormAnswers(form *HtmlForm, data *FormData) []FormAnswer {
	questions := FieldQuestions(form)
	answers := make([]FormAnswer, 0, len(form.Fields))
	for _, f := range form.Fields {
		if f.Name == "" {
			continue
		}
		q := questions[f]
		if q == "" {
			q = f.Name
		}
		val, ok := data.Get(f.Name)
		answers = append(answers, FormAnswer{
			Name:     f.Name,
			Question: q,
			Answer:   answerText(f, val, ok),
		})
	}
	return answers
}

func answerText(f *FormField, val any, ok bool) string {
	if !ok || val == nil {
		return ""
	}
	if f.Kind == "text" {
		switch v := val.(type) {
		case string:
			return v
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		default:
			return ""
		}
	}
	labelOf := func(raw string) string {
		for _, opt := range f.Options {
			if opt.Value == raw {
				if l := oneLine(SpansText(opt.Label)); l != "" {
					return l
				}
				return raw
			}
		}
		if f.Other {
			return OtherLabel + ": " + raw
		}
		return raw
	}
	if arr, isArr := val.([]string); isArr {
		out := make([]string, len(arr))
		for i, s := range arr {
			out[i] = labelOf(s)
		}
		return strings.Join(out, ", ")
	}
	if s, isStr := val.(string); isStr {
		return labelOf(s)
	}
	return ""
}
